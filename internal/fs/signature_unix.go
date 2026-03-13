//go:build !windows

package fs

import (
	"os"
	"syscall"
)

func StatSig(path string) (FileSig, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileSig{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if ok {
		return FileSig{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
	}
	return fallbackFileSig(fi), nil
}
