package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteOKEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "req-1")
	rec := httptest.NewRecorder()

	WriteOK(rec, req, http.StatusAccepted, map[string]string{"status": "ok"})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if rec.Header().Get("X-Request-ID") != "req-1" {
		t.Fatalf("missing request id header")
	}

	var envelope Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.OK {
		t.Fatal("expected ok envelope")
	}
	if envelope.RequestID != "req-1" {
		t.Fatalf("request id = %q", envelope.RequestID)
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts", nil)
	rec := httptest.NewRecorder()

	WriteMethodNotAllowed(rec, req, http.MethodGet)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if rec.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("allow header = %q", rec.Header().Get("Allow"))
	}

	var envelope Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.OK {
		t.Fatal("expected error envelope")
	}
	if envelope.Error == nil || envelope.Error.Code != CodeMethodNotAllowed {
		t.Fatalf("unexpected error: %+v", envelope.Error)
	}
}
