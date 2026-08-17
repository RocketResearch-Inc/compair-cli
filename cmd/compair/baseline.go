package compair

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	baselinePreviewUsageExitCode     = 2
	baselinePreviewAuthExitCode      = 3
	baselinePreviewNotFoundExitCode  = 4
	baselinePreviewTransportExitCode = 5
)

func newBaselineCommand() *cobra.Command {
	baselineCmd := &cobra.Command{
		Use:   "baseline",
		Short: "Plan, upload, and read opt-in baseline data",
	}

	var jobID string
	var digestID string
	previewCmd := &cobra.Command{
		Use:   "preview",
		Short: "Preview one authorized durable baseline result as JSON",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return withCLIExitCode(
					baselinePreviewUsageExitCode,
					fmt.Errorf("baseline preview accepts no positional arguments"),
				)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupIdent, groupWasExplicit := flagValue(cmd, "group")
			if !groupWasExplicit || strings.TrimSpace(groupIdent) == "" {
				return withCLIExitCode(
					baselinePreviewUsageExitCode,
					fmt.Errorf("baseline preview requires an explicit --group"),
				)
			}
			jobID = strings.TrimSpace(jobID)
			digestID = strings.TrimSpace(digestID)
			if (jobID == "") == (digestID == "") {
				return withCLIExitCode(
					baselinePreviewUsageExitCode,
					fmt.Errorf("use exactly one of --job-id or --digest-id"),
				)
			}

			client := api.NewClient(viper.GetString("api.base"))
			groupID, err := groups.ResolveID(client, groupIdent, "")
			if err != nil {
				return baselinePreviewCommandError(err)
			}
			preview, err := client.PostBaselinePreview(groupID, jobID, digestID)
			if err != nil {
				return baselinePreviewCommandError(err)
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(preview); err != nil {
				return withCLIExitCode(
					baselinePreviewTransportExitCode,
					fmt.Errorf("write baseline preview JSON: %w", err),
				)
			}
			return nil
		},
	}
	previewCmd.Flags().StringVar(&jobID, "job-id", "", "Durable baseline control job ID")
	previewCmd.Flags().StringVar(&digestID, "digest-id", "", "Durable baseline notification digest ID")
	previewCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return withCLIExitCode(baselinePreviewUsageExitCode, err)
	})
	baselineCmd.AddCommand(previewCmd, newBaselineScanCommand(), newBaselineUploadCommand(), newBaselineIndexCommand(), newBaselineRunCommand())
	return baselineCmd
}

func baselinePreviewCommandError(err error) error {
	var responseError *api.BaselinePreviewHTTPError
	if errors.As(err, &responseError) {
		switch responseError.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return withCLIExitCode(baselinePreviewAuthExitCode, err)
		case http.StatusNotFound:
			return withCLIExitCode(baselinePreviewNotFoundExitCode, err)
		default:
			return withCLIExitCode(baselinePreviewTransportExitCode, err)
		}
	}

	// Group-name resolution uses the established generic API client. Preserve
	// that behavior while assigning this command's documented process codes.
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "401") || strings.Contains(message, "403") ||
		strings.Contains(message, "not logged in") {
		return withCLIExitCode(baselinePreviewAuthExitCode, err)
	}
	if strings.Contains(message, "404") || strings.Contains(message, "no group found") {
		return withCLIExitCode(baselinePreviewNotFoundExitCode, err)
	}
	return withCLIExitCode(baselinePreviewTransportExitCode, err)
}

func init() {
	rootCmd.AddCommand(newBaselineCommand())
}
