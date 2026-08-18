package compair

import (
	"fmt"

	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := auth.Remove(); err != nil {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	},
}

func init() { rootCmd.AddCommand(logoutCmd) }
