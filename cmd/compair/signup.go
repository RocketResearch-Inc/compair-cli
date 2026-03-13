package compair

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

var signupEmail string
var signupName string
var signupReferral string

var signupCmd = &cobra.Command{
	Use:   "signup",
	Short: "Create a new Compair account",
	RunE: func(cmd *cobra.Command, args []string) error {
		email := strings.TrimSpace(signupEmail)
		if email == "" {
			return errors.New("email is required (use --email)")
		}
		name := strings.TrimSpace(signupName)
		if name == "" {
			if i := strings.Index(email, "@"); i > 0 {
				name = email[:i]
			} else {
				name = email
			}
		}

		fmt.Print("Password: ")
		pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		password := strings.TrimSpace(string(pwBytes))
		if password == "" {
			return errors.New("password cannot be empty")
		}

		client := api.NewClient(viper.GetString("api.base"))
		res, err := client.SignUp(email, name, password, signupReferral)
		if err != nil {
			return err
		}
		if msg := strings.TrimSpace(res.Message); msg != "" {
			fmt.Println(msg)
			return nil
		}
		fmt.Println("Sign-up successful.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(signupCmd)
	signupCmd.Flags().StringVar(&signupEmail, "email", "", "Email address for the new account")
	signupCmd.Flags().StringVar(&signupName, "name", "", "Display name (optional)")
	signupCmd.Flags().StringVar(&signupReferral, "referral", "", "Referral code (optional)")
}
