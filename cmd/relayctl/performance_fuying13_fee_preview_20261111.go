package main

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"ti-relay-trader/internal/ledger"
)

func (s *fuying13SplitPreview) ListOrderFeeRecords(ctx context.Context, q ledger.OrderFeeRecordQuery) ([]ledger.OrderFeeRecord, error) {
	if len(s.repoFees) == 0 || q.AccountID != fuying13Account {
		return s.Repository.ListOrderFeeRecords(ctx, q)
	}
	// This dated preview accepts one complete bounded daily slice, then paginates
	// the merged evidence. It must not append overlays to each database page.
	if q.TradeDate == "" {
		return nil, errors.New("repo fee preview requires an exact day")
	}
	all := q
	all.Cursor = ""
	all.Limit = 5000
	old, err := s.Repository.ListOrderFeeRecords(ctx, all)
	if err != nil {
		return nil, err
	}
	if len(old) >= 5000 {
		return nil, errors.New("daily fee evidence exceeds preview bound")
	}
	return mergeFuyingRepoFees(old, s.repoFees, q)
}

func mergeFuyingRepoFees(old, overlay []ledger.OrderFeeRecord, q ledger.OrderFeeRecordQuery) ([]ledger.OrderFeeRecord, error) {
	offset := 0
	if q.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(q.Cursor)
		if err != nil || offset < 0 {
			return nil, errors.New("invalid fee preview cursor")
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	result := append([]ledger.OrderFeeRecord(nil), old...)
	for _, f := range overlay {
		if f.AccountID != q.AccountID || f.TradeDate != q.TradeDate || (q.DateFrom != "" && f.TradeDate < q.DateFrom) || (q.DateTo != "" && f.TradeDate > q.DateTo) || (q.GatewayOrderID != "" && f.GatewayOrderID != q.GatewayOrderID) || (q.FeeComplete != nil && f.FeeComplete != *q.FeeComplete) {
			continue
		}
		for _, prior := range result {
			if prior.AccountID == f.AccountID && (prior.FeeRecordID == f.FeeRecordID || prior.GatewayOrderID == f.GatewayOrderID) {
				return nil, errors.New("fee overlay conflicts with existing evidence")
			}
		}
		result = append(result, f)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.TradeDate != b.TradeDate {
			return a.TradeDate > b.TradeDate
		}
		if !a.FeeAsOf.Equal(b.FeeAsOf) {
			return a.FeeAsOf.After(b.FeeAsOf)
		}
		return a.FeeRecordID < b.FeeRecordID
	})
	if offset >= len(result) {
		return []ledger.OrderFeeRecord{}, nil
	}
	end := len(result)
	if limit < end-offset {
		end = offset + limit
	}
	return result[offset:end], nil
}
