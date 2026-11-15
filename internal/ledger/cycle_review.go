package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CycleReviewVersion struct {
	AccountID       string    `json:"account_id"`
	VersionID       string    `json:"version_id"`
	DateFrom        string    `json:"date_from"`
	ObservedThrough string    `json:"observed_through"`
	PayloadSHA256   string    `json:"payload_sha256"`
	Payload         []byte    `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
}

func validateCycleReviewVersion(v CycleReviewVersion) error {
	for _, s := range []string{v.AccountID, v.VersionID} {
		if s == "" || strings.TrimSpace(s) != s || len(s) > 160 || strings.ContainsRune(s, 0) {
			return ErrInvalidLedgerInput
		}
	}
	for _, s := range []string{v.DateFrom, v.ObservedThrough} {
		d, e := time.Parse("2006-01-02", s)
		if e != nil || d.Format("2006-01-02") != s {
			return ErrInvalidLedgerInput
		}
	}
	if v.DateFrom > v.ObservedThrough || len(v.Payload) < 2 || len(v.Payload) > 2<<20 || !json.Valid(v.Payload) || strings.TrimSpace(string(v.Payload))[0] != '{' || fmt.Sprintf("%x", sha256.Sum256(v.Payload)) != v.PayloadSHA256 {
		return ErrInvalidLedgerInput
	}
	return nil
}

func (r *Repository) AppendCycleReviewVersion(ctx context.Context, v CycleReviewVersion) (CycleReviewVersion, error) {
	if err := validateCycleReviewVersion(v); err != nil {
		return CycleReviewVersion{}, err
	}
	_, err := r.exec.ExecContext(ctx, `INSERT INTO performance_cycle_reviews(account_id,version_id,date_from,observed_through,payload_sha256,payload_text) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(account_id,version_id) DO NOTHING`, v.AccountID, v.VersionID, v.DateFrom, v.ObservedThrough, v.PayloadSHA256, string(v.Payload))
	if err != nil {
		return CycleReviewVersion{}, err
	}
	saved, err := r.GetCycleReviewVersion(ctx, v.AccountID, v.VersionID)
	if err == nil && (saved.DateFrom != v.DateFrom || saved.ObservedThrough != v.ObservedThrough || saved.PayloadSHA256 != v.PayloadSHA256 || string(saved.Payload) != string(v.Payload)) {
		return CycleReviewVersion{}, fmt.Errorf("%w: cycle review version conflict", ErrInvalidLedgerInput)
	}
	return saved, err
}

func (r *Repository) GetCycleReviewVersion(ctx context.Context, account, version string) (CycleReviewVersion, error) {
	q, err := r.queryer()
	if err != nil {
		return CycleReviewVersion{}, err
	}
	rows, err := q.QueryContext(ctx, `SELECT account_id,version_id,date_from::text,observed_through::text,payload_sha256,payload_text,created_at FROM performance_cycle_reviews WHERE account_id=$1 AND ($2='' OR version_id=$2) ORDER BY review_pk DESC LIMIT 1`, account, version)
	if err != nil {
		return CycleReviewVersion{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return CycleReviewVersion{}, err
		}
		return CycleReviewVersion{}, sql.ErrNoRows
	}
	var v CycleReviewVersion
	if err := rows.Scan(&v.AccountID, &v.VersionID, &v.DateFrom, &v.ObservedThrough, &v.PayloadSHA256, &v.Payload, &v.CreatedAt); err != nil {
		return v, err
	}
	return v, validateCycleReviewVersion(v)
}
