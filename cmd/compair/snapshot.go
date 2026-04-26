package compair

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
)

type snapshotOptions struct {
	MaxTreeEntries int
	MaxFiles       int
	MaxTotalBytes  int
	MaxFileBytes   int
	MaxFileRead    int
	IncludeGlobs   []string
	ExcludeGlobs   []string
}

const snapshotChunkDelimiter = "<<<COMPAIR_CHUNK>>>"

const (
	snapshotChunkProfileDefault         = "default"
	snapshotChunkProfileSemanticLite    = "semantic-lite"
	snapshotChunkProfileSemanticContext = "semantic-context"
	snapshotChunkProfileMarkdownStrict  = "markdown-strict"
	snapshotChunkProfileMarkdownH2      = "markdown-h2"
	snapshotChunkProfileMarkdownH2Win   = "markdown-h2-window"
	snapshotChunkProfileSignalStress    = "signal-stress"
)

func defaultSnapshotOptions() snapshotOptions {
	return snapshotOptions{
		MaxTreeEntries: 0,
		MaxFiles:       0,
		MaxTotalBytes:  0,
		MaxFileBytes:   0,
		MaxFileRead:    0,
		IncludeGlobs:   nil,
		ExcludeGlobs:   nil,
	}
}

type snapshotStats struct {
	TotalFiles     int
	TotalBytes     int64
	TreeEntries    int
	IncludedFiles  int
	TruncatedFiles int
	OmittedFiles   int
	IgnoredFiles   int
	NonTextFiles   int
	TooLargeFiles  int
	BudgetBytes    int
	IncludedBytes  int
}

type snapshotResult struct {
	Text         string
	Head         string
	Stats        snapshotStats
	ChunkProfile string
	ManifestPath string
}

type snapshotFile struct {
	Path     string
	Size     int64
	Priority int
}

type snapshotChunkManifest struct {
	GeneratedAt  string                      `json:"generated_at"`
	Root         string                      `json:"root"`
	GroupID      string                      `json:"group_id,omitempty"`
	DocumentID   string                      `json:"document_id,omitempty"`
	RemoteURL    string                      `json:"remote_url,omitempty"`
	Branch       string                      `json:"branch,omitempty"`
	Head         string                      `json:"head,omitempty"`
	ChunkProfile string                      `json:"chunk_profile"`
	Stats        snapshotStats               `json:"stats"`
	Files        []snapshotChunkManifestFile `json:"files"`
}

type snapshotChunkManifestFile struct {
	Path            string `json:"path"`
	Lang            string `json:"lang,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	ChunkCount      int    `json:"chunk_count"`
	ChunkLineCounts []int  `json:"chunk_line_counts,omitempty"`
	ChunkCharCounts []int  `json:"chunk_char_counts,omitempty"`
}

var errNonText = errors.New("non-text file")

func buildRepoSnapshot(root, groupID string, repo *config.Repo, opts snapshotOptions) (snapshotResult, error) {
	chunkProfile := snapshotChunkProfile()
	files, err := listTrackedFiles(root)
	if err != nil {
		return snapshotResult{}, err
	}
	sort.Strings(files)
	ig := fsutil.LoadIgnore(root)

	var tree []string
	var candidates []snapshotFile
	var manifestFiles []snapshotChunkManifestFile
	var ignored int
	var tooLarge int
	var nonText int
	totalFiles := 0
	var totalBytes int64

	includeGlobs := normalizeGlobs(opts.IncludeGlobs)
	excludeGlobs := normalizeGlobs(opts.ExcludeGlobs)

	for _, rel := range files {
		relSlash := filepath.ToSlash(rel)
		if matchesAnyGlob(relSlash, excludeGlobs) || matchesAnyGlob(filepath.Base(relSlash), excludeGlobs) {
			continue
		}
		if len(includeGlobs) > 0 && !matchesAnyGlob(relSlash, includeGlobs) && !matchesAnyGlob(filepath.Base(relSlash), includeGlobs) {
			continue
		}
		if ig.ShouldIgnore(rel, false) {
			ignored++
			continue
		}
		full := filepath.Join(root, rel)
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			continue
		}
		totalFiles++
		totalBytes += fi.Size()
		if opts.MaxTreeEntries <= 0 || len(tree) < opts.MaxTreeEntries {
			tree = append(tree, fmt.Sprintf("- %s (%s)", rel, formatBytes(fi.Size())))
		}
		if opts.MaxFileRead > 0 && fi.Size() > int64(opts.MaxFileRead) {
			tooLarge++
			continue
		}
		if !looksLikeTextFile(full) {
			nonText++
			continue
		}
		candidates = append(candidates, snapshotFile{
			Path:     rel,
			Size:     fi.Size(),
			Priority: priorityForFile(rel),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		if candidates[i].Size != candidates[j].Size {
			return candidates[i].Size < candidates[j].Size
		}
		return candidates[i].Path < candidates[j].Path
	})

	remote := strings.TrimSpace(repo.RemoteURL)
	if remote == "" {
		if r, err := git.OriginURLAt(root); err == nil {
			remote = r
		}
	}
	branch := repo.DefaultBranch
	if strings.TrimSpace(branch) == "" {
		branch = git.DefaultBranchAt(root)
	}
	head := headCommit(root)

	builder := strings.Builder{}
	builder.WriteString("# Compair baseline snapshot\n")
	builder.WriteString(fmt.Sprintf("- Generated: %s\n", time.Now().Format(time.RFC3339)))
	if repo.DocumentID != "" {
		builder.WriteString(fmt.Sprintf("- Document: %s\n", repo.DocumentID))
	}
	if strings.TrimSpace(groupID) != "" {
		builder.WriteString(fmt.Sprintf("- Group: %s\n", groupID))
	}
	if strings.TrimSpace(remote) != "" {
		builder.WriteString(fmt.Sprintf("- Remote: %s\n", remote))
	}
	if strings.TrimSpace(branch) != "" {
		builder.WriteString(fmt.Sprintf("- Branch: %s\n", branch))
	}
	if strings.TrimSpace(head) != "" {
		builder.WriteString(fmt.Sprintf("- Commit: %s\n", head))
	}
	builder.WriteString(fmt.Sprintf("- Files tracked: %d\n", totalFiles))
	builder.WriteString("- Snapshot: full (baseline)\n\n")

	builder.WriteString("## File tree (tracked)\n")
	for _, line := range tree {
		builder.WriteString(line + "\n")
	}
	if totalFiles > len(tree) {
		builder.WriteString(fmt.Sprintf("\n_Trimmed file tree after %d entries (total: %d)._ \n", len(tree), totalFiles))
	}
	builder.WriteString("\n")

	builder.WriteString("## Selected file contents\n")

	remaining := remainingSnapshotBudget(opts.MaxTotalBytes, builder.Len())
	if opts.MaxTotalBytes > 0 && remaining <= 0 {
		includedBytes := opts.MaxTotalBytes - remaining
		if includedBytes < 0 {
			includedBytes = 0
		}
		stats := snapshotStats{
			TotalFiles:    totalFiles,
			TotalBytes:    totalBytes,
			TreeEntries:   len(tree),
			IgnoredFiles:  ignored,
			NonTextFiles:  nonText,
			TooLargeFiles: tooLarge,
			BudgetBytes:   opts.MaxTotalBytes,
			IncludedBytes: includedBytes,
		}
		manifestPath := writeSnapshotManifestIfConfigured(snapshotChunkManifest{
			GeneratedAt:  time.Now().Format(time.RFC3339),
			Root:         root,
			GroupID:      strings.TrimSpace(groupID),
			DocumentID:   strings.TrimSpace(repo.DocumentID),
			RemoteURL:    strings.TrimSpace(remote),
			Branch:       strings.TrimSpace(branch),
			Head:         strings.TrimSpace(head),
			ChunkProfile: chunkProfile,
			Stats:        stats,
			Files:        manifestFiles,
		})
		return snapshotResult{
			Text:         builder.String(),
			Head:         head,
			Stats:        stats,
			ChunkProfile: chunkProfile,
			ManifestPath: manifestPath,
		}, nil
	}

	includedFiles := 0
	truncatedFiles := 0
	omittedFiles := 0
	writtenChunks := 0

	for _, candidate := range candidates {
		if (opts.MaxFiles > 0 && includedFiles >= opts.MaxFiles) || (opts.MaxTotalBytes > 0 && remaining <= 0) {
			break
		}
		full := filepath.Join(root, candidate.Path)
		content, truncated, err := readLimitedTextFile(full, opts.MaxFileBytes)
		if err != nil {
			if errors.Is(err, errNonText) {
				nonText++
			}
			omittedFiles++
			continue
		}
		if strings.TrimSpace(content) == "" {
			omittedFiles++
			continue
		}
		lang, rule := chunkRuleForFile(candidate.Path)
		chunks := chunkText(content, candidate.Path, lang, rule, chunkProfile)
		if len(chunks) == 0 {
			omittedFiles++
			continue
		}
		manifestFiles = append(manifestFiles, buildSnapshotChunkManifestFile(candidate.Path, lang, truncated, chunks))
		added := false
		for i, chunk := range chunks {
			if opts.MaxTotalBytes > 0 && remaining <= 0 {
				break
			}
			meta := fmt.Sprintf("### File: %s", candidate.Path)
			parts := []string{}
			if len(chunks) > 1 {
				parts = append(parts, fmt.Sprintf("part %d/%d", i+1, len(chunks)))
			}
			if lang != "" {
				parts = append(parts, "lang "+lang)
			}
			if truncated && i == 0 {
				parts = append(parts, "truncated")
			}
			if len(parts) > 0 {
				meta += " (" + strings.Join(parts, ", ") + ")"
			}
			chunk = strings.TrimRight(chunk, "\n")
			payload, ok := fitChunk(meta, chunk, lang, rule.MaxLines, remaining)
			if !ok {
				if opts.MaxTotalBytes > 0 {
					remaining = 0
				}
				break
			}
			if writtenChunks == 0 {
				builder.WriteString(snapshotChunkDelimiter + "\n")
			} else {
				builder.WriteString("\n" + snapshotChunkDelimiter + "\n")
			}
			builder.WriteString(payload)
			remaining = remainingSnapshotBudget(opts.MaxTotalBytes, builder.Len())
			added = true
			writtenChunks++
		}
		if added {
			includedFiles++
			if truncated {
				truncatedFiles++
			}
		} else {
			omittedFiles++
		}
	}

	if writtenChunks > 0 {
		builder.WriteString("\n" + snapshotChunkDelimiter + "\n")
	}
	builder.WriteString("## Snapshot limits\n")
	builder.WriteString(fmt.Sprintf("- Content budget: %s\n", describeSnapshotLimitBytes(opts.MaxTotalBytes)))
	builder.WriteString(fmt.Sprintf("- Files included: %d\n", includedFiles))
	if truncatedFiles > 0 {
		builder.WriteString(fmt.Sprintf("- Files truncated: %d\n", truncatedFiles))
	}
	if omittedFiles > 0 {
		builder.WriteString(fmt.Sprintf("- Files omitted (empty/unreadable): %d\n", omittedFiles))
	}
	if ignored > 0 {
		builder.WriteString(fmt.Sprintf("- Ignored by .compairignore: %d\n", ignored))
	}
	if nonText > 0 {
		builder.WriteString(fmt.Sprintf("- Skipped non-text files: %d\n", nonText))
	}
	if tooLarge > 0 {
		builder.WriteString(fmt.Sprintf("- Skipped large files: %d\n", tooLarge))
	}

	includedBytes := builder.Len()
	stats := snapshotStats{
		TotalFiles:     totalFiles,
		TotalBytes:     totalBytes,
		TreeEntries:    len(tree),
		IncludedFiles:  includedFiles,
		TruncatedFiles: truncatedFiles,
		OmittedFiles:   omittedFiles,
		IgnoredFiles:   ignored,
		NonTextFiles:   nonText,
		TooLargeFiles:  tooLarge,
		BudgetBytes:    opts.MaxTotalBytes,
		IncludedBytes:  includedBytes,
	}
	manifestPath := writeSnapshotManifestIfConfigured(snapshotChunkManifest{
		GeneratedAt:  time.Now().Format(time.RFC3339),
		Root:         root,
		GroupID:      strings.TrimSpace(groupID),
		DocumentID:   strings.TrimSpace(repo.DocumentID),
		RemoteURL:    strings.TrimSpace(remote),
		Branch:       strings.TrimSpace(branch),
		Head:         strings.TrimSpace(head),
		ChunkProfile: chunkProfile,
		Stats:        stats,
		Files:        manifestFiles,
	})
	return snapshotResult{
		Text:         builder.String(),
		Head:         head,
		Stats:        stats,
		ChunkProfile: chunkProfile,
		ManifestPath: manifestPath,
	}, nil
}

func listTrackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(out), "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func headCommit(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	kb := float64(size) / 1024.0
	if kb < 1024 {
		return fmt.Sprintf("%.1f KB", kb)
	}
	mb := kb / 1024.0
	if mb < 1024 {
		return fmt.Sprintf("%.1f MB", mb)
	}
	gb := mb / 1024.0
	return fmt.Sprintf("%.1f GB", gb)
}

func looksLikeTextFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return looksLikeText(buf[:n])
}

func readLimitedTextFile(path string, maxBytes int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	probe := make([]byte, 4096)
	n, err := f.Read(probe)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	if !looksLikeText(probe[:n]) {
		return "", false, errNonText
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	if maxBytes <= 0 {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", false, err
		}
		return string(data), false, nil
	}
	limited := int64(maxBytes + 1)
	data, err := io.ReadAll(io.LimitReader(f, limited))
	if err != nil {
		return "", false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return string(data), truncated, nil
}

func snapshotChunkProfile() string {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("COMPAIR_SNAPSHOT_CHUNK_PROFILE"))) {
	case "", snapshotChunkProfileDefault:
		return snapshotChunkProfileDefault
	case snapshotChunkProfileSemanticLite:
		return snapshotChunkProfileSemanticLite
	case snapshotChunkProfileSemanticContext:
		return snapshotChunkProfileSemanticContext
	case snapshotChunkProfileMarkdownStrict:
		return snapshotChunkProfileMarkdownStrict
	case snapshotChunkProfileMarkdownH2:
		return snapshotChunkProfileMarkdownH2
	case snapshotChunkProfileMarkdownH2Win:
		return snapshotChunkProfileMarkdownH2Win
	case snapshotChunkProfileSignalStress:
		return snapshotChunkProfileSignalStress
	default:
		return snapshotChunkProfileDefault
	}
}

func buildSnapshotChunkManifestFile(path, lang string, truncated bool, chunks []string) snapshotChunkManifestFile {
	lineCounts := make([]int, 0, len(chunks))
	charCounts := make([]int, 0, len(chunks))
	for _, chunk := range chunks {
		trimmed := strings.TrimRight(chunk, "\n")
		if trimmed == "" {
			lineCounts = append(lineCounts, 0)
		} else {
			lineCounts = append(lineCounts, strings.Count(trimmed, "\n")+1)
		}
		charCounts = append(charCounts, len(chunk))
	}
	return snapshotChunkManifestFile{
		Path:            path,
		Lang:            strings.TrimSpace(lang),
		Truncated:       truncated,
		ChunkCount:      len(chunks),
		ChunkLineCounts: lineCounts,
		ChunkCharCounts: charCounts,
	}
}

func writeSnapshotManifestIfConfigured(manifest snapshotChunkManifest) string {
	dir := strings.TrimSpace(os.Getenv("COMPAIR_SNAPSHOT_MANIFEST_DIR"))
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ""
	}
	name := fmt.Sprintf(
		"%s-%d-%s.json",
		time.Now().UTC().Format("20060102-150405.000000000"),
		os.Getpid(),
		sanitizeSnapshotManifestName(manifest.Root),
	)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return ""
	}
	return path
}

func sanitizeSnapshotManifestName(root string) string {
	name := strings.TrimSpace(filepath.Base(root))
	if name == "" {
		name = "snapshot"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", "\t", "-")
	name = replacer.Replace(name)
	if name == "" {
		return "snapshot"
	}
	return name
}

type chunkRule struct {
	MaxLines      int
	MinLines      int
	SplitPrefixes []string
}

func chunkRuleForFile(path string) (string, chunkRule) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == "dockerfile" || strings.HasPrefix(base, "dockerfile."):
		return "dockerfile", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"FROM ", "RUN ", "COPY ", "ADD ", "CMD ", "ENTRYPOINT "}}
	case base == "makefile" || strings.HasPrefix(base, "makefile."):
		return "makefile", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{".PHONY", "all:", "build:", "test:", "lint:"}}
	case base == "justfile":
		return "makefile", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"@", "build:", "test:", "lint:"}}
	}
	switch ext {
	case "go":
		return "go", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"func ", "type ", "var ", "const "}}
	case "py":
		return "python", chunkRule{MaxLines: 140, MinLines: 12, SplitPrefixes: []string{"def ", "class ", "async def "}}
	case "js":
		return "javascript", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"function ", "class ", "export ", "const ", "let "}}
	case "ts", "tsx":
		return "typescript", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"interface ", "type ", "class ", "export ", "const ", "let ", "function "}}
	case "jsx":
		return "jsx", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"function ", "class ", "export ", "const ", "let "}}
	case "rs":
		return "rust", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"fn ", "struct ", "enum ", "impl "}}
	case "java":
		return "java", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"class ", "interface ", "enum "}}
	case "c", "h":
		return "c", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"struct ", "typedef ", "enum ", "static ", "void ", "int ", "char "}}
	case "cc", "cpp", "cxx", "hpp":
		return "cpp", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"class ", "struct ", "enum ", "namespace ", "template ", "static ", "void "}}
	case "cs":
		return "csharp", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"class ", "struct ", "interface ", "enum ", "namespace "}}
	case "kt", "kts":
		return "kotlin", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"class ", "interface ", "fun ", "object ", "sealed "}}
	case "swift":
		return "swift", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"class ", "struct ", "enum ", "protocol ", "func "}}
	case "rb":
		return "ruby", chunkRule{MaxLines: 140, MinLines: 10, SplitPrefixes: []string{"class ", "module ", "def "}}
	case "php":
		return "php", chunkRule{MaxLines: 140, MinLines: 10, SplitPrefixes: []string{"class ", "interface ", "trait ", "function "}}
	case "scala":
		return "scala", chunkRule{MaxLines: 160, MinLines: 12, SplitPrefixes: []string{"class ", "object ", "trait ", "def "}}
	case "sql":
		return "sql", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"SELECT ", "WITH ", "INSERT ", "UPDATE ", "DELETE ", "CREATE "}}
	case "proto":
		return "proto", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"message ", "service ", "enum "}}
	case "graphql", "gql":
		return "graphql", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"type ", "input ", "enum ", "interface ", "schema "}}
	case "tf", "tfvars", "hcl":
		return "hcl", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"resource ", "module ", "variable ", "output ", "provider "}}
	case "ini", "cfg":
		return "ini", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"["}}
	case "md", "markdown":
		return "markdown", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"# "}}
	case "yaml", "yml":
		return "yaml", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"---"}}
	case "json":
		return "json", chunkRule{MaxLines: 120, MinLines: 8}
	case "toml":
		return "toml", chunkRule{MaxLines: 120, MinLines: 8, SplitPrefixes: []string{"["}}
	case "sh":
		return "bash", chunkRule{MaxLines: 140, MinLines: 10, SplitPrefixes: []string{"function ", "#!/"}}
	case "css":
		return "css", chunkRule{MaxLines: 140, MinLines: 10, SplitPrefixes: []string{"@", ".", "#"}}
	case "html", "htm":
		return "html", chunkRule{MaxLines: 140, MinLines: 10, SplitPrefixes: []string{"<section", "<div", "<main", "<article"}}
	default:
		return "", chunkRule{MaxLines: 140, MinLines: 10}
	}
}

func chunkText(text, path, lang string, rule chunkRule, profile string) []string {
	if profile == snapshotChunkProfileSignalStress {
		switch {
		case isMarkdownLike(lang, path):
			if chunks := chunkMarkdownSignalStress(text, rule); len(chunks) > 0 {
				return chunks
			}
		case isStructuredLike(lang, path):
			if chunks := chunkStructuredSignalStress(text, lang, path, rule); len(chunks) > 0 {
				return chunks
			}
		}
	}
	if isMarkdownLike(lang, path) && isMarkdownSpecificProfile(profile) {
		if chunks := chunkMarkdownSectionsWithProfile(text, rule, profile); len(chunks) > 0 {
			return chunks
		}
	}
	if profile == snapshotChunkProfileSemanticLite || profile == snapshotChunkProfileSemanticContext {
		includePreamble := profile == snapshotChunkProfileSemanticContext
		if chunks := chunkTextSemantic(text, path, lang, rule, includePreamble); len(chunks) > 0 {
			return chunks
		}
	}
	return chunkTextDefault(text, rule)
}

func isMarkdownSpecificProfile(profile string) bool {
	switch profile {
	case snapshotChunkProfileMarkdownStrict, snapshotChunkProfileMarkdownH2, snapshotChunkProfileMarkdownH2Win:
		return true
	default:
		return false
	}
}

func chunkTextDefault(text string, rule chunkRule) []string {
	if rule.MaxLines <= 0 {
		rule.MaxLines = 140
	}
	if rule.MinLines <= 0 {
		rule.MinLines = 10
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) <= rule.MaxLines {
		return []string{text}
	}
	chunks := []string{}
	start := 0
	for i := 0; i < len(lines); i++ {
		if i-start >= rule.MaxLines {
			chunks = append(chunks, strings.Join(lines[start:i], "\n"))
			start = i
			continue
		}
		if i-start < rule.MinLines {
			continue
		}
		line := strings.TrimSpace(lines[i])
		for _, prefix := range rule.SplitPrefixes {
			if strings.HasPrefix(line, prefix) {
				chunks = append(chunks, strings.Join(lines[start:i], "\n"))
				start = i
				break
			}
		}
	}
	if start < len(lines) {
		chunks = append(chunks, strings.Join(lines[start:], "\n"))
	}
	return chunks
}

func chunkTextSemantic(text, path, lang string, rule chunkRule, includePreamble bool) []string {
	switch {
	case isMarkdownLike(lang, path):
		return chunkMarkdownSections(text, rule)
	case isStructuredLike(lang, path):
		return chunkStructuredSections(text, lang, path, rule)
	case isCodeLike(lang):
		return chunkCodeSymbols(text, lang, rule, includePreamble)
	default:
		return nil
	}
}

func isCodeLike(lang string) bool {
	switch lang {
	case "go", "python", "javascript", "typescript", "jsx", "rust", "java", "c", "cpp", "csharp", "kotlin", "swift", "ruby", "php", "scala", "makefile", "dockerfile", "bash":
		return true
	default:
		return false
	}
}

func isMarkdownLike(lang, path string) bool {
	if lang == "markdown" {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return base == "license" || base == "copying" || base == "notice"
}

func isStructuredLike(lang, path string) bool {
	switch lang {
	case "toml", "yaml", "json", "ini", "hcl", "sql", "proto", "graphql":
		return true
	default:
		base := strings.ToLower(filepath.Base(path))
		return base == "license" || base == "copying" || base == "notice"
	}
}

func normalizeLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(text, "\n")
}

func trimBlankEdges(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func joinNonEmptySegments(segments [][]string) []string {
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		trimmed := strings.TrimSpace(strings.Join(trimBlankEdges(segment), "\n"))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func splitOversizedSegments(segments []string, rule chunkRule) []string {
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		lineCount := len(strings.Split(segment, "\n"))
		if lineCount > rule.MaxLines {
			out = append(out, chunkTextDefault(segment, rule)...)
			continue
		}
		out = append(out, segment)
	}
	return out
}

func mergeSmallSegments(segments []string, rule chunkRule) []string {
	if len(segments) < 2 {
		return segments
	}
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		lineCount := len(strings.Split(segment, "\n"))
		if lineCount < rule.MinLines && len(out) > 0 {
			merged := out[len(out)-1] + "\n" + segment
			if len(strings.Split(merged, "\n")) <= rule.MaxLines {
				out[len(out)-1] = merged
				continue
			}
		}
		out = append(out, segment)
	}
	return out
}

func chunkCodeSymbols(text, lang string, rule chunkRule, includePreamble bool) []string {
	lines := normalizeLines(text)
	if len(lines) <= rule.MaxLines {
		return nil
	}
	starts := symbolStartIndices(lines, lang, rule)
	if len(starts) == 0 {
		return nil
	}
	segments := make([][]string, 0, len(starts)+1)
	firstStart := starts[0]
	preamble := trimBlankEdges(lines[:firstStart])
	contextPreamble := preambleContextLines(preamble, rule)
	if len(preamble) > 0 && !includePreamble {
		segments = append(segments, preamble)
	}
	for i, start := range starts {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		segment := trimBlankEdges(lines[start:end])
		if len(segment) == 0 {
			continue
		}
		if includePreamble && len(contextPreamble) > 0 {
			segment = append(append([]string{}, contextPreamble...), append([]string{""}, segment...)...)
		} else if i == 0 && len(preamble) > 0 && len(segments) == 0 {
			segment = append(append([]string{}, preamble...), append([]string{""}, segment...)...)
		}
		segments = append(segments, segment)
	}
	return mergeSmallSegments(splitOversizedSegments(joinNonEmptySegments(segments), rule), rule)
}

func preambleContextLines(preamble []string, rule chunkRule) []string {
	trimmed := trimBlankEdges(preamble)
	if len(trimmed) == 0 {
		return nil
	}
	limit := 24
	if rule.MinLines > 0 && rule.MinLines < limit {
		limit = rule.MinLines
	}
	if len(trimmed) > limit {
		trimmed = trimmed[:limit]
	}
	return trimmed
}

func symbolStartIndices(lines []string, lang string, rule chunkRule) []int {
	starts := []int{}
	seen := map[int]struct{}{}
	for idx := 0; idx < len(lines); idx++ {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed == "" {
			continue
		}
		if lang == "python" && strings.HasPrefix(trimmed, "@") && isTopLevelLine(lines[idx], lang) {
			next := idx + 1
			for next < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[next])
				if nextTrimmed == "" {
					next++
					continue
				}
				if isPythonSymbolStart(nextTrimmed) && isTopLevelLine(lines[next], lang) {
					start := expandSymbolStart(lines, idx, lang)
					if _, ok := seen[start]; !ok {
						starts = append(starts, start)
						seen[start] = struct{}{}
					}
				}
				break
			}
			continue
		}
		if !lineStartsSymbol(trimmed, lang, rule) || !isTopLevelLine(lines[idx], lang) {
			continue
		}
		start := expandSymbolStart(lines, idx, lang)
		if _, ok := seen[start]; ok {
			continue
		}
		starts = append(starts, start)
		seen[start] = struct{}{}
	}
	sort.Ints(starts)
	return starts
}

func isTopLevelLine(line, lang string) bool {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if trimmedLeft == line {
		return true
	}
	if lang == "python" {
		return len(line)-len(trimmedLeft) == 0
	}
	return false
}

func lineStartsSymbol(trimmed, lang string, rule chunkRule) bool {
	if lang == "python" {
		return isPythonSymbolStart(trimmed)
	}
	for _, prefix := range rule.SplitPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func isPythonSymbolStart(trimmed string) bool {
	return strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "async def ")
}

func expandSymbolStart(lines []string, idx int, lang string) int {
	start := idx
	for start > 0 {
		prev := strings.TrimSpace(lines[start-1])
		if prev == "" {
			start--
			continue
		}
		if isCommentLine(prev, lang) || (lang == "python" && strings.HasPrefix(prev, "@")) {
			start--
			continue
		}
		break
	}
	return start
}

func isCommentLine(trimmed, lang string) bool {
	switch lang {
	case "python", "bash", "yaml", "toml", "ini", "makefile":
		return strings.HasPrefix(trimmed, "#")
	case "go", "javascript", "typescript", "jsx", "rust", "java", "c", "cpp", "csharp", "kotlin", "swift", "php", "scala":
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")
	case "ruby":
		return strings.HasPrefix(trimmed, "#")
	default:
		return strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//")
	}
}

func chunkMarkdownSections(text string, rule chunkRule) []string {
	lines := normalizeLines(text)
	if len(lines) <= rule.MaxLines {
		return nil
	}
	segments := [][]string{}
	preamble := []string{}
	current := []string{}
	headingStack := []string{}
	sectionHeadings := []string{}
	inFence := false

	flush := func() {
		body := trimBlankEdges(current)
		if len(body) == 0 {
			current = nil
			return
		}
		segment := append([]string{}, sectionHeadings...)
		if len(segment) > 0 && len(body) > 0 {
			segment = append(segment, "")
		}
		segment = append(segment, body...)
		segments = append(segments, segment)
		current = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(trimmed, "#") {
			flush()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			heading := strings.TrimSpace(trimmed)
			if level <= 0 {
				continue
			}
			if level > len(headingStack)+1 {
				level = len(headingStack) + 1
			}
			if level-1 < len(headingStack) {
				headingStack = append([]string{}, headingStack[:level-1]...)
			}
			headingStack = append(headingStack, heading)
			sectionHeadings = append([]string{}, headingStack...)
			continue
		}
		if len(sectionHeadings) == 0 {
			preamble = append(preamble, line)
			continue
		}
		current = append(current, line)
	}
	flush()
	if len(segments) == 0 {
		if len(trimBlankEdges(preamble)) == 0 {
			return nil
		}
		return chunkTextDefault(strings.Join(trimBlankEdges(preamble), "\n"), rule)
	}
	if len(trimBlankEdges(preamble)) > 0 {
		segments[0] = append(append([]string{}, trimBlankEdges(preamble)...), append([]string{""}, segments[0]...)...)
	}
	return mergeSmallSegments(splitOversizedSegments(joinNonEmptySegments(segments), rule), rule)
}

type markdownSection struct {
	level    int
	headings []string
	body     []string
}

type markdownGroup struct {
	root      markdownSection
	children  []markdownSection
	rootIntro []string
}

func chunkMarkdownSectionsWithProfile(text string, rule chunkRule, profile string) []string {
	lines := normalizeLines(text)
	if len(lines) <= rule.MaxLines {
		return nil
	}
	sections := collectMarkdownSections(lines)
	if len(sections) == 0 {
		return nil
	}
	switch profile {
	case snapshotChunkProfileMarkdownStrict:
		return chunkMarkdownStrictSections(sections, rule)
	case snapshotChunkProfileMarkdownH2:
		return chunkMarkdownH2Sections(sections, rule, false)
	case snapshotChunkProfileMarkdownH2Win:
		return chunkMarkdownH2Sections(sections, rule, true)
	default:
		return nil
	}
}

func collectMarkdownSections(lines []string) []markdownSection {
	sections := []markdownSection{}
	headingStack := []string{}
	preamble := []string{}
	inFence := false
	var current *markdownSection

	flush := func() {
		if current == nil {
			return
		}
		body := trimBlankEdges(current.body)
		if len(body) == 0 && len(current.headings) == 0 {
			current = nil
			return
		}
		sections = append(sections, markdownSection{
			level:    current.level,
			headings: append([]string{}, current.headings...),
			body:     append([]string{}, body...),
		})
		current = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level > 0 {
				flush()
				if level > len(headingStack)+1 {
					level = len(headingStack) + 1
				}
				if level-1 < len(headingStack) {
					headingStack = append([]string{}, headingStack[:level-1]...)
				}
				headingStack = append(headingStack, trimmed)
				current = &markdownSection{
					level:    level,
					headings: append([]string{}, headingStack...),
				}
				continue
			}
		}
		if current == nil {
			preamble = append(preamble, line)
			continue
		}
		current.body = append(current.body, line)
	}
	flush()
	if len(trimBlankEdges(preamble)) > 0 {
		sections = append([]markdownSection{{
			level:    0,
			headings: nil,
			body:     trimBlankEdges(preamble),
		}}, sections...)
	}
	return sections
}

func renderMarkdownSection(headings []string, body []string) []string {
	segment := []string{}
	if len(headings) > 0 {
		segment = append(segment, headings...)
	}
	body = trimBlankEdges(body)
	if len(segment) > 0 && len(body) > 0 {
		segment = append(segment, "")
	}
	segment = append(segment, body...)
	return trimBlankEdges(segment)
}

func chunkMarkdownStrictSections(sections []markdownSection, rule chunkRule) []string {
	segments := make([][]string, 0, len(sections))
	for _, section := range sections {
		segment := renderMarkdownSection(section.headings, section.body)
		if len(segment) == 0 {
			continue
		}
		segments = append(segments, segment)
	}
	return splitOversizedSegments(joinNonEmptySegments(segments), rule)
}

func chunkMarkdownH2Sections(sections []markdownSection, rule chunkRule, includeIntroWindow bool) []string {
	segments := [][]string{}
	groups := []markdownGroup{}
	var currentGroup *markdownGroup

	flushGroup := func() {
		if currentGroup == nil {
			return
		}
		groups = append(groups, *currentGroup)
		currentGroup = nil
	}

	for _, section := range sections {
		switch {
		case section.level == 0:
			flushGroup()
			if segment := renderMarkdownSection(nil, section.body); len(segment) > 0 {
				segments = append(segments, segment)
			}
		case section.level == 1:
			flushGroup()
			if len(trimBlankEdges(section.body)) > 0 {
				segments = append(segments, renderMarkdownSection(section.headings, section.body))
			}
		case section.level == 2:
			flushGroup()
			currentGroup = &markdownGroup{
				root:      section,
				rootIntro: trimBlankEdges(section.body),
			}
		default:
			if currentGroup == nil {
				segments = append(segments, renderMarkdownSection(section.headings, section.body))
				continue
			}
			currentGroup.children = append(currentGroup.children, section)
		}
	}
	flushGroup()

	for _, group := range groups {
		groupSegments := renderMarkdownGroup(group, rule, includeIntroWindow)
		for _, segment := range groupSegments {
			if len(segment) > 0 {
				segments = append(segments, segment)
			}
		}
	}

	return splitOversizedSegments(joinNonEmptySegments(segments), rule)
}

func renderMarkdownGroup(group markdownGroup, rule chunkRule, includeIntroWindow bool) [][]string {
	renderCombined := func() []string {
		body := append([]string{}, trimBlankEdges(group.root.body)...)
		rootDepth := len(group.root.headings)
		for _, child := range group.children {
			relativeHeadings := child.headings
			if len(relativeHeadings) > rootDepth {
				relativeHeadings = relativeHeadings[rootDepth:]
			}
			childSegment := renderMarkdownSection(relativeHeadings, child.body)
			if len(childSegment) == 0 {
				continue
			}
			if len(body) > 0 {
				body = append(body, "")
			}
			body = append(body, childSegment...)
		}
		return renderMarkdownSection(group.root.headings, body)
	}

	combined := renderCombined()
	if len(strings.Split(strings.Join(combined, "\n"), "\n")) <= rule.MaxLines {
		return [][]string{combined}
	}
	if len(group.children) == 0 {
		return [][]string{combined}
	}

	segments := [][]string{}
	if len(group.rootIntro) > 0 && !includeIntroWindow {
		segments = append(segments, renderMarkdownSection(group.root.headings, group.rootIntro))
	}
	rootDepth := len(group.root.headings)
	for _, child := range group.children {
		relativeHeadings := child.headings
		if len(relativeHeadings) > rootDepth {
			relativeHeadings = relativeHeadings[rootDepth:]
		}
		body := append([]string{}, trimBlankEdges(child.body)...)
		if includeIntroWindow && len(group.rootIntro) > 0 {
			body = append(append([]string{}, group.rootIntro...), append([]string{""}, body...)...)
		}
		segment := renderMarkdownSection(append(append([]string{}, group.root.headings...), relativeHeadings...), body)
		segments = append(segments, segment)
	}
	return segments
}

func chunkMarkdownSignalStress(text string, rule chunkRule) []string {
	lines := normalizeLines(text)
	sections := collectMarkdownSections(lines)
	if len(sections) == 0 {
		return nil
	}
	segments := [][]string{}
	seen := map[string]struct{}{}
	addSegment := func(segment []string) {
		segment = trimBlankEdges(segment)
		if len(segment) == 0 {
			return
		}
		key := strings.Join(segment, "\n")
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		segments = append(segments, segment)
	}

	for _, section := range sections {
		base := renderMarkdownSection(section.headings, section.body)
		if len(base) == 0 {
			continue
		}
		signalRanges := markdownSignalRanges(section.body)
		if len(signalRanges) == 0 {
			addSegment(base)
			continue
		}
		intro := markdownSectionIntro(section.body, 8)
		for _, signalRange := range signalRanges {
			start, end := signalRange[0], signalRange[1]
			if start < 0 {
				start = 0
			}
			if end > len(section.body) {
				end = len(section.body)
			}
			body := append([]string{}, intro...)
			window := trimBlankEdges(section.body[start:end])
			if len(body) > 0 && len(window) > 0 {
				body = append(body, "")
			}
			body = append(body, window...)
			addSegment(renderMarkdownSection(section.headings, body))
		}
	}

	if len(segments) == 0 {
		return nil
	}
	return splitOversizedSegments(joinNonEmptySegments(segments), rule)
}

func markdownSectionIntro(body []string, maxLines int) []string {
	intro := []string{}
	for _, line := range trimBlankEdges(body) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(intro) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			break
		}
		intro = append(intro, line)
		if len(intro) >= maxLines {
			break
		}
	}
	return trimBlankEdges(intro)
}

func markdownSignalRanges(body []string) [][2]int {
	ranges := [][2]int{}
	inFence := false
	fenceStart := -1
	signalLines := map[int]struct{}{}
	for idx, line := range body {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				fenceStart = idx
			} else {
				inFence = false
				if fenceStart >= 0 {
					ranges = append(ranges, [2]int{maxInt(0, fenceStart-3), minInt(len(body), idx+4)})
				}
				fenceStart = -1
			}
			continue
		}
		if isSignalText(trimmed) {
			signalLines[idx] = struct{}{}
		}
	}
	for idx := range signalLines {
		ranges = append(ranges, [2]int{maxInt(0, idx-4), minInt(len(body), idx+5)})
	}
	return mergeRanges(ranges)
}

func chunkStructuredSignalStress(text, lang, path string, rule chunkRule) []string {
	lines := normalizeLines(text)
	ranges := [][2]int{}
	sectionStarts := structuredSectionStarts(lines, lang, path)
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !isSignalText(trimmed) {
			continue
		}
		start := maxInt(0, idx-3)
		for _, sectionStart := range sectionStarts {
			if sectionStart <= idx {
				start = maxInt(start, sectionStart)
			}
		}
		ranges = append(ranges, [2]int{start, minInt(len(lines), idx+5)})
	}
	if len(ranges) == 0 {
		return nil
	}
	ranges = mergeRanges(ranges)
	segments := [][]string{}
	seen := map[string]struct{}{}
	for _, window := range ranges {
		segment := trimBlankEdges(lines[window[0]:window[1]])
		if len(segment) == 0 {
			continue
		}
		key := strings.Join(segment, "\n")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return nil
	}
	return splitOversizedSegments(joinNonEmptySegments(segments), rule)
}

func mergeRanges(ranges [][2]int) [][2]int {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i][0] == ranges[j][0] {
			return ranges[i][1] < ranges[j][1]
		}
		return ranges[i][0] < ranges[j][0]
	})
	merged := [][2]int{ranges[0]}
	for _, current := range ranges[1:] {
		last := &merged[len(merged)-1]
		if current[0] <= last[1] {
			if current[1] > last[1] {
				last[1] = current[1]
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func isSignalText(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, token := range []string{
		"mailer",
		"email",
		"smtp",
		"backend",
		"provider",
		"license",
		"license-file",
		"copying",
		"notice",
		"notification",
		"delivery",
		"api",
		"route",
		"endpoint",
		"config",
		"settings",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	if strings.Contains(trimmed, "`") && (strings.Contains(trimmed, "=") || strings.Contains(trimmed, "/") || strings.Contains(trimmed, ".")) {
		return true
	}
	return hasEnvLikeToken(trimmed)
}

func hasEnvLikeToken(line string) bool {
	tokens := strings.FieldsFunc(line, func(r rune) bool {
		switch r {
		case ' ', '\t', ',', ':', ';', '(', ')', '[', ']', '{', '}', '`', '"', '\'':
			return true
		default:
			return false
		}
	})
	for _, token := range tokens {
		if len(token) < 4 || !strings.Contains(token, "_") {
			continue
		}
		allCaps := true
		hasLetter := false
		for _, r := range token {
			switch {
			case r >= 'A' && r <= 'Z':
				hasLetter = true
			case r >= '0' && r <= '9':
			case r == '_':
			default:
				allCaps = false
			}
			if !allCaps {
				break
			}
		}
		if allCaps && hasLetter {
			return true
		}
	}
	return false
}

func chunkStructuredSections(text, lang, path string, rule chunkRule) []string {
	lines := normalizeLines(text)
	if len(lines) <= rule.MaxLines {
		return nil
	}
	starts := structuredSectionStarts(lines, lang, path)
	if len(starts) == 0 {
		return nil
	}
	segments := [][]string{}
	preamble := trimBlankEdges(lines[:starts[0]])
	if len(preamble) > 0 {
		segments = append(segments, preamble)
	}
	for i, start := range starts {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		segment := trimBlankEdges(lines[start:end])
		if len(segment) > 0 {
			segments = append(segments, segment)
		}
	}
	return mergeSmallSegments(splitOversizedSegments(joinNonEmptySegments(segments), rule), rule)
}

func structuredSectionStarts(lines []string, lang, path string) []int {
	starts := []int{}
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case isMarkdownLike(lang, path):
			if isLegalHeadingLine(trimmed) {
				starts = append(starts, idx)
			}
		case lang == "toml" || lang == "ini" || lang == "hcl":
			if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "resource ") || strings.HasPrefix(trimmed, "module ") || strings.HasPrefix(trimmed, "variable ") || strings.HasPrefix(trimmed, "output ") || strings.HasPrefix(trimmed, "provider ") {
				starts = append(starts, idx)
			}
		case lang == "yaml":
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "- ") {
				starts = append(starts, idx)
			}
		case lang == "json":
			if strings.HasPrefix(trimmed, "\"") && strings.Contains(trimmed, "\":") {
				starts = append(starts, idx)
			}
		case lang == "sql":
			if strings.HasPrefix(trimmed, "CREATE ") || strings.HasPrefix(trimmed, "ALTER ") || strings.HasPrefix(trimmed, "INSERT ") || strings.HasPrefix(trimmed, "UPDATE ") || strings.HasPrefix(trimmed, "DELETE ") || strings.HasPrefix(trimmed, "WITH ") || strings.HasPrefix(trimmed, "SELECT ") {
				starts = append(starts, idx)
			}
		case lang == "proto":
			if strings.HasPrefix(trimmed, "message ") || strings.HasPrefix(trimmed, "service ") || strings.HasPrefix(trimmed, "enum ") {
				starts = append(starts, idx)
			}
		case lang == "graphql":
			if strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "input ") || strings.HasPrefix(trimmed, "enum ") || strings.HasPrefix(trimmed, "interface ") || strings.HasPrefix(trimmed, "schema ") {
				starts = append(starts, idx)
			}
		}
	}
	return starts
}

func isLegalHeadingLine(trimmed string) bool {
	if trimmed == strings.ToUpper(trimmed) && len(trimmed) <= 120 {
		return true
	}
	if strings.HasPrefix(trimmed, "Section ") || strings.HasPrefix(trimmed, "SECTION ") {
		return true
	}
	if strings.HasPrefix(trimmed, "Article ") || strings.HasPrefix(trimmed, "ARTICLE ") {
		return true
	}
	if len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed, ".") {
		return true
	}
	return false
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func fitChunk(header, chunk, lang string, maxLines int, remaining int) (string, bool) {
	openFence := "```\n"
	if lang != "" {
		openFence = "```" + lang + "\n"
	}
	closeFence := "\n```\n\n"
	overhead := len(header) + 1 + len(openFence) + len(closeFence)
	unlimited := remaining <= 0
	if !unlimited && remaining <= overhead+20 {
		return "", false
	}
	maxContent := remaining - overhead
	text := chunk
	if !unlimited && len(text) > maxContent {
		text = trimContext(text, maxLines, maxContent-20)
		text = strings.TrimRight(text, "\n") + "\n... [truncated]"
	}
	payload := header + "\n" + openFence + text + closeFence
	if !unlimited && len(payload) > remaining {
		return "", false
	}
	return payload, true
}

func remainingSnapshotBudget(maxTotal, used int) int {
	if maxTotal <= 0 {
		return 0
	}
	return maxTotal - used
}

func describeSnapshotLimitBytes(limit int) string {
	if limit <= 0 {
		return "full repo (no cap)"
	}
	return formatBytes(int64(limit))
}

func priorityForFile(path string) int {
	normalizedPath := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	if base == "" {
		return 3
	}
	licenseNames := map[string]struct{}{
		"license":     {},
		"license.md":  {},
		"license.txt": {},
	}
	if _, ok := licenseNames[base]; ok {
		return 5
	}
	docNames := map[string]struct{}{
		"readme":          {},
		"readme.md":       {},
		"readme.txt":      {},
		"changelog.md":    {},
		"security.md":     {},
		"contributing.md": {},
		"architecture.md": {},
	}
	if _, ok := docNames[base]; ok || strings.Contains(normalizedPath, "/docs/") {
		return 4
	}
	manifestPaths := map[string]struct{}{
		"go.mod":            {},
		"go.sum":            {},
		"package.json":      {},
		"pyproject.toml":    {},
		"requirements.txt":  {},
		"cargo.toml":        {},
		"cargo.lock":        {},
		"gemfile":           {},
		"pom.xml":           {},
		"build.gradle":      {},
		"composer.json":     {},
		"composer.lock":     {},
		"package-lock.json": {},
		"yarn.lock":         {},
		"pnpm-lock.yaml":    {},
	}
	if _, ok := manifestPaths[base]; ok {
		return 2
	}
	if strings.Contains(normalizedPath, "/api/") ||
		strings.Contains(normalizedPath, "/auth/") ||
		strings.Contains(normalizedPath, "/router") ||
		strings.Contains(normalizedPath, "/routes") ||
		strings.Contains(normalizedPath, "/server/") ||
		strings.Contains(normalizedPath, "/settings") ||
		strings.Contains(normalizedPath, "/config") ||
		strings.Contains(normalizedPath, "/billing") ||
		strings.Contains(normalizedPath, "/notification") ||
		strings.Contains(normalizedPath, "/sync") ||
		strings.Contains(normalizedPath, "/main.") ||
		strings.Contains(normalizedPath, "/app.") {
		return 0
	}
	lang, _ := chunkRuleForFile(path)
	if lang != "" && lang != "markdown" {
		return 1
	}
	return 3
}

func normalizeGlobs(globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	out := make([]string, 0, len(globs))
	for _, g := range globs {
		trimmed := strings.TrimSpace(g)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func matchesAnyGlob(path string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
	}
	return false
}

func languageForFile(path string) string {
	lang, _ := chunkRuleForFile(path)
	if lang != "" {
		return lang
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "txt":
		return "text"
	case "xml":
		return "xml"
	}
	return "other"
}
