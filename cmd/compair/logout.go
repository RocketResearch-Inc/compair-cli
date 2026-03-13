package compair

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		p := filepath.Join(h, ".compair", "credentials.json")
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	},
}

func init() { rootCmd.AddCommand(logoutCmd) }
