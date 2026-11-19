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
	c := terminalCommand{id: 1, scope: qc.commandScope(), target: publicationTarget(), publicationState: "published"}
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

func TestTerminalPositionsDoNotRequireManualAudit(t *testing.T) {
	r, c, raw := terminalFixture(t, false)
	v := TerminalQuery{State: "awaiting_reply"}
	if err := r.renderObservation(&v, c, [][]byte{raw}); err != nil {
		t.Fatal(err)
	}
	if v.State != "response_ready" || !v.ResponseValidated || len(v.Positions) == 0 || v.RowCount == nil || v.Reviewed || v.BusinessPublished {
		t.Fatalf("response not displayed or promoted to independent audit: %+v", v)
	}
}

func TestTerminalPositionResponseIntegrity(t *testing.T) {
	for _, scenario := range []string{"incomplete", "empty", "failed", "unknown_publication", "wrong_session", "wrong_day", "wrong_correlation", "wrong_generation", "snapshot_false", "sdk_error", "wrong_row_account", "changed_ticket", "changed_sdk_id", "conflicting_page", "out_of_order"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, raw := terminalFixture(t, false)
			if scenario == "unknown_publication" {
				c.publicationState = "attempted"
			}
			changed := editBody(t, raw, func(m map[string]any) {
				switch scenario {
				case "incomplete":
					m["status"] = "partial"
					m["chunk"].(map[string]any)["is_last"] = false
				case "empty":
					m["payload"].(map[string]any)["items"] = []any{}
				case "failed":
					m["status"], m["code"] = "failed", "QUERY_FAILED"
					delete(m, "chunk")
					delete(m["payload"].(map[string]any), "items")
					evidence(m)["snapshot_applied"] = false
				case "wrong_session":
					evidence(m)["counter_session_id"] = "different"
				case "wrong_day":
					m["payload"].(map[string]any)["trade_date"] = "20260101"
				case "wrong_correlation":
					m["correlation_id"] = "different"
				case "wrong_generation":
					evidence(m)["connection_generation"] = 999
				case "snapshot_false":
					evidence(m)["snapshot_applied"] = false
				case "sdk_error":
					evidence(m)["sdk_return_code"] = -2
				case "wrong_row_account":
					m["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)["account_id"] = "999999"
				}
			})
			pages := [][]byte{changed}
			if scenario == "changed_ticket" || scenario == "changed_sdk_id" || scenario == "conflicting_page" || scenario == "out_of_order" {
				first := editBody(t, raw, func(m map[string]any) {
					m["message_id"], m["status"] = "first", "partial"
					m["chunk"].(map[string]any)["is_last"] = false
				})
				last := editBody(t, raw, func(m map[string]any) {
					m["message_id"] = "last"
					m["chunk"].(map[string]any)["index"] = 1
					m["payload"].(map[string]any)["items"] = []any{}
					if scenario == "changed_ticket" {
						evidence(m)["ticket"] = "other"
					}
					if scenario == "changed_sdk_id" {
						evidence(m)["sdk_request_id"] = 999
					}
					if scenario == "conflicting_page" {
						m["chunk"].(map[string]any)["index"] = 0
					}
				})
				pages = [][]byte{last, first, first}
			}
			v := TerminalQuery{}
			err := r.renderObservation(&v, c, pages)
			switch scenario {
			case "empty", "out_of_order":
				if err != nil || v.State != "response_ready" || !v.ResponseValidated || v.RowCount == nil {
					t.Fatal(v, err)
				}
				if scenario == "empty" && *v.RowCount != 0 {
					t.Fatal(v)
				}
			case "incomplete", "failed":
				want := "collecting"
				if scenario == "failed" {
					want = "failed"
				}
				if err != nil || v.State != want || v.RowCount != nil || v.ResponseValidated {
					t.Fatal(v, err)
				}
			default:
				if err == nil || v.ResponseValidated || v.Positions != nil {
					t.Fatal("invalid response exposed", v, err)
				}
			}
			if v.Reviewed || v.BusinessPublished {
				t.Fatal("observation promoted to audit/business state")
			}
		})
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
