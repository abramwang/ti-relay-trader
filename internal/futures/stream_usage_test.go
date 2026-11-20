package futures

import (
	"errors"
	"math"
	"testing"
)

func TestStreamUsageSeparatesHistoryAndBacklog(t *testing.T) {
	s := StreamStatus{Records: 100000, Bytes: 128 << 20, Delivered: 100000, DeliveredBytes: 128 << 20}
	if err := s.admitCapture(1, 1024); err != nil {
		t.Fatal("completed history consumed processing capacity", err)
	}
	for _, s := range []StreamStatus{
		{Records: maxStreamBacklogCount, Pending: maxStreamBacklogCount},
		{Records: maxStreamBacklogCount, Quarantined: maxStreamBacklogCount},
		{Records: 2, Pending: 1, Quarantined: 1, Bytes: maxStreamBacklogBytes,
			PendingBytes: 1, QuarantinedBytes: maxStreamBacklogBytes - 1},
	} {
		if err := s.validateUsage(); err != nil {
			t.Fatal("full backlog must remain inspectable", err)
		}
		if err := s.admitCapture(1, 1); !errors.Is(err, ErrStreamBacklog) {
			t.Fatal(s, err)
		}
	}
	for _, s := range []StreamStatus{
		{Records: -1}, {Bytes: -1}, {Pending: -1}, {PendingBytes: -1},
		{Records: 2, Pending: 1}, {Records: 1, Pending: 1, Bytes: 2, PendingBytes: 1},
		{Records: math.MaxInt64, Pending: math.MaxInt, Quarantined: 1},
		{Bytes: math.MaxInt64, PendingBytes: math.MaxInt64, QuarantinedBytes: 1},
	} {
		if err := s.validateUsage(); !errors.Is(err, ErrStreamCounters) {
			t.Fatal(s, err)
		}
	}
	for _, v := range [][2]int64{{-1, 0}, {0, -1}} {
		if err := (StreamStatus{}).admitCapture(v[0], v[1]); !errors.Is(err, ErrAuditCapacity) {
			t.Fatal(v, err)
		}
	}
	if err := (StreamStatus{}).admitCapture(math.MaxInt64, math.MaxInt64); !errors.Is(err, ErrStreamBacklog) {
		t.Fatal(err)
	}
}
