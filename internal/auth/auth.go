package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/appdir"
)

type Credentials struct {
	AuthToken    string    `json:"auth_token,omitempty"` // legacy
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	Username     string    `json:"username,omitempty"`
}

func credsPath() (string, error) {
	d, err := appdir.Root()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return appdir.Path("credentials.json")
}

func Save(c Credentials) error {
	p, err := credsPath()
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	return nil
}

func Load() (Credentials, error) {
	p, err := credsPath()
	if err != nil {
		return Credentials{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, err
	}
	return c, nil
}

func Token() string {
	c, err := Load()
	if err != nil {
		return ""
	}
	if c.AccessToken != "" {
		return c.AccessToken
	}
	return c.AuthToken
}

func Remove() error {
	p, err := credsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func PrintPostLogin(c Credentials) {
	fmt.Println("Logged in. Token stored in private Compair application state.")
}
