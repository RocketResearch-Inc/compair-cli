package compair

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

func TestNotificationEventsAvailableFallsBackToEndpointProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notification_events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[],"total_count":0}`))
	}))
	defer server.Close()

	client := api.NewClient(server.URL)
	caps := &api.Capabilities{}

	available, err := notificationEventsAvailable(client, caps, "group-123")
	if err != nil {
		t.Fatalf("unexpected error probing notification events endpoint: %v", err)
	}
	if !available {
		t.Fatal("expected notification events to be treated as available when the endpoint probe succeeds")
	}
}

func TestNotificationEventsAvailableReturnsFalseWhenEndpointMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := api.NewClient(server.URL)
	caps := &api.Capabilities{}

	available, err := notificationEventsAvailable(client, caps, "group-123")
	if err != nil {
		t.Fatalf("expected missing endpoint to be treated as unavailable, not an error: %v", err)
	}
	if available {
		t.Fatal("expected notification events to be unavailable when the endpoint probe returns 404")
	}
}
