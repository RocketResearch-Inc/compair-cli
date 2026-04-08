package compair

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	coreProviderPreset                string
	coreGenerationProvider            string
	coreEmbeddingProvider             string
	coreOpenAIAPIKey                  string
	coreOpenAIModel                   string
	coreOpenAICodeModel               string
	coreOpenAINotifModel              string
	coreOpenAIEmbedModel              string
	coreOpenAIBaseURL                 string
	coreNotificationScoringTimeoutS   int
	coreNotificationScoringMaxRetries int
	coreReferenceTrace                bool
	coreReferenceTraceMaxCandidates   int
	coreGenerationEndpoint            string
	coreAuthMode                      string
	corePort                          int
	coreImage                         string
	coreContainerName                 string
	coreDataVolume                    string
	coreClearOpenAIAPIKey             bool
	coreDownPurge                     bool
	coreLogsFollow                    bool
	coreLogsTail                      string
	coreDoctorJSON                    bool
)

var coreCmd = &cobra.Command{
	Use:   "core",
	Short: "Manage a local Compair Core runtime",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreStatus()
	},
}

var coreStatusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"show"},
	Short:   "Show local Compair Core runtime config and Docker status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreStatus()
	},
}

var coreConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or update the local Compair Core runtime config",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreConfigShow()
	},
}

var coreConfigShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the saved local Compair Core runtime config",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreConfigShow()
	},
}

var coreConfigSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Update local Compair Core runtime settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadCoreRuntime()
		if err != nil {
			return err
		}
		applyCoreRuntimeOverrides(cmd, cfg)
		if cfg.UsesOpenAI() && strings.TrimSpace(cfg.ResolvedOpenAIAPIKey()) == "" {
			key, err := promptPassword("OpenAI API key: ")
			if err != nil {
				return err
			}
			cfg.OpenAIAPIKey = strings.TrimSpace(key)
		}
		if cfg.GenerationProvider == "http" && strings.TrimSpace(cfg.GenerationEndpoint) == "" {
			return fmt.Errorf("generation provider 'http' requires --generation-endpoint")
		}
		if err := config.SaveCoreRuntime(cfg); err != nil {
			return err
		}
		fmt.Println("Saved local Compair Core runtime config.")
		if err := maybeWarnCoreRestartNeeded(cfg); err != nil {
			return err
		}
		return runCoreConfigShow()
	},
}

var coreUpCmd = &cobra.Command{
	Use:     "up",
	Aliases: []string{"start"},
	Short:   "Start or recreate the local Compair Core container",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreUp()
	},
}

var coreDownCmd = &cobra.Command{
	Use:     "down",
	Aliases: []string{"stop"},
	Short:   "Stop and remove the local Compair Core container",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadCoreRuntime()
		if err != nil {
			return err
		}
		if err := ensureDockerAvailable(); err != nil {
			return err
		}
		if _, err := runDocker("rm", "-f", cfg.ContainerName); err != nil {
			msg := strings.TrimSpace(err.Error())
			if !strings.Contains(strings.ToLower(msg), "no such container") {
				return err
			}
		}
		fmt.Println("Stopped local Compair Core container.")
		if coreDownPurge {
			if _, err := runDocker("volume", "rm", cfg.DataVolume); err != nil {
				msg := strings.TrimSpace(err.Error())
				if !strings.Contains(strings.ToLower(msg), "no such volume") {
					return err
				}
			}
			fmt.Printf("Removed data volume: %s\n", cfg.DataVolume)
		}
		return nil
	},
}

var coreRestartCmd = &cobra.Command{
	Use:     "restart",
	Aliases: []string{"reload"},
	Short:   "Recreate the local Compair Core container using the saved config",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Recreating local Compair Core container...")
		return runCoreUp()
	},
}

var coreLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show logs from the local Compair Core container",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadCoreRuntime()
		if err != nil {
			return err
		}
		status, err := dockerContainerStatus(cfg.ContainerName)
		if err != nil {
			return err
		}
		if status == "not created" {
			return fmt.Errorf("local Core container '%s' has not been created yet; run 'compair core up' first", cfg.ContainerName)
		}
		logArgs := []string{"logs"}
		if coreLogsFollow {
			logArgs = append(logArgs, "-f")
		}
		if strings.TrimSpace(coreLogsTail) != "" {
			logArgs = append(logArgs, "--tail", strings.TrimSpace(coreLogsTail))
		}
		logArgs = append(logArgs, cfg.ContainerName)
		return runDockerStreaming(logArgs...)
	},
}

type coreDoctorReport struct {
	APIBase      string        `json:"api_base"`
	Container    string        `json:"container"`
	Warnings     int           `json:"warnings"`
	Errors       int           `json:"errors"`
	Checks       []doctorCheck `json:"checks"`
	ProfileLocal string        `json:"profile_local,omitempty"`
}

var coreDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose local Core runtime and profile problems",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadCoreRuntime()
		if err != nil {
			return err
		}
		report := coreDoctorReport{
			APIBase:   cfg.APIBase(),
			Container: cfg.ContainerName,
		}
		summary := doctorSummary{}
		emit := !coreDoctorJSON

		if emit {
			fmt.Println("Compair core doctor")
			fmt.Println()
		}

		coreDoctorInfo(&report, emit, "API base", cfg.APIBase())
		coreDoctorInfo(&report, emit, "Container", cfg.ContainerName)
		coreDoctorInfo(&report, emit, "Auth mode", cfg.AuthMode)
		coreDoctorInfo(&report, emit, "Generation provider", cfg.GenerationProvider)
		coreDoctorInfo(&report, emit, "Embedding provider", cfg.EmbeddingProvider)

		if cfg.UsesOpenAI() {
			if strings.TrimSpace(cfg.ResolvedOpenAIAPIKey()) == "" {
				coreDoctorFail(&report, &summary, emit, "OpenAI API key", "missing", "Run 'compair core config set --provider openai --openai-api-key <key>' or set COMPAIR_OPENAI_API_KEY.")
			} else {
				coreDoctorOK(&report, emit, "OpenAI API key", "present")
			}
			if baseURL := strings.TrimSpace(cfg.ResolvedOpenAIBaseURL()); baseURL != "" {
				coreDoctorInfo(&report, emit, "OpenAI base URL", baseURL)
			}
			coreDoctorInfo(&report, emit, "OpenAI model", cfg.OpenAIModel)
			coreDoctorInfo(&report, emit, "OpenAI code model", orInherited(cfg.OpenAICodeModel, cfg.OpenAIModel))
			coreDoctorInfo(&report, emit, "OpenAI notification model", orInherited(cfg.OpenAINotifModel, cfg.OpenAIModel))
			coreDoctorInfo(&report, emit, "OpenAI embed model", cfg.OpenAIEmbedModel)
			if cfg.NotificationScoringTimeoutS > 0 {
				coreDoctorInfo(&report, emit, "Notification scoring timeout", fmt.Sprintf("%ds", cfg.NotificationScoringTimeoutS))
			}
			if cfg.NotificationScoringMaxRetries > 0 {
				coreDoctorInfo(&report, emit, "Notification scoring retries", strconv.Itoa(cfg.NotificationScoringMaxRetries))
			}
		}
		if cfg.GenerationProvider == "http" {
			if strings.TrimSpace(cfg.GenerationEndpoint) == "" {
				coreDoctorFail(&report, &summary, emit, "Generation endpoint", "missing", "Set one with 'compair core config set --generation-provider http --generation-endpoint <url>'.")
			} else {
				coreDoctorOK(&report, emit, "Generation endpoint", cfg.GenerationEndpoint)
			}
		}

		profs, profErr := config.LoadProfiles()
		if profErr != nil {
			coreDoctorWarn(&report, &summary, emit, "Local profile", compactErr(profErr), "Run 'compair core up' to refresh the local profile.")
		} else {
			local := profs.Profiles["local"]
			localBase := strings.TrimSpace(local.APIBase)
			report.ProfileLocal = localBase
			if localBase == "" {
				coreDoctorWarn(&report, &summary, emit, "Local profile", "missing", fmt.Sprintf("Run 'compair profile set local --api-base %s' or 'compair core up'.", cfg.APIBase()))
			} else if localBase != cfg.APIBase() {
				coreDoctorWarn(&report, &summary, emit, "Local profile", localBase, fmt.Sprintf("Expected %s. Run 'compair core up' or 'compair profile set local --api-base %s'.", cfg.APIBase(), cfg.APIBase()))
			} else {
				coreDoctorOK(&report, emit, "Local profile", localBase)
			}
		}

		if err := ensureDockerAvailable(); err != nil {
			coreDoctorFail(&report, &summary, emit, "Docker", compactErr(err), "Install Docker Desktop/Engine before using 'compair core up'.")
			return finishCoreDoctor(report, summary, emit)
		}
		coreDoctorOK(&report, emit, "Docker", "available")

		containerStatus, err := dockerContainerStatus(cfg.ContainerName)
		if err != nil {
			coreDoctorFail(&report, &summary, emit, "Container status", compactErr(err), "Run 'compair core up' after Docker is healthy.")
			return finishCoreDoctor(report, summary, emit)
		}
		switch containerStatus {
		case "running":
			coreDoctorOK(&report, emit, "Container status", "running")
		case "not created":
			coreDoctorWarn(&report, &summary, emit, "Container status", "not created", "Run 'compair core up' to start the local runtime.")
			return finishCoreDoctor(report, summary, emit)
		default:
			coreDoctorFail(&report, &summary, emit, "Container status", containerStatus, "Run 'compair core restart' or inspect logs with 'compair core logs --tail 200'.")
			return finishCoreDoctor(report, summary, emit)
		}

		health, err := fetchCoreHealth(cfg.APIBase())
		if err != nil {
			coreDoctorFail(&report, &summary, emit, "API health", compactErr(err), "Inspect logs with 'compair core logs --tail 200' and confirm the local port is reachable.")
			return finishCoreDoctor(report, summary, emit)
		}
		coreDoctorOK(&report, emit, "API health", health.Status)
		if strings.TrimSpace(health.Edition) != "" {
			if strings.EqualFold(strings.TrimSpace(health.Edition), "core") {
				coreDoctorOK(&report, emit, "API edition", health.Edition)
			} else {
				coreDoctorWarn(&report, &summary, emit, "API edition", health.Edition, "This endpoint is responding, but it does not identify itself as Compair Core.")
			}
		}
		if strings.TrimSpace(health.Version) != "" {
			coreDoctorInfo(&report, emit, "API version", health.Version)
		}

		client := api.NewClient(cfg.APIBase())
		caps, capsErr := fetchPublicCapabilities(cfg.APIBase())
		if capsErr != nil {
			coreDoctorWarn(&report, &summary, emit, "Capabilities", compactErr(capsErr), "The API is up, but /capabilities could not be read.")
		} else {
			coreDoctorOK(&report, emit, "Capabilities", "available")
			if strings.TrimSpace(caps.Server) != "" {
				coreDoctorInfo(&report, emit, "Server", caps.Server)
			}
			if strings.TrimSpace(caps.Version) != "" && strings.TrimSpace(caps.Version) != strings.TrimSpace(health.Version) {
				coreDoctorInfo(&report, emit, "Capabilities version", caps.Version)
			}
			if cfg.AuthMode == "single-user" {
				if caps.Auth.Required || !caps.Auth.SingleUser {
					coreDoctorFail(&report, &summary, emit, "Auth mode match", "server requires accounts", "Recreate Core with 'compair core config set --auth single-user' followed by 'compair core restart'.")
				} else {
					coreDoctorOK(&report, emit, "Auth mode match", "single-user")
				}
			} else {
				if !caps.Auth.Required {
					coreDoctorFail(&report, &summary, emit, "Auth mode match", "server is running in single-user mode", "Recreate Core with 'compair core config set --auth accounts' followed by 'compair core restart'.")
				} else {
					coreDoctorOK(&report, emit, "Auth mode match", "accounts")
				}
			}
		}

		if cfg.AuthMode == "single-user" {
			if session, err := client.EnsureSession(); err != nil {
				coreDoctorFail(&report, &summary, emit, "Single-user session", compactErr(err), "The API is reachable but did not auto-establish a local session.")
			} else {
				detail := strings.TrimSpace(session.UserID)
				if strings.TrimSpace(session.Username) != "" {
					detail = fmt.Sprintf("%s (%s)", session.Username, session.UserID)
				}
				coreDoctorOK(&report, emit, "Single-user session", detail)
			}
		} else {
			if tok := strings.TrimSpace(auth.Token()); tok == "" {
				coreDoctorWarn(&report, &summary, emit, "CLI auth token", "missing", "Run 'compair login' after starting the local runtime if you want to test account-based auth.")
			} else if session, err := client.EnsureSession(); err != nil {
				coreDoctorWarn(&report, &summary, emit, "CLI auth token", compactErr(err), "Run 'compair login' again if you expected a valid authenticated session.")
			} else {
				detail := strings.TrimSpace(session.UserID)
				if strings.TrimSpace(session.Username) != "" {
					detail = fmt.Sprintf("%s (%s)", session.Username, session.UserID)
				}
				coreDoctorOK(&report, emit, "CLI auth token", detail)
			}
		}

		return finishCoreDoctor(report, summary, emit)
	},
}

func init() {
	rootCmd.AddCommand(coreCmd)
	coreCmd.AddCommand(coreStatusCmd)
	coreCmd.AddCommand(coreUpCmd)
	coreCmd.AddCommand(coreDownCmd)
	coreCmd.AddCommand(coreRestartCmd)
	coreCmd.AddCommand(coreLogsCmd)
	coreCmd.AddCommand(coreDoctorCmd)
	coreCmd.AddCommand(coreConfigCmd)
	coreConfigCmd.AddCommand(coreConfigShowCmd)
	coreConfigCmd.AddCommand(coreConfigSetCmd)

	coreConfigSetCmd.Flags().StringVar(&coreProviderPreset, "provider", "", "Provider preset: local, openai, fallback")
	coreConfigSetCmd.Flags().StringVar(&coreGenerationProvider, "generation-provider", "", "Generation provider: local, openai, http, fallback")
	coreConfigSetCmd.Flags().StringVar(&coreEmbeddingProvider, "embedding-provider", "", "Embedding provider: local, openai")
	coreConfigSetCmd.Flags().StringVar(&coreOpenAIAPIKey, "openai-api-key", "", "OpenAI API key to save for the local Core runtime")
	coreConfigSetCmd.Flags().StringVar(&coreOpenAIModel, "openai-model", "", "OpenAI generation model")
	coreConfigSetCmd.Flags().StringVar(&coreOpenAICodeModel, "openai-code-model", "", "OpenAI code-review generation model for local Core")
	coreConfigSetCmd.Flags().StringVar(&coreOpenAINotifModel, "openai-notif-model", "", "OpenAI notification scoring model for local Core")
	coreConfigSetCmd.Flags().StringVar(&coreOpenAIEmbedModel, "openai-embed-model", "", "OpenAI embedding model")
	coreConfigSetCmd.Flags().StringVar(&coreOpenAIBaseURL, "openai-base-url", "", "OpenAI-compatible base URL for local Core")
	coreConfigSetCmd.Flags().IntVar(&coreNotificationScoringTimeoutS, "notification-scoring-timeout-s", 0, "Notification scoring timeout in seconds for local Core (0 keeps backend default)")
	coreConfigSetCmd.Flags().IntVar(&coreNotificationScoringMaxRetries, "notification-scoring-max-retries", 0, "Notification scoring retry count for local Core (0 keeps backend default)")
	coreConfigSetCmd.Flags().BoolVar(&coreReferenceTrace, "reference-trace", false, "Enable detailed reference candidate tracing in local Core logs")
	coreConfigSetCmd.Flags().IntVar(&coreReferenceTraceMaxCandidates, "reference-trace-max-candidates", 0, "Max candidate records to emit per source chunk when reference tracing is enabled (0 = all)")
	coreConfigSetCmd.Flags().StringVar(&coreGenerationEndpoint, "generation-endpoint", "", "Custom generation endpoint when using generation-provider=http")
	coreConfigSetCmd.Flags().StringVar(&coreAuthMode, "auth", "", "Auth mode: single-user or accounts")
	coreConfigSetCmd.Flags().IntVar(&corePort, "port", 0, "Local host port for the Core API")
	coreConfigSetCmd.Flags().StringVar(&coreImage, "image", "", "Container image to run")
	coreConfigSetCmd.Flags().StringVar(&coreContainerName, "container-name", "", "Docker container name")
	coreConfigSetCmd.Flags().StringVar(&coreDataVolume, "data-volume", "", "Docker volume for persistent Core data")
	coreConfigSetCmd.Flags().BoolVar(&coreClearOpenAIAPIKey, "clear-openai-api-key", false, "Remove the saved OpenAI API key from the local Core runtime config")

	coreDownCmd.Flags().BoolVar(&coreDownPurge, "purge", false, "Also remove the persistent Docker volume used by the local Core runtime")
	coreLogsCmd.Flags().BoolVarP(&coreLogsFollow, "follow", "f", false, "Follow the log stream")
	coreLogsCmd.Flags().StringVar(&coreLogsTail, "tail", "200", "Number of log lines to show (or 'all')")
	coreDoctorCmd.Flags().BoolVar(&coreDoctorJSON, "json", false, "Output machine-readable diagnostics JSON")
}

func runCoreStatus() error {
	cfg, err := config.LoadCoreRuntime()
	if err != nil {
		return err
	}
	fmt.Println("Local Compair Core runtime")
	fmt.Println()
	fmt.Println("Config:")
	fmt.Printf("  Image: %s\n", cfg.Image)
	fmt.Printf("  Container: %s\n", cfg.ContainerName)
	fmt.Printf("  Data volume: %s\n", cfg.DataVolume)
	fmt.Printf("  API base: %s\n", cfg.APIBase())
	fmt.Printf("  Auth mode: %s\n", cfg.AuthMode)
	fmt.Printf("  Generation provider: %s\n", cfg.GenerationProvider)
	fmt.Printf("  Embedding provider: %s\n", cfg.EmbeddingProvider)
	if cfg.GenerationProvider == "http" {
		fmt.Printf("  Generation endpoint: %s\n", orNone(cfg.GenerationEndpoint))
	}
	if cfg.UsesOpenAI() {
		fmt.Printf("  OpenAI API key: %s\n", presence(cfg.ResolvedOpenAIAPIKey()))
		fmt.Printf("  OpenAI base URL: %s\n", orNone(cfg.ResolvedOpenAIBaseURL()))
		fmt.Printf("  OpenAI model: %s\n", cfg.OpenAIModel)
		fmt.Printf("  OpenAI code model: %s\n", orInherited(cfg.OpenAICodeModel, cfg.OpenAIModel))
		fmt.Printf("  OpenAI notification model: %s\n", orInherited(cfg.OpenAINotifModel, cfg.OpenAIModel))
		fmt.Printf("  OpenAI embed model: %s\n", cfg.OpenAIEmbedModel)
	}
	if cfg.NotificationScoringTimeoutS > 0 {
		fmt.Printf("  Notification scoring timeout: %ds\n", cfg.NotificationScoringTimeoutS)
	}
	if cfg.NotificationScoringMaxRetries > 0 {
		fmt.Printf("  Notification scoring retries: %d\n", cfg.NotificationScoringMaxRetries)
	}
	if cfg.ReferenceTrace {
		limit := "all"
		if cfg.ReferenceTraceMaxCandidates > 0 {
			limit = strconv.Itoa(cfg.ReferenceTraceMaxCandidates)
		}
		fmt.Printf("  Reference trace: on (max candidates %s)\n", limit)
	}
	if usesBundledLocalProviders(cfg) {
		fmt.Println("  Review quality: bundled local fallback (functional, lower fidelity than Cloud)")
	} else if cfg.UsesOpenAI() {
		fmt.Println("  Review quality: OpenAI-backed local Core")
	}
	fmt.Println()

	fmt.Println("Docker:")
	status, err := dockerContainerStatus(cfg.ContainerName)
	if err != nil {
		fmt.Printf("  Available: no (%s)\n", err)
		fmt.Println("  Tip: install Docker Desktop/Engine to run the local Core container.")
		return nil
	}
	fmt.Println("  Available: yes")
	fmt.Printf("  Container status: %s\n", status)
	if status == "not created" {
		fmt.Println("  Tip: run 'compair core up' to start the local Core runtime.")
	}
	return nil
}

func runCoreConfigShow() error {
	cfg, err := config.LoadCoreRuntime()
	if err != nil {
		return err
	}
	fmt.Println("Local Compair Core config")
	fmt.Println()
	fmt.Printf("  Image: %s\n", cfg.Image)
	fmt.Printf("  Container: %s\n", cfg.ContainerName)
	fmt.Printf("  Data volume: %s\n", cfg.DataVolume)
	fmt.Printf("  Port: %d\n", cfg.Port)
	fmt.Printf("  API base: %s\n", cfg.APIBase())
	fmt.Printf("  Auth mode: %s\n", cfg.AuthMode)
	fmt.Printf("  Generation provider: %s\n", cfg.GenerationProvider)
	fmt.Printf("  Embedding provider: %s\n", cfg.EmbeddingProvider)
	if cfg.GenerationProvider == "http" {
		fmt.Printf("  Generation endpoint: %s\n", orNone(cfg.GenerationEndpoint))
	}
	if cfg.UsesOpenAI() {
		fmt.Printf("  OpenAI API key: %s\n", presence(cfg.ResolvedOpenAIAPIKey()))
		fmt.Printf("  OpenAI base URL: %s\n", orNone(cfg.ResolvedOpenAIBaseURL()))
		fmt.Printf("  OpenAI model: %s\n", cfg.OpenAIModel)
		fmt.Printf("  OpenAI code model: %s\n", orInherited(cfg.OpenAICodeModel, cfg.OpenAIModel))
		fmt.Printf("  OpenAI notification model: %s\n", orInherited(cfg.OpenAINotifModel, cfg.OpenAIModel))
		fmt.Printf("  OpenAI embed model: %s\n", cfg.OpenAIEmbedModel)
	}
	if usesBundledLocalProviders(cfg) {
		fmt.Println("  Review quality: bundled local fallback (functional, lower fidelity than Cloud)")
	} else if cfg.UsesOpenAI() {
		fmt.Println("  Review quality: OpenAI-backed local Core")
	}
	if cfg.ReferenceTrace {
		limit := "all"
		if cfg.ReferenceTraceMaxCandidates > 0 {
			limit = strconv.Itoa(cfg.ReferenceTraceMaxCandidates)
		}
		fmt.Printf("  Reference trace: on (max candidates %s)\n", limit)
	}
	return nil
}

func maybeWarnCoreRestartNeeded(cfg *config.CoreRuntime) error {
	if cfg == nil {
		return nil
	}
	if err := ensureDockerAvailable(); err != nil {
		return nil
	}
	status, err := dockerContainerStatus(cfg.ContainerName)
	if err != nil {
		return nil
	}
	if status != "running" {
		return nil
	}
	fmt.Println()
	fmt.Println("Note: the local Core container is already running.")
	fmt.Println("Config changes are saved, but they do not apply to the running container until you restart it.")
	fmt.Println("Run 'compair core restart' before evaluating the new provider settings.")
	return nil
}

func applyCoreRuntimeOverrides(cmd *cobra.Command, cfg *config.CoreRuntime) {
	if cfg == nil {
		return
	}
	if v, ok := getStringFlagIfChanged(cmd, "provider"); ok {
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "openai":
			cfg.GenerationProvider = "openai"
			cfg.EmbeddingProvider = "openai"
		case "fallback":
			cfg.GenerationProvider = "fallback"
			cfg.EmbeddingProvider = "local"
		default:
			cfg.GenerationProvider = "local"
			cfg.EmbeddingProvider = "local"
		}
	}
	if v, ok := getStringFlagIfChanged(cmd, "generation-provider"); ok {
		cfg.GenerationProvider = v
	}
	if v, ok := getStringFlagIfChanged(cmd, "embedding-provider"); ok {
		cfg.EmbeddingProvider = v
	}
	if v, ok := getStringFlagIfChanged(cmd, "openai-api-key"); ok {
		cfg.OpenAIAPIKey = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "openai-model"); ok {
		cfg.OpenAIModel = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "openai-code-model"); ok {
		cfg.OpenAICodeModel = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "openai-notif-model"); ok {
		cfg.OpenAINotifModel = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "openai-embed-model"); ok {
		cfg.OpenAIEmbedModel = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "openai-base-url"); ok {
		cfg.OpenAIBaseURL = strings.TrimSpace(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "notification-scoring-timeout-s"); ok {
		cfg.NotificationScoringTimeoutS = v
	}
	if v, ok := getIntFlagIfChanged(cmd, "notification-scoring-max-retries"); ok {
		cfg.NotificationScoringMaxRetries = v
	}
	if cmd.Flags().Changed("reference-trace") {
		cfg.ReferenceTrace = coreReferenceTrace
	}
	if v, ok := getIntFlagIfChanged(cmd, "reference-trace-max-candidates"); ok {
		cfg.ReferenceTraceMaxCandidates = v
	}
	if v, ok := getStringFlagIfChanged(cmd, "generation-endpoint"); ok {
		cfg.GenerationEndpoint = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "auth"); ok {
		cfg.AuthMode = strings.TrimSpace(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "port"); ok {
		cfg.Port = v
	}
	if v, ok := getStringFlagIfChanged(cmd, "image"); ok {
		cfg.Image = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "container-name"); ok {
		cfg.ContainerName = strings.TrimSpace(v)
	}
	if v, ok := getStringFlagIfChanged(cmd, "data-volume"); ok {
		cfg.DataVolume = strings.TrimSpace(v)
	}
	if cmd.Flags().Changed("clear-openai-api-key") && coreClearOpenAIAPIKey {
		cfg.OpenAIAPIKey = ""
	}
}

func ensureDockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not available on PATH")
	}
	return nil
}

func runDocker(args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("docker %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func runDockerStreaming(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

type coreHealthResponse struct {
	Status  string `json:"status"`
	Edition string `json:"edition"`
	Version string `json:"version"`
}

func fetchCoreHealth(apiBase string) (coreHealthResponse, error) {
	var out coreHealthResponse
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(strings.TrimSpace(apiBase), "/")+"/health", nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("GET /health: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func dockerContainerStatus(name string) (string, error) {
	if err := ensureDockerAvailable(); err != nil {
		return "", err
	}
	out, err := runDocker("inspect", "-f", "{{.State.Status}}", name)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such object") || strings.Contains(strings.ToLower(err.Error()), "no such container") {
			return "not created", nil
		}
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "unknown", nil
	}
	return strings.TrimSpace(out), nil
}

func runCoreUp() error {
	cfg, err := config.LoadCoreRuntime()
	if err != nil {
		return err
	}
	if cfg.UsesOpenAI() && strings.TrimSpace(cfg.ResolvedOpenAIAPIKey()) == "" {
		return fmt.Errorf("OpenAI mode is configured but no API key is available; run 'compair core config set --provider openai --openai-api-key <key>' or set COMPAIR_OPENAI_API_KEY")
	}
	if cfg.GenerationProvider == "http" && strings.TrimSpace(cfg.GenerationEndpoint) == "" {
		return fmt.Errorf("generation provider 'http' requires a configured generation endpoint")
	}
	if err := ensureDockerAvailable(); err != nil {
		return err
	}
	if _, err := runDocker("rm", "-f", cfg.ContainerName); err != nil {
		msg := strings.TrimSpace(err.Error())
		if !strings.Contains(strings.ToLower(msg), "no such container") {
			return err
		}
	}

	args := []string{
		"run", "-d",
		"--name", cfg.ContainerName,
		"-p", fmt.Sprintf("%d:8000", cfg.Port),
		"-v", fmt.Sprintf("%s:/data", cfg.DataVolume),
		"-e", "COMPAIR_REQUIRE_AUTHENTICATION=" + strconv.FormatBool(cfg.AuthMode == "accounts"),
		"-e", "COMPAIR_GENERATION_PROVIDER=" + cfg.GenerationProvider,
		"-e", "COMPAIR_EMBEDDING_PROVIDER=" + cfg.EmbeddingProvider,
		"-e", "COMPAIR_OPENAI_MODEL=" + cfg.OpenAIModel,
		"-e", "COMPAIR_OPENAI_EMBED_MODEL=" + cfg.OpenAIEmbedModel,
	}
	if strings.TrimSpace(cfg.OpenAICodeModel) != "" {
		args = append(args, "-e", "COMPAIR_OPENAI_CODE_MODEL="+strings.TrimSpace(cfg.OpenAICodeModel))
	}
	if strings.TrimSpace(cfg.OpenAINotifModel) != "" {
		args = append(args, "-e", "COMPAIR_OPENAI_NOTIF_MODEL="+strings.TrimSpace(cfg.OpenAINotifModel))
	}
	if baseURL := strings.TrimSpace(cfg.ResolvedOpenAIBaseURL()); baseURL != "" && cfg.UsesOpenAI() {
		args = append(args, "-e", "COMPAIR_OPENAI_BASE_URL="+baseURL)
	}
	if cfg.NotificationScoringTimeoutS > 0 {
		args = append(args, "-e", "COMPAIR_NOTIFICATION_SCORING_TIMEOUT_S="+strconv.Itoa(cfg.NotificationScoringTimeoutS))
	}
	if cfg.NotificationScoringMaxRetries > 0 {
		args = append(args, "-e", "COMPAIR_NOTIFICATION_SCORING_MAX_RETRIES="+strconv.Itoa(cfg.NotificationScoringMaxRetries))
	}
	if cfg.ReferenceTrace {
		args = append(args, "-e", "COMPAIR_REFERENCE_TRACE=1")
	}
	if cfg.ReferenceTraceMaxCandidates > 0 {
		args = append(args, "-e", "COMPAIR_REFERENCE_TRACE_MAX_CANDIDATES="+strconv.Itoa(cfg.ReferenceTraceMaxCandidates))
	}
	if cfg.GenerationProvider == "http" && strings.TrimSpace(cfg.GenerationEndpoint) != "" {
		args = append(args, "-e", "COMPAIR_GENERATION_ENDPOINT="+strings.TrimSpace(cfg.GenerationEndpoint))
	}
	if key := strings.TrimSpace(cfg.ResolvedOpenAIAPIKey()); key != "" && cfg.UsesOpenAI() {
		args = append(args, "-e", "COMPAIR_OPENAI_API_KEY="+key)
	}
	args = append(args, cfg.Image)

	out, err := runDocker(args...)
	if err != nil {
		return err
	}
	_ = syncLocalProfile(cfg.APIBase())
	_ = api.ClearCapabilitiesCache()

	fmt.Println("Started local Compair Core container.")
	if id := strings.TrimSpace(out); id != "" {
		fmt.Printf("Container ID: %s\n", id)
	}
	fmt.Printf("API base: %s\n", cfg.APIBase())
	fmt.Println("Local profile 'local' updated to match this API base.")
	fmt.Println("Next steps:")
	fmt.Println("  compair profile use local")
	fmt.Println("  compair login")
	if usesBundledLocalProviders(cfg) {
		fmt.Println()
		fmt.Println("Note: the bundled no-key local providers are functional, but review quality is lower-fidelity than Cloud.")
		if strings.TrimSpace(cfg.ResolvedOpenAIAPIKey()) != "" {
			fmt.Println("For stronger local review quality, switch Core to your OpenAI-backed setup with 'compair core config set --provider openai' or 'compair core config set --generation-provider openai --embedding-provider local', then run 'compair core restart'.")
		} else {
			fmt.Println("For stronger local review quality, configure your own OpenAI key with 'compair core config set --generation-provider openai --embedding-provider local --openai-api-key <key>' and then run 'compair core restart'.")
		}
	}
	return nil
}

func syncLocalProfile(apiBase string) error {
	profs, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	prof := profs.Profiles["local"]
	prof.APIBase = strings.TrimSpace(apiBase)
	profs.Profiles["local"] = prof
	return config.SaveProfiles(profs)
}

func presence(v string) string {
	if strings.TrimSpace(v) == "" {
		return "missing"
	}
	return "present"
}

func orNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(none)"
	}
	return strings.TrimSpace(v)
}

func orInherited(v string, inherited string) string {
	if strings.TrimSpace(v) == "" {
		return "(inherits " + strings.TrimSpace(inherited) + ")"
	}
	return strings.TrimSpace(v)
}

func orDefault(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return "(default " + strings.TrimSpace(fallback) + ")"
	}
	return strings.TrimSpace(v)
}

func usesBundledLocalProviders(cfg *config.CoreRuntime) bool {
	if cfg == nil {
		return false
	}
	gen := strings.TrimSpace(strings.ToLower(cfg.GenerationProvider))
	embed := strings.TrimSpace(strings.ToLower(cfg.EmbeddingProvider))
	return (gen == "local" || gen == "fallback") && embed == "local"
}

func coreDoctorOK(report *coreDoctorReport, emit bool, label, detail string) {
	if report != nil {
		report.Checks = append(report.Checks, doctorCheck{Status: "ok", Label: label, Detail: normalizeDoctorDetail(detail)})
	}
	if emit {
		fmt.Printf("[ok]   %s: %s\n", label, normalizeDoctorDetail(detail))
	}
}

func coreDoctorWarn(report *coreDoctorReport, summary *doctorSummary, emit bool, label, detail, fix string) {
	if summary != nil {
		summary.Warnings++
	}
	if report != nil {
		report.Checks = append(report.Checks, doctorCheck{Status: "warn", Label: label, Detail: normalizeDoctorDetail(detail), Fix: strings.TrimSpace(fix)})
	}
	if emit {
		fmt.Printf("[warn] %s: %s\n", label, normalizeDoctorDetail(detail))
		if strings.TrimSpace(fix) != "" {
			fmt.Printf("       fix: %s\n", fix)
		}
	}
}

func coreDoctorFail(report *coreDoctorReport, summary *doctorSummary, emit bool, label, detail, fix string) {
	if summary != nil {
		summary.Errors++
	}
	if report != nil {
		report.Checks = append(report.Checks, doctorCheck{Status: "fail", Label: label, Detail: normalizeDoctorDetail(detail), Fix: strings.TrimSpace(fix)})
	}
	if emit {
		fmt.Printf("[fail] %s: %s\n", label, normalizeDoctorDetail(detail))
		if strings.TrimSpace(fix) != "" {
			fmt.Printf("       fix: %s\n", fix)
		}
	}
}

func coreDoctorInfo(report *coreDoctorReport, emit bool, label, detail string) {
	if report != nil {
		report.Checks = append(report.Checks, doctorCheck{Status: "info", Label: label, Detail: normalizeDoctorDetail(detail)})
	}
	if emit {
		fmt.Printf("       %s: %s\n", label, normalizeDoctorDetail(detail))
	}
}

func finishCoreDoctor(report coreDoctorReport, summary doctorSummary, emit bool) error {
	report.Errors = summary.Errors
	report.Warnings = summary.Warnings
	if emit {
		fmt.Println()
		if summary.Errors == 0 && summary.Warnings == 0 {
			fmt.Println("Core doctor summary: no obvious problems found.")
			return nil
		}
		fmt.Printf("Core doctor summary: %d error(s), %d warning(s).\n", summary.Errors, summary.Warnings)
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
