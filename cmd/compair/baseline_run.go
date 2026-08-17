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
	baselineRunSuccessExitCode    = 0
	baselineRunUsageExitCode      = 2
	baselineRunAuthExitCode       = 3
	baselineRunCapabilityExitCode = 4
	baselineRunConflictExitCode   = 5
	baselineRunRetryableExitCode  = 6
	baselineRunTerminalExitCode   = 7
	baselineRunContractExitCode   = 8
	baselineRunInternalExitCode   = 9
)

func newBaselineRunCommand() *cobra.Command {
	var planPath string
	var indexResultPath string
	var resume bool
	var wait bool
	var timeout time.Duration
	var pollInterval time.Duration
	var jsonOutput bool
	var allowLoopbackHTTP bool

	command := &cobra.Command{
		Use:   "run",
		Short: "Submit or inspect one document-level baseline review",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("baseline run accepts no positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, explicitGroup := flagValue(cmd, "group")
			groupID, planPath, indexResultPath = strings.TrimSpace(groupID), strings.TrimSpace(planPath), strings.TrimSpace(indexResultPath)
			if !explicitGroup || groupID == "" || planPath == "" || indexResultPath == "" || timeout <= 0 || pollInterval <= 0 {
				return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("baseline run requires explicit --group, --plan, --index-result, and positive timeout values"))
			}
			input, err := baseline.LoadScanInput(planPath)
			if err != nil {
				return baselineRunCommandError(&baseline.RunError{Kind: baseline.RunFailureInput, Reason: "scan_plan_invalid"})
			}
			_ = jsonOutput
			execution, runErr := baseline.RunBaselineRun(cmd.Context(), groupID, input, indexResultPath, baseline.RunOptions{
				BaseURL: viper.GetString("api.base"), Token: auth.Token(), AllowLoopbackHTTP: allowLoopbackHTTP,
				Resume: resume, Wait: wait, Timeout: timeout, PollInterval: pollInterval,
				Progress: func(state string, attempt int) {
					fmt.Fprintf(cmd.ErrOrStderr(), "baseline run: state=%s attempt=%d\n", state, attempt)
				},
			})
			if execution != nil {
				if encodeErr := baseline.EncodeRunResult(cmd.OutOrStdout(), execution.Result); encodeErr != nil {
					return withCLIExitCode(baselineRunInternalExitCode, fmt.Errorf("baseline run result could not be written"))
				}
				if finalizeErr := execution.Finalize(); finalizeErr != nil {
					return withCLIExitCode(baselineRunInternalExitCode, fmt.Errorf("baseline run state cleanup failed"))
				}
			}
			if runErr != nil {
				return baselineRunCommandError(runErr)
			}
			return nil
		},
	}
	command.Flags().StringVar(&planPath, "plan", "", "Path to the immutable baseline-scanner-inputs.v1 plan")
	command.Flags().StringVar(&indexResultPath, "index-result", "", "Path to one successful baseline-index-result.v1 JSON value")
	command.Flags().BoolVar(&resume, "resume", false, "Resume the exact protected document-level run identity")
	command.Flags().BoolVar(&wait, "wait", false, "Poll through retrieval, persistence, and generation completion")
	command.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "Maximum submission and optional wait duration")
	command.Flags().DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Initial bounded status polling interval")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Explicitly select the default single-JSON output")
	command.Flags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("invalid baseline run arguments"))
	})
	command.AddCommand(newBaselineRunStatusCommand())
	return command
}

func newBaselineRunStatusCommand() *cobra.Command {
	var jobID string
	var jsonOutput bool
	var allowLoopbackHTTP bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Read one authorized document-level baseline run job",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("baseline run status accepts no positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, explicitGroup := flagValue(cmd, "group")
			groupID, jobID = strings.TrimSpace(groupID), strings.TrimSpace(jobID)
			if !explicitGroup || groupID == "" || jobID == "" {
				return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("baseline run status requires explicit --group and --job-id"))
			}
			_ = jsonOutput
			execution, runErr := baseline.RunBaselineRunStatus(cmd.Context(), groupID, jobID, baseline.RunOptions{
				BaseURL: viper.GetString("api.base"), Token: auth.Token(), AllowLoopbackHTTP: allowLoopbackHTTP,
				Timeout: 30 * time.Second,
			})
			if execution != nil {
				if encodeErr := baseline.EncodeRunResult(cmd.OutOrStdout(), execution.Result); encodeErr != nil {
					return withCLIExitCode(baselineRunInternalExitCode, fmt.Errorf("baseline run status result could not be written"))
				}
			}
			if runErr != nil {
				return baselineRunCommandError(runErr)
			}
			return nil
		},
	}
	command.Flags().StringVar(&jobID, "job-id", "", "Durable baseline control run job ID")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Explicitly select the default single-JSON output")
	command.Flags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("invalid baseline run status arguments"))
	})
	return command
}

func baselineRunCommandError(err error) error {
	reason := baseline.SafeRunReason(err)
	switch baseline.RunFailure(err) {
	case baseline.RunFailureInput:
		return withCLIExitCode(baselineRunUsageExitCode, fmt.Errorf("baseline run input rejected: %s", reason))
	case baseline.RunFailureAuth:
		return withCLIExitCode(baselineRunAuthExitCode, fmt.Errorf("baseline run authorization failed: %s", reason))
	case baseline.RunFailureCapability:
		return withCLIExitCode(baselineRunCapabilityExitCode, fmt.Errorf("baseline run unavailable: %s", reason))
	case baseline.RunFailureConflict:
		return withCLIExitCode(baselineRunConflictExitCode, fmt.Errorf("baseline run intent rejected: %s", reason))
	case baseline.RunFailureRetryable:
		return withCLIExitCode(baselineRunRetryableExitCode, fmt.Errorf("baseline run incomplete: %s", reason))
	case baseline.RunFailureTerminal:
		return withCLIExitCode(baselineRunTerminalExitCode, fmt.Errorf("baseline run terminated: %s", reason))
	case baseline.RunFailureContract:
		return withCLIExitCode(baselineRunContractExitCode, fmt.Errorf("baseline run server contract failed: %s", reason))
	default:
		return withCLIExitCode(baselineRunInternalExitCode, fmt.Errorf("baseline run failed internally"))
	}
}
