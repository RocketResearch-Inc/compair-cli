package compair

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	baselineRepositoryUsageExitCode     = 2
	baselineRepositoryAuthExitCode      = 3
	baselineRepositoryContractExitCode  = 4
	baselineRepositoryTransportExitCode = 5
)

func newBaselineRepositoryCommand() *cobra.Command {
	var allowLoopbackHTTP bool
	command := &cobra.Command{Use: "repository", Short: "Manage approved local baseline repository bindings"}
	command.PersistentFlags().BoolVar(&allowLoopbackHTTP, "allow-loopback-http", false, "Explicitly permit HTTP only when the connected peer is loopback")

	var registerPath, sourceDocumentID, displayName string
	var registerJSON bool
	register := &cobra.Command{
		Use: "register", Short: "Approve a local logical repository and create its protected binding", Args: baselineNoPositionalArgs("baseline repository register"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := resolveExplicitBaselineGroup(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(registerPath) == "" {
				return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("baseline repository register requires --path"))
			}
			result, err := baseline.RegisterLocalRepository(cmd.Context(), groupID, registerPath, strings.TrimSpace(sourceDocumentID), displayName, baselineRepositoryOptions(allowLoopbackHTTP))
			if err != nil {
				return baselineRepositoryCommandError(err)
			}
			if registerJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered %s (%s); protected binding %s\n", result.RegistrationID, result.State, result.BindingID)
			return nil
		},
	}
	register.Flags().StringVar(&registerPath, "path", "", "Exact local Git repository root")
	register.Flags().StringVar(&sourceDocumentID, "source-document-id", "", "Authoritative changed-repository document ID")
	register.Flags().StringVar(&displayName, "name", "", "Local-only safe display name")
	register.Flags().BoolVar(&registerJSON, "json", false, "Emit one safe JSON result")

	var listJSON bool
	list := &cobra.Command{
		Use: "list", Short: "List repository registrations as a group administrator", Args: baselineNoPositionalArgs("baseline repository list"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := resolveExplicitBaselineGroup(cmd)
			if err != nil {
				return err
			}
			result, err := baseline.ListRepositoryRegistrations(cmd.Context(), groupID, baselineRepositoryOptions(allowLoopbackHTTP))
			if err != nil {
				return baselineRepositoryCommandError(err)
			}
			if listJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(writer, "REGISTRATION\tSTATE\tSOURCE DOCUMENT")
			for _, item := range result.Repositories {
				source := "-"
				if item.SourceDocumentID != nil {
					source = *item.SourceDocumentID
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\n", item.RegistrationID, item.State, source)
			}
			return writer.Flush()
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "Emit one safe JSON result")

	var inspectRegistrationID string
	var inspectJSON bool
	inspect := &cobra.Command{
		Use: "inspect", Short: "Inspect one authorized repository registration", Args: baselineNoPositionalArgs("baseline repository inspect"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := resolveExplicitBaselineGroup(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(inspectRegistrationID) == "" {
				return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("baseline repository inspect requires --registration-id"))
			}
			result, err := baseline.InspectRepositoryRegistration(cmd.Context(), groupID, strings.TrimSpace(inspectRegistrationID), baselineRepositoryOptions(allowLoopbackHTTP))
			if err != nil {
				return baselineRepositoryCommandError(err)
			}
			if inspectJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			source := "-"
			if result.Repository.SourceDocumentID != nil {
				source = *result.Repository.SourceDocumentID
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registration: %s\nState: %s\nSource document: %s\n", result.Repository.RegistrationID, result.Repository.State, source)
			return nil
		},
	}
	inspect.Flags().StringVar(&inspectRegistrationID, "registration-id", "", "Opaque Core repository registration ID")
	inspect.Flags().BoolVar(&inspectJSON, "json", false, "Emit one safe JSON result")

	var stateRegistrationID string
	var stateActive, stateJSON bool
	state := &cobra.Command{
		Use: "state", Short: "Enable or disable a repository registration", Args: baselineNoPositionalArgs("baseline repository state"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := resolveExplicitBaselineGroup(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(stateRegistrationID) == "" || !cmd.Flags().Changed("active") {
				return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("baseline repository state requires --registration-id and --active=true|false"))
			}
			result, err := baseline.SetRepositoryRegistrationState(cmd.Context(), groupID, strings.TrimSpace(stateRegistrationID), stateActive, baselineRepositoryOptions(allowLoopbackHTTP))
			if err != nil {
				return baselineRepositoryCommandError(err)
			}
			if stateJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registration %s is %s\n", result.RegistrationID, result.State)
			return nil
		},
	}
	state.Flags().StringVar(&stateRegistrationID, "registration-id", "", "Opaque Core repository registration ID")
	state.Flags().BoolVar(&stateActive, "active", false, "Set registration active state")
	state.Flags().BoolVar(&stateJSON, "json", false, "Emit one safe JSON result")

	var bindRegistrationID, bindPath, bindName string
	var bindJSON bool
	bind := &cobra.Command{
		Use: "bind", Short: "Explicitly bind another local working copy to an approved registration", Args: baselineNoPositionalArgs("baseline repository bind"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := resolveExplicitBaselineGroup(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(bindRegistrationID) == "" || strings.TrimSpace(bindPath) == "" {
				return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("baseline repository bind requires --registration-id and --path"))
			}
			result, err := baseline.BindLocalRepository(cmd.Context(), groupID, strings.TrimSpace(bindRegistrationID), bindPath, bindName, baselineRepositoryOptions(allowLoopbackHTTP))
			if err != nil {
				return baselineRepositoryCommandError(err)
			}
			if bindJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Bound %s with protected binding %s\n", result.RegistrationID, result.BindingID)
			return nil
		},
	}
	bind.Flags().StringVar(&bindRegistrationID, "registration-id", "", "Opaque Core repository registration ID")
	bind.Flags().StringVar(&bindPath, "path", "", "Exact local Git repository root")
	bind.Flags().StringVar(&bindName, "name", "", "Local-only safe display name")
	bind.Flags().BoolVar(&bindJSON, "json", false, "Emit one safe JSON result")

	for _, child := range []*cobra.Command{register, list, inspect, state, bind} {
		child.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
			return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("invalid baseline repository arguments"))
		})
		command.AddCommand(child)
	}
	return command
}

func baselineNoPositionalArgs(name string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("%s accepts no positional arguments", name))
		}
		return nil
	}
}

func resolveExplicitBaselineGroup(cmd *cobra.Command) (string, error) {
	groupIdent, explicit := flagValue(cmd, "group")
	if !explicit || strings.TrimSpace(groupIdent) == "" {
		return "", withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("an explicit --group is required"))
	}
	groupID, err := groups.ResolveID(api.NewClient(viper.GetString("api.base")), groupIdent, "")
	if err != nil {
		return "", withCLIExitCode(baselineRepositoryAuthExitCode, err)
	}
	return groupID, nil
}

func baselineRepositoryOptions(allowLoopbackHTTP bool) baseline.RepositoryOptions {
	return baseline.RepositoryOptions{BaseURL: viper.GetString("api.base"), Token: auth.Token(), AllowLoopbackHTTP: allowLoopbackHTTP}
}

func baselineRepositoryCommandError(err error) error {
	safe := baseline.SafeRepositoryReason(err)
	switch baseline.RepositoryFailure(err) {
	case baseline.RepositoryFailureUsage:
		return withCLIExitCode(baselineRepositoryUsageExitCode, fmt.Errorf("baseline repository operation failed: %s", safe))
	case baseline.RepositoryFailureAuthentication:
		return withCLIExitCode(baselineRepositoryAuthExitCode, fmt.Errorf("baseline repository operation failed: %s", safe))
	case baseline.RepositoryFailureContract, baseline.RepositoryFailureRepository:
		return withCLIExitCode(baselineRepositoryContractExitCode, fmt.Errorf("baseline repository operation failed: %s", safe))
	default:
		return withCLIExitCode(baselineRepositoryTransportExitCode, fmt.Errorf("baseline repository operation failed: %s", safe))
	}
}
