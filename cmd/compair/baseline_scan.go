package compair

import (
	"fmt"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/spf13/cobra"
)

const (
	baselineScanUsageExitCode      = 2
	baselineScanRepositoryExitCode = 3
	baselineScanContractExitCode   = 4
	baselineScanInternalExitCode   = 5
)

func newBaselineScanCommand() *cobra.Command {
	var dryRun bool
	var planPath string
	command := &cobra.Command{
		Use:   "scan",
		Short: "Build a deterministic local baseline snapshot plan",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(baselineScanUsageExitCode, fmt.Errorf("baseline scan accepts no positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, explicitGroup := flagValue(cmd, "group")
			if !explicitGroup || strings.TrimSpace(groupID) == "" || strings.TrimSpace(planPath) == "" || !dryRun {
				return withCLIExitCode(baselineScanUsageExitCode, fmt.Errorf("baseline scan requires explicit --group, --plan, and --dry-run"))
			}
			input, err := baseline.LoadScanInput(planPath)
			if err != nil {
				return baselineScanCommandError(err)
			}
			result, err := baseline.NewScanner().Scan(cmd.Context(), groupID, input)
			if err != nil {
				return baselineScanCommandError(err)
			}
			defer result.ClearProtected()
			if err := baseline.EncodeDryRunReport(cmd.OutOrStdout(), result.Report); err != nil {
				return withCLIExitCode(baselineScanInternalExitCode, fmt.Errorf("baseline scan output could not be written"))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Build a local plan without network or persistence (required)")
	command.Flags().StringVar(&planPath, "plan", "", "Path to a baseline-scanner-inputs.v1 JSON plan")
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineScanUsageExitCode, fmt.Errorf("invalid baseline scan arguments"))
	})
	return command
}

func baselineScanCommandError(err error) error {
	message := baseline.SafeScannerDiagnostic(err)
	switch baseline.ScanFailure(err) {
	case baseline.ScanFailureRepository:
		return withCLIExitCode(baselineScanRepositoryExitCode, fmt.Errorf("%s", message))
	case baseline.ScanFailureContract:
		return withCLIExitCode(baselineScanContractExitCode, fmt.Errorf("%s", message))
	default:
		return withCLIExitCode(baselineScanInternalExitCode, fmt.Errorf("%s", message))
	}
}
