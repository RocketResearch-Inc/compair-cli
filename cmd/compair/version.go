package compair

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""

	versionJSON bool
)

type versionReport struct {
	CLI struct {
		Version string `json:"version"`
		Commit  string `json:"commit,omitempty"`
		Built   string `json:"built,omitempty"`
	} `json:"cli"`
	API struct {
		Base      string `json:"base"`
		Profile   string `json:"profile,omitempty"`
		Reachable bool   `json:"reachable"`
		Server    string `json:"server,omitempty"`
		Version   string `json:"version,omitempty"`
		Detail    string `json:"detail,omitempty"`
	} `json:"api"`
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show CLI and target server version information",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		report := versionReport{}
		report.CLI.Version = resolvedCLIVersion()
		report.CLI.Commit = resolvedCLICommit()
		report.CLI.Built = resolvedCLIBuildDate()
		report.API.Base = strings.TrimSpace(viper.GetString("api.base"))
		report.API.Profile = strings.TrimSpace(viper.GetString("profile.active"))

		if caps, err := fetchPublicCapabilities(report.API.Base); err != nil {
			report.API.Reachable = false
			report.API.Detail = strings.TrimSpace(err.Error())
		} else {
			report.API.Reachable = true
			report.API.Server = strings.TrimSpace(caps.Server)
			report.API.Version = strings.TrimSpace(caps.Version)
		}

		if versionJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}

		fmt.Println("Compair version")
		fmt.Println()
		fmt.Println("CLI:")
		fmt.Printf("  Version: %s\n", orUnknown(report.CLI.Version))
		fmt.Printf("  Commit: %s\n", orUnknown(report.CLI.Commit))
		fmt.Printf("  Built: %s\n", orUnknown(report.CLI.Built))
		fmt.Println()
		fmt.Println("API:")
		fmt.Printf("  Base: %s\n", orUnknown(report.API.Base))
		if report.API.Profile != "" {
			fmt.Printf("  Profile: %s\n", report.API.Profile)
		}
		if report.API.Reachable {
			fmt.Println("  Reachable: yes")
			fmt.Printf("  Server: %s\n", orUnknown(report.API.Server))
			fmt.Printf("  Version: %s\n", orUnknown(report.API.Version))
		} else {
			fmt.Println("  Reachable: no")
			if report.API.Detail != "" {
				fmt.Printf("  Detail: %s\n", report.API.Detail)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "Output machine-readable version details JSON")
}

func resolvedCLIVersion() string {
	if strings.TrimSpace(Version) != "" && Version != "dev" {
		return strings.TrimSpace(Version)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	return strings.TrimSpace(Version)
}

func resolvedCLICommit() string {
	if strings.TrimSpace(Commit) != "" {
		return strings.TrimSpace(Commit)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		revision := ""
		modified := false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = strings.TrimSpace(setting.Value)
			case "vcs.modified":
				modified = strings.TrimSpace(setting.Value) == "true"
			}
		}
		if revision != "" {
			if modified {
				return revision + "-dirty"
			}
			return revision
		}
	}
	return strings.TrimSpace(Commit)
}

func resolvedCLIBuildDate() string {
	if strings.TrimSpace(BuildDate) != "" {
		return strings.TrimSpace(BuildDate)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.time" {
				return strings.TrimSpace(setting.Value)
			}
		}
	}
	return strings.TrimSpace(BuildDate)
}

func fetchPublicCapabilities(base string) (*api.Capabilities, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "http://localhost:4000"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/capabilities", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET /capabilities: %s", resp.Status)
	}
	var caps api.Capabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return nil, err
	}
	return &caps, nil
}

func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unknown)"
	}
	return strings.TrimSpace(v)
}
