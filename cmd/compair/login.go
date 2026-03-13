package compair

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var loginToken string
var loginTokenUserID string
var loginTokenUsername string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Compair",
	Long: "Login to Compair using the best available method for the target server.\n\n" +
		"- single-user Core: auto-establish a local session\n" +
		"- Cloud with Google enabled: open the browser and complete sign-in\n" +
		"- password-based auth: prompt for email/password unless provided via flags\n" +
		"- CI/device handoff: save an existing auth token with --token",
	RunE: func(cmd *cobra.Command, args []string) error {
		if loginToken != "" {
			cred := auth.Credentials{
				AuthToken:   loginToken,
				AccessToken: loginToken,
				UserID:      loginTokenUserID,
				Username:    loginTokenUsername,
			}
			return finishLogin(cred, nil)
		}

		client := api.NewClient(viper.GetString("api.base"))
		caps, err := client.Capabilities(10 * time.Minute)
		if err != nil {
			return err
		}
		if !caps.Auth.Required {
			return loginSingleUser(client, caps)
		}

		email, _ := cmd.Flags().GetString("email")
		password, _ := cmd.Flags().GetString("password")
		if strings.TrimSpace(email) != "" || strings.TrimSpace(password) != "" {
			return loginWithPassword(client, caps, email, password)
		}

		method, err := chooseLoginMethod(caps)
		if err != nil {
			return err
		}
		switch method {
		case "browser":
			return loginWithBrowser(client, caps)
		case "password":
			return loginWithPassword(client, caps, "", "")
		default:
			return fmt.Errorf("unsupported login method: %s", method)
		}
	},
}

var loginBrowserCmd = &cobra.Command{
	Use:     "browser",
	Aliases: []string{"web", "google"},
	Short:   "Login in the browser using Google sign-in",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		caps, err := client.Capabilities(10 * time.Minute)
		if err != nil {
			return err
		}
		if !caps.Auth.Required {
			return loginSingleUser(client, caps)
		}
		return loginWithBrowser(client, caps)
	},
}

func finishLogin(cred auth.Credentials, caps *api.Capabilities) error {
	if err := auth.Save(cred); err != nil {
		return err
	}
	auth.PrintPostLogin(cred)
	_ = api.ClearCapabilitiesCache()
	if caps != nil && !caps.Inputs.OCR {
		fmt.Println("Note: OCR uploads are disabled on this server. Configure COMPAIR_OCR_ENDPOINT or upgrade to Compair Cloud for OCR support.")
	}
	return nil
}

func loginSingleUser(client *api.Client, caps *api.Capabilities) error {
	fmt.Println("This server is running in single-user mode; no login required.")
	session, err := client.EnsureSession()
	if err != nil {
		return fmt.Errorf("failed to establish session: %w", err)
	}
	userInfo, err := client.LoadUserByID(session.UserID)
	if err != nil {
		userInfo.Username = ""
	}
	cred := auth.Credentials{
		AuthToken: session.ID,
		UserID:    session.UserID,
		Username:  userInfo.Username,
	}
	return finishLogin(cred, caps)
}

func loginWithPassword(client *api.Client, caps *api.Capabilities, email, password string) error {
	if !caps.Auth.PasswordLogin {
		return fmt.Errorf("password login is not enabled on this server")
	}
	var err error
	email = strings.TrimSpace(email)
	if email == "" {
		email, err = promptLine("Email: ")
		if err != nil {
			return err
		}
	}
	if email == "" {
		return fmt.Errorf("email is required")
	}
	password = strings.TrimSpace(password)
	if password == "" {
		password, err = promptPassword("Password: ")
		if err != nil {
			return err
		}
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}
	res, err := client.Login(email, password)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(res.AuthToken)
	if token == "" {
		token = strings.TrimSpace(res.AccessToken)
	}
	if token == "" {
		return fmt.Errorf("login succeeded but returned no auth token")
	}
	cred := auth.Credentials{
		AuthToken:    token,
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		UserID:       res.UserID,
		Username:     res.Username,
	}
	return finishLogin(cred, caps)
}

func loginWithBrowser(client *api.Client, caps *api.Capabilities) error {
	if !caps.Auth.GoogleOAuth {
		if caps.Auth.DeviceFlow {
			return fmt.Errorf("this server advertises external sign-in but no browser-capable OAuth provider is configured; use 'compair login --token <auth_token>' after completing sign-in elsewhere")
		}
		return fmt.Errorf("browser sign-in is not enabled on this server")
	}
	start, err := client.StartGoogleOAuthDevice()
	if err != nil {
		return err
	}
	authURL := strings.TrimSpace(start.AuthURL)
	pollToken := strings.TrimSpace(start.PollToken)
	if authURL == "" || pollToken == "" {
		return fmt.Errorf("google sign-in start response was incomplete")
	}
	var expiresAt time.Time
	if raw := strings.TrimSpace(start.ExpiresAt); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			expiresAt = parsed
		}
	}

	fmt.Println("Opening browser for Google sign-in...")
	fmt.Printf("If the browser does not open, visit:\n  %s\n", authURL)
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser automatically: %v\n", err)
	}
	fmt.Println("Waiting for sign-in to complete...")

	lastNotice := time.Time{}
	for {
		if !expiresAt.IsZero() && time.Now().After(expiresAt.Add(5*time.Second)) {
			return fmt.Errorf("google sign-in expired; start again")
		}
		res, err := client.PollGoogleOAuthDevice(pollToken)
		if err != nil {
			return err
		}
		status := strings.ToLower(strings.TrimSpace(res.Status))
		if status != "complete" && strings.TrimSpace(res.AuthToken) != "" {
			status = "complete"
		}
		switch status {
		case "", "pending":
			if lastNotice.IsZero() || time.Since(lastNotice) >= 30*time.Second {
				fmt.Println("Still waiting for browser sign-in...")
				lastNotice = time.Now()
			}
			time.Sleep(2 * time.Second)
		case "complete":
			token := strings.TrimSpace(res.AuthToken)
			if token == "" {
				return fmt.Errorf("google sign-in completed but returned no auth token")
			}
			cred := auth.Credentials{
				AuthToken:   token,
				AccessToken: token,
				UserID:      strings.TrimSpace(res.UserID),
				Username:    strings.TrimSpace(res.Username),
			}
			return finishLogin(cred, caps)
		case "error":
			msg := strings.TrimSpace(res.Error)
			if msg == "" {
				msg = "google sign-in failed"
			}
			return fmt.Errorf("%s", msg)
		case "expired":
			msg := strings.TrimSpace(res.Error)
			if msg == "" {
				msg = "google sign-in expired; start again"
			}
			return fmt.Errorf("%s", msg)
		default:
			return fmt.Errorf("unexpected google sign-in status: %s", res.Status)
		}
	}
}

func chooseLoginMethod(caps *api.Capabilities) (string, error) {
	passwordAvailable := caps.Auth.PasswordLogin
	browserAvailable := caps.Auth.GoogleOAuth
	switch {
	case browserAvailable && passwordAvailable:
		fmt.Println("Available login methods:")
		fmt.Println("  1. Browser (Google)")
		fmt.Println("  2. Email/password")
		for {
			choice, err := promptLine("Choose a login method [1/2]: ")
			if err != nil {
				return "", err
			}
			switch strings.TrimSpace(strings.ToLower(choice)) {
			case "1", "browser", "google", "web":
				return "browser", nil
			case "2", "password", "email":
				return "password", nil
			}
			fmt.Println("Enter 1 for browser sign-in or 2 for email/password.")
		}
	case browserAvailable:
		return "browser", nil
	case passwordAvailable:
		return "password", nil
	case caps.Auth.DeviceFlow:
		return "", fmt.Errorf("this server advertises external sign-in but no browser-capable OAuth provider is configured; use 'compair login --token <auth_token>' after completing sign-in elsewhere")
	default:
		return "", fmt.Errorf("this server doesn't expose a supported interactive login method")
	}
}

func promptLine(label string) (string, error) {
	fmt.Print(label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptPassword(label string) (string, error) {
	fmt.Print(label)
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pwBytes)), nil
}

func openBrowser(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("browser target is empty")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().String("email", "", "Login email")
	loginCmd.Flags().String("password", "", "Login password")
	loginCmd.Flags().StringVar(&loginToken, "token", "", "Save an existing auth/access token directly (for CI or external sign-in flows)")
	loginCmd.Flags().StringVar(&loginTokenUserID, "user-id", "", "Optional user ID to persist with --token")
	loginCmd.Flags().StringVar(&loginTokenUsername, "username", "", "Optional username to persist with --token")
	loginCmd.AddCommand(loginBrowserCmd)
}
