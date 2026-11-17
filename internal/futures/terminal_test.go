package futures

import (
	"encoding/json"
	"testing"
)

func terminalFixture(t *testing.T, reviewed bool) (*TerminalReader, terminalCommand, []byte) {
	t.Helper()
	qc := batchContext("positions")
	qc.NativeInvestorID = "990000000001"
	qc.NativeUserID = "990000000001"
	c := terminalCommand{id: 1, scope: qc.commandScope(), target: publicationTarget()}
	c.target.CounterSessionID = qc.CounterSessionID
	c.target.TradeDate = qc.TradeDate
	c.command = Command{SubmitCommand: SubmitCommand{MessageID: qc.OriginMessageID, Action: qc.Action, RequestID: "terminal-req", CorrelationID: "terminal-corr", IdempotencyKey: "terminal-idem"}}
	r := &TerminalReader{reviews: map[int64]QueryRequestReview{}}
	if reviewed {
		r.reviews[1] = QueryRequestReview{CommandID: 1, Query: qc, Target: c.target}
	}
	raw := editBody(t, fixtureBody(t, "messages.json", "positions"), func(m map[string]any) {
		m["request_id"] = c.command.RequestID
		m["correlation_id"] = c.command.CorrelationID
		m["idempotency_key"] = c.command.IdempotencyKey
	})
	return r, c, raw
}

func TestTerminalUnreviewedCannotExposeFinancialRows(t *testing.T) {
	r, c, raw := terminalFixture(t, false)
	v := TerminalQuery{State: "awaiting_reply"}
	if err := r.renderObservation(&v, c, [][]byte{raw}); err != nil {
		t.Fatal(err)
	}
	if v.State != "pending_review" || v.Positions != nil || v.RowCount != nil || v.Reviewed || v.BusinessPublished {
		t.Fatalf("unreviewed row escaped: %+v", v)
	}
}

func TestTerminalHistoricalReviewDoesNotPublishBusinessSnapshot(t *testing.T) {
	r, c, raw := terminalFixture(t, true)
	v := TerminalQuery{}
	if err := r.renderObservation(&v, c, [][]byte{raw}); err != nil {
		t.Fatal(err)
	}
	if v.State != "ready" || !v.Reviewed || v.RowCount == nil || *v.RowCount == 0 || v.BusinessPublished {
		t.Fatalf("unexpected observation: %+v", v)
	}
}

func TestTerminalIncompleteAndCorruptEvidence(t *testing.T) {
	for _, scenario := range []string{"partial", "ticket", "account", "scope", "target", "correlation", "biztype"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, raw := terminalFixture(t, true)
			if scenario == "scope" {
				c.scope.InvestorID = "different"
			} else if scenario == "target" {
				c.target.Epoch = "changed"
			} else {
				raw = editBody(t, raw, func(m map[string]any) {
					switch scenario {
					case "partial":
						m["status"] = "partial"
						m["chunk"].(map[string]any)["is_last"] = false
					case "ticket":
						evidence(m)["ticket"] = "other"
					case "account":
						m["routing"].(map[string]any)["account_id"] = "999999"
					case "correlation":
						m["correlation_id"] = "foreign"
					case "biztype":
						m["payload"].(map[string]any)["product_type"] = "stock"
					}
				})
			}
			v := TerminalQuery{}
			err := r.renderObservation(&v, c, [][]byte{raw})
			if scenario == "partial" {
				if err != nil || v.State != "collecting" {
					t.Fatal(v, err)
				}
			} else if err == nil {
				t.Fatal("invalid evidence accepted")
			}
			if v.Positions != nil || v.RowCount != nil || v.BusinessPublished {
				t.Fatal("partial/invalid rows exposed")
			}
		})
	}
}

func TestTerminalFailedIsNotZeroAndEmptyIsKnownZero(t *testing.T) {
	for _, failed := range []bool{true, false} {
		r, c, raw := terminalFixture(t, true)
		raw = editBody(t, raw, func(m map[string]any) {
			p := m["payload"].(map[string]any)
			if failed {
				m["status"] = "failed"
				m["code"] = "QUERY_FAILED"
				m["message"] = "missing_biz_type"
				delete(m, "chunk")
				delete(p, "items")
				evidence(m)["snapshot_applied"] = false
			} else {
				p["items"] = []any{}
			}
		})
		v := TerminalQuery{}
		if err := r.renderObservation(&v, c, [][]byte{raw}); err != nil {
			t.Fatal(err)
		}
		if failed {
			if v.State != "failed" || v.Message != "missing_biz_type" || v.RowCount != nil {
				t.Fatal(v)
			}
		} else if v.State != "ready" || v.RowCount == nil || *v.RowCount != 0 {
			t.Fatal(v)
		}
	}
}

func TestTerminalOutOfOrderPagesAndConflict(t *testing.T) {
	r, c, raw := terminalFixture(t, true)
	first := editBody(t, raw, func(m map[string]any) {
		m["message_id"] = "page-zero"
		m["status"] = "partial"
		m["chunk"].(map[string]any)["is_last"] = false
	})
	last := editBody(t, raw, func(m map[string]any) {
		m["message_id"] = "page-one"
		m["chunk"].(map[string]any)["index"] = json.Number("1")
		m["payload"].(map[string]any)["items"] = []any{}
	})
	v := TerminalQuery{}
	if err := r.renderObservation(&v, c, [][]byte{last, first}); err != nil || v.State != "ready" {
		t.Fatal(v, err)
	}
	v = TerminalQuery{}
	if err := r.renderObservation(&v, c, [][]byte{first, last, raw}); err == nil {
		t.Fatal("contradictory terminal accepted")
	}
}
