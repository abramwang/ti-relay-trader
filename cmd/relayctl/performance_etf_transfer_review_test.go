package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestETFTransferReviewRejectsInvalidScopeAndWriteOptions(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-account", "acct-1", "-date-from", "20260230"},
		{"-account", "acct-1", "-date-from", "20261016", "-date-to", "20261015"},
		{"-account", "acct-1", "-date-from", "20261001", "-date-to", "20261101"},
		{"-account", "acct-1", "-date-from", "20261016", "-timeout", "0s"},
		{"-account", "acct-1", "-date-from", "20261016", "-persist"},
		{"-account", "acct-1", "-date-from", "20261016", "unexpected"},
	} {
		if err := runETFTransferReview(args); err == nil {
			t.Fatalf("invalid review options accepted: %v", args)
		}
	}
}

func TestETFTransferReviewOutputIsPrivateAndNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "review.json")
	if err := writeETFTransferReviewFile(path, map[string]int{"database_writes": 0}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("report permissions: %v / %v", info, err)
	}
	if err := writeETFTransferReviewFile(path, map[string]int{"database_writes": 1}); err == nil {
		t.Fatal("existing evidence report was overwritten")
	}
	data, _ := os.ReadFile(path)
	var result map[string]int
	if err := json.Unmarshal(data, &result); err != nil || result["database_writes"] != 0 {
		t.Fatalf("original report changed: %s / %v", data, err)
	}
}
