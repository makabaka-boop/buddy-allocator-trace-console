package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReplayHandlerOK(t *testing.T) {
	body := `{"capacity":16,"events":[
		{"eventId":"e1","type":"allocate","allocId":"a","size":3},
		{"eventId":"e2","type":"free","allocId":"a"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	replayHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	evs := resp["events"].([]any)
	if len(evs) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(evs))
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header")
	}
}

func TestReplayHandlerStructuralError(t *testing.T) {
	body := `{"capacity":16,"events":[{"eventId":"e1","type":"free","allocId":"ghost"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	replayHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var eb struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil || eb.Error == "" {
		t.Fatalf("expected error body, got %s", rec.Body.String())
	}
}

func TestReplayHandlerBadJSONAndUnknownField(t *testing.T) {
	for _, body := range []string{"{not json", `{"capacity":16,"events":[],"bogus":1}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/replay", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		replayHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d want 400", body, rec.Code)
		}
	}
}

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	healthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}
