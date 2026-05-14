package fs

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Simple ignore matcher with defaults + optional .compairignore globs.
type Ignore struct {
	dirNames  map[string]struct{}
	fileExts  map[string]struct{}
	fileNames map[string]struct{}
	globs     []string
}

func DefaultIgnore() *Ignore {
	return &Ignore{
		dirNames: map[string]struct{}{
			".git": {}, ".compair": {}, "node_modules": {}, "dist": {}, "build": {}, "target": {}, ".venv": {}, "__pycache__": {}, "vendor": {},
		},
		fileExts:  map[string]struct{}{".min.js": {}, ".map": {}, ".lock": {}, ".bin": {}, ".exe": {}, ".dll": {}, ".class": {}},
		fileNames: map[string]struct{}{".DS_Store": {}},
		globs:     []string{},
	}
}

func LoadIgnore(root string) *Ignore {
	ig := DefaultIgnore()
	p := filepath.Join(root, ".compairignore")
	f, err := os.Open(p)
	if err != nil {
		return ig
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ig.globs = append(ig.globs, filepath.ToSlash(line))
	}
	return ig
}

func (ig *Ignore) ShouldIgnore(path string, isDir bool) bool {
	path = filepath.ToSlash(path)
	base := filepath.Base(path)
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		if _, ok := ig.dirNames[part]; ok {
			return true
		}
	}
	if isDir {
		if _, ok := ig.dirNames[base]; ok {
			return true
		}
	} else {
		if _, ok := ig.fileNames[base]; ok {
			return true
		}
		// ext matching including multi-dot extensions
		for ext := range ig.fileExts {
			if strings.HasSuffix(strings.ToLower(base), ext) {
				return true
			}
		}
	}
	// Glob patterns match against both repo-relative path and basename. A
	// trailing slash is treated as a directory-prefix rule for tracked files.
	for _, g := range ig.globs {
		g = filepath.ToSlash(strings.TrimSpace(g))
		if g == "" {
			continue
		}
		if strings.HasSuffix(g, "/") {
			prefix := strings.TrimSuffix(g, "/")
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return true
			}
			continue
		}
		if path == g || base == g {
			return true
		}
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
	}
	return false
}
