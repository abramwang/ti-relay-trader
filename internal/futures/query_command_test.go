package futures

import (
	"testing"
	"time"
)

func TestQueryCommandBindingModelValidationAndCopies(t *testing.T) {
	c := batchContext("positions")
	c.NativeInvestorID = "FIXTURE_INV"
	c.NativeUserID = "FIXTURE_USER"
	c.Command = &QueryCommandBinding{1, "request", "correlation", "idempotency"}
	b, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
	if err != nil {
		t.Fatal(err)
	}
	c.Command.RequestID = "mutated"
	if b.context.Command.RequestID != "request" {
		t.Fatal("mutable external binding")
	}
	for _, change := range []func(*QueryContext){
		func(c *QueryContext) { c.Command.CommandID = 0 },
		func(c *QueryContext) { c.Command.RequestID = "" },
		func(c *QueryContext) { c.Command.CorrelationID = "" },
		func(c *QueryContext) { c.Command.IdempotencyKey = "" },
		func(c *QueryContext) { c.NativeUserID = "" },
		func(c *QueryContext) { c.NativeInvestorID = "" },
	} {
		bad := copyContext(b.context)
		change(&bad)
		if _, err := NewQueryBatch(bad, 0, time.Minute, DefaultBatchLimits()); err == nil {
			t.Fatal("bad binding accepted")
		}
	}
}
