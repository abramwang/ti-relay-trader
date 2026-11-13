package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/timeutil"
)

const t0AllocationSource = "relay.t0_allocation.v1"
const t0AllocationDateSource = "relay.t0_allocation.v2"

// T0Allocation is an immutable revision, not a replacement for an OC order.
// Quantity describes the entire executed quantity of the referenced order.
type T0Allocation struct {
	LinkID         string    `json:"link_id"`
	AccountID      string    `json:"account_id"`
	TradeDate      string    `json:"trade_date"`
	GatewayOrderID string    `json:"gateway_order_id"`
	SecurityID     string    `json:"security_id"`
	TradeSide      string    `json:"trade_side"`
	Quantity       int64     `json:"quantity"`
	GroupID        string    `json:"group_id"`
	Role           string    `json:"role"`
	Revision       int       `json:"revision"`
	Supersedes     string    `json:"supersedes,omitempty"`
	Status         string    `json:"status"`
	EvidenceType   string    `json:"evidence_type"`
	EvidenceRef    string    `json:"evidence_ref"`
	ConfirmedBy    string    `json:"confirmed_by"`
	ConfirmedAt    time.Time `json:"confirmed_at"`
	ConfirmedDate  string    `json:"confirmed_date,omitempty"`
}

// Date-only evidence must not acquire a fabricated midnight/event timestamp.
func (item T0Allocation) MarshalJSON() ([]byte, error) {
	type plain T0Allocation
	var at *time.Time
	if !item.ConfirmedAt.IsZero() {
		at = &item.ConfirmedAt
	}
	return json.Marshal(struct {
		*plain
		ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	}{(*plain)(&item), at})
}

func t0AllocationStorageSource(item T0Allocation) string {
	if item.ConfirmedDate != "" {
		return t0AllocationDateSource
	}
	return t0AllocationSource
}

func NormalizeT0Allocation(item T0Allocation) (T0Allocation, error) {
	date, err := normalizeTradeDate(item.TradeDate)
	if err != nil {
		return item, err
	}
	item.TradeDate = date
	for _, value := range []string{item.LinkID, item.AccountID, item.GatewayOrderID, item.GroupID, item.EvidenceRef, item.ConfirmedBy} {
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) {
			return item, fmt.Errorf("%w: missing or invalid T0 allocation identity/evidence", ErrInvalidLedgerInput)
		}
	}
	parts := strings.Split(item.SecurityID, ".")
	if len(parts) != 2 || len(parts[0]) != 6 || (parts[1] != "SH" && parts[1] != "SZ") {
		return item, fmt.Errorf("%w: invalid allocation security", ErrInvalidLedgerInput)
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return item, fmt.Errorf("%w: invalid allocation symbol", ErrInvalidLedgerInput)
		}
	}
	if item.Quantity <= 0 || item.Quantity > 1000000000000 || item.Revision < 1 || (item.Revision == 1) != (item.Supersedes == "") || item.Supersedes == item.LinkID {
		return item, fmt.Errorf("%w: invalid allocation quantity/revision", ErrInvalidLedgerInput)
	}
	if (item.Role != "residual" && item.Role != "redemption") ||
		(item.TradeSide != "B" && item.TradeSide != "S" && item.TradeSide != "R") ||
		(item.Role == "residual" && item.TradeSide == "R") || (item.Role == "redemption" && item.TradeSide == "S") {
		return item, fmt.Errorf("%w: invalid allocation role/side", ErrInvalidLedgerInput)
	}
	if (item.Status != "active" && item.Status != "revoked") ||
		(item.EvidenceType != "user_confirmation" && item.EvidenceType != "inventory_evidence") {
		return item, fmt.Errorf("%w: invalid allocation audit", ErrInvalidLedgerInput)
	}
	if item.ConfirmedDate != "" {
		d, e := time.ParseInLocation("2006-01-02", item.ConfirmedDate, timeutil.Location())
		if e != nil || d.Format("2006-01-02") != item.ConfirmedDate || !item.ConfirmedAt.IsZero() {
			return item, fmt.Errorf("%w: invalid or ambiguous confirmation precision", ErrInvalidLedgerInput)
		}
	} else {
		if item.ConfirmedAt.IsZero() {
			return item, fmt.Errorf("%w: missing confirmation time/date", ErrInvalidLedgerInput)
		}
		item.ConfirmedAt = item.ConfirmedAt.In(timeutil.Location())
	}
	return item, nil
}

// ResolveT0Allocations rejects forks and gaps instead of picking a last writer.
func ResolveT0Allocations(history []T0Allocation) ([]T0Allocation, error) {
	items := append([]T0Allocation(nil), history...)
	sort.Slice(items, func(i, j int) bool { return items[i].Revision < items[j].Revision })
	current := make(map[string]T0Allocation)
	seen := make(map[string]bool)
	for _, item := range items {
		var err error
		item, err = NormalizeT0Allocation(item)
		if err != nil {
			return nil, err
		}
		key := item.AccountID + "\x00" + item.TradeDate + "\x00" + item.GatewayOrderID
		previous := current[key]
		// Do not hide a later revision from a running v1-only reader. Precision
		// changes require a separate migration, not an implicit revision upgrade.
		if previous.Revision > 0 && t0AllocationStorageSource(previous) != t0AllocationStorageSource(item) {
			return nil, fmt.Errorf("%w: allocation precision migration required", ErrInvalidLedgerInput)
		}
		if seen[item.LinkID] || item.Revision != previous.Revision+1 || item.Supersedes != previous.LinkID {
			return nil, fmt.Errorf("%w: T0 allocation revision conflict", ErrInvalidLedgerInput)
		}
		seen[item.LinkID] = true
		current[key] = item
	}
	result := make([]T0Allocation, 0, len(current))
	for _, item := range current {
		if item.Status == "active" {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LinkID < result[j].LinkID })
	return result, nil
}

func (repo *Repository) ListT0AllocationHistory(ctx context.Context, accountID, tradeDate string) ([]T0Allocation, error) {
	date, err := normalizeTradeDate(tradeDate)
	if err != nil {
		return nil, err
	}
	q, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT link_id, gateway_order_id, t0_order_group_id, security_id, quantity::bigint, quantity=trunc(quantity), source_payload, source
FROM performance_attribution_links WHERE account_id=$1 AND trade_date=$2::date AND source IN ($3,$4) AND link_type='order'
ORDER BY attribution_link_pk LIMIT 10001`, accountID, date, t0AllocationSource, t0AllocationDateSource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []T0Allocation
	for rows.Next() {
		var id, order, group, security, source string
		var quantity int64
		var integral bool
		var raw []byte
		var item T0Allocation
		if err := rows.Scan(&id, &order, &group, &security, &quantity, &integral, &raw, &source); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		item, err = NormalizeT0Allocation(item)
		if err != nil {
			return nil, err
		}
		if source != t0AllocationStorageSource(item) || !integral || item.LinkID != id || item.AccountID != accountID || item.TradeDate != date || item.GatewayOrderID != order || item.GroupID != group || item.SecurityID != security || item.Quantity != quantity {
			return nil, fmt.Errorf("T0 allocation envelope/payload mismatch")
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) > 10000 {
		return nil, fmt.Errorf("T0 allocation history exceeds bounded read")
	}
	return result, nil
}

// AppendT0Allocation serializes revisions per account/date/order; old rows never change.
func (repo *Repository) AppendT0Allocation(ctx context.Context, item T0Allocation) (T0Allocation, error) {
	item, err := NormalizeT0Allocation(item)
	if err != nil {
		return item, err
	}
	if db, ok := repo.exec.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return item, err
		}
		defer tx.Rollback()
		result, err := NewRepository(tx).AppendT0Allocation(ctx, item)
		if err != nil {
			return item, err
		}
		return result, tx.Commit()
	}
	tx, ok := repo.exec.(*sql.Tx)
	if !ok {
		return item, fmt.Errorf("T0 allocation append requires a transaction")
	}
	var isolation string
	if err := tx.QueryRowContext(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
		return item, err
	}
	if isolation != "read committed" {
		return item, fmt.Errorf("T0 allocation append requires read committed revision visibility")
	}
	_, err = repo.exec.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t0AllocationSource+"|"+item.AccountID+"|"+item.TradeDate+"|"+item.GatewayOrderID)
	if err != nil {
		return item, err
	}
	history, err := repo.ListT0AllocationHistory(ctx, item.AccountID, item.TradeDate)
	if err != nil {
		return item, err
	}
	for _, existing := range history {
		if existing.LinkID == item.LinkID {
			if reflect.DeepEqual(existing, item) {
				return existing, nil
			}
			return item, fmt.Errorf("T0 allocation link id conflict")
		}
	}
	if _, err = ResolveT0Allocations(append(history, item)); err != nil {
		return item, err
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return item, err
	}
	parts := strings.Split(item.SecurityID, ".")
	_, err = repo.exec.ExecContext(ctx, `INSERT INTO performance_attribution_links
(link_id,account_id,trade_date,strategy_type,t0_order_group_id,security_id,symbol,exchange,gateway_order_id,link_type,quantity,source,source_payload)
VALUES ($1,$2,$3::date,'etf_redemption_t0',$4,$5,$6,$7,$8,'order',$9,$10,$11::jsonb)`,
		item.LinkID, item.AccountID, item.TradeDate, item.GroupID, item.SecurityID, parts[0], parts[1], item.GatewayOrderID, item.Quantity, t0AllocationStorageSource(item), raw)
	return item, err
}
