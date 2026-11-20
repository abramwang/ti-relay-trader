package ledger

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrLedgerWriterBusy = errors.New("ledger stream already has an active writer")

// All work must use the pinned connection, not the pool: losing its session
// loses both the advisory locks and the ability to continue business writes.
type LedgerStreamWriter struct {
	conn *sql.Conn
	keys []string
}

func (repo *Repository) AcquireStreamWriter(ctx context.Context, keys []string, durable bool) (*LedgerStreamWriter, error) {
	if repo == nil {
		return nil, ErrInvalidLedgerInput
	}
	db, ok := repo.exec.(*sql.DB)
	if !ok || len(keys) == 0 || len(keys) > 128 {
		return nil, ErrInvalidLedgerInput
	}
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	for i, key := range keys {
		if !validBaselineText(key) || !strings.HasPrefix(key, "relay:") || (i > 0 && keys[i-1] == key) {
			return nil, ErrInvalidLedgerInput
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	w := &LedgerStreamWriter{conn: conn}
	fail := func(e error) (*LedgerStreamWriter, error) { w.Close(); return nil, e }
	for _, key := range keys {
		var held bool
		if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended('relay.ledger-writer:' || $1,0))`, key).Scan(&held); err != nil {
			return fail(err)
		}
		if !held {
			return fail(ErrLedgerWriterBusy)
		}
		w.keys = append(w.keys, key)
	}
	var exists bool
	if err = conn.QueryRowContext(ctx, `SELECT to_regclass('ledger_stream_receivers') IS NOT NULL`).Scan(&exists); err != nil {
		return fail(err)
	}
	if durable && !exists {
		return fail(ErrLedgerCaptureBinding)
	}
	if !durable && exists {
		for _, key := range keys {
			var registered bool
			if err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ledger_stream_receivers WHERE stream_key=$1)`, key).Scan(&registered); err != nil {
				return fail(err)
			}
			if registered {
				return fail(ErrLedgerCaptureBinding)
			}
		}
	}
	return w, nil
}

func (w *LedgerStreamWriter) Repository() *Repository       { return NewRepository(w.conn) }
func (w *LedgerStreamWriter) Receipts() *LedgerReceiptStore { return &LedgerReceiptStore{db: w.conn} }

func (w *LedgerStreamWriter) Check(ctx context.Context) error { return w.conn.PingContext(ctx) }

func (w *LedgerStreamWriter) Close() {
	if w.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, key := range w.keys {
		var released bool
		if err := w.conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended('relay.ledger-writer:' || $1,0))`, key).Scan(&released); err != nil || !released {
			// Never return a possibly locked session to the connection pool.
			_ = w.conn.Raw(func(any) error { return driver.ErrBadConn })
			break
		}
	}
	_ = w.conn.Close()
	w.conn = nil
}

func lockLedgerStreamTransition(ctx context.Context, tx *sql.Tx, key string) error {
	var held bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('relay.ledger-writer:' || $1,0))`, key).Scan(&held); err != nil {
		return err
	}
	if !held {
		return ErrLedgerWriterBusy
	}
	return nil
}

// Attach never registers a new source or resets a cursor. The manifest's owner
// must already exist; a previous owner cannot regain control on restart.
func (s *LedgerReceiptStore) Attach(ctx context.Context, b LedgerStreamBinding, owner string) (LedgerStreamHandle, error) {
	key, err := b.Stream()
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	h := LedgerStreamHandle{binding: b, stream: key, owner: owner}
	st, err := s.Status(ctx, h)
	if err != nil {
		return h, err
	}
	if st.BlockedReason != "" {
		return h, ErrLedgerCaptureBlocked
	}
	if _, err = s.Receipt(ctx, h, "bootstrap-v1"); err != nil {
		return h, err
	}
	return h, nil
}
