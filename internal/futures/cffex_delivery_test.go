package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cffexFactsDir = "../../testdata/oc_ctp_cffex_facts_review_20261119"
const cffexExecutionDir = "../../testdata/oc_ctp_cffex_execution_review_20261119"

type cffexBody struct {
	Body  string `json:"body"`
	Bytes int    `json:"body_bytes"`
	SHA   string `json:"body_sha256"`
}
type cffexCase struct {
	Scenario        string                          `json:"scenario"`
	Inputs          []struct{ Body, Stream string } `json:"inputs"`
	Outputs         []cffexBody                     `json:"outputs"`
	Encoded         map[string]cffexBody            `json:"encoded_bodies"`
	InsertCalls     int                             `json:"insert_calls"`
	CancelCalls     int                             `json:"cancel_calls"`
	InstrumentCalls int                             `json:"instrument_calls"`
	DepthCalls      int                             `json:"depth_calls"`
	SettlementCalls int                             `json:"settlement_calls"`
	Pending         int                             `json:"pending"`
	NativeSubmits   []struct {
		Exchange, Symbol, Offset, Side, Price string
		Qty                                   int
	} `json:"native_submits"`
}

func cffexRead(t *testing.T, dir, name string, dst any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatal(err)
	}
}
func cffexCases(t *testing.T) []cffexCase {
	t.Helper()
	var d struct {
		Synthetic bool        `json:"synthetic_only"`
		SDK       bool        `json:"real_sdk_calls"`
		Redis     bool        `json:"real_redis"`
		Cases     []cffexCase `json:"cases"`
	}
	cffexRead(t, cffexExecutionDir, "execution_examples.json", &d)
	if !d.Synthetic || d.SDK || d.Redis || len(d.Cases) != 6 {
		t.Fatal("incorrect fixture boundary")
	}
	return d.Cases
}
func cffexScope() CommandScope {
	return CommandScope{QueryRoute{"test", "ctp_4900", "300052141", "300052141"}, "4900", "300052141", "300052141"}
}
func cffexTradeScope() TradeScope {
	c := cffexScope()
	return TradeScope{c.Route, c.NativeBrokerID, c.InvestorID, "20261117"}
}
func cffexOriginal(t *testing.T, v cffexBody) []byte {
	t.Helper()
	b := []byte(v.Body)
	if len(b) != v.Bytes || commandDigest(b) != v.SHA {
		t.Fatal("original body changed")
	}
	return b
}

// These are synthetic fixture preconditions, never proof of an online session.
func cffexTarget(t *testing.T, raw []byte) TestQueryTarget {
	t.Helper()
	var v struct {
		Payload struct {
			Session string `json:"counter_session_id"`
			Day     string `json:"trade_date"`
		}
		Adapter struct {
			Evidence struct {
				Session string `json:"counter_session_id"`
			} `json:"query_evidence"`
		} `json:"adapter_context"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	r := publicationTarget()
	if v.Payload.Session == "" {
		v.Payload.Session = v.Adapter.Evidence.Session
	}
	r.CounterSessionID, r.TradeDate = v.Payload.Session, v.Payload.Day
	r.ConnectionGeneration = 1
	return r
}
func cffexCommands(t *testing.T, v cffexCase) map[string]CommandRegistration {
	t.Helper()
	out := map[string]CommandRegistration{}
	for _, input := range v.Inputs {
		raw := []byte(input.Body)
		role := input.Stream[strings.LastIndex(input.Stream, ":")+1:]
		command, err := DecodeCommand(raw, cffexScope(), role)
		if err != nil {
			// The bad-price scenario intentionally cannot enter Relay's registry.
			o, e := parseReply(raw)
			var p rawObject
			if e != nil || json.Unmarshal(o["payload"], &p) != nil || textField(p, "limit_price") != "not-a-price" {
				t.Fatal(err)
			}
			continue
		}
		if old, ok := out[command.MessageID]; ok {
			if !bytes.Equal(old.Original.Raw, raw) {
				t.Fatal("fixture command collision")
			}
			continue
		}
		out[command.MessageID] = CommandRegistration{CommandID: int64(len(out) + 1), Scope: cffexScope(), Original: command}
	}
	return out
}
func TestCFFEXDeliveryDigests(t *testing.T) {
	for dir, count := range map[string]int{cffexFactsDir: 4, cffexExecutionDir: 9} {
		b, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) != count {
			t.Fatal("inventory count")
		}
		for _, line := range lines {
			f := strings.Fields(line)
			if len(f) != 2 || filepath.Base(f[1]) != f[1] {
				t.Fatal("manifest")
			}
			raw, err := os.ReadFile(filepath.Join(dir, f[1]))
			if err != nil || commandDigest(raw) != f[0] {
				t.Fatal(f[1], err)
			}
		}
	}
}
func TestCFFEXDeliveryFacts(t *testing.T) {
	var v struct {
		Inputs     []struct{ Body string }
		Outputs    []cffexBody
		TradeCalls int  `json:"trade_calls"`
		Synthetic  bool `json:"synthetic_only"`
		SDK        bool `json:"real_sdk_calls"`
	}
	cffexRead(t, cffexFactsDir, "native_facts_example.json", &v)
	if !v.Synthetic || v.SDK || v.TradeCalls != 0 || len(v.Inputs) != 1 || len(v.Outputs) != 1 {
		t.Fatal("fixture scope")
	}
	c, err := DecodeCommand([]byte(v.Inputs[0].Body), cffexScope(), "cmd.query")
	if err != nil {
		t.Fatal(err)
	}
	raw := cffexOriginal(t, v.Outputs[0])
	r := CommandRegistration{CommandID: 1, Scope: cffexScope(), Original: c}
	got := assembleReplyResult(r, cffexTarget(t, raw), [][]byte{raw, raw})
	if got.State != "ready" || got.Snapshot == nil || len(got.Snapshot.Constraints) != 1 || got.NativeAuditVerified || got.OrderStateChanged {
		t.Fatal(got)
	}
	f := got.Snapshot.Constraints[0]
	if f.Exchange != "CFFEX" || f.Symbol != "IF2701" || f.Dynamic.BidPrice1 != nil || f.Dynamic.AskPrice1 != nil {
		t.Fatal("facts rewritten", f)
	}
	prod := cffexScope()
	prod.Route.Env = "prod"
	if _, err := DecodeCommand(c.Raw, prod, "cmd.query"); err == nil {
		t.Fatal("TEST action enabled in production")
	}
}
func TestCFFEXDeliveryRuntime(t *testing.T) {
	total, inputs, selected, fills := 0, 0, 0, 0
	for _, v := range cffexCases(t) {
		t.Run(v.Scenario, func(t *testing.T) {
			commands := cffexCommands(t, v)
			inputs += len(v.Inputs)
			if v.InstrumentCalls != 0 || v.DepthCalls != 0 || v.SettlementCalls != 0 || v.Pending != 0 || v.InsertCalls != 8 {
				t.Fatal("unexpected implicit calls", v.Scenario)
			}
			for _, n := range v.NativeSubmits {
				if n.Exchange != "CFFEX" || n.Symbol != "IF2701" || n.Price != "3500.21" || n.Qty != 101 || !oneOf(n.Offset, "0", "1", "3", "4") || !oneOf(n.Side, "0", "1") {
					t.Fatal("native terms rewritten", n)
				}
			}
			projection := newTradeProjection(cffexTradeScope())
			for i, b := range v.Outputs {
				raw := cffexOriginal(t, b)
				total++
				o, err := parseReply(raw)
				if err != nil {
					t.Fatal(err)
				}
				origin := textField(o, "origin_message_id")
				c, known := commands[origin]
				if !known {
					var a rawObject
					if origin != "offline-cmd.trade-102" || textField(o, "status") != "failed" || json.Unmarshal(o["adapter_context"], &a) != nil || !literal(a["sdk_calls"], "0") {
						t.Fatal("unmatched output", i)
					}
					continue
				}
				target := cffexTarget(t, raw)
				if textField(o, "message_type") == "reply" {
					got := assembleReplyResult(c, target, [][]byte{raw, raw})
					want := textField(o, "status")
					if want == "completed" {
						want = "ready"
					}
					if got.State != want || !got.ResponseValidated || got.NativeAuditVerified || got.OrderStateChanged || got.RetrySafe {
						t.Fatalf("output %d: %+v", i, got)
					}
					if got.Snapshot != nil {
						fills += len(got.Snapshot.Fills)
					}
				} else if textField(o, "action") == "order.cancel" {
					got, err := DecodeCancelRejection(raw, CancelRejectionContext{cffexScope(), c.Original.Raw, target.CounterSessionID, 1})
					if err != nil || got.OrderStateChanged || got.RetrySafe {
						t.Fatal("cancel rejection", err)
					}
				} else {
					e, err := DecodeTradeEvent(raw, EventContext{cffexTradeScope(), &origin})
					if err != nil || !bytes.Equal(e.Raw, raw) {
						t.Fatal("event", i, err)
					}
					if e.Order != nil {
						projection.order(*e.Order)
					} else if e.Fill != nil {
						projection.fill(fmt.Sprint(i), *e.Fill)
						fills++
					} else {
						t.Fatal("missing event")
					}
				}
			}
			projection.reconcile()
			if projection.report.Blocked || projection.report.PendingCount != 0 {
				t.Fatal(projection.report)
			}
			for _, b := range v.Encoded {
				raw := cffexOriginal(t, b)
				selected++
				o, err := parseReply(raw)
				if err != nil {
					t.Fatal(err)
				}
				if textField(o, "message_type") == "heartbeat" {
					h, err := DecodeHeartbeat(raw, cffexScope().Route)
					if err != nil || !h.Assessment.relayExecution() {
						t.Fatal("heartbeat", err)
					}
				}
			}
		})
	}
	if total != 215 || inputs != 117 || selected != 21 || fills != 32 {
		t.Fatal(total, inputs, selected, fills)
	}
}
