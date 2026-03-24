package compair

import (
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
	profile := strings.ToLower(strings.TrimSpace(viper.GetString("profile.active")))
	localCore := strings.HasPrefix(path, "compair core") ||
		strings.Contains(apiBase, "localhost:8000") ||
		strings.Contains(apiBase, "127.0.0.1:8000") ||
		profile == "local"

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
		"401",
		"403",
		"unauthorized",
		"forbidden",
	) {
		add("Run 'compair doctor' to check auth, group, and repo binding health.")
		add("If you just need a fresh session, run 'compair login'.")
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
	) {
		add("Run 'compair doctor' to verify auth, active group, and repo binding.")
		add("If the active group is wrong, run 'compair group ls' then 'compair group use <id>'.")
	}

	if containsAny(msg,
		"chunk task ",
		"processing timeout",
		"saved processing task",
		"pending review task",
		"ended with status failure",
		"demo priming failed",
	) {
		add("Run 'compair doctor' to inspect pending tasks, repo binding, and current sync state.")
		if localCore {
			add("If this is against local Core, run 'compair core doctor' to check Docker, the container, and /health.")
		} else {
			add("If this keeps reproducing on hosted Compair Cloud, send the task id and 'compair doctor --json' output to support@compair.sh.")
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
		} else {
			add("Run 'compair doctor' to confirm the API base, auth, and current repo/group binding.")
			add("If the API is healthy but hosted Cloud still returns server errors, contact support@compair.sh with the failing command and 'compair doctor --json' output.")
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
