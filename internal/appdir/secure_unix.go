//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package appdir

import (
	"errors"
	"os"
	"syscall"
)

func secureConfiguredRoot(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if mkdirErr := os.Mkdir(root, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			return invalid("unavailable")
		}
		info, err = os.Lstat(root)
	}
	if err != nil {
		return invalid("unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return invalid("symlink")
	}
	if !info.IsDir() {
		return invalid("not_directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return invalid("unsafe_permissions")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return invalid("wrong_owner")
	}
	if err := syscall.Access(root, 0o7); err != nil {
		return invalid("inaccessible")
	}
	return nil
}
