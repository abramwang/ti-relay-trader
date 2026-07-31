package redisstream

import (
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestConsumerGroupProbesPreservesPELAndLag(t *testing.T) {
	groups := []redis.XInfoGroup{
		{
			Name:            "oc-trade",
			Consumers:       2,
			Pending:         3,
			LastDeliveredID: "1785482796209-0",
			EntriesRead:     42,
			Lag:             1,
		},
	}
	want := []ConsumerGroupProbe{
		{
			Name:            "oc-trade",
			Consumers:       2,
			Pending:         3,
			LastDeliveredID: "1785482796209-0",
			EntriesRead:     42,
			Lag:             1,
		},
	}
	if got := consumerGroupProbes(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("consumerGroupProbes() = %#v, want %#v", got, want)
	}
}

func TestConsumerGroupProbesHandlesNoGroups(t *testing.T) {
	if got := consumerGroupProbes(nil); got != nil {
		t.Fatalf("consumerGroupProbes(nil) = %#v, want nil", got)
	}
}

func TestPendingEntryProbesPreservesDeliveryEvidence(t *testing.T) {
	entries := []redis.XPendingExt{
		{
			ID:         "1785481811217-0",
			Consumer:   "oc-query-1",
			Idle:       2500 * time.Millisecond,
			RetryCount: 4,
		},
	}
	want := []PendingEntryProbe{
		{
			ID:               "1785481811217-0",
			Consumer:         "oc-query-1",
			IdleMilliseconds: 2500,
			DeliveryCount:    4,
		},
	}
	if got := pendingEntryProbes(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingEntryProbes() = %#v, want %#v", got, want)
	}
}
