package api

import (
	"testing"
	"time"
)

func TestClientForPathUsesDefaultProcessDocTimeout(t *testing.T) {
	client := NewClient("http://example.com")

	processClient := client.clientForPath("/process_doc")
	if processClient.Timeout != processDocTimeout {
		t.Fatalf("expected default process_doc timeout %v, got %v", processDocTimeout, processClient.Timeout)
	}

	otherClient := client.clientForPath("/documents")
	if otherClient.Timeout != defaultHTTPTimeout {
		t.Fatalf("expected default timeout %v for non-process_doc path, got %v", defaultHTTPTimeout, otherClient.Timeout)
	}
}

func TestClientForPathHonorsConfiguredProcessDocTimeout(t *testing.T) {
	t.Setenv("COMPAIR_PROCESS_DOC_HTTP_TIMEOUT_SEC", "900")

	client := NewClient("http://example.com")
	processClient := client.clientForPath("/process_doc")

	expected := 15 * time.Minute
	if processClient.Timeout != expected {
		t.Fatalf("expected configured process_doc timeout %v, got %v", expected, processClient.Timeout)
	}
}
