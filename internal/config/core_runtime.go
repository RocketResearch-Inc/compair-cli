package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultCoreImage          = "compairsteven/compair-core"
	defaultCoreContainerName  = "compair-core"
	defaultCoreDataVolume     = "compair-core-data"
	defaultCorePort           = 8000
	defaultCoreAuthMode       = "single-user"
	defaultGenerationProvider = "local"
	defaultEmbeddingProvider  = "local"
	defaultOpenAIModel        = "gpt-5-nano"
	defaultOpenAIEmbedModel   = "text-embedding-3-small"
)

type CoreRuntime struct {
	Image                         string `yaml:"image"`
	ContainerName                 string `yaml:"container_name"`
	DataVolume                    string `yaml:"data_volume"`
	Port                          int    `yaml:"port"`
	AuthMode                      string `yaml:"auth_mode"`
	GenerationProvider            string `yaml:"generation_provider"`
	EmbeddingProvider             string `yaml:"embedding_provider"`
	OpenAIAPIKey                  string `yaml:"openai_api_key,omitempty"`
	OpenAIModel                   string `yaml:"openai_model,omitempty"`
	OpenAICodeModel               string `yaml:"openai_code_model,omitempty"`
	OpenAINotifModel              string `yaml:"openai_notif_model,omitempty"`
	OpenAIEmbedModel              string `yaml:"openai_embed_model,omitempty"`
	OpenAIBaseURL                 string `yaml:"openai_base_url,omitempty"`
	NotificationScoringTimeoutS   int    `yaml:"notification_scoring_timeout_s,omitempty"`
	NotificationScoringMaxRetries int    `yaml:"notification_scoring_max_retries,omitempty"`
	ReferenceTrace                bool   `yaml:"reference_trace,omitempty"`
	ReferenceTraceMaxCandidates   int    `yaml:"reference_trace_max_candidates,omitempty"`
	GenerationEndpoint            string `yaml:"generation_endpoint,omitempty"`
}

func defaultCoreRuntime() *CoreRuntime {
	return normalizeCoreRuntime(&CoreRuntime{
		Image:              defaultCoreImage,
		ContainerName:      defaultCoreContainerName,
		DataVolume:         defaultCoreDataVolume,
		Port:               defaultCorePort,
		AuthMode:           defaultCoreAuthMode,
		GenerationProvider: defaultGenerationProvider,
		EmbeddingProvider:  defaultEmbeddingProvider,
		OpenAIModel:        defaultOpenAIModel,
		OpenAIEmbedModel:   defaultOpenAIEmbedModel,
	})
}

func coreRuntimePath() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".compair", "core_runtime.yaml"), nil
}

func LoadCoreRuntime() (*CoreRuntime, error) {
	path, err := coreRuntimePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := defaultCoreRuntime()
			_ = SaveCoreRuntime(cfg)
			return cfg, nil
		}
		return nil, err
	}
	var cfg CoreRuntime
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return normalizeCoreRuntime(&cfg), nil
}

func SaveCoreRuntime(cfg *CoreRuntime) error {
	path, err := coreRuntimePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cfg = normalizeCoreRuntime(cfg)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func normalizeCoreRuntime(cfg *CoreRuntime) *CoreRuntime {
	if cfg == nil {
		return defaultCoreRuntime()
	}
	if strings.TrimSpace(cfg.Image) == "" {
		cfg.Image = defaultCoreImage
	}
	if strings.TrimSpace(cfg.ContainerName) == "" {
		cfg.ContainerName = defaultCoreContainerName
	}
	if strings.TrimSpace(cfg.DataVolume) == "" {
		cfg.DataVolume = defaultCoreDataVolume
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultCorePort
	}
	cfg.AuthMode = normalizeCoreAuthMode(cfg.AuthMode)
	cfg.GenerationProvider = normalizeGenerationProvider(cfg.GenerationProvider)
	cfg.EmbeddingProvider = normalizeEmbeddingProvider(cfg.EmbeddingProvider)
	if strings.TrimSpace(cfg.OpenAIModel) == "" {
		cfg.OpenAIModel = defaultOpenAIModel
	}
	cfg.OpenAICodeModel = strings.TrimSpace(cfg.OpenAICodeModel)
	cfg.OpenAINotifModel = strings.TrimSpace(cfg.OpenAINotifModel)
	if strings.TrimSpace(cfg.OpenAIEmbedModel) == "" {
		cfg.OpenAIEmbedModel = defaultOpenAIEmbedModel
	}
	cfg.OpenAIBaseURL = strings.TrimSpace(cfg.OpenAIBaseURL)
	if cfg.NotificationScoringTimeoutS < 0 {
		cfg.NotificationScoringTimeoutS = 0
	}
	if cfg.NotificationScoringMaxRetries < 0 {
		cfg.NotificationScoringMaxRetries = 0
	}
	if cfg.ReferenceTraceMaxCandidates < 0 {
		cfg.ReferenceTraceMaxCandidates = 0
	}
	cfg.GenerationEndpoint = strings.TrimSpace(cfg.GenerationEndpoint)
	cfg.OpenAIAPIKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	return cfg
}

func normalizeCoreAuthMode(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "accounts", "auth", "authenticated":
		return "accounts"
	default:
		return "single-user"
	}
}

func normalizeGenerationProvider(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "openai", "http", "fallback":
		return strings.TrimSpace(strings.ToLower(raw))
	default:
		return "local"
	}
}

func normalizeEmbeddingProvider(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "openai":
		return "openai"
	default:
		return "local"
	}
}

func (c *CoreRuntime) APIBase() string {
	cfg := normalizeCoreRuntime(c)
	return fmt.Sprintf("http://localhost:%d", cfg.Port)
}

func (c *CoreRuntime) UsesOpenAI() bool {
	cfg := normalizeCoreRuntime(c)
	return cfg.GenerationProvider == "openai" || cfg.EmbeddingProvider == "openai"
}

func (c *CoreRuntime) ResolvedOpenAIAPIKey() string {
	cfg := normalizeCoreRuntime(c)
	if cfg.OpenAIAPIKey != "" {
		return cfg.OpenAIAPIKey
	}
	if val := strings.TrimSpace(os.Getenv("COMPAIR_OPENAI_API_KEY")); val != "" {
		return val
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

func (c *CoreRuntime) ResolvedOpenAIBaseURL() string {
	cfg := normalizeCoreRuntime(c)
	if cfg.OpenAIBaseURL != "" {
		return cfg.OpenAIBaseURL
	}
	if val := strings.TrimSpace(os.Getenv("COMPAIR_OPENAI_BASE_URL")); val != "" {
		return val
	}
	return strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
}

func (c *CoreRuntime) ResolvedOpenAICodeModel() string {
	cfg := normalizeCoreRuntime(c)
	if cfg.OpenAICodeModel != "" {
		return cfg.OpenAICodeModel
	}
	return cfg.OpenAIModel
}

func (c *CoreRuntime) ResolvedOpenAINotifModel() string {
	cfg := normalizeCoreRuntime(c)
	if cfg.OpenAINotifModel != "" {
		return cfg.OpenAINotifModel
	}
	return cfg.OpenAIModel
}
