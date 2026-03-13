package compair

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Print the current authenticated user",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := auth.Load()
		if err != nil {
			return fmt.Errorf("not logged in")
		}
		if c.Username != "" {
			fmt.Printf("%s (%s)\n", c.Username, c.UserID)
		}
		if c.Username == "" && c.UserID != "" {
			fmt.Println(c.UserID)
		}
		if c.Username == "" && c.UserID == "" {
			fmt.Println("Unknown user (token present)")
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(whoamiCmd) }
