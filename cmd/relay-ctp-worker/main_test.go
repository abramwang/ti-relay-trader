package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerDefaultIsOffline(t *testing.T) {
	var output, diag bytes.Buffer
	getenv := func(string) string { t.Fatal("offline check accessed environment"); return "" }
	for _, extra := range [][]string{nil, {"--check-config"}} {
		output.Reset()
		args := append([]string{"--config", "../../config/ctp-capture-service.example.json"}, extra...)
		if err := run(context.Background(), args, getenv, &output, &diag); err != nil || !strings.Contains(output.String(), "check_only_no_connections") {
			t.Fatal(output.String(), err)
		}
	}
}

func TestWorkerRejectsUnsafeConfigBeforeConnection(t *testing.T) {
	raw, err := os.ReadFile("../../config/ctp-capture-service.example.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{strings.Replace(string(raw), `"env": "test"`, `"env": "prod"`, 1),
		strings.Replace(string(raw), "relay_ctp_example_test", "live_account", 1),
		strings.Replace(string(raw), `"version":`, `"unknown":true,"version":`, 1),
		strings.Replace(string(raw), `"version":`, `"version":"duplicate","version":`, 1), strings.Repeat(" ", 16385)} {
		file := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := run(context.Background(), []string{"--config", file, "--run"}, func(string) string { t.Fatal("read credentials before validation"); return "" }, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted unsafe config")
		}
	}
}

func TestWorkerSecretsAndShutdown(t *testing.T) {
	for _, dsn := range []string{"password=SECRET host=bad dbname=wrong_test", "postgres://user:SECRET@bad/wrong_test"} {
		err := run(context.Background(), []string{"--config", "../../config/ctp-capture-service.example.json", "--run"}, func(key string) string {
			if strings.HasSuffix(key, "DATABASE_URL") {
				return dsn
			}
			return "redis://:SECRET@bad:6379"
		}, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal(err)
		}
	}
	if !cancellationOnly(errors.Join(context.Canceled, context.Canceled)) || cancellationOnly(errors.Join(context.Canceled, errors.New("checkpoint failed"))) {
		t.Fatal("shutdown hid storage failure")
	}
	if strings.Contains(safeError(errors.New("password=SECRET")).Error(), "SECRET") {
		t.Fatal("leaked error")
	}
}

func TestWorkerStorageInspectionFlags(t *testing.T) {
	for _, extra := range [][]string{
		{"--storage-check", "--run"}, {"--storage-check", "--check-config"},
		{"--storage-path", "/tmp"}, {"--storage-check", "--storage-path", "relative"},
		{"--storage-check", "--storage-path", "/tmp", "--storage-path", "/tmp"},
	} {
		args := append([]string{"--config", "../../config/ctp-capture-service.example.json"}, extra...)
		if err := run(context.Background(), args, func(string) string { t.Fatal("unsafe flags reached environment"); return "" }, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted unsafe flags", args)
		}
	}
	args := []string{"--config", "../../config/ctp-capture-service.example.json", "--storage-check"}
	if err := run(context.Background(), args, func(string) string { return "" }, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "dedicated") {
		t.Fatal("inspection silently fell back to active configuration", err)
	}
}
