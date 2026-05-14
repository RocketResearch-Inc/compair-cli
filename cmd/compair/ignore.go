package compair

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

var ignoreAll bool
var ignoreJSON bool
var ignoreWrite bool
var ignoreIncludeReview bool
var ignoreMinLargeFileBytes int
var ignoreMinDirFiles int
var ignoreMinDirBytes int

type ignoreSuggestOptions struct {
	MinLargeFileBytes int
	MinDirFiles       int
	MinDirBytes       int
	IncludeReview     bool
}

type ignoreSuggestion struct {
	Pattern      string   `json:"pattern"`
	Confidence   string   `json:"confidence"`
	Reason       string   `json:"reason"`
	MatchedFiles int      `json:"matched_files"`
	Bytes        int64    `json:"bytes"`
	Examples     []string `json:"examples,omitempty"`
}

type ignoreSuggestReport struct {
	Root             string             `json:"root"`
	Remote           string             `json:"remote,omitempty"`
	TotalFiles       int                `json:"total_files"`
	IncludedFiles    int                `json:"included_files"`
	IncludedBytes    int64              `json:"included_bytes"`
	ExistingPatterns []string           `json:"existing_patterns,omitempty"`
	Suggestions      []ignoreSuggestion `json:"suggestions"`
	WrittenPatterns  []string           `json:"written_patterns,omitempty"`
}

type ignoreDirStats struct {
	pattern     string
	reason      string
	files       int
	bytes       int64
	markerFiles int
	examples    []string
}

var ignoreCmd = &cobra.Command{
	Use:   "ignore",
	Short: "Suggest and manage repo-local .compairignore files",
}

var ignoreSuggestCmd = &cobra.Command{
	Use:   "suggest [PATH ...]",
	Short: "Suggest .compairignore patterns for large or generated repo surfaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		groupID := ""
		var err error
		if ignoreAll {
			groupID, err = config.ResolveActiveGroup(viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		roots, err := collectRepoRoots(args, groupID, ignoreAll)
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			printer.Info("No repositories found.")
			return nil
		}

		opts := ignoreSuggestOptions{
			MinLargeFileBytes: ignoreMinLargeFileBytes,
			MinDirFiles:       ignoreMinDirFiles,
			MinDirBytes:       ignoreMinDirBytes,
			IncludeReview:     ignoreIncludeReview,
		}
		ids := make([]string, 0, len(roots))
		for root := range roots {
			ids = append(ids, root)
		}
		sort.Strings(ids)

		reports := make([]ignoreSuggestReport, 0, len(ids))
		for _, root := range ids {
			report, err := suggestCompairIgnore(root, opts)
			if err != nil {
				printer.Warn(fmt.Sprintf("Ignore suggestion failed for %s: %v", root, err))
				continue
			}
			if ignoreWrite {
				written, err := writeCompairIgnoreSuggestions(root, report.Suggestions, ignoreIncludeReview)
				if err != nil {
					return err
				}
				report.WrittenPatterns = written
			}
			reports = append(reports, report)
		}

		if ignoreJSON {
			out, _ := json.MarshalIndent(reports, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		for idx, report := range reports {
			if idx > 0 {
				fmt.Println()
			}
			renderIgnoreSuggestReport(report)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(ignoreCmd)
	ignoreCmd.AddCommand(ignoreSuggestCmd)
	ignoreSuggestCmd.Flags().BoolVar(&ignoreAll, "all", false, "Suggest ignores for all tracked repos in the active group")
	ignoreSuggestCmd.Flags().BoolVar(&ignoreJSON, "json", false, "Output JSON")
	ignoreSuggestCmd.Flags().BoolVar(&ignoreWrite, "write", false, "Append high-confidence suggestions to .compairignore")
	ignoreSuggestCmd.Flags().BoolVar(&ignoreIncludeReview, "include-review", false, "Include lower-confidence review suggestions in output and --write")
	ignoreSuggestCmd.Flags().IntVar(&ignoreMinLargeFileBytes, "min-large-file-bytes", 250000, "Suggest individual review candidates at or above this size")
	ignoreSuggestCmd.Flags().IntVar(&ignoreMinDirFiles, "min-dir-files", 25, "Suggest generated-looking directories at or above this tracked file count")
	ignoreSuggestCmd.Flags().IntVar(&ignoreMinDirBytes, "min-dir-bytes", 250000, "Suggest generated-looking directories at or above this total byte size")
}

func suggestCompairIgnore(root string, opts ignoreSuggestOptions) (ignoreSuggestReport, error) {
	files, err := listTrackedFiles(root)
	if err != nil {
		return ignoreSuggestReport{}, err
	}
	sort.Strings(files)

	existingPatterns, _ := readCompairIgnorePatterns(root)
	existingSet := map[string]struct{}{}
	for _, pattern := range existingPatterns {
		existingSet[pattern] = struct{}{}
	}

	ig := fsutil.LoadIgnore(root)
	suggestions := map[string]*ignoreSuggestion{}
	generatedFilePatterns := map[string]*ignoreSuggestion{}
	dirs := map[string]*ignoreDirStats{}

	report := ignoreSuggestReport{
		Root:             root,
		Remote:           loadRepoConfig(root).Remote,
		TotalFiles:       len(files),
		ExistingPatterns: existingPatterns,
	}

	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		if ig.ShouldIgnore(rel, false) {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			continue
		}
		size := info.Size()
		report.IncludedFiles++
		report.IncludedBytes += size

		if pattern, reason := lockfileIgnorePattern(rel); pattern != "" {
			addIgnoreSuggestion(suggestions, existingSet, pattern, "high", reason, rel, size)
		}
		if pattern, reason := generatedFileIgnorePattern(rel); pattern != "" {
			addIgnoreSuggestion(generatedFilePatterns, existingSet, pattern, "high", reason, rel, size)
		}
		dirPattern, dirReason := generatedDirectoryIgnorePattern(rel)
		if dirPattern != "" {
			stat := dirs[dirPattern]
			if stat == nil {
				stat = &ignoreDirStats{pattern: dirPattern, reason: dirReason}
				dirs[dirPattern] = stat
			}
			stat.files++
			stat.bytes += size
			if len(stat.examples) < 3 {
				stat.examples = append(stat.examples, rel)
			}
			if fileHasGeneratedMarker(full) {
				stat.markerFiles++
			}
		}
		if dirPattern == "" && opts.MinLargeFileBytes > 0 && size >= int64(opts.MinLargeFileBytes) && priorityForFile(rel) > 2 && looksLikeTextFile(full) {
			confidence := "review"
			reason := "Large tracked text-ish file; consider ignoring if it is generated, fixture, or reference output."
			if fileHasGeneratedMarker(full) {
				confidence = "high"
				reason = "Large tracked file appears generated; excluding it can reduce snapshot cost and reference noise."
			}
			addIgnoreSuggestion(suggestions, existingSet, rel, confidence, reason, rel, size)
		}
	}

	for pattern, suggestion := range generatedFilePatterns {
		if suggestion.MatchedFiles >= 3 || suggestion.Bytes >= 100000 {
			suggestions[pattern] = suggestion
		}
	}
	for pattern, stat := range dirs {
		if stat.files < maxInt(1, opts.MinDirFiles) && stat.bytes < int64(maxInt(1, opts.MinDirBytes)) {
			continue
		}
		confidence := "review"
		reason := stat.reason
		if stat.markerFiles > 0 || isHighConfidenceGeneratedDir(pattern) {
			confidence = "high"
			reason = reason + " Generated markers or path conventions suggest this is safe to ignore after review."
		}
		addIgnoreSuggestion(suggestions, existingSet, pattern, confidence, reason, "", stat.bytes)
		if s := suggestions[pattern]; s != nil {
			s.MatchedFiles = stat.files
			s.Bytes = stat.bytes
			s.Examples = append([]string{}, stat.examples...)
		}
	}

	report.Suggestions = flattenIgnoreSuggestions(suggestions, opts.IncludeReview)
	return report, nil
}

func renderIgnoreSuggestReport(report ignoreSuggestReport) {
	name := report.Remote
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(report.Root)
	}
	fmt.Println("Repo:", name)
	fmt.Println("Root:", report.Root)
	fmt.Printf("Included after current ignores: %d tracked files, %s\n", report.IncludedFiles, formatBytes(report.IncludedBytes))
	if len(report.Suggestions) == 0 {
		printer.Success("No obvious .compairignore additions found.")
		return
	}
	fmt.Println("Suggested .compairignore patterns:")
	for _, s := range report.Suggestions {
		fmt.Printf("  [%s] %s (%d files, %s)\n", s.Confidence, s.Pattern, s.MatchedFiles, formatBytes(s.Bytes))
		fmt.Printf("       %s\n", s.Reason)
		for _, ex := range s.Examples {
			fmt.Printf("       e.g. %s\n", ex)
		}
	}
	if len(report.WrittenPatterns) > 0 {
		fmt.Println("Written to .compairignore:")
		for _, pattern := range report.WrittenPatterns {
			fmt.Printf("  %s\n", pattern)
		}
	} else {
		fmt.Println("To append high-confidence suggestions, rerun with --write.")
		fmt.Println("Use --include-review with --write only after checking lower-confidence entries.")
	}
}

func readCompairIgnorePatterns(root string) ([]string, error) {
	f, err := os.Open(filepath.Join(root, ".compairignore"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, filepath.ToSlash(line))
	}
	return patterns, scanner.Err()
}

func writeCompairIgnoreSuggestions(root string, suggestions []ignoreSuggestion, includeReview bool) ([]string, error) {
	var writable []ignoreSuggestion
	for _, s := range suggestions {
		if s.Confidence == "high" || includeReview {
			writable = append(writable, s)
		}
	}
	if len(writable) == 0 {
		return nil, nil
	}
	path := filepath.Join(root, ".compairignore")
	existing, _ := os.ReadFile(path)
	existingPatterns, _ := readCompairIgnorePatterns(root)
	existingSet := map[string]struct{}{}
	for _, pattern := range existingPatterns {
		existingSet[pattern] = struct{}{}
	}

	var builder strings.Builder
	builder.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		builder.WriteString("\n")
	}
	if !strings.Contains(string(existing), "Suggested by compair ignore suggest") {
		if len(existing) > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("# Suggested by compair ignore suggest\n")
	}
	var written []string
	for _, s := range writable {
		if _, ok := existingSet[s.Pattern]; ok {
			continue
		}
		builder.WriteString("# ")
		builder.WriteString(s.Reason)
		builder.WriteString("\n")
		builder.WriteString(s.Pattern)
		builder.WriteString("\n")
		existingSet[s.Pattern] = struct{}{}
		written = append(written, s.Pattern)
	}
	if len(written) == 0 {
		return nil, nil
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return nil, err
	}
	return written, nil
}

func addIgnoreSuggestion(target map[string]*ignoreSuggestion, existing map[string]struct{}, pattern, confidence, reason, rel string, size int64) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return
	}
	if _, ok := existing[pattern]; ok {
		return
	}
	s := target[pattern]
	if s == nil {
		s = &ignoreSuggestion{
			Pattern:    pattern,
			Confidence: confidence,
			Reason:     reason,
		}
		target[pattern] = s
	}
	if confidenceRank(confidence) < confidenceRank(s.Confidence) {
		s.Confidence = confidence
	}
	s.MatchedFiles++
	s.Bytes += size
	if rel != "" && len(s.Examples) < 3 {
		s.Examples = append(s.Examples, rel)
	}
}

func flattenIgnoreSuggestions(suggestions map[string]*ignoreSuggestion, includeReview bool) []ignoreSuggestion {
	out := make([]ignoreSuggestion, 0, len(suggestions))
	for _, s := range suggestions {
		if s == nil {
			continue
		}
		if s.Confidence != "high" && !includeReview {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if confidenceRank(out[i].Confidence) != confidenceRank(out[j].Confidence) {
			return confidenceRank(out[i].Confidence) < confidenceRank(out[j].Confidence)
		}
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

func confidenceRank(confidence string) int {
	if confidence == "high" {
		return 0
	}
	return 1
}

func lockfileIgnorePattern(path string) (string, string) {
	base := strings.ToLower(filepath.Base(path))
	lockfiles := map[string]string{
		"go.sum":            "Go checksum files are often large dependency metadata; go.mod remains available for dependency drift.",
		"package-lock.json": "Package lockfiles are usually generated dependency metadata and can dominate snapshots.",
		"yarn.lock":         "Package lockfiles are usually generated dependency metadata and can dominate snapshots.",
		"pnpm-lock.yaml":    "Package lockfiles are usually generated dependency metadata and can dominate snapshots.",
		"poetry.lock":       "Python lockfiles are generated dependency metadata and are often low-signal for cross-repo review.",
		"pipfile.lock":      "Python lockfiles are generated dependency metadata and are often low-signal for cross-repo review.",
		"gemfile.lock":      "Ruby lockfiles are generated dependency metadata and are often low-signal for cross-repo review.",
		"composer.lock":     "PHP lockfiles are generated dependency metadata and are often low-signal for cross-repo review.",
	}
	if reason, ok := lockfiles[base]; ok {
		return base, reason
	}
	return "", ""
}

func generatedFileIgnorePattern(path string) (string, string) {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(base, ".pb.go"):
		return "*.pb.go", "Generated protobuf Go files are usually redundant with schema/source definitions."
	case strings.HasSuffix(base, ".pb.gw.go"):
		return "*.pb.gw.go", "Generated protobuf gateway files are usually redundant with schema/source definitions."
	case strings.HasSuffix(base, ".gen.go"):
		return "*.gen.go", "Generated Go files are usually redundant with source templates or schemas."
	case strings.HasSuffix(base, ".generated.go"):
		return "*.generated.go", "Generated Go files are usually redundant with source templates or schemas."
	case strings.HasSuffix(base, "_generated.go"):
		return "*_generated.go", "Generated Go files are usually redundant with source templates or schemas."
	case strings.HasSuffix(base, ".tsbuildinfo"):
		return "*.tsbuildinfo", "TypeScript build-info files are generated compiler metadata."
	}
	return "", ""
}

func generatedDirectoryIgnorePattern(path string) (string, string) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts[:maxInt(0, len(parts)-1)] {
		lower := strings.ToLower(part)
		if isGeneratedDirName(lower) {
			return strings.Join(parts[:i+1], "/") + "/", "Generated/cache directory detected by path name."
		}
		if lower == "sdk" && i+1 < len(parts)-1 && strings.ToLower(parts[i+1]) == "docs" {
			return strings.Join(parts[:i+2], "/") + "/", "SDK documentation trees are often generated and can duplicate SDK source surfaces."
		}
		if lower == "docs" && i+1 < len(parts)-1 {
			next := strings.ToLower(parts[i+1])
			if next == "models" || next == "sdks" {
				return strings.Join(parts[:i+2], "/") + "/", "Generated SDK/API docs tree detected by path convention."
			}
		}
	}
	return "", ""
}

func isGeneratedDirName(name string) bool {
	switch name {
	case ".cache", ".next", ".nuxt", ".turbo", "__generated__", "__snapshots__", "coverage", "generated", "generated-sources", "gen", "out", "storybook-static":
		return true
	default:
		return false
	}
}

func isHighConfidenceGeneratedDir(pattern string) bool {
	lower := strings.ToLower(pattern)
	return strings.Contains(lower, "__generated__") ||
		strings.Contains(lower, "__snapshots__") ||
		strings.Contains(lower, "/generated/") ||
		strings.Contains(lower, "/generated-sources/") ||
		strings.HasSuffix(lower, "/generated/") ||
		strings.Contains(lower, "/coverage/") ||
		strings.HasSuffix(lower, "/coverage/")
}

func fileHasGeneratedMarker(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 16384)
	n, _ := f.Read(buf)
	text := strings.ToLower(string(buf[:n]))
	markers := []string{
		"automatically generated",
		"code generated",
		"do not edit",
		"generated by speakeasy",
		"generated code",
		"this file was generated",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
