package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCredentialInputFromStdin(t *testing.T) {
	value, err := readCredentialInput("-", strings.NewReader(`{
  "broker_login_user": "user",
  "broker_password": "password",
  "dynamic_password": ""
}`))
	if err != nil {
		t.Fatal(err)
	}
	if value.BrokerLoginUser != "user" || value.BrokerPassword != "password" || value.DynamicPassword != "" {
		t.Fatalf("credential input = %#v", value)
	}
}

func TestReadCredentialInputRejectsUnknownFieldsAndLoosePermissions(t *testing.T) {
	if _, err := readCredentialInput("-", strings.NewReader(`{"broker_login_user":"u","broker_password":"p","extra":"no"}`)); err == nil {
		t.Fatal("unknown credential field accepted")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "credentials.json")
	if err := os.WriteFile(path, []byte(`{"broker_login_user":"u","broker_password":"p","dynamic_password":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialInput(path, nil); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("loose credential permissions error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialInput(path, nil); err != nil {
		t.Fatalf("0600 credential file rejected: %v", err)
	}
}
