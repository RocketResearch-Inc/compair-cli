package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func debugHTTPEnabled() bool {
	return envFlagEnabled("COMPAIR_DEBUG_HTTP") || envFlagEnabled("COMPAIR_VERBOSE")
}

func envFlagEnabled(name string) bool {
	val := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch val {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func requestIDFromHeaders(h http.Header) string {
	candidates := []string{
		"X-Request-Id",
		"X-Request-ID",
		"X-Correlation-Id",
		"X-Correlation-ID",
		"X-Amzn-Trace-Id",
		"CF-Ray",
		"X-Cloudflare-Request-Id",
	}
	for _, key := range candidates {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

func logHTTP(method, path string, status int, dur time.Duration, reqID string, err error) {
	if !debugHTTPEnabled() {
		return
	}
	d := dur.Truncate(time.Millisecond)
	if d == 0 {
		d = dur
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[http] %s %s error=%v dur=%s\n", method, path, err, d)
		return
	}
	msg := fmt.Sprintf("[http] %s %s status=%d dur=%s", method, path, status, d)
	if reqID != "" {
		msg += " request_id=" + reqID
	}
	fmt.Fprintln(os.Stderr, msg)
}
