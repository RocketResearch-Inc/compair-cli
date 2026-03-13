package compair

import (
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
	Text  string
	Head  string
	Stats snapshotStats
}

type snapshotFile struct {
	Path     string
	Size     int64
	Priority int
}

var errNonText = errors.New("non-text file")

func buildRepoSnapshot(root, groupID string, repo *config.Repo, opts snapshotOptions) (snapshotResult, error) {
	files, err := listTrackedFiles(root)
	if err != nil {
		return snapshotResult{}, err
	}
	sort.Strings(files)
	ig := fsutil.LoadIgnore(root)

	var tree []string
	var candidates []snapshotFile
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
		return snapshotResult{
			Text: builder.String(),
			Head: head,
			Stats: snapshotStats{
				TotalFiles:    totalFiles,
				TotalBytes:    totalBytes,
				TreeEntries:   len(tree),
				IgnoredFiles:  ignored,
				NonTextFiles:  nonText,
				TooLargeFiles: tooLarge,
				BudgetBytes:   opts.MaxTotalBytes,
				IncludedBytes: includedBytes,
			},
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
		chunks := chunkText(content, rule)
		if len(chunks) == 0 {
			omittedFiles++
			continue
		}
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
	return snapshotResult{
		Text: builder.String(),
		Head: head,
		Stats: snapshotStats{
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
		},
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

func chunkText(text string, rule chunkRule) []string {
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
