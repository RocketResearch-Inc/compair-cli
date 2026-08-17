package compair

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/spf13/cobra"
)

func newBaselinePlanCommand() *cobra.Command {
	command := &cobra.Command{Use: "plan", Short: "Create protected local baseline scanner inputs"}
	var changed, base, head, output string
	var siblings []string
	var overwrite, emitJSON, allowLoopbackHTTP bool
	create := &cobra.Command{
		Use: "create", Short: "Resolve approved repository bindings into a scan plan", Args: baselineNoPositionalArgs("baseline plan create"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := resolveExplicitBaselineGroup(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(changed) == "" || strings.TrimSpace(base) == "" || strings.TrimSpace(head) == "" || len(siblings) == 0 || strings.TrimSpace(output) == "" {
				return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("baseline plan create requires --changed, --base, --head, one or more --sibling, and --output"))
			}
			result, err := baseline.CreateLocalScanPlan(cmd.Context(), baseline.PlanCreateInput{GroupID: groupID, ChangedPath: changed, Base: base, Head: head, SiblingPaths: siblings, OutputPath: output, Overwrite: overwrite}, baselineRepositoryOptions(allowLoopbackHTTP))
			if err != nil {
				return baselineRepositoryCommandError(err)
			}
			if emitJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created baseline scan plan %s for %s\n", result.PlanSHA256, result.ChangedRepositoryRegistration)
			return nil
		},
	}
	create.Flags().StringVar(&changed, "changed", "", "Bound changed-repository Git root")
	create.Flags().StringVar(&base, "base", "", "Changed-repository base commit or ref")
	create.Flags().StringVar(&head, "head", "", "Changed-repository head commit or ref")
	create.Flags().StringArrayVar(&siblings, "sibling", nil, "Bound sibling-repository Git root (repeatable)")
	create.Flags().StringVar(&output, "output", "", "New baseline-scanner-inputs.v1 JSON file")
	create.Flags().BoolVar(&overwrite, "overwrite", false, "Explicitly replace an existing regular output file")
	create.Flags().BoolVar(&emitJSON, "json", false, "Emit one safe JSON result")
	create.Flags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")
	create.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("invalid baseline plan arguments"))
	})
	command.AddCommand(create)
	return command
}
