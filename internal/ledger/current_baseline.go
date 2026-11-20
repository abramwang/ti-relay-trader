package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrCurrentBaselineInput       = errors.New("invalid current baseline input")
	ErrCurrentBaselineConflict    = errors.New("current baseline collection identity conflict")
	ErrCurrentBaselineUnavailable = errors.New("current baseline unavailable")
	ErrCurrentBaselineScope       = errors.New("current baseline scope changed")
	ErrCurrentBaselineCorrupt     = errors.New("current baseline integrity check failed")
	ErrCurrentBaselineLimit       = errors.New("current baseline size limit exceeded")
)

const currentBaselineMaxRows = 100000
const currentBaselineMaxBytes = 64 << 20

var currentBaselineResources = []string{"positions", "orders", "fills"}

type CurrentBaselineScope struct {
	Environment          string `json:"environment"`
	AccountID            string `json:"account_id"`
	BrokerID             string `json:"broker_id"`
	GatewayID            string `json:"gateway_id"`
	TradeDate            string `json:"trade_date"`
	CounterSessionID     string `json:"counter_session_id"`
	ConnectionGeneration *int64 `json:"connection_generation"`
}

// CurrentBaselineRecord is an observed, normalized row, not completeness proof.
// Key must be its stable resource identity; Document must be a JSON object.
type CurrentBaselineRecord struct {
	Key      string          `json:"key"`
	Document json.RawMessage `json:"document"`
}

type CurrentBaselineCandidate struct {
	Scope            CurrentBaselineScope               `json:"scope"`
	CollectionID     string                             `json:"collection_id"`
	StartedAt        time.Time                          `json:"collection_started_at"`
	CompletedAt      time.Time                          `json:"collection_completed_at"`
	Cash             json.RawMessage                    `json:"cash"`
	Queries          json.RawMessage                    `json:"queries"`
	Resources        map[string][]CurrentBaselineRecord `json:"resources"`
	Limitations      []CurrentBaselineLimitation        `json:"limitations"`
	RevisionEvidence *CurrentBaselineRevisionEvidence   `json:"revision_evidence,omitempty"`
	SourceHealth     *CurrentBaselineSourceHealth       `json:"source_health,omitempty"`
}

type CurrentBaselineResource struct {
	Completeness  string `json:"completeness"`
	TotalRows     int    `json:"total_rows"`
	ContentSHA256 string `json:"content_sha256"`
}

type CurrentBaselineConsistency struct {
	Level                  string `json:"level"`
	BrokerAtomic           bool   `json:"broker_atomic"`
	CoverageVerified       bool   `json:"coverage_verified"`
	QueryEffectsReconciled bool   `json:"query_effects_reconciled"`
}

type CurrentBaselineLimitation struct {
	Code      string   `json:"code"`
	Resource  string   `json:"resource"`
	RecordIDs []string `json:"record_ids"`
	Detail    string   `json:"detail"`
}

type CurrentBaselineManifest struct {
	SchemaVersion            string                             `json:"schema_version"`
	BaselineID               string                             `json:"baseline_id"`
	Scope                    CurrentBaselineScope               `json:"scope"`
	CollectionID             string                             `json:"collection_id"`
	PublicationGeneration    string                             `json:"publication_generation"`
	LedgerRevision           *string                            `json:"ledger_revision"`
	RevisionEvidence         *CurrentBaselineRevisionEvidence   `json:"revision_evidence,omitempty"`
	SourceHealth             *CurrentBaselineSourceHealth       `json:"source_health,omitempty"`
	StartedAt                time.Time                          `json:"collection_started_at"`
	CompletedAt              time.Time                          `json:"collection_completed_at"`
	PublishedAt              time.Time                          `json:"published_at"`
	ValidUntil               *time.Time                         `json:"valid_until"`
	Completeness             string                             `json:"completeness"`
	MissingSecurityMeansZero bool                               `json:"missing_security_means_zero"`
	Cash                     json.RawMessage                    `json:"cash"`
	Queries                  json.RawMessage                    `json:"queries"`
	Resources                map[string]CurrentBaselineResource `json:"resources"`
	Consistency              CurrentBaselineConsistency         `json:"consistency"`
	Limitations              []CurrentBaselineLimitation        `json:"limitations"`
}

type CurrentBaselinePage struct {
	BaselineID            string            `json:"baseline_id"`
	PublicationGeneration string            `json:"publication_generation"`
	LedgerRevision        *string           `json:"ledger_revision"`
	Resource              string            `json:"resource"`
	Offset                int               `json:"offset"`
	Count                 int               `json:"count"`
	TotalRows             int               `json:"total_rows"`
	Items                 []json.RawMessage `json:"items"`
	PageSHA256            string            `json:"page_sha256"`
	NextCursor            string            `json:"next_cursor"`
	PageComplete          bool              `json:"page_complete"`
}

// CurrentBaselineStore persists reference-only candidates. It deliberately has
// no query sender, event consumer, business verifier or execution-ready switch.
type CurrentBaselineStore struct{ db *sql.DB }

func NewCurrentBaselineStore(db *sql.DB) (*CurrentBaselineStore, error) {
	if db == nil {
		return nil, ErrCurrentBaselineInput
	}
	return &CurrentBaselineStore{db: db}, nil
}

func baselineHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func validBaselineText(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsRune(s, 0)
}

func (s CurrentBaselineScope) validate() error {
	if s.Environment != "test" && s.Environment != "production" {
		return ErrCurrentBaselineInput
	}
	for _, v := range []string{s.AccountID, s.BrokerID, s.GatewayID} {
		if !validBaselineText(v) {
			return ErrCurrentBaselineInput
		}
	}
	t, err := time.Parse("20060102", s.TradeDate)
	if err != nil || t.Format("20060102") != s.TradeDate {
		return ErrCurrentBaselineInput
	}
	if !validBaselineText(s.CounterSessionID) {
		return ErrCurrentBaselineInput
	}
	if s.ConnectionGeneration == nil || *s.ConnectionGeneration <= 0 {
		return ErrCurrentBaselineInput
	}
	return nil
}

func isBaselineResource(s string) bool {
	if s == "commands" {
		return true
	}
	for _, r := range currentBaselineResources {
		if r == s {
			return true
		}
	}
	return false
}

// Commands are optional so existing publications retain their exact identity.
func baselineResourceNames(resources map[string][]CurrentBaselineRecord) []string {
	if _, ok := resources["commands"]; ok {
		return []string{"positions", "orders", "fills", "commands"}
	}
	return currentBaselineResources
}

func baselineRowsJSON(rows []CurrentBaselineRecord) []byte {
	var b strings.Builder
	b.WriteByte('[')
	for i, r := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(r.Document)
	}
	b.WriteByte(']')
	return []byte(b.String())
}

func prepareCurrentBaseline(in CurrentBaselineCandidate) (CurrentBaselineCandidate, string, error) {
	if err := in.Scope.validate(); err != nil {
		return in, "", err
	}
	if !validBaselineText(in.CollectionID) || in.StartedAt.IsZero() || in.CompletedAt.Before(in.StartedAt) {
		return in, "", ErrCurrentBaselineInput
	}
	// Copy input before normalizing; a caller's slices and raw evidence stay unchanged.
	out := in
	if in.SourceHealth != nil {
		if in.SourceHealth.ContinuityVerified || !in.SourceHealth.ObservedAt.Equal(in.CompletedAt) {
			return in, "", ErrCurrentBaselineInput
		}
		raw, err := json.Marshal(in.SourceHealth)
		if err != nil {
			return in, "", err
		}
		if len(raw) > 1<<20 {
			return in, "", ErrCurrentBaselineLimit
		}
		var h CurrentBaselineSourceHealth
		if err = json.Unmarshal(raw, &h); err != nil {
			return in, "", err
		}
		out.SourceHealth = &h
	}
	if in.RevisionEvidence != nil {
		if !in.RevisionEvidence.valid() {
			return in, "", ErrCurrentBaselineInput
		}
		r := *in.RevisionEvidence
		out.RevisionEvidence = &r
	}
	if in.Scope.ConnectionGeneration != nil {
		n := *in.Scope.ConnectionGeneration
		out.Scope.ConnectionGeneration = &n
	}
	out.StartedAt, out.CompletedAt = in.StartedAt.UTC(), in.CompletedAt.UTC()
	out.Resources = make(map[string][]CurrentBaselineRecord, 3)
	size := len(in.Cash) + len(in.Queries)
	if size > 4<<20 {
		return out, "", ErrCurrentBaselineLimit
	}
	var err error
	if len(in.Cash) == 0 {
		out.Cash = json.RawMessage(`null`)
	} else {
		out.Cash, err = currentBaselineJSON(in.Cash)
	}
	if err != nil || (string(out.Cash) != "null" && out.Cash[0] != '{') {
		return out, "", ErrCurrentBaselineInput
	}
	if len(in.Queries) == 0 {
		out.Queries = json.RawMessage(`[]`)
	} else {
		out.Queries, err = currentBaselineJSON(in.Queries)
	}
	if err != nil || out.Queries[0] != '[' {
		return out, "", ErrCurrentBaselineInput
	}
	for r := range in.Resources {
		if !isBaselineResource(r) {
			return out, "", ErrCurrentBaselineInput
		}
	}
	for _, resource := range baselineResourceNames(in.Resources) {
		rows := in.Resources[resource]
		if len(rows) > currentBaselineMaxRows {
			return out, "", ErrCurrentBaselineLimit
		}
		out.Resources[resource] = make([]CurrentBaselineRecord, 0, len(rows))
		for _, r := range rows {
			size += len(r.Key) + len(r.Document)
			if size > currentBaselineMaxBytes {
				return out, "", ErrCurrentBaselineLimit
			}
			if !validBaselineText(r.Key) {
				return out, "", ErrCurrentBaselineInput
			}
			doc, err := currentBaselineJSON(r.Document)
			if err != nil || doc[0] != '{' {
				return out, "", ErrCurrentBaselineInput
			}
			out.Resources[resource] = append(out.Resources[resource], CurrentBaselineRecord{r.Key, doc})
		}
		sort.Slice(out.Resources[resource], func(i, j int) bool { return out.Resources[resource][i].Key < out.Resources[resource][j].Key })
		for i := 1; i < len(rows); i++ {
			if out.Resources[resource][i-1].Key == out.Resources[resource][i].Key {
				return out, "", ErrCurrentBaselineInput
			}
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, "", err
	}
	raw, err = currentBaselineJSON(raw)
	if err != nil {
		return out, "", err
	}
	if len(raw) > currentBaselineMaxBytes {
		return out, "", ErrCurrentBaselineLimit
	}
	return out, baselineHash(raw), nil
}

// PublishCandidate atomically stores all resources or none. Reusing a collection
// identity with different facts is a conflict, never an implicit correction.
func (s *CurrentBaselineStore) PublishCandidate(ctx context.Context, in CurrentBaselineCandidate) (CurrentBaselineManifest, error) {
	c, hash, err := prepareCurrentBaseline(in)
	if err != nil {
		return CurrentBaselineManifest{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CurrentBaselineManifest{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_baseline_heads(environment,account_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.Scope.Environment, c.Scope.AccountID); err != nil {
		return CurrentBaselineManifest{}, err
	}
	var gen int64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM account_baseline_heads WHERE environment=$1 AND account_id=$2 FOR UPDATE`, c.Scope.Environment, c.Scope.AccountID).Scan(&gen); err != nil {
		return CurrentBaselineManifest{}, err
	}
	var id, oldHash string
	err = tx.QueryRowContext(ctx, `SELECT baseline_id,candidate_sha256 FROM account_baseline_publications WHERE environment=$1 AND account_id=$2 AND collection_id=$3`, c.Scope.Environment, c.Scope.AccountID, c.CollectionID).Scan(&id, &oldHash)
	if err == nil {
		if oldHash != hash {
			return CurrentBaselineManifest{}, ErrCurrentBaselineConflict
		}
		m, err := readCurrentBaselineManifest(ctx, tx, c.Scope, id)
		if err != nil {
			return m, err
		}
		return m, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CurrentBaselineManifest{}, err
	}
	// Idempotent retries above return the original publication, even after its
	// revision has expired. Only a new publication must still match its source.
	if err = checkCurrentBaselineRevisionForPublication(ctx, tx, c.Scope.AccountID, c.RevisionEvidence); err != nil {
		return CurrentBaselineManifest{}, err
	}
	if c.SourceHealth != nil {
		ok, e := currentBaselineHealthTracking(ctx, tx)
		if e != nil {
			return CurrentBaselineManifest{}, e
		}
		if !ok || !c.SourceHealth.TrackingAvailable || c.RevisionEvidence == nil {
			return CurrentBaselineManifest{}, ErrCurrentBaselineTracking
		}
		if c.SourceHealth.ReceiptTrackingAvailable {
			_, ok, e = currentBaselineReceiptTracking(ctx, tx)
			if e != nil {
				return CurrentBaselineManifest{}, e
			}
			if !ok {
				return CurrentBaselineManifest{}, ErrCurrentBaselineTracking
			}
		}
	}
	gen++
	m := CurrentBaselineManifest{
		SchemaVersion: "relay.account_baseline.v1", BaselineID: "ab-" + hash, Scope: c.Scope, CollectionID: c.CollectionID,
		PublicationGeneration: strconv.FormatInt(gen, 10), StartedAt: c.StartedAt, CompletedAt: c.CompletedAt,
		PublishedAt: time.Now().UTC(), Completeness: "unavailable", Cash: c.Cash, Queries: c.Queries,
		Resources: map[string]CurrentBaselineResource{}, Consistency: CurrentBaselineConsistency{Level: "relay_published_view"},
		Limitations:  []CurrentBaselineLimitation{{"coverage_unverified", "account", []string{}, "Local revisions do not prove source completeness or query/event inclusion."}},
		SourceHealth: c.SourceHealth,
	}
	if c.RevisionEvidence != nil {
		m.RevisionEvidence = c.RevisionEvidence
		r := c.RevisionEvidence.AccountRevision
		m.LedgerRevision = &r
	}
	if string(c.Cash) != "null" {
		m.Completeness = "partial"
	}
	m.Limitations = append(m.Limitations, c.Limitations...)
	for _, r := range baselineResourceNames(c.Resources) {
		state := "unavailable"
		if len(c.Resources[r]) > 0 {
			state = "partial"
			m.Completeness = "partial"
		}
		m.Resources[r] = CurrentBaselineResource{state, len(c.Resources[r]), baselineHash(baselineRowsJSON(c.Resources[r]))}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	raw, err = currentBaselineJSON(raw)
	if err != nil {
		return m, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_baseline_publications(baseline_id,environment,account_id,broker_id,gateway_id,trade_date,collection_id,generation,candidate_sha256,manifest,manifest_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, m.BaselineID, c.Scope.Environment, c.Scope.AccountID, c.Scope.BrokerID, c.Scope.GatewayID, c.Scope.TradeDate, c.CollectionID, gen, hash, string(raw), baselineHash(raw))
	if err != nil {
		return m, err
	}
	for _, r := range baselineResourceNames(c.Resources) {
		for start := 0; start < len(c.Resources[r]); start += 500 {
			end := start + 500
			if end > len(c.Resources[r]) {
				end = len(c.Resources[r])
			}
			args := []any{m.BaselineID, r}
			values := make([]string, 0, end-start)
			for i := start; i < end; i++ {
				row := c.Resources[r][i]
				n := len(args) + 1
				values = append(values, fmt.Sprintf("($1,$2,$%d,$%d,$%d,$%d)", n, n+1, n+2, n+3))
				args = append(args, i, row.Key, string(row.Document), baselineHash(row.Document))
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO account_baseline_rows(baseline_id,resource,ordinal,record_key,document,document_sha256) VALUES `+strings.Join(values, ","), args...)
			if err != nil {
				return m, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_baseline_heads SET generation=$3 WHERE environment=$1 AND account_id=$2`, c.Scope.Environment, c.Scope.AccountID, gen); err != nil {
		return m, err
	}
	if err = tx.Commit(); err != nil {
		return CurrentBaselineManifest{}, err
	}
	return m, nil
}

type currentBaselineReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readCurrentBaselineManifest(ctx context.Context, q currentBaselineReader, scope CurrentBaselineScope, id string) (CurrentBaselineManifest, error) {
	m, err := readCurrentBaselineManifestForAccount(ctx, q, scope.Environment, scope.AccountID, id)
	if err != nil {
		return m, err
	}
	if !reflect.DeepEqual(m.Scope, scope) {
		return CurrentBaselineManifest{}, ErrCurrentBaselineScope
	}
	return m, nil
}

func readCurrentBaselineManifestForAccount(ctx context.Context, q currentBaselineReader, environment, account, id string) (CurrentBaselineManifest, error) {
	var m CurrentBaselineManifest
	var raw, hash, candidate, collection, broker, gateway, day string
	var gen int64
	err := q.QueryRowContext(ctx, `SELECT manifest,manifest_sha256,candidate_sha256,generation,collection_id,broker_id,gateway_id,trade_date FROM account_baseline_publications WHERE environment=$1 AND account_id=$2 AND baseline_id=$3`, environment, account, id).Scan(&raw, &hash, &candidate, &gen, &collection, &broker, &gateway, &day)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrCurrentBaselineUnavailable
	}
	if err != nil {
		return m, err
	}
	if baselineHash([]byte(raw)) != hash || json.Unmarshal([]byte(raw), &m) != nil || m.Scope.validate() != nil || m.BaselineID != id || id != "ab-"+candidate || m.PublicationGeneration != strconv.FormatInt(gen, 10) || m.CollectionID != collection || m.Scope.Environment != environment || m.Scope.AccountID != account || m.Scope.BrokerID != broker || m.Scope.GatewayID != gateway || m.Scope.TradeDate != day {
		return m, ErrCurrentBaselineCorrupt
	}
	if m.RevisionEvidence == nil {
		if m.LedgerRevision != nil {
			return CurrentBaselineManifest{}, ErrCurrentBaselineCorrupt
		}
	} else if !m.RevisionEvidence.valid() || m.LedgerRevision == nil || *m.LedgerRevision != m.RevisionEvidence.AccountRevision {
		return CurrentBaselineManifest{}, ErrCurrentBaselineCorrupt
	}
	if m.SourceHealth != nil && (m.SourceHealth.ContinuityVerified || !m.SourceHealth.TrackingAvailable || m.RevisionEvidence == nil || !m.SourceHealth.ObservedAt.Equal(m.CompletedAt)) {
		return CurrentBaselineManifest{}, ErrCurrentBaselineCorrupt
	}
	if m.SchemaVersion != "relay.account_baseline.v1" || m.MissingSecurityMeansZero || m.ValidUntil != nil || m.Consistency != (CurrentBaselineConsistency{Level: "relay_published_view"}) || (m.Completeness != "partial" && m.Completeness != "unavailable") || (len(m.Resources) != 3 && len(m.Resources) != 4) {
		return CurrentBaselineManifest{}, ErrCurrentBaselineCorrupt
	}
	for _, r := range currentBaselineResources {
		if _, ok := m.Resources[r]; !ok {
			return CurrentBaselineManifest{}, ErrCurrentBaselineCorrupt
		}
	}
	for r, x := range m.Resources {
		if !isBaselineResource(r) || x.TotalRows < 0 || x.TotalRows > currentBaselineMaxRows || len(x.ContentSHA256) != 64 || (x.Completeness != "partial" && x.Completeness != "unavailable") {
			return CurrentBaselineManifest{}, ErrCurrentBaselineCorrupt
		}
	}
	return m, nil
}

func (s *CurrentBaselineStore) Manifest(ctx context.Context, scope CurrentBaselineScope, id string) (CurrentBaselineManifest, error) {
	if err := scope.validate(); err != nil {
		return CurrentBaselineManifest{}, err
	}
	if !validBaselineText(id) {
		return CurrentBaselineManifest{}, ErrCurrentBaselineInput
	}
	return readCurrentBaselineManifest(ctx, s.db, scope, id)
}

// Latest does not skip a newer partial, failed or different-session observation.
func (s *CurrentBaselineStore) Latest(ctx context.Context, scope CurrentBaselineScope) (CurrentBaselineManifest, error) {
	if err := scope.validate(); err != nil {
		return CurrentBaselineManifest{}, err
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT baseline_id FROM account_baseline_publications WHERE environment=$1 AND account_id=$2 ORDER BY generation DESC LIMIT 1`, scope.Environment, scope.AccountID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return CurrentBaselineManifest{}, ErrCurrentBaselineUnavailable
	}
	if err != nil {
		return CurrentBaselineManifest{}, err
	}
	return s.Manifest(ctx, scope, id)
}

type currentBaselineCursor struct {
	BaselineID string `json:"baseline_id"`
	Resource   string `json:"resource"`
	Offset     int    `json:"offset"`
}

func (s *CurrentBaselineStore) Page(ctx context.Context, scope CurrentBaselineScope, id, resource, cursor string, limit int) (CurrentBaselinePage, error) {
	p := CurrentBaselinePage{Items: []json.RawMessage{}}
	if scope.validate() != nil || !validBaselineText(id) || !isBaselineResource(resource) || limit < 1 || limit > 500 || len(cursor) > 2048 {
		return p, ErrCurrentBaselineInput
	}
	offset := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var c currentBaselineCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.BaselineID != id || c.Resource != resource || c.Offset <= 0 {
			return p, ErrCurrentBaselineInput
		}
		canonical, _ := json.Marshal(c)
		if string(canonical) != string(raw) {
			return p, ErrCurrentBaselineInput
		}
		offset = c.Offset
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	m, err := readCurrentBaselineManifest(ctx, tx, scope, id)
	if err != nil {
		return p, err
	}
	summary, ok := m.Resources[resource]
	if !ok {
		return p, ErrCurrentBaselineUnavailable
	}
	total := summary.TotalRows
	if offset > 0 && offset >= total {
		return p, ErrCurrentBaselineInput
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM account_baseline_rows WHERE baseline_id=$1 AND resource=$2`, id, resource).Scan(&count); err != nil {
		return p, err
	}
	if count != total {
		return p, ErrCurrentBaselineCorrupt
	}
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,document,document_sha256 FROM account_baseline_rows WHERE baseline_id=$1 AND resource=$2 AND ordinal >= $3 ORDER BY ordinal LIMIT $4`, id, resource, offset, limit)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var doc, hash string
		if err = rows.Scan(&n, &doc, &hash); err != nil {
			return p, err
		}
		canonical, e := currentBaselineJSON([]byte(doc))
		if e != nil || string(canonical) != doc || baselineHash(canonical) != hash || n != offset+len(p.Items) {
			return CurrentBaselinePage{}, ErrCurrentBaselineCorrupt
		}
		p.Items = append(p.Items, json.RawMessage(doc))
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	if err = rows.Close(); err != nil {
		return p, err
	}
	expected := total - offset
	if expected > limit {
		expected = limit
	}
	if len(p.Items) != expected {
		return CurrentBaselinePage{}, ErrCurrentBaselineCorrupt
	}
	p.BaselineID, p.PublicationGeneration, p.Resource, p.Offset, p.Count, p.TotalRows = id, m.PublicationGeneration, resource, offset, len(p.Items), total
	p.LedgerRevision = m.LedgerRevision
	parts := make([]CurrentBaselineRecord, len(p.Items))
	for i, doc := range p.Items {
		parts[i].Document = doc
	}
	p.PageSHA256 = baselineHash(baselineRowsJSON(parts))
	p.PageComplete = offset+p.Count == total
	if offset == 0 && p.PageComplete && p.PageSHA256 != m.Resources[resource].ContentSHA256 {
		return CurrentBaselinePage{}, ErrCurrentBaselineCorrupt
	}
	if !p.PageComplete {
		raw, _ := json.Marshal(currentBaselineCursor{id, resource, offset + p.Count})
		p.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err = tx.Commit(); err != nil {
		return CurrentBaselinePage{}, err
	}
	return p, nil
}
