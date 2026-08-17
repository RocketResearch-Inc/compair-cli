package compair

import (
	"fmt"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	baselineIndexSuccessExitCode    = 0
	baselineIndexUsageExitCode      = 2
	baselineIndexAuthExitCode       = 3
	baselineIndexCapabilityExitCode = 4
	baselineIndexConflictExitCode   = 5
	baselineIndexRetryableExitCode  = 6
	baselineIndexTerminalExitCode   = 7
	baselineIndexContractExitCode   = 8
	baselineIndexInternalExitCode   = 9
)

func newBaselineIndexCommand() *cobra.Command {
	var uploadResultPath string
	var resume bool
	var wait bool
	var timeout time.Duration
	var pollInterval time.Duration
	var jsonOutput bool
	var allowLoopbackHTTP bool

	command := &cobra.Command{
		Use:   "index",
		Short: "Submit or inspect a compatible baseline index build",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("baseline index accepts no positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, explicitGroup := flagValue(cmd, "group")
			if !explicitGroup || strings.TrimSpace(groupID) == "" || strings.TrimSpace(uploadResultPath) == "" || timeout <= 0 || pollInterval <= 0 {
				return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("baseline index requires explicit --group, --upload-result, and positive timeout values"))
			}
			_ = jsonOutput
			execution, runErr := baseline.RunIndex(cmd.Context(), strings.TrimSpace(groupID), strings.TrimSpace(uploadResultPath), baseline.IndexOptions{
				BaseURL: viper.GetString("api.base"), Token: auth.Token(), AllowLoopbackHTTP: allowLoopbackHTTP,
				Resume: resume, Wait: wait, Timeout: timeout, PollInterval: pollInterval,
				Progress: func(state string, attempt int) {
					fmt.Fprintf(cmd.ErrOrStderr(), "baseline index: state=%s attempt=%d\n", state, attempt)
				},
			})
			if execution != nil {
				if encodeErr := baseline.EncodeIndexResult(cmd.OutOrStdout(), execution.Result); encodeErr != nil {
					return withCLIExitCode(baselineIndexInternalExitCode, fmt.Errorf("baseline index result could not be written"))
				}
				if finalizeErr := execution.Finalize(); finalizeErr != nil {
					return withCLIExitCode(baselineIndexInternalExitCode, fmt.Errorf("baseline index state cleanup failed"))
				}
			}
			if runErr != nil {
				return baselineIndexCommandError(runErr)
			}
			return nil
		},
	}
	command.Flags().StringVar(&uploadResultPath, "upload-result", "", "Path to one successful baseline-snapshot-upload-result.v1 JSON value")
	command.Flags().BoolVar(&resume, "resume", false, "Resume the exact protected index operation identity")
	command.Flags().BoolVar(&wait, "wait", false, "Poll until compatible-index publication reaches a terminal state")
	command.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "Maximum submission and optional wait duration")
	command.Flags().DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Initial bounded status polling interval")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Explicitly select the default single-JSON output")
	command.Flags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("invalid baseline index arguments"))
	})
	command.AddCommand(newBaselineIndexStatusCommand())
	return command
}

func newBaselineIndexStatusCommand() *cobra.Command {
	var jobID string
	var jsonOutput bool
	var allowLoopbackHTTP bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Read one authorized compatible-index job",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("baseline index status accepts no positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, explicitGroup := flagValue(cmd, "group")
			if !explicitGroup || strings.TrimSpace(groupID) == "" || strings.TrimSpace(jobID) == "" {
				return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("baseline index status requires explicit --group and --job-id"))
			}
			_ = jsonOutput
			execution, runErr := baseline.RunIndexStatus(cmd.Context(), strings.TrimSpace(groupID), strings.TrimSpace(jobID), baseline.IndexOptions{
				BaseURL: viper.GetString("api.base"), Token: auth.Token(), AllowLoopbackHTTP: allowLoopbackHTTP,
				Timeout: 30 * time.Second,
			})
			if execution != nil {
				if encodeErr := baseline.EncodeIndexResult(cmd.OutOrStdout(), execution.Result); encodeErr != nil {
					return withCLIExitCode(baselineIndexInternalExitCode, fmt.Errorf("baseline index status result could not be written"))
				}
			}
			if runErr != nil {
				return baselineIndexCommandError(runErr)
			}
			return nil
		},
	}
	command.Flags().StringVar(&jobID, "job-id", "", "Durable compatible-index job ID")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Explicitly select the default single-JSON output")
	command.Flags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("invalid baseline index status arguments"))
	})
	return command
}

func baselineIndexCommandError(err error) error {
	reason := baseline.SafeIndexReason(err)
	switch baseline.IndexFailure(err) {
	case baseline.IndexFailureInput:
		return withCLIExitCode(baselineIndexUsageExitCode, fmt.Errorf("baseline index input rejected: %s", reason))
	case baseline.IndexFailureAuth:
		return withCLIExitCode(baselineIndexAuthExitCode, fmt.Errorf("baseline index authorization failed: %s", reason))
	case baseline.IndexFailureCapability:
		return withCLIExitCode(baselineIndexCapabilityExitCode, fmt.Errorf("baseline index unavailable: %s", reason))
	case baseline.IndexFailureConflict:
		return withCLIExitCode(baselineIndexConflictExitCode, fmt.Errorf("baseline index intent rejected: %s", reason))
	case baseline.IndexFailureRetryable:
		return withCLIExitCode(baselineIndexRetryableExitCode, fmt.Errorf("baseline index incomplete: %s", reason))
	case baseline.IndexFailureTerminal:
		return withCLIExitCode(baselineIndexTerminalExitCode, fmt.Errorf("baseline index terminated: %s", reason))
	case baseline.IndexFailureContract:
		return withCLIExitCode(baselineIndexContractExitCode, fmt.Errorf("baseline index server contract failed: %s", reason))
	default:
		return withCLIExitCode(baselineIndexInternalExitCode, fmt.Errorf("baseline index failed internally"))
	}
}
