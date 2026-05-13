package compair

import "testing"

func TestNowQuoteCanRunDefaultsToTrueWithoutBilling(t *testing.T) {
	canRun, reason := nowQuoteCanRun(map[string]interface{}{"model": "gpt-5.4-mini"})
	if !canRun || reason != "" {
		t.Fatalf("expected runnable quote without billing metadata, got canRun=%v reason=%q", canRun, reason)
	}
}

func TestNowQuoteCanRunReadsBlockedBillingReason(t *testing.T) {
	canRun, reason := nowQuoteCanRun(map[string]interface{}{
		"billing": map[string]interface{}{
			"can_run":         false,
			"blocking_reason": "insufficient_credits",
		},
	})
	if canRun {
		t.Fatal("expected blocked billing quote")
	}
	if reason != "insufficient_credits" {
		t.Fatalf("expected insufficient_credits reason, got %q", reason)
	}
}
