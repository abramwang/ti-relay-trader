package futures

import "context"

// A decoded historical message is not current readiness. Raw receipts remain
// pending/quarantined until a separate durable lifecycle consumer owns them.
type HeartbeatAuditRecord struct {
	Receipt     StreamRecord
	Decoded     *Heartbeat
	DecodeError string
}

// HeartbeatAudit reads every receipt in order, including quarantined/invalid
// messages. It neither observes a session guard nor restores freshness after a
// restart. A page failure returns no partial evidence or advanced scan cursor.
func (s *StreamStore) HeartbeatAudit(ctx context.Context, h StreamHandle, after int64, limit int) ([]HeartbeatAuditRecord, error) {
	if h.config.Role != "hb" {
		return nil, ErrStreamIdentity
	}
	records, err := s.streamRecords(ctx, h, after, limit, false)
	if err != nil {
		return nil, err
	}
	out := make([]HeartbeatAuditRecord, 0, len(records))
	for _, record := range records {
		item := HeartbeatAuditRecord{Receipt: record}
		decoded, err := DecodeHeartbeat(record.Raw, h.config.Scope.Route)
		if err != nil {
			item.DecodeError = err.Error()
		} else {
			item.Decoded = &decoded
		}
		out = append(out, item)
	}
	return out, nil
}
