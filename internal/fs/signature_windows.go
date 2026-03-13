//go:build windows

package fs

import "os"

func StatSig(path string) (FileSig, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileSig{}, err
	}
	return fallbackFileSig(fi), nil
}
