package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const MaxResearchCostVersionBytes = 16 * 1024 * 1024

// ResearchCostVersion archives a complete research chain, never a current NAV.
// Payload is text so PostgreSQL cannot normalize the bytes covered by its digest.
type ResearchCostVersion struct {
	AccountID     string
	VersionID     string
	AnchorDate    string
	DateFrom      string
	DateTo        string
	PayloadSHA256 string
	Payload       []byte
	CreatedAt     time.Time
}

func validateResearchCostVersion(v ResearchCostVersion) error {
	for _, s := range []string{v.AccountID, v.VersionID} {
		if s == "" || strings.TrimSpace(s) != s || len(s) > 160 || strings.ContainsRune(s, 0) {
			return fmt.Errorf("%w: invalid research version identity", ErrInvalidLedgerInput)
		}
	}
	for _, s := range []string{v.AnchorDate, v.DateFrom, v.DateTo} {
		if d, err := time.Parse("2006-01-02", s); err != nil || d.Format("2006-01-02") != s {
			return fmt.Errorf("%w: invalid research version date", ErrInvalidLedgerInput)
		}
	}
	if v.AnchorDate >= v.DateFrom || v.DateFrom > v.DateTo || len(v.Payload) < 2 || len(v.Payload) > MaxResearchCostVersionBytes || !json.Valid(v.Payload) || strings.TrimSpace(string(v.Payload))[0] != '{' {
		return fmt.Errorf("%w: invalid research version coverage/payload", ErrInvalidLedgerInput)
	}
	h := sha256.Sum256(v.Payload)
	if v.PayloadSHA256 != hex.EncodeToString(h[:]) {
		return fmt.Errorf("%w: research version digest mismatch", ErrInvalidLedgerInput)
	}
	return nil
}

// AppendResearchCostVersion inserts the whole chain in one statement. A failed
// transaction leaves no partial days. Replays must match every byte and scope.
func (repo *Repository) AppendResearchCostVersion(ctx context.Context, v ResearchCostVersion) (ResearchCostVersion, error) {
	if err := validateResearchCostVersion(v); err != nil {
		return ResearchCostVersion{}, err
	}
	if repo == nil || repo.exec == nil {
		return ResearchCostVersion{}, ErrInvalidLedgerInput
	}
	_, err := repo.exec.ExecContext(ctx, `INSERT INTO performance_research_cost_versions
	(account_id,version_id,anchor_date,date_from,date_to,payload_sha256,payload_text)
	VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(account_id,version_id) DO NOTHING`,
		v.AccountID, v.VersionID, v.AnchorDate, v.DateFrom, v.DateTo, v.PayloadSHA256, string(v.Payload))
	if err != nil {
		return ResearchCostVersion{}, fmt.Errorf("append research cost version: %w", err)
	}
	stored, err := repo.GetResearchCostVersion(ctx, v.AccountID, v.VersionID)
	if err != nil {
		return ResearchCostVersion{}, err
	}
	if stored.AnchorDate != v.AnchorDate || stored.DateFrom != v.DateFrom || stored.DateTo != v.DateTo || stored.PayloadSHA256 != v.PayloadSHA256 || string(stored.Payload) != string(v.Payload) {
		return ResearchCostVersion{}, fmt.Errorf("%w: research version identity conflict", ErrInvalidLedgerInput)
	}
	return stored, nil
}

// No latest/current fallback: callers pin an account and an explicit version.
// Selecting an older version for research does not rewrite financial history.
func (repo *Repository) GetResearchCostVersion(ctx context.Context, accountID, versionID string) (ResearchCostVersion, error) {
	q, err := repo.queryer()
	if err != nil {
		return ResearchCostVersion{}, err
	}
	rows, err := q.QueryContext(ctx, `SELECT account_id,version_id,anchor_date::text,date_from::text,date_to::text,payload_sha256,payload_text,created_at
	FROM performance_research_cost_versions WHERE account_id=$1 AND version_id=$2`, accountID, versionID)
	if err != nil {
		return ResearchCostVersion{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ResearchCostVersion{}, err
		}
		return ResearchCostVersion{}, sql.ErrNoRows
	}
	var v ResearchCostVersion
	if err := rows.Scan(&v.AccountID, &v.VersionID, &v.AnchorDate, &v.DateFrom, &v.DateTo, &v.PayloadSHA256, &v.Payload, &v.CreatedAt); err != nil {
		return ResearchCostVersion{}, err
	}
	if err := validateResearchCostVersion(v); err != nil {
		return ResearchCostVersion{}, err
	}
	return v, nil
}
