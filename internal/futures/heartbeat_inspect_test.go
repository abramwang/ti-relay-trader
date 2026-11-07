package futures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func inspectFixture(t *testing.T, route QueryRoute, bodies ...[]byte) (*fakeStreamReader, HeartbeatInspectionRequest) {
	t.Helper()
	req := HeartbeatInspectionRequest{route, "0-0", 32}
	key, err := req.Stream()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeStreamReader{info: &redis.XInfoStream{LastGeneratedID: "0-0", MaxDeletedEntryID: "0-0"}}
	if len(bodies) > 0 {
		messages := make([]redis.XMessage, 0, len(bodies))
		for i, body := range bodies {
			id := strconv.Itoa(i+1) + "-0"
			messages = append(messages, redis.XMessage{ID: id, Values: map[string]any{"body": string(body)}})
		}
		f.rows = []redis.XStream{{Stream: key, Messages: messages}}
		f.info.Length, f.info.EntriesAdded = int64(len(bodies)), int64(len(bodies))
		f.info.FirstEntry, f.info.LastGeneratedID = messages[0], messages[len(messages)-1].ID
	}
	f.read = func(args *redis.XReadArgs) {
		if args.Streams[0] != key || args.Streams[1] != req.After || args.Block != -1 {
			t.Fatal("unexpected read", args)
		}
	}
	return f, req
}

func inspectContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHeartbeatInspectionPreservesSequenceWithoutAuthority(t *testing.T) {
	g, at := guardFixture(t)
	good := guardBody(t, g, "first", at)
	down := editBody(t, good, func(m map[string]any) { m["payload"].(map[string]any)["broker_ready"] = false })
	foreign := editBody(t, good, func(m map[string]any) { m["routing"].(map[string]any)["account_id"] = "other" })
	bodies := [][]byte{good, down, {0xff, 0}, foreign, good}
	f, req := inspectFixture(t, g.scope.Route, bodies...)
	out, err := InspectTestHeartbeats(inspectContext(t), f, req)
	if err != nil || len(out.Records) != 5 || out.BusinessAuthorized || out.NextAfter != "5-0" || out.MoreAtObservedTail {
		t.Fatal(out, err)
	}
	for i, item := range out.Records {
		digest := sha256.Sum256(bodies[i])
		if item.BodySHA256 != hex.EncodeToString(digest[:]) || item.BodyBytes != len(bodies[i]) {
			t.Fatal("digest changed")
		}
		invalid := i == 2 || i == 3
		if (item.Classification == "invalid_heartbeat") != invalid || (item.Advertised == nil) != invalid {
			t.Fatal("invalid classified as ready")
		}
	}
	if out.Records[1].Advertised.BrokerReady || !out.Records[0].Advertised.ProducedAt.Equal(at) || *out.Records[0].Advertised.TradeDate != g.expected.Scope.TradeDate {
		t.Fatal("disconnected or source day lost")
	}
	if !errors.Is(g.Check(), ErrTestSessionMissing) {
		t.Fatal("diagnostics activated guard")
	}
	encoded, _ := json.Marshal(out)
	if !strings.Contains(string(encoded), "+08:00") || strings.Contains(string(encoded), `"raw"`) {
		t.Fatal("unsafe or ambiguous diagnostic output")
	}
}

func TestHeartbeatInspectionOCStage1NullAndRecovery(t *testing.T) {
	for _, sample := range runtimeHeartbeats(t) {
		f, req := inspectFixture(t, runtimeRoute(t, sample), []byte(sample.Body))
		out, err := InspectTestHeartbeats(inspectContext(t), f, req)
		if err != nil || out.BusinessAuthorized || len(out.Records) != 1 {
			t.Fatal(out, err)
		}
		item := out.Records[0]
		if item.Classification != "transport_recovery_only" || item.Advertised.TradeDate != nil || item.Advertised.RecoveryClear == nil {
			t.Fatal("stage1 identity fabricated")
		}
	}
}

func TestHeartbeatInspectionBoundsAndNoConnectionsOnInvalidInput(t *testing.T) {
	g, _ := guardFixture(t)
	f, req := inspectFixture(t, g.scope.Route)
	for _, mutate := range []func(*HeartbeatInspectionRequest){
		func(r *HeartbeatInspectionRequest) { r.Route.Env = "prod" },
		func(r *HeartbeatInspectionRequest) { r.Route.BrokerID = "huaxin" },
		func(r *HeartbeatInspectionRequest) { r.Route.GatewayID = "" },
		func(r *HeartbeatInspectionRequest) { r.Route.AccountID = "" },
		func(r *HeartbeatInspectionRequest) { r.After = "$" },
		func(r *HeartbeatInspectionRequest) { r.After = "01-0" },
		func(r *HeartbeatInspectionRequest) { r.Limit = 0 },
		func(r *HeartbeatInspectionRequest) { r.Limit = 257 },
	} {
		r := req
		mutate(&r)
		if _, err := InspectTestHeartbeats(inspectContext(t), f, r); err == nil {
			t.Fatal("invalid request accepted", r)
		}
	}
	if _, err := InspectTestHeartbeats(context.Background(), f, req); err == nil {
		t.Fatal("no deadline accepted")
	}
	long, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	if _, err := InspectTestHeartbeats(long, f, req); err == nil {
		t.Fatal("unbounded read")
	}
	cancelled, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if _, err := InspectTestHeartbeats(cancelled, f, req); err == nil {
		t.Fatal("cancelled read")
	}
	if f.infoCalls != 0 {
		t.Fatal("invalid input reached Redis")
	}
}

func TestHeartbeatInspectionEmptyPartialAndLoss(t *testing.T) {
	g, at := guardFixture(t)
	for _, scenario := range []string{"empty", "partial", "trim_before", "trim_during", "rewind", "invalid_shape", "metadata_error"} {
		t.Run(scenario, func(t *testing.T) {
			f, req := inspectFixture(t, g.scope.Route, guardBody(t, g, "one", at), guardBody(t, g, "two", at))
			switch scenario {
			case "empty":
				f, req = inspectFixture(t, g.scope.Route)
			case "partial":
				f.rows[0].Messages = f.rows[0].Messages[:1]
				req.Limit = 1
			case "trim_before":
				f.info.EntriesAdded++
			case "trim_during":
				f.onInfo = func(n int) {
					if n == 2 {
						f.info.MaxDeletedEntryID = "1-0"
					}
				}
			case "rewind":
				f.onInfo = func(n int) {
					if n == 2 {
						f.info.LastGeneratedID = "1-0"
					}
				}
			case "invalid_shape":
				f.rows[0].Messages[0].Values["extra"] = "invalid"
			case "metadata_error":
				f.err = errors.New("secret-credential-private-endpoint")
			}
			out, err := InspectTestHeartbeats(inspectContext(t), f, req)
			if scenario == "empty" || scenario == "partial" {
				if err != nil || out.BusinessAuthorized || out.MoreAtObservedTail != (scenario == "partial") {
					t.Fatal(out, err)
				}
			} else if err == nil || out.NextAfter != "" || len(out.Records) != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal("partial progress or sensitive error escaped", out, err)
			}
		})
	}
}

type inspectionRedisError string

func (e inspectionRedisError) Error() string { return string(e) }
func (e inspectionRedisError) RedisError()   {}

func TestHeartbeatInspectionClassifiedErrorsDoNotLeakServerText(t *testing.T) {
	for _, c := range []struct {
		err  error
		code string
	}{
		{inspectionRedisError("ERR no such key secret"), "stream_missing"},
		{inspectionRedisError("WRONGTYPE secret"), "wrong_stream_type"},
		{inspectionRedisError("NOAUTH secret"), "authentication_failed"},
		{inspectionRedisError("WRONGPASS secret"), "authentication_failed"},
		{inspectionRedisError("NOPERM secret"), "permission_denied"},
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "timeout"},
		{&net.DNSError{Err: "secret", IsTimeout: true}, "timeout"},
		{errors.New("secret"), "redis_unavailable"},
	} {
		err := inspectionReadError("read", c.err)
		if !strings.HasSuffix(err.Error(), c.code) || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}
