package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const EnvironmentVariable = "COMPAIR_APP_DIR"

var resolved struct {
	sync.RWMutex
	initialized bool
	root        string
	err         error
}

// Error is deliberately path-free so invalid application roots cannot leak
// through ordinary CLI diagnostics.
type Error struct {
	Reason string
}

func (err *Error) Error() string {
	if err == nil || err.Reason == "" {
		return "invalid configured application root"
	}
	return "invalid configured application root: " + err.Reason
}

// InitializeFromEnvironment resolves the application root once for one CLI
// invocation. It is called at the root Cobra boundary before stateful work.
func InitializeFromEnvironment() error {
	root, err := resolveFromEnvironment()
	resolved.Lock()
	resolved.initialized = true
	resolved.root = root
	resolved.err = err
	resolved.Unlock()
	return err
}

// Root returns the centrally initialized root. Direct library callers that do
// not run the CLI initializer retain the historical ~/.compair default; they
// must use explicit state-directory injection for isolated baseline stores.
func Root() (string, error) {
	resolved.RLock()
	if resolved.initialized {
		root, err := resolved.root, resolved.err
		resolved.RUnlock()
		return root, err
	}
	resolved.RUnlock()
	return legacyRoot()
}

func Path(elements ...string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	parts := append([]string{root}, elements...)
	return filepath.Join(parts...), nil
}

func resolveFromEnvironment() (string, error) {
	raw, configured := os.LookupEnv(EnvironmentVariable)
	if !configured {
		return legacyRoot()
	}
	if raw == "" || strings.TrimSpace(raw) == "" {
		return "", invalid("empty")
	}
	if raw != strings.TrimSpace(raw) || !utf8.ValidString(raw) || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", invalid("malformed")
	}
	if !filepath.IsAbs(raw) {
		return "", invalid("not_absolute")
	}
	clean := filepath.Clean(raw)
	if clean != raw {
		return "", invalid("not_canonical")
	}
	if err := secureConfiguredRoot(clean); err != nil {
		return "", err
	}
	return clean, nil
}

func legacyRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("application root unavailable")
	}
	return filepath.Join(home, ".compair"), nil
}

func invalid(reason string) error {
	return &Error{Reason: reason}
}
