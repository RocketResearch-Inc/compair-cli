package fs

import (
    "fmt"
    "os"
    "path/filepath"
    "runtime"
    "syscall"
)

type FileSig struct { Dev uint64; Ino uint64 }

func StatSig(path string) (FileSig, error) {
    fi, err := os.Lstat(path)
    if err != nil { return FileSig{}, err }
    st, ok := fi.Sys().(*syscall.Stat_t)
    if ok {
        return FileSig{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
    }
    // Fallback for non-POSIX: use size+mtime hash surrogate (weak)
    return FileSig{Dev: uint64(fi.Size()), Ino: uint64(fi.ModTime().Unix())}, nil
}

func SigString(s FileSig) string { return fmt.Sprintf("%d:%d", s.Dev, s.Ino) }

// CanonicalPath resolves to an absolute path. If followSymlinks is true, attempts to resolve symlinks.
func CanonicalPath(p string, followSymlinks bool) (string, error) {
    if p == "" { return "", fmt.Errorf("empty path") }
    // On Windows, EvalSymlinks is best-effort but fine.
    var err error
    if !filepath.IsAbs(p) {
        p, err = filepath.Abs(p); if err != nil { return "", err }
    }
    if followSymlinks {
        r, err := filepath.EvalSymlinks(p)
        if err == nil { p = r }
    }
    // Normalize case on Windows
    if runtime.GOOS == "windows" { p = filepath.Clean(p) }
    return p, nil
}

