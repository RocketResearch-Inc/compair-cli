package compair

import (
	"strings"
	"testing"
)

func TestDefaultSnapshotOptionsUseFullRepoDefaults(t *testing.T) {
	opts := defaultSnapshotOptions()
	if opts.MaxTreeEntries != 0 || opts.MaxFiles != 0 || opts.MaxTotalBytes != 0 || opts.MaxFileBytes != 0 || opts.MaxFileRead != 0 {
		t.Fatalf("expected full-repo defaults, got %+v", opts)
	}
}

func TestDescribeSnapshotLimitBytes(t *testing.T) {
	if got := describeSnapshotLimitBytes(0); got != "full repo (no cap)" {
		t.Fatalf("unexpected unlimited label: %q", got)
	}
	if got := describeSnapshotLimitBytes(1024); got != "1.0 KB" {
		t.Fatalf("unexpected sized label: %q", got)
	}
}

func TestFitChunkAllowsUnlimitedBudget(t *testing.T) {
	chunk := strings.Repeat("line\n", 500)
	payload, ok := fitChunk("### File: demo.go", chunk, "go", 160, 0)
	if !ok {
		t.Fatalf("expected chunk to fit when budget is unlimited")
	}
	if strings.Contains(payload, "[truncated]") {
		t.Fatalf("did not expect unlimited budget payload to be truncated")
	}
}
