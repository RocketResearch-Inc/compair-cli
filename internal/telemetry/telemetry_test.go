package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnableDisableTelemetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	status, err := Enable()
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if !status.Enabled {
		t.Fatalf("Enable() did not enable telemetry")
	}
	if status.InstallID == "" {
		t.Fatalf("Enable() did not create install ID")
	}

	disabled, err := Disable()
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if disabled.Enabled {
		t.Fatalf("Disable() did not disable telemetry")
	}
	if disabled.InstallID == "" {
		t.Fatalf("Disable() should retain install ID")
	}
}

func TestMaybeSendDailyHeartbeatSendsOncePerDay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var hits int
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	t.Setenv("COMPAIR_TELEMETRY_BASE", server.URL)

	if _, err := Enable(); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := MaybeSendDailyHeartbeat("compair status", "0.2.0"); err != nil {
		t.Fatalf("MaybeSendDailyHeartbeat() first call error = %v", err)
	}
	if err := MaybeSendDailyHeartbeat("compair status", "0.2.0"); err != nil {
		t.Fatalf("MaybeSendDailyHeartbeat() second call error = %v", err)
	}

	if hits != 1 {
		t.Fatalf("expected 1 telemetry request, got %d", hits)
	}
	if got := body["client"]; got != "cli" {
		t.Fatalf("client = %v, want cli", got)
	}
	events, _ := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	status, err := CurrentStatus()
	if err != nil {
		t.Fatalf("CurrentStatus() error = %v", err)
	}
	if status.LastHeartbeatAt == "" {
		t.Fatalf("expected last heartbeat timestamp to be recorded")
	}
}
