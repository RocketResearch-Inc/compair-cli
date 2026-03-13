package compair

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

var profileCmd = &cobra.Command{Use: "profile", Short: "Manage API profiles"}

var profileLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List configured profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		profs, err := config.LoadProfiles()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(profs.Profiles))
		for name := range profs.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Println("Name\tAPI Base")
		for _, name := range names {
			mark := ""
			if name == profs.Default {
				mark = " (default)"
			}
			fmt.Printf("%s\t%s%s\n", name, profs.Profiles[name].APIBase, mark)
		}
		return nil
	},
}

var profileUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the default profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		profs, err := config.LoadProfiles()
		if err != nil {
			return err
		}
		if _, ok := profs.Profiles[name]; !ok {
			return fmt.Errorf("profile '%s' not found", name)
		}
		profs.Default = name
		if err := config.SaveProfiles(profs); err != nil {
			return err
		}
		_ = api.ClearCapabilitiesCache()
		fmt.Println("Default profile set to", name)
		return nil
	},
}

var profileSetAPICmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Create or update a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		profs, err := config.LoadProfiles()
		if err != nil {
			return err
		}
		prof := profs.Profiles[name]
		apiBase, _ := cmd.Flags().GetString("api-base")
		if apiBase == "" {
			if prof.APIBase == "" {
				return fmt.Errorf("--api-base is required for new profiles")
			}
		} else {
			prof.APIBase = apiBase
		}
		applySnapshotProfileOverrides(cmd, &prof)
		profs.Profiles[name] = prof
		if profs.Default == "" {
			profs.Default = name
		}
		if err := config.SaveProfiles(profs); err != nil {
			return err
		}
		_ = api.ClearCapabilitiesCache()
		fmt.Printf("Profile '%s' set to %s\n", name, prof.APIBase)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileLsCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileSetAPICmd.Flags().String("api-base", "", "API base URL for this profile")
	profileSetAPICmd.Flags().Int("snapshot-max-tree", 0, "Snapshot limit: max tree entries (0 = full repo default)")
	profileSetAPICmd.Flags().Int("snapshot-max-files", 0, "Snapshot limit: max included files (0 = full repo default)")
	profileSetAPICmd.Flags().Int("snapshot-max-total-bytes", 0, "Snapshot limit: total content budget in bytes (0 = full repo default)")
	profileSetAPICmd.Flags().Int("snapshot-max-file-bytes", 0, "Snapshot limit: max bytes per file (0 = full file default)")
	profileSetAPICmd.Flags().Int("snapshot-max-file-read", 0, "Snapshot limit: max bytes read per file (0 = no read cap by default)")
	profileSetAPICmd.Flags().StringArray("snapshot-include", nil, "Snapshot include glob (repeatable)")
	profileSetAPICmd.Flags().StringArray("snapshot-exclude", nil, "Snapshot exclude glob (repeatable)")
	profileCmd.AddCommand(profileSetAPICmd)
}

func applySnapshotProfileOverrides(cmd *cobra.Command, prof *config.Profile) {
	if prof == nil {
		return
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-tree"); ok {
		prof.Snapshot.MaxTreeEntries = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-files"); ok {
		prof.Snapshot.MaxFiles = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-total-bytes"); ok {
		prof.Snapshot.MaxTotalBytes = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-file-bytes"); ok {
		prof.Snapshot.MaxFileBytes = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-file-read"); ok {
		prof.Snapshot.MaxFileRead = normalizeSnapshotLimit(v)
	}
	if v, ok := getStringArrayFlagIfChanged(cmd, "snapshot-include"); ok {
		prof.Snapshot.IncludeGlobs = v
	}
	if v, ok := getStringArrayFlagIfChanged(cmd, "snapshot-exclude"); ok {
		prof.Snapshot.ExcludeGlobs = v
	}
}
