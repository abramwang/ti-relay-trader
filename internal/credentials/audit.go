package credentials

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type AuditEvent struct {
	OperationID       string
	Environment       string
	BrokerID          string
	AccountID         string
	Action            string
	Status            string
	CredentialVersion *int64
	KeyID             string
	EnvelopeSHA256    string
	Operator          string
	ErrorCode         string
	Metadata          map[string]any
	CreatedAt         time.Time
}

type AuditStore interface {
	AppendCredentialAudit(ctx context.Context, event AuditEvent) error
}

type SQLAuditStore struct {
	db *sql.DB
}

func NewSQLAuditStore(db *sql.DB) *SQLAuditStore {
	return &SQLAuditStore{db: db}
}

func (store *SQLAuditStore) AppendCredentialAudit(ctx context.Context, event AuditEvent) error {
	if store == nil || store.db == nil {
		return errors.New("credential audit database is not configured")
	}
	metadata := event.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return errors.New("encode credential audit metadata")
	}
	_, err = store.db.ExecContext(ctx, `
INSERT INTO oc_credential_audit (
    operation_id,
    environment,
    broker_id,
    account_id,
    action,
    status,
    credential_version,
    key_id,
    envelope_sha256,
    operator,
    error_code,
    metadata,
    created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13)
`, event.OperationID, event.Environment, event.BrokerID, event.AccountID, event.Action, event.Status,
		event.CredentialVersion, event.KeyID, event.EnvelopeSHA256, event.Operator, event.ErrorCode,
		string(metadataJSON), event.CreatedAt)
	if err != nil {
		return errors.New("write credential audit record")
	}
	return nil
}
