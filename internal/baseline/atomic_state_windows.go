//go:build windows

package baseline

import (
	"os"

	"golang.org/x/sys/windows"
)

func replaceFileAtomically(source, target string) error {
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPath, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePath,
		targetPath,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

// MoveFileEx WRITE_THROUGH flushes the replacement. Windows does not support
// opening a directory and fsyncing it through os.File like POSIX systems.
func syncDirectory(_ string) error { return nil }

// The application directory is below the current user's profile and inherits
// its protected ACL. POSIX permission bits are not an ACL signal on Windows.
func ensurePrivateDirectoryPermissions(_ string, _ os.FileInfo) error { return nil }

func privateFilePermissions(_ os.FileInfo) bool { return true }
