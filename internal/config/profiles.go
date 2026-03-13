package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultCloudAPIBase = "https://app.compair.sh/api"
	legacyCloudAPIBase  = "https://www.compair.sh/api"
	defaultLocalAPIBase = "http://localhost:4000"
)

type Profile struct {
	APIBase  string         `yaml:"api_base"`
	Snapshot SnapshotConfig `yaml:"snapshot,omitempty"`
}

type SnapshotConfig struct {
	MaxTreeEntries int      `yaml:"max_tree_entries,omitempty"`
	MaxFiles       int      `yaml:"max_files,omitempty"`
	MaxTotalBytes  int      `yaml:"max_total_bytes,omitempty"`
	MaxFileBytes   int      `yaml:"max_file_bytes,omitempty"`
	MaxFileRead    int      `yaml:"max_file_read,omitempty"`
	IncludeGlobs   []string `yaml:"include_globs,omitempty"`
	ExcludeGlobs   []string `yaml:"exclude_globs,omitempty"`
}

type Profiles struct {
	Default  string             `yaml:"default"`
	Profiles map[string]Profile `yaml:"profiles"`
}

func defaultProfiles() *Profiles {
	return &Profiles{
		Default: "cloud",
		Profiles: map[string]Profile{
			"cloud": {APIBase: defaultCloudAPIBase},
			"local": {APIBase: defaultLocalAPIBase},
		},
	}
}

func profilesPath() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".compair", "profiles.yaml"), nil
}

func LoadProfiles() (*Profiles, error) {
	p, err := profilesPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			prof := defaultProfiles()
			_ = SaveProfiles(prof)
			return prof, nil
		}
		return nil, err
	}
	var prof Profiles
	if err := yaml.Unmarshal(b, &prof); err != nil {
		return nil, err
	}
	if prof.Profiles == nil {
		prof.Profiles = map[string]Profile{}
	}
	if prof.Default == "" {
		prof.Default = "cloud"
	}
	if ensureDefaultProfiles(&prof) {
		_ = SaveProfiles(&prof)
	}
	return &prof, nil
}

func ensureDefaultProfiles(prof *Profiles) bool {
	if prof == nil {
		return false
	}
	changed := false

	cloud := prof.Profiles["cloud"]
	if cloud.APIBase == "" {
		cloud.APIBase = defaultCloudAPIBase
		prof.Profiles["cloud"] = cloud
		changed = true
	} else if cloud.APIBase == legacyCloudAPIBase {
		cloud.APIBase = defaultCloudAPIBase
		prof.Profiles["cloud"] = cloud
		changed = true
	}

	if local, ok := prof.Profiles["local"]; !ok {
		prof.Profiles["local"] = Profile{APIBase: defaultLocalAPIBase}
		changed = true
	} else if local.APIBase == "" {
		local.APIBase = defaultLocalAPIBase
		prof.Profiles["local"] = local
		changed = true
	}

	return changed
}

func SaveProfiles(prof *Profiles) error {
	p, err := profilesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(prof)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
