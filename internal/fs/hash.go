package fs

import (
    "io"
    "os"
    "time"

    xxhash "github.com/cespare/xxhash/v2"
)

// FastHash returns (hash,size,mtime)
func FastHash(path string) (string, int64, int64, error) {
    f, err := os.Open(path)
    if err != nil { return "", 0, 0, err }
    defer f.Close()
    h := xxhash.New()
    n, err := io.Copy(h, f)
    if err != nil { return "", 0, 0, err }
    fi, _ := f.Stat()
    mtime := fi.ModTime().Unix()
    return toHex(h.Sum64()), n, mtime, nil
}

func toHex(u uint64) string { return formatUint64Hex(u) }

// fast hex formatter to avoid fmt overhead
func formatUint64Hex(u uint64) string {
    const hexdigits = "0123456789abcdef"
    var a [16]byte
    for i := 15; i >= 0; i-- { a[i] = hexdigits[u&0xF]; u >>= 4 }
    return string(a[:])
}

// FileTimes returns size and mtime (seconds)
func FileTimes(path string) (int64, int64, error) {
    fi, err := os.Stat(path); if err != nil { return 0, 0, err }
    return fi.Size(), fi.ModTime().Unix(), nil
}

// IsRegular returns true if the path is a regular file
func IsRegular(path string) bool {
    fi, err := os.Lstat(path); if err != nil { return false }
    return fi.Mode().IsRegular()
}

// IsDir returns true if the path is a directory
func IsDir(path string) bool {
    fi, err := os.Lstat(path); if err != nil { return false }
    return fi.IsDir()
}

// NowSec returns current epoch seconds
func NowSec() int64 { return time.Now().Unix() }

