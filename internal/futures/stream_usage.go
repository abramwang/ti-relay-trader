package futures

import "errors"

var (
	ErrStreamCounters = errors.New("futures stream usage counters inconsistent")
	ErrStreamBacklog  = errors.New("futures stream unprocessed backlog capacity reached")
)

const (
	maxStreamBacklogCount = 50000
	maxStreamBacklogBytes = 64 * 1024 * 1024
)

// The rejected page was not committed. Keep only its capacity requirement,
// not an in-memory replacement for the original Redis messages.
type streamBackpressure struct {
	count, bytes int64
}

func (*streamBackpressure) Error() string { return ErrStreamBacklog.Error() }
func (*streamBackpressure) Unwrap() error { return ErrStreamBacklog }

// Completed history is retained but does not consume processing capacity.
// Quarantine remains unresolved; it is not an escape hatch for new ingestion.
func (s StreamStatus) validateUsage() error {
	count, size := s.Records, s.Bytes
	for _, v := range []struct{ count, size int64 }{
		{int64(s.Pending), s.PendingBytes},
		{int64(s.Quarantined), s.QuarantinedBytes},
		{int64(s.Delivered), s.DeliveredBytes},
	} {
		if v.count < 0 || v.size < 0 || v.count > count || v.size > size {
			return ErrStreamCounters
		}
		count -= v.count
		size -= v.size
	}
	if count != 0 || size != 0 {
		return ErrStreamCounters
	}
	return nil
}

func (s StreamStatus) admitCapture(count, size int64) error {
	if err := s.validateUsage(); err != nil {
		return err
	}
	if count < 0 || size < 0 {
		return ErrAuditCapacity
	}
	if int64(s.Pending)+int64(s.Quarantined) > maxStreamBacklogCount-count ||
		s.PendingBytes+s.QuarantinedBytes > maxStreamBacklogBytes-size {
		return ErrStreamBacklog
	}
	return nil
}
