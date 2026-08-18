package baseline

import (
	"path/filepath"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/appdir"
)

func resolveBaselineStateDirectory(override, leaf string) (string, error) {
	if override != "" {
		clean := strings.TrimSpace(override)
		if clean == "" || clean != override || !filepath.IsAbs(clean) || filepath.Clean(clean) != clean {
			return "", uploadError(UploadFailureContract, "unsafe_state_path")
		}
		if err := ensureProtectedDirectory(clean); err != nil {
			return "", err
		}
		return clean, nil
	}

	root, err := appdir.Root()
	if err != nil {
		return "", uploadError(UploadFailureInternal, "state_directory_unavailable")
	}
	if err := ensureProtectedDirectory(root); err != nil {
		return "", err
	}
	stateRoot := filepath.Join(root, "state")
	if err := ensureProtectedDirectory(stateRoot); err != nil {
		return "", err
	}
	directory := filepath.Join(stateRoot, leaf)
	if err := ensureProtectedDirectory(directory); err != nil {
		return "", err
	}
	return directory, nil
}
