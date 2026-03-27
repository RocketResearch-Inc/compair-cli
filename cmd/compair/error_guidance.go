package compair

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func formatCLIError(cmd *cobra.Command, err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return ""
	}
	hints := errorGuidance(cmd, msg)
	if len(hints) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("\n\nNext step:\n")
	for _, hint := range hints {
		b.WriteString("  - ")
		b.WriteString(hint)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func errorGuidance(cmd *cobra.Command, message string) []string {
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return nil
	}

	path := ""
	if cmd != nil {
		path = strings.ToLower(strings.TrimSpace(cmd.CommandPath()))
	}
	apiBase := strings.ToLower(strings.TrimSpace(viper.GetString("api.base")))
	apiHost := normalizeAPIHost(apiBase)
	profile := strings.ToLower(strings.TrimSpace(viper.GetString("profile.active")))
	localCore := strings.HasPrefix(path, "compair core") ||
		apiHost == "localhost" ||
		apiHost == "127.0.0.1" ||
		profile == "local"
	hostedCloud := apiHost == "app.compair.sh"
	selfHostedRemote := !hostedCloud && !localCore && apiHost != ""

	hints := []string{}
	add := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			for _, existing := range hints {
				if existing == value {
					return
				}
			}
			hints = append(hints, value)
			return
		}
	}

	if containsAny(msg,
		"not logged in",
		"failed to establish session",
		"load_session returned no session id",
		"login succeeded but returned no auth token",
		"google sign-in",
		"browser sign-in is not enabled",
		"password login is not enabled",
	) {
		add("Run 'compair doctor' to check auth, group, and repo binding health.")
		add("If you just need a fresh session, run 'compair login'.")
	}
	if containsAny(msg, "401", "403", "unauthorized", "forbidden") {
		if localCore {
			add("Run 'compair core doctor' to confirm the local profile, auth mode, and Core /health endpoint.")
			add("If this Core install is meant to require auth, run 'compair login'. If it is meant to be single-user, confirm Core is running with authentication disabled.")
		} else if selfHostedRemote {
			add("Run 'compair doctor' to confirm the API base, auth, and current repo/group binding.")
			add("If this is your own Core deployment, confirm whether the server is configured for single-user or full authentication before retrying login.")
		} else {
			add("Run 'compair doctor' to check auth, group, and repo binding health.")
			add("If you just need a fresh session, run 'compair login'.")
		}
	}

	if containsAny(msg,
		"no groups found",
		"could not resolve a group",
		"no group found",
		"multiple groups named",
		"active group",
		"missing .compair/config.yaml",
		"document_id missing",
		"repo document",
		"repo binding",
		"one or more requested groups",
		"no matching groups found for provided ids",
	) {
		add("Run 'compair doctor' to verify auth, active group, and repo binding.")
		add("If the active group is wrong, run 'compair group ls' then 'compair group use <id>'.")
	}

	if containsAny(msg,
		"chunk task ",
		"processing timeout",
		"saved processing task",
		"pending review task",
		"pending processing task",
		"ended with status failure",
		"demo priming failed",
	) {
		add("Run 'compair doctor' to inspect pending tasks, repo binding, and current sync state.")
		if localCore {
			add("If this is against local Core, run 'compair core doctor' to check Docker, the container, and /health.")
		} else if hostedCloud {
			add("If this keeps reproducing on hosted Compair Cloud, send the task id and 'compair doctor --json' output to support@compair.sh.")
		} else if selfHostedRemote {
			add("If this is your own server, check the API/worker logs before retrying.")
		}
	}

	if containsAny(msg,
		"connection refused",
		"dial tcp",
		"no such host",
		"server misbehaving",
		"broken pipe",
		"tls handshake timeout",
		"timeout awaiting response headers",
		"operation timed out",
		"client.timeout",
	) || hasServerStatus(message) {
		if localCore {
			add("Run 'compair core doctor' to check Docker, the local profile, container health, and the Core /health endpoint.")
		} else if hostedCloud {
			add("Run 'compair doctor' to confirm the API base, auth, and current repo/group binding.")
			add("If the API is healthy but hosted Cloud still returns server errors, contact support@compair.sh with the failing command and 'compair doctor --json' output.")
		} else if selfHostedRemote {
			add("Run 'compair doctor' to confirm the API base, auth, and current repo/group binding.")
			add("If this is your own server, check the server logs or ask the server admin to inspect the backend.")
		}
	}

	if containsAny(msg,
		"docker is not available on path",
		"local core container",
		"get /health",
		"openai mode is configured but no api key is available",
		"generation provider 'http' requires",
	) || strings.HasPrefix(path, "compair core") {
		add("Run 'compair core doctor' for Docker, container, provider, and auth-mode diagnostics.")
		if containsAny(msg, "docker is not available on path") {
			add("Start Docker Desktop or Docker Engine, then rerun 'compair core up'.")
		}
	}

	return hints
}

func containsAny(value string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(value, strings.ToLower(strings.TrimSpace(pattern))) {
			return true
		}
	}
	return false
}

func hasServerStatus(message string) bool {
	upper := strings.ToUpper(message)
	for _, method := range []string{"GET ", "POST ", "DELETE "} {
		if !strings.Contains(upper, method) {
			continue
		}
		for _, code := range []string{" 500", " 502", " 503", " 504"} {
			if strings.Contains(upper, code) {
				return true
			}
		}
	}
	return false
}

func normalizeAPIHost(apiBase string) string {
	value := strings.TrimSpace(apiBase)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil {
		if host := strings.TrimSpace(parsed.Hostname()); host != "" {
			return strings.ToLower(host)
		}
	}
	if strings.Contains(value, "://") {
		return ""
	}
	host := value
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return strings.ToLower(strings.TrimSpace(host))
}
