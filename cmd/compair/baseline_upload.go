package compair

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	baselineUploadSuccessExitCode    = 0
	baselineUploadUsageExitCode      = 2
	baselineUploadAuthExitCode       = 3
	baselineUploadRepositoryExitCode = 4
	baselineUploadContractExitCode   = 5
	baselineUploadRetryableExitCode  = 6
	baselineUploadTerminalExitCode   = 7
	baselineUploadInternalExitCode   = 8
)

func newBaselineUploadCommand() *cobra.Command {
	var planPath string
	var wait bool
	var timeout time.Duration
	var pollInterval time.Duration
	var resume bool
	var jsonOutput bool
	var allowLoopbackHTTP bool
	command := &cobra.Command{
		Use:   "upload",
		Short: "Upload and seal one deterministic baseline snapshot",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(baselineUploadUsageExitCode, fmt.Errorf("baseline upload accepts no positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, explicitGroup := flagValue(cmd, "group")
			if !explicitGroup || strings.TrimSpace(groupID) == "" || strings.TrimSpace(planPath) == "" || timeout <= 0 || pollInterval <= 0 {
				return withCLIExitCode(baselineUploadUsageExitCode, fmt.Errorf("baseline upload requires explicit --group, --plan, and positive timeout values"))
			}
			_ = jsonOutput // JSON is the only output format; --json is an explicit compatibility alias.
			input, err := baseline.LoadScanInput(planPath)
			if err != nil {
				return baselineUploadCommandError(err)
			}
			token := auth.Token()
			options := baseline.UploadOptions{
				BaseURL: viper.GetString("api.base"), Token: token, AllowLoopbackHTTP: allowLoopbackHTTP,
				Resume: resume, Wait: wait, Timeout: timeout, PollInterval: pollInterval,
				Progress: func(stage string, completed, total int) {
					fmt.Fprintf(cmd.ErrOrStderr(), "baseline upload: %s %d/%d\n", stage, completed, total)
				},
			}
			execution, runErr := baseline.RunUpload(cmd.Context(), strings.TrimSpace(groupID), input, options)
			if execution != nil {
				if encodeErr := baseline.EncodeUploadResult(cmd.OutOrStdout(), execution.Result); encodeErr != nil {
					return withCLIExitCode(baselineUploadInternalExitCode, fmt.Errorf("baseline upload result could not be written"))
				}
				if finalizeErr := execution.Finalize(); finalizeErr != nil {
					return withCLIExitCode(baselineUploadInternalExitCode, fmt.Errorf("baseline upload state cleanup failed"))
				}
			}
			if runErr != nil {
				return baselineUploadCommandError(runErr)
			}
			return nil
		},
	}
	command.Flags().StringVar(&planPath, "plan", "", "Path to a baseline-scanner-inputs.v1 JSON plan")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for the sealed-snapshot ingestion continuation")
	command.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum upload and optional wait duration")
	command.Flags().DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Continuation status poll interval")
	command.Flags().BoolVar(&resume, "resume", false, "Resume an exact retained scan/upload identity")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Explicitly select the default single-JSON output")
	command.Flags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineUploadUsageExitCode, fmt.Errorf("invalid baseline upload arguments"))
	})
	return command
}

func baselineUploadCommandError(err error) error {
	var scanErr *baseline.ScanError
	if errors.As(err, &scanErr) {
		kind := baseline.ScanFailure(err)
		if kind == baseline.ScanFailureRepository {
			return withCLIExitCode(baselineUploadRepositoryExitCode, fmt.Errorf("%s", baseline.SafeScannerDiagnostic(err)))
		}
		return withCLIExitCode(baselineUploadContractExitCode, fmt.Errorf("%s", baseline.SafeScannerDiagnostic(err)))
	}
	reason := baseline.SafeUploadReason(err)
	switch baseline.UploadFailure(err) {
	case baseline.UploadFailureAuthentication:
		return withCLIExitCode(baselineUploadAuthExitCode, fmt.Errorf("baseline upload failed: %s", reason))
	case baseline.UploadFailureRepository:
		return withCLIExitCode(baselineUploadRepositoryExitCode, fmt.Errorf("baseline upload failed: %s", reason))
	case baseline.UploadFailureContract:
		return withCLIExitCode(baselineUploadContractExitCode, fmt.Errorf("baseline upload failed: %s", reason))
	case baseline.UploadFailureRetryable:
		return withCLIExitCode(baselineUploadRetryableExitCode, fmt.Errorf("baseline upload incomplete: %s", reason))
	case baseline.UploadFailureTerminal:
		return withCLIExitCode(baselineUploadTerminalExitCode, fmt.Errorf("baseline upload terminated: %s", reason))
	default:
		return withCLIExitCode(baselineUploadInternalExitCode, fmt.Errorf("baseline upload failed internally"))
	}
}
