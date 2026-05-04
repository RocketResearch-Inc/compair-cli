package compair

import "testing"

func TestParseWaitTimeoutSeconds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "default empty", input: "", want: 600},
		{name: "minutes", input: "10m", want: 600},
		{name: "seconds", input: "45s", want: 45},
		{name: "round up fractional seconds", input: "1500ms", want: 2},
		{name: "indefinite bare zero", input: "0", want: 0},
		{name: "indefinite duration zero", input: "0s", want: 0},
		{name: "invalid", input: "later", wantErr: true},
		{name: "negative", input: "-1m", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseWaitTimeoutSeconds(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseWaitTimeoutSeconds(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestReviewAndWaitFlagSurface(t *testing.T) {
	t.Parallel()

	if flag := reviewCmd.Flags().Lookup("feedback-wait"); flag == nil || !flag.Hidden {
		t.Fatalf("expected review feedback-wait flag to be hidden")
	}
	if flag := reviewCmd.Flags().Lookup("process-timeout-sec"); flag == nil || !flag.Hidden {
		t.Fatalf("expected review process-timeout-sec flag to be hidden")
	}
	if flag := waitCmd.Flags().Lookup("timeout"); flag == nil || flag.Hidden {
		t.Fatalf("expected wait timeout flag to be visible")
	}
	if flag := waitCmd.Flags().Lookup("process-timeout-sec"); flag == nil || !flag.Hidden {
		t.Fatalf("expected wait process-timeout-sec flag to be hidden")
	}
}
