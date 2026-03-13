package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Group struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

type Repo struct {
	Provider                   string `yaml:"provider"`
	RemoteURL                  string `yaml:"remote_url"`
	RepoID                     string `yaml:"repo_id"`
	DefaultBranch              string `yaml:"default_branch"`
	LastSyncedCommit           string `yaml:"last_synced_commit"`
	DocumentID                 string `yaml:"document_id"`
	Unpublished                bool   `yaml:"unpublished,omitempty"`
	PendingTaskID              string `yaml:"pending_task_id,omitempty"`
	PendingTaskCommit          string `yaml:"pending_task_commit,omitempty"`
	PendingTaskInitialFeedback int    `yaml:"pending_task_initial_feedback,omitempty"`
	PendingTaskStartedAt       string `yaml:"pending_task_started_at,omitempty"`
}

type Project struct {
	Version     int    `yaml:"version"`
	ProjectName string `yaml:"project_name"`
	Group       Group  `yaml:"group"`
	Repos       []Repo `yaml:"repos"`
}

func projectPath(root string) string { return filepath.Join(root, ".compair", "config.yaml") }

func ReadProjectConfig(root string) (Project, error) {
	p := projectPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		return Project{}, err
	}
	var cfg Project
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Project{}, err
	}
	return cfg, nil
}

func WriteProjectConfig(root string, cfg Project) error {
	p := projectPath(root)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := yaml.Marshal(cfg)
	return os.WriteFile(p, b, 0o644)
}

// Debug helper
func (p Project) JSON() string { b, _ := json.MarshalIndent(p, "", "  "); return string(b) }
