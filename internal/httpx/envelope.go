package httpx

import (
	"encoding/json"
	"net/http"
	"time"
)

type ErrorCode string

const (
	CodeBadRequest       ErrorCode = "BAD_REQUEST"
	CodeForbidden        ErrorCode = "FORBIDDEN"
	CodeMethodNotAllowed ErrorCode = "METHOD_NOT_ALLOWED"
	CodeNotFound         ErrorCode = "NOT_FOUND"
	CodeInternal         ErrorCode = "INTERNAL"
	CodeNotImplemented   ErrorCode = "NOT_IMPLEMENTED"
	CodeUnavailable      ErrorCode = "UNAVAILABLE"
)

type Envelope struct {
	OK        bool      `json:"ok"`
	Data      any       `json:"data,omitempty"`
	Error     *Error    `json:"error,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Time      time.Time `json:"time"`
}

type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Detail  any       `json:"detail,omitempty"`
}

func WriteOK(w http.ResponseWriter, r *http.Request, status int, data any) {
	if status == 0 {
		status = http.StatusOK
	}
	writeJSON(w, r, status, Envelope{
		OK:        true,
		Data:      data,
		RequestID: RequestID(r),
		Time:      time.Now().UTC(),
	})
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, detail any) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	writeJSON(w, r, status, Envelope{
		OK: false,
		Error: &Error{
			Code:    code,
			Message: message,
			Detail:  detail,
		},
		RequestID: RequestID(r),
		Time:      time.Now().UTC(),
	})
}

func WriteMethodNotAllowed(w http.ResponseWriter, r *http.Request, allowed string) {
	if allowed != "" {
		w.Header().Set("Allow", allowed)
	}
	WriteError(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed", nil)
}

func WriteNotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, http.StatusNotFound, CodeNotFound, "resource not found", nil)
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, envelope Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if requestID := RequestID(r); requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}
