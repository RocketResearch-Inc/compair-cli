package compair

import "testing"

func TestParseSelfFeedbackState(t *testing.T) {
	tests := []struct {
		input   string
		want    bool
		wantErr bool
	}{
		{input: "on", want: true},
		{input: "enable", want: true},
		{input: "true", want: true},
		{input: "off", want: false},
		{input: "disable", want: false},
		{input: "false", want: false},
		{input: "maybe", wantErr: true},
	}

	for _, tt := range tests {
		got, err := parseSelfFeedbackState(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseSelfFeedbackState(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseSelfFeedbackState(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("parseSelfFeedbackState(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
