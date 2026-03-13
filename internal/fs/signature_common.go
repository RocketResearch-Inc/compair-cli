package fs

import (
	"fmt"
	"os"
	"path/filepath"
)

type FileSig struct {
	Dev uint64
	Ino uint64
}

func SigString(s FileSig) string { return fmt.Sprintf("%d:%d", s.Dev, s.Ino) }

// CanonicalPath resolves to an absolute path. If followSymlinks is true, attempts to resolve symlinks.
func CanonicalPath(p string, followSymlinks bool) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(p) {
		var err error
		p, err = filepath.Abs(p)
		if err != nil {
			return "", err
		}
	}
	if followSymlinks {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			p = r
		}
	}
	return filepath.Clean(p), nil
}

func fallbackFileSig(fi os.FileInfo) FileSig {
	return FileSig{
		Dev: uint64(fi.Size()),
		Ino: uint64(fi.ModTime().Unix()),
	}
}
