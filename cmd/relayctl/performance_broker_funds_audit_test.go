package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBrokerTransactionEvidenceIgnoresManualFreezes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.csv")
	body := "客户号,方向,成交日期,成交时间\n" +
		"acct-1,手工冻结,20260910,22:30:00\n" +
		"acct-1,卖出,20260910,09:30:02\n" +
		"acct-1,卖出,20260910,09:30:01\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	evidence, err := readBrokerTransactionEvidence("acct-1", path)
	if err != nil {
		t.Fatalf("readBrokerTransactionEvidence() error = %v", err)
	}
	if evidence.rowsByDate["20260910"] != 2 {
		t.Fatalf("rows = %d, want 2", evidence.rowsByDate["20260910"])
	}
	if evidence.firstIntradayByDate["20260910"] != "09:30:01" {
		t.Fatalf("first intraday = %q", evidence.firstIntradayByDate["20260910"])
	}
}
