package telemetry

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

const (
	defaultBaseURL         = "https://app.compair.sh/api"
	heartbeatInterval      = 24 * time.Hour
	maxHTTPResponsePreview = 512
)

type Status struct {
	Enabled         bool
	InstallID       string
	LastHeartbeatAt string
	BaseURL         string
}

func BaseURL() string {
	if raw := strings.TrimSpace(os.Getenv("COMPAIR_TELEMETRY_BASE")); raw != "" {
		return strings.TrimRight(raw, "/")
	}
	return defaultBaseURL
}

func CurrentStatus() (Status, error) {
	global, err := config.ReadGlobal()
	if err != nil {
		return Status{}, err
	}
	return Status{
		Enabled:         global.Telemetry.Enabled,
		InstallID:       strings.TrimSpace(global.Telemetry.InstallID),
		LastHeartbeatAt: strings.TrimSpace(global.Telemetry.LastHeartbeatAt),
		BaseURL:         BaseURL(),
	}, nil
}

func Enable() (Status, error) {
	global, err := config.ReadGlobal()
	if err != nil {
		return Status{}, err
	}
	global.Telemetry.Enabled = true
	if strings.TrimSpace(global.Telemetry.InstallID) == "" {
		global.Telemetry.InstallID = newInstallID()
	}
	if err := config.WriteGlobal(global); err != nil {
		return Status{}, err
	}
	return CurrentStatus()
}

func Disable() (Status, error) {
	global, err := config.ReadGlobal()
	if err != nil {
		return Status{}, err
	}
	global.Telemetry.Enabled = false
	if err := config.WriteGlobal(global); err != nil {
		return Status{}, err
	}
	return CurrentStatus()
}

func MaybeSendDailyHeartbeat(command string, version string) error {
	global, err := config.ReadGlobal()
	if err != nil {
		return err
	}
	if !global.Telemetry.Enabled {
		return nil
	}
	if strings.TrimSpace(global.Telemetry.InstallID) == "" {
		global.Telemetry.InstallID = newInstallID()
		if err := config.WriteGlobal(global); err != nil {
			return err
		}
	}
	if last, ok := parseRFC3339(global.Telemetry.LastHeartbeatAt); ok && time.Since(last) < heartbeatInterval {
		return nil
	}

	now := time.Now().UTC()
	event := map[string]any{
		"install_id":      global.Telemetry.InstallID,
		"client_event_id": heartbeatEventID(global.Telemetry.InstallID, now),
		"event":           "active_day",
		"source":          "cli",
		"kind":            "usage",
		"app_version":     strings.TrimSpace(version),
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
		"ts":              now.Unix(),
		"payload": map[string]any{
			"command": strings.TrimSpace(command),
		},
	}
	payload := map[string]any{
		"client": "cli",
		"events": []map[string]any{event},
	}
	if err := postJSON(BaseURL()+"/client-metrics/anonymous", payload); err != nil {
		return err
	}

	global.Telemetry.LastHeartbeatAt = now.Format(time.RFC3339)
	return config.WriteGlobal(global)
}

func newInstallID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("cli-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func heartbeatEventID(installID string, now time.Time) string {
	sum := sha256.Sum256([]byte(installID + "|cli|active_day|" + now.UTC().Format("2006-01-02")))
	return hex.EncodeToString(sum[:16])
}

func parseRFC3339(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func postJSON(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "compair-cli")
	req.Header.Set("X-Compair-Client", "cli")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponsePreview))
		return fmt.Errorf("telemetry POST failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
