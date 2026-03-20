package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Global config lives in ~/.compair/config.yaml
type Global struct {
	APIBase   string          `yaml:"api_base,omitempty"`
	Defaults  map[string]any  `yaml:"defaults,omitempty"`
	Telemetry TelemetryConfig `yaml:"telemetry,omitempty"`
}

type TelemetryConfig struct {
	Enabled         bool   `yaml:"enabled,omitempty"`
	InstallID       string `yaml:"install_id,omitempty"`
	LastHeartbeatAt string `yaml:"last_heartbeat_at,omitempty"`
}

func globalDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(h, ".compair")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

func GlobalConfigPath() (string, error) {
	d, err := globalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

func ReadGlobal() (Global, error) {
	p, err := GlobalConfigPath()
	if err != nil {
		return Global{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Global{}, nil
		}
		return Global{}, err
	}
	var g Global
	if err := yaml.Unmarshal(b, &g); err != nil {
		return Global{}, err
	}
	return g, nil
}

func WriteGlobal(g Global) error {
	p, err := GlobalConfigPath()
	if err != nil {
		return err
	}
	b, _ := yaml.Marshal(g)
	return os.WriteFile(p, b, 0o600)
}

// Active group resolution uses: env COMPAIR_ACTIVE_GROUP -> flag --group -> ~/.compair/active_group
// The flag is expected to be provided by the caller. This function reads env and active_group file.
func ResolveActiveGroup(flagVal string) (string, error) {
	if v := os.Getenv("COMPAIR_ACTIVE_GROUP"); strings.TrimSpace(v) != "" {
		return v, nil
	}
	if strings.TrimSpace(flagVal) != "" {
		return flagVal, nil
	}
	d, err := globalDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(d, "active_group")
	b, err := os.ReadFile(p)
	if err == nil {
		v := strings.TrimSpace(string(b))
		if v != "" {
			return v, nil
		}
	}
	return "", errors.New("no active group set; use 'compair group use <id>' or set --group/COMPAIR_ACTIVE_GROUP")
}

func WriteActiveGroup(id string) error {
	d, err := globalDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "active_group"), []byte(strings.TrimSpace(id)), 0o600)
}
