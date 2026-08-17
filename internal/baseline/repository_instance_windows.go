//go:build windows

package baseline

import (
	"fmt"
	"os"
	"syscall"
)

// repositoryInstanceIdentity identifies one local Git metadata object. It is
// hashed into the non-authoritative binding sanity fingerprint and never sent
// to Core or used as proof of repository ownership.
func repositoryInstanceIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return "", fmt.Errorf("unsafe Git metadata path")
	}
	stat, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return "", fmt.Errorf("Git metadata identity unavailable")
	}
	return fmt.Sprintf(
		"windows:%d:%d",
		stat.CreationTime.HighDateTime,
		stat.CreationTime.LowDateTime,
	), nil
}
