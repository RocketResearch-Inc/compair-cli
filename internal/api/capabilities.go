package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Capabilities struct {
	Auth struct {
		PasswordLogin bool `json:"password_login"`
		PasswordReset bool `json:"password_reset"`
		DeviceFlow    bool `json:"device_flow"`
		GoogleOAuth   bool `json:"google_oauth"`
		Required      bool `json:"required"`
		SingleUser    bool `json:"single_user"`
	} `json:"auth"`
	Inputs struct {
		Text  bool `json:"text"`
		OCR   bool `json:"ocr"`
		Repos bool `json:"repos"`
	} `json:"inputs"`
	Models struct {
		Premium bool `json:"premium"`
		Open    bool `json:"open"`
	} `json:"models"`
	Integrations struct {
		Slack  bool `json:"slack"`
		Github bool `json:"github"`
	} `json:"integrations"`
	Limits struct {
		Docs           *int `json:"docs"`
		FeedbackPerDay *int `json:"feedback_per_day"`
	} `json:"limits"`
	Features struct {
		OCRUpload               bool `json:"ocr_upload"`
		ActivityFeed            bool `json:"activity_feed"`
		NotificationEvents      bool `json:"notification_events"`
		NotificationScoring     bool `json:"notification_scoring"`
		NotificationPreferences bool `json:"notification_preferences"`
		NotificationDelivery    bool `json:"notification_delivery"`
	} `json:"features"`
	Server       string `json:"server,omitempty"`
	Version      string `json:"version,omitempty"`
	LegacyRoutes bool   `json:"legacy_routes,omitempty"`
}

type cachedCapabilities struct {
	APIBase    string       `json:"api_base"`
	At         time.Time    `json:"at"`
	TTLSeconds int64        `json:"ttl_seconds"`
	Data       Capabilities `json:"data"`
}

func capabilitiesCachePath() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".compair", "cache", "capabilities.json"), nil
}

func (c *Client) fetchCapabilities() (*Capabilities, error) {
	var caps Capabilities
	if err := c.get("/capabilities", &caps); err != nil {
		return nil, err
	}
	return &caps, nil
}

func defaultCapabilities() *Capabilities {
	caps := &Capabilities{}
	caps.Auth.PasswordLogin = true
	caps.Auth.Required = true
	caps.Inputs.Text = true
	caps.Inputs.OCR = true
	caps.Inputs.Repos = true
	caps.Models.Open = true
	return caps
}

func (c *Client) probeCapabilities() *Capabilities {
	caps := defaultCapabilities()
	req, _ := http.NewRequest(http.MethodGet, c.BaseURL+"/load_session", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return caps
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		caps.Auth.Required = false
		caps.Auth.SingleUser = true
	}
	return caps
}

func (c *Client) Capabilities(ttl time.Duration) (*Capabilities, error) {
	if ttl < 0 {
		ttl = 0
	}
	if ttl > 0 {
		if path, err := capabilitiesCachePath(); err == nil {
			if data, err := os.ReadFile(path); err == nil {
				var cached cachedCapabilities
				if json.Unmarshal(data, &cached) == nil {
					if cached.APIBase == c.BaseURL && cached.TTLSeconds > 0 {
						if time.Since(cached.At) < time.Duration(cached.TTLSeconds)*time.Second {
							return &cached.Data, nil
						}
					}
				}
			}
		}
	}
	caps, err := c.fetchCapabilities()
	if err != nil {
		// Fallback for servers without capabilities endpoint
		return c.probeCapabilities(), nil
	}
	if ttl > 0 {
		if path, err := capabilitiesCachePath(); err == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			cached := cachedCapabilities{
				APIBase:    c.BaseURL,
				At:         time.Now(),
				TTLSeconds: int64(ttl / time.Second),
				Data:       *caps,
			}
			if b, err := json.MarshalIndent(cached, "", "  "); err == nil {
				_ = os.WriteFile(path, b, 0o644)
			}
		}
	}
	return caps, nil
}

func ClearCapabilitiesCache() error {
	path, err := capabilitiesCachePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}
