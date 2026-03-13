package compair

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type syncGatePreset struct {
	Name           string
	Description    string
	FailOnSeverity []string
	FailOnType     []string
	FailOnFeedback int
}

var syncGatePresets = map[string]syncGatePreset{
	"off": {
		Name:        "off",
		Description: "Disable preset gating and use only explicitly provided low-level flags.",
	},
	"api-contract": {
		Name:           "api-contract",
		Description:    "Recommended default for CI: block on high-severity contract conflicts.",
		FailOnSeverity: []string{"high"},
		FailOnType:     []string{"potential_conflict"},
		FailOnFeedback: 1,
	},
	"cross-product": {
		Name:           "cross-product",
		Description:    "Catch high-severity contract and drift issues across products.",
		FailOnSeverity: []string{"high"},
		FailOnType:     []string{"potential_conflict", "hidden_overlap"},
		FailOnFeedback: 1,
	},
	"review": {
		Name:           "review",
		Description:    "Code-review oriented: block on conflicts and high-signal updates.",
		FailOnSeverity: []string{"high"},
		FailOnType:     []string{"potential_conflict", "relevant_update"},
		FailOnFeedback: 1,
	},
	"strict": {
		Name:           "strict",
		Description:    "Aggressive gate for integration branches.",
		FailOnSeverity: []string{"high", "medium"},
		FailOnType:     []string{"potential_conflict", "hidden_overlap", "relevant_update"},
		FailOnFeedback: 1,
	},
}

func applySyncGatePreset(cmd *cobra.Command) (bool, error) {
	gate := strings.ToLower(strings.TrimSpace(syncGate))
	if gate == "" || gate == "off" {
		return false, nil
	}
	if gate == "help" || gate == "list" {
		printSyncGatePresets()
		return true, nil
	}

	preset, ok := syncGatePresets[gate]
	if !ok {
		return false, fmt.Errorf("unknown sync gate preset %q (use --gate help)", syncGate)
	}

	if _, changed := getStringArrayFlagIfChanged(cmd, "fail-on-severity"); !changed && len(syncFailOnSeverity) == 0 {
		syncFailOnSeverity = append([]string(nil), preset.FailOnSeverity...)
	}
	if _, changed := getStringArrayFlagIfChanged(cmd, "fail-on-type"); !changed && len(syncFailOnType) == 0 {
		syncFailOnType = append([]string(nil), preset.FailOnType...)
	}
	if _, changed := getIntFlagIfChanged(cmd, "fail-on-feedback"); !changed && syncFailOnFeedback == 0 {
		syncFailOnFeedback = preset.FailOnFeedback
	}

	return false, nil
}

func printSyncGatePresets() {
	fmt.Println("Available sync gate presets:")
	keys := make([]string, 0, len(syncGatePresets))
	for key := range syncGatePresets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		preset := syncGatePresets[key]
		fmt.Printf("- %s: %s\n", preset.Name, preset.Description)
		if len(preset.FailOnSeverity) > 0 {
			fmt.Printf("    severity: %s\n", strings.Join(preset.FailOnSeverity, ", "))
		}
		if len(preset.FailOnType) > 0 {
			fmt.Printf("    type: %s\n", strings.Join(preset.FailOnType, ", "))
		}
		if preset.FailOnFeedback > 0 {
			fmt.Printf("    fallback count: %d\n", preset.FailOnFeedback)
		}
	}
	fmt.Println("- help: print this list and exit")
}
