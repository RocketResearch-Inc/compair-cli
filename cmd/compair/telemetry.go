package compair

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cliTelemetry "github.com/RocketResearch-Inc/compair-cli/internal/telemetry"
)

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Manage anonymous opt-in CLI telemetry",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printTelemetryStatus()
	},
}

var telemetryOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Enable anonymous CLI telemetry",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := cliTelemetry.Enable()
		if err != nil {
			return err
		}
		fmt.Println("Anonymous CLI telemetry enabled.")
		fmt.Printf("Endpoint: %s\n", status.BaseURL)
		fmt.Printf("Install ID: %s\n", orUnset(status.InstallID))
		return nil
	},
}

var telemetryOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Disable anonymous CLI telemetry",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := cliTelemetry.Disable()
		if err != nil {
			return err
		}
		fmt.Println("Anonymous CLI telemetry disabled.")
		if strings.TrimSpace(status.InstallID) != "" {
			fmt.Printf("Install ID retained locally: %s\n", status.InstallID)
		}
		return nil
	},
}

var telemetryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show anonymous CLI telemetry status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printTelemetryStatus()
	},
}

func init() {
	rootCmd.AddCommand(telemetryCmd)
	telemetryCmd.AddCommand(telemetryStatusCmd)
	telemetryCmd.AddCommand(telemetryOnCmd)
	telemetryCmd.AddCommand(telemetryOffCmd)
}

func printTelemetryStatus() error {
	status, err := cliTelemetry.CurrentStatus()
	if err != nil {
		return err
	}
	fmt.Println("CLI telemetry")
	fmt.Printf("  Enabled: %s\n", onOff(status.Enabled))
	fmt.Printf("  Endpoint: %s\n", status.BaseURL)
	fmt.Printf("  Install ID: %s\n", orUnset(status.InstallID))
	fmt.Printf("  Last heartbeat: %s\n", orUnset(status.LastHeartbeatAt))
	fmt.Println("  Scope: one anonymous daily heartbeat with CLI version, OS, arch, and last command name.")
	return nil
}

func orUnset(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unset)"
	}
	return strings.TrimSpace(v)
}
