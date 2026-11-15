package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

const publishedProvisionalPlanSHA = "d59ec0d195d59a17ccce42177639cf8954db41536deb258e2fff577847cb4c31"
const fuyingTransferCurrentState = "failed_not_withdrawn"

type brokerRefreshFees struct {
	Total         int64            `json:"fee_cents"`
	Components    map[string]int64 `json:"fee_components_cents"`
	Rows          int              `json:"rows"`
	SourceRows    []int            `json:"source_rows"`
	RepoPrincipal int64            `json:"repo_principal_cents"`
}
type brokerRefreshDay struct {
	Funds map[string]string `json:"funds"`
	Fees  brokerRefreshFees `json:"fees"`
}
type brokerRefreshAudit struct {
	Schema  string `json:"schema"`
	Account string `json:"account_id"`
	Sources map[string]struct {
		Path string `json:"path"`
		SHA  string `json:"sha256"`
		Rows int    `json:"rows"`
	} `json:"sources"`
	Days    map[string]brokerRefreshDay `json:"days"`
	NewCash []struct {
		ETF bool              `json:"etf_related"`
		Raw map[string]string `json:"raw_fields"`
	} `json:"new_cash_rows"`
	CashOverlap struct {
		Conflicts int `json:"financial_conflicts"`
		Rows      int `json:"overlap_rows"`
	} `json:"cash_overlap"`
	CashBridge struct {
		Days   int `json:"funds_days"`
		Passed int `json:"cash_bridges_passed"`
	} `json:"cash_bridge"`
	Final bool `json:"final_settlement_verified"`
}

func refreshedBrokerAsset(date string, old performance.ReconstructionAssetDay, day brokerRefreshDay, auditSHA string) (performance.ReconstructionAssetDay, error) {
	if day.Funds["客户号"] != fuying13Account || compactToISODate(day.Funds["交易日"]) != date || day.Fees.Rows <= 0 || day.Fees.Total < 0 || day.Fees.RepoPrincipal < 0 {
		return old, errors.New("broker refresh account/day/fees invalid")
	}
	values := map[string]int64{}
	for _, key := range []string{"客户资金", "总资产", "市值", "入金", "出金", "当日盈亏"} {
		v, err := parseBrokerMoneyCents(day.Funds[key])
		if err != nil {
			return old, err
		}
		values[key] = v
	}
	if values["入金"] != 0 || values["出金"] != 0 {
		return old, errors.New("unexpected external flow in broker refresh")
	}
	if values["总资产"] <= 0 || values["市值"] < 0 {
		return old, errors.New("invalid broker assets")
	}
	var total int64
	for _, key := range []string{"实收佣金", "印花税", "交易规费", "过户费"} {
		value, ok := day.Fees.Components[key]
		if !ok || value < 0 {
			return old, errors.New("fee components incomplete")
		}
		total += value
	}
	if len(day.Fees.Components) != 4 || total != day.Fees.Total {
		return old, errors.New("fee total mismatch")
	}
	close := float64(values["总资产"]) / 100
	if date <= "2026-11-09" && (old.Date != date || old.ReportedClose != close) {
		return old, errors.New("previous broker asset changed")
	}
	if date >= "2026-11-10" && (old.ReceivableCents != 0 || old.PayableCents != 0 || old.InferredReceivableCents != 0 || old.InferredPayableCents != 0) {
		return old, errors.New("unreviewed pending rights in refreshed days")
	}
	previous := old
	old.Date, old.ReportedClose = date, close
	old.ObservedOpen = float64(values["总资产"]-values["当日盈亏"]) / 100
	old.OtherCents = values["总资产"] - values["客户资金"] - values["市值"] - day.Fees.RepoPrincipal
	old.Source = "broker_statement_verified_refresh"
	old.Evidence = map[string]any{"previous_observation": previous, "broker_funds": day.Funds, "broker_fee_rows": day.Fees, "refresh_audit_sha256": auditSHA,
		"broker_statement_fees": map[string]any{"total": float64(day.Fees.Total) / 100, "components_cents": day.Fees.Components, "source_rows": day.Fees.SourceRows, "date_basis": "broker_statement_display_date", "order_level_verified": false, "already_in_asset_and_cash": true}}
	old.InputSHA = reconstructionSHA(old.Evidence)
	return old, nil
}

func readBrokerRefresh20261115(ctx context.Context, tx *sql.Tx, path, digest string) (provisionalRecoveryPlan, error) {
	var p provisionalRecoveryPlan
	if err := readProvisionalPlan("outputs/recovery/fuying13_completion_20261115/plan-final.json", publishedProvisionalPlanSHA, &p); err != nil {
		return p, err
	}
	var audit brokerRefreshAudit
	if err := readPinnedJSON(path, digest, &audit); err != nil {
		return p, err
	}
	if audit.Schema != "relay.fuying13_broker_refresh.v1" || audit.Account != fuying13Account || audit.Final || len(audit.Sources) != 4 || audit.CashOverlap.Rows != 12535 || audit.CashOverlap.Conflicts != 0 || audit.CashBridge.Days != 48 || audit.CashBridge.Passed != 47 || len(audit.NewCash) != 345 {
		return p, errors.New("broker refresh evidence scope changed")
	}
	for kind, source := range audit.Sources {
		if source.Rows <= 0 {
			return p, errors.New("empty source")
		}
		data, err := os.ReadFile(source.Path)
		if err != nil {
			return p, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != source.SHA {
			return p, errors.New("broker source changed after audit")
		}
		p.InputSHA["broker_refresh_"+kind] = source.SHA
	}
	for _, cash := range audit.NewCash {
		if cash.ETF || cash.Raw["交易日期"] < "20261110" || cash.Raw["交易日期"] > "20261113" || cash.Raw["投资者"] != fuying13Account {
			return p, errors.New("new cash includes unreviewed ETF/identity/date")
		}
	}
	repo := ledger.NewRepository(tx)
	current, err := repo.ListPerformanceNAVs(ctx, fuying13Account, "2026-10-09", "2026-11-13")
	if err != nil {
		return p, err
	}
	if len(current) != 25 || !sameProvisionalNAV(current[0], p.Anchor) {
		return p, errors.New("published baseline/anchor changed")
	}
	for i, n := range p.NAVs {
		n.Source = "relay.provisional_reconstruction:" + publishedProvisionalPlanSHA
		if !sameProvisionalNAV(current[i+1], n) {
			return p, errors.New("published provisional version changed; refresh already applied or needs review")
		}
	}
	cash, err := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: fuying13Account, DateFrom: "2026-11-10", DateTo: "2026-11-15", Limit: 1000})
	if err != nil {
		return p, err
	}
	if len(cash) != 0 {
		return p, errors.New("cash ledger requires review before failed-transfer correction")
	}
	p.Before = current
	p.Dates = append(p.Dates, "2026-11-13")
	p.Assets = append(p.Assets, performance.ReconstructionAssetDay{})
	p.Authorization["broker_refresh_date"] = "2026-11-15"
	p.Authorization["nov13_transfer_state"] = fuyingTransferCurrentState
	p.Authorization["nov13_transfer_user_confirmation"] = "这个是当日转出失败，要下个交易日才能转出了"
	p.Authorization["future_transfer_not_recorded"] = true
	p.InputSHA["broker_refresh_audit"] = digest
	for i, date := range p.Dates {
		day, ok := audit.Days[compactDateForBroker(date)]
		if !ok {
			return p, fmt.Errorf("missing broker day %s", date)
		}
		p.Assets[i], err = refreshedBrokerAsset(date, p.Assets[i], day, digest)
		if err != nil {
			return p, err
		}
	}
	p.NAVs, err = performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	return p, err
}

func compactDateForBroker(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return d.Format("20060102")
}
