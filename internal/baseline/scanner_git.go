package baseline

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errGitOutputLimit = errors.New("Git output exceeded its bounded read")

func (scanner *Scanner) validateRepositoryRoots(ctx context.Context, changed ChangedRepositoryInput, siblings []SiblingRepositoryInput) error {
	seen := make(map[string]struct{}, len(siblings)+1)
	changedRoot, err := scanner.canonicalRepositoryRoot(ctx, changed.LocalPath)
	if err != nil {
		return err
	}
	seen[changedRoot] = struct{}{}
	for _, sibling := range siblings {
		root, err := scanner.canonicalRepositoryRoot(ctx, sibling.LocalPath)
		if err != nil {
			return err
		}
		if _, duplicate := seen[root]; duplicate {
			return scanError(ScanFailureContract, "changed and sibling repositories must be distinct")
		}
		seen[root] = struct{}{}
	}
	return nil
}

func (scanner *Scanner) canonicalRepositoryRoot(ctx context.Context, localPath string) (string, error) {
	inputRoot, err := filepath.Abs(localPath)
	if err != nil {
		return "", scanError(ScanFailureRepository, "local repository root is unavailable")
	}
	inputRoot, err = filepath.EvalSymlinks(inputRoot)
	if err != nil {
		return "", scanError(ScanFailureRepository, "local repository root is unavailable")
	}
	output, err := scanner.gitOutput(ctx, localPath, maximumGitScalarBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", scanError(ScanFailureRepository, "local repository root is not a readable Git repository")
	}
	gitRoot := strings.TrimSpace(string(output))
	gitRoot, err = filepath.EvalSymlinks(gitRoot)
	if err != nil || filepath.Clean(gitRoot) != filepath.Clean(inputRoot) {
		return "", scanError(ScanFailureContract, "scan input must identify the Git repository root")
	}
	return filepath.Clean(gitRoot), nil
}

func (scanner *Scanner) resolveCommit(ctx context.Context, localPath, revision string) (string, error) {
	isolation, cleanup, err := scanner.isolatedObjectEnvironment(ctx, localPath)
	if err != nil {
		return "", err
	}
	defer cleanup()
	output, err := scanner.gitOutputWithEnvironment(ctx, localPath, maximumGitScalarBytes, isolation, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", scanError(ScanFailureRepository, "an immutable commit revision could not be verified")
	}
	resolved := strings.TrimSpace(string(output))
	if !validGitRevision(resolved) {
		return "", scanError(ScanFailureRepository, "Git returned an invalid commit identity")
	}
	return resolved, nil
}

func (scanner *Scanner) requireAncestor(ctx context.Context, localPath, baseRevision, headRevision string) error {
	isolation, cleanup, err := scanner.isolatedObjectEnvironment(ctx, localPath)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := scanner.gitCommandWithEnvironment(ctx, localPath, isolation, "merge-base", "--is-ancestor", baseRevision, headRevision)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return scanError(ScanFailureRepository, "base revision is not an ancestor of head revision")
	}
	return nil
}

func (scanner *Scanner) rawDiff(ctx context.Context, localPath, baseRevision, headRevision string) ([]byte, error) {
	isolation, cleanup, err := scanner.isolatedObjectEnvironment(ctx, localPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	output, err := scanner.gitOutputWithEnvironment(ctx, localPath, MaxRawDiffBytes, isolation,
		"diff", baseRevision, headRevision, "--no-ext-diff")
	if errors.Is(err, errGitOutputLimit) {
		return nil, scanError(ScanFailureContract, "raw Git diff exceeds the frozen byte limit")
	}
	if err != nil {
		return nil, scanError(ScanFailureRepository, "raw Git diff could not be generated")
	}
	if len(output) == 0 {
		return nil, scanError(ScanFailureContract, "raw Git diff must not be empty")
	}
	if !utf8Bytes(output) {
		return nil, scanError(ScanFailureContract, "raw Git diff is not UTF-8")
	}
	return output, nil
}

func (scanner *Scanner) finalVerify(ctx context.Context, input ScanInput, siblings []SiblingRepositoryInput) error {
	for _, target := range []struct {
		root     string
		revision string
	}{
		{input.Changed.LocalPath, input.BaseRevision},
		{input.Changed.LocalPath, input.HeadRevision},
	} {
		resolved, err := scanner.resolveCommit(ctx, target.root, target.revision)
		if err != nil || resolved != target.revision {
			return scanError(ScanFailureRepository, finalRevisionDriftReason)
		}
	}
	for _, sibling := range siblings {
		resolved, err := scanner.resolveCommit(ctx, sibling.LocalPath, sibling.RepositoryRevision)
		if err != nil || resolved != sibling.RepositoryRevision {
			return scanError(ScanFailureRepository, finalRevisionDriftReason)
		}
	}
	return nil
}

func (scanner *Scanner) gitOutput(ctx context.Context, localPath string, limit int64, args ...string) ([]byte, error) {
	return scanner.gitOutputWithEnvironment(ctx, localPath, limit, nil, args...)
}

func (scanner *Scanner) gitOutputWithEnvironment(ctx context.Context, localPath string, limit int64, environment []string, args ...string) ([]byte, error) {
	cmd := scanner.gitCommandWithEnvironment(ctx, localPath, environment, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, scanError(ScanFailureInternal, "Git output pipe could not be created")
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, scanError(ScanFailureRepository, "Git command could not be started")
	}
	value, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	if int64(len(value)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errGitOutputLimit
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, scanError(ScanFailureRepository, "Git command output was incomplete")
	}
	if err := cmd.Wait(); err != nil {
		return nil, scanError(ScanFailureRepository, "Git command failed")
	}
	return value, nil
}

func (scanner *Scanner) gitCommand(ctx context.Context, localPath string, args ...string) *exec.Cmd {
	return scanner.gitCommandWithEnvironment(ctx, localPath, nil, args...)
}

func (scanner *Scanner) gitCommandWithEnvironment(ctx context.Context, localPath string, environment []string, args ...string) *exec.Cmd {
	commandArgs := []string{"-C", localPath, "--no-replace-objects"}
	commandArgs = append(commandArgs, args...)
	cmd := exec.CommandContext(ctx, scanner.gitBinary, commandArgs...)
	cmd.Env = append(hardenedGitEnvironment(), environment...)
	return cmd
}

func (scanner *Scanner) isolatedObjectEnvironment(ctx context.Context, localPath string) ([]string, func(), error) {
	objectsOutput, err := scanner.gitOutput(ctx, localPath, maximumGitScalarBytes, "rev-parse", "--git-path", "objects")
	if err != nil {
		return nil, func() {}, scanError(ScanFailureRepository, "Git object database could not be resolved")
	}
	objectDirectory := strings.TrimSpace(string(objectsOutput))
	if !filepath.IsAbs(objectDirectory) {
		objectDirectory = filepath.Join(localPath, objectDirectory)
	}
	objectDirectory, err = filepath.Abs(objectDirectory)
	if err != nil {
		return nil, func() {}, scanError(ScanFailureRepository, "Git object database could not be resolved")
	}
	formatOutput, err := scanner.gitOutput(ctx, localPath, maximumGitScalarBytes, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, func() {}, scanError(ScanFailureRepository, "Git object format could not be resolved")
	}
	objectFormat := strings.TrimSpace(string(formatOutput))
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return nil, func() {}, scanError(ScanFailureContract, "Git object format is outside the frozen contract")
	}
	temporaryGitDirectory, err := os.MkdirTemp(scanner.temporaryRoot, "compair-baseline-git-")
	if err != nil {
		return nil, func() {}, scanError(ScanFailureInternal, "isolated Git metadata could not be created")
	}
	cleanup := func() { _ = os.RemoveAll(temporaryGitDirectory) }
	for _, directory := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(temporaryGitDirectory, directory), 0o700); err != nil {
			cleanup()
			return nil, func() {}, scanError(ScanFailureInternal, "isolated Git metadata could not be configured")
		}
	}
	if err := os.WriteFile(filepath.Join(temporaryGitDirectory, "HEAD"), []byte("ref: refs/heads/baseline-isolated\n"), 0o600); err != nil {
		cleanup()
		return nil, func() {}, scanError(ScanFailureInternal, "isolated Git metadata could not be configured")
	}
	repositoryFormat := "0"
	extension := ""
	if objectFormat == "sha256" {
		repositoryFormat = "1"
		extension = "[extensions]\n\tobjectFormat = sha256\n"
	}
	config := "[core]\n\trepositoryFormatVersion = " + repositoryFormat + "\n\tbare = true\n\tattributesFile = " + os.DevNull + "\n" + extension
	if err := os.WriteFile(filepath.Join(temporaryGitDirectory, "config"), []byte(config), 0o600); err != nil {
		cleanup()
		return nil, func() {}, scanError(ScanFailureInternal, "isolated Git metadata could not be configured")
	}
	return []string{
		"GIT_DIR=" + temporaryGitDirectory,
		"GIT_OBJECT_DIRECTORY=" + objectDirectory,
		"GIT_ATTR_NOSYSTEM=1",
	}, cleanup, nil
}

func hardenedGitEnvironment() []string {
	values := []string{
		"LC_ALL=C",
		"LANG=C",
		"TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"GIT_CONFIG_COUNT=4",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=core.hooksPath",
		"GIT_CONFIG_VALUE_1=" + os.DevNull,
		"GIT_CONFIG_KEY_2=protocol.allow",
		"GIT_CONFIG_VALUE_2=never",
		"GIT_CONFIG_KEY_3=protocol.file.allow",
		"GIT_CONFIG_VALUE_3=never",
	}
	if runtime.GOOS == "windows" {
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			values = append(values, "SystemRoot="+systemRoot)
		}
	}
	return values
}

func (scanner *Scanner) readBlobMetadata(ctx context.Context, localPath string, environment []string, entries []*rawTreeEntry) error {
	blobCount := 0
	for _, entry := range entries {
		if entry.objectType == "blob" {
			blobCount++
		}
	}
	if blobCount == 0 {
		return nil
	}

	cmd := scanner.gitCommandWithEnvironment(ctx, localPath, environment, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return scanError(ScanFailureInternal, "Git object input pipe could not be created")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return scanError(ScanFailureInternal, "Git object output pipe could not be created")
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return scanError(ScanFailureRepository, "Git object reader could not be started")
	}
	reader := bufio.NewReaderSize(stdout, maximumBatchHeaderBytes)
	fail := func(result error) error {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return result
	}
	for _, entry := range entries {
		if entry.objectType != "blob" {
			continue
		}
		if _, err := io.WriteString(stdin, entry.objectID+"\n"); err != nil {
			return fail(scanError(ScanFailureRepository, "Git object reader became unavailable"))
		}
		header, err := reader.ReadString('\n')
		if err != nil || len(header) > maximumBatchHeaderBytes {
			return fail(scanError(ScanFailureRepository, "Git object metadata was incomplete"))
		}
		fields := strings.Fields(strings.TrimSuffix(header, "\n"))
		if len(fields) != 3 || fields[0] != entry.objectID || fields[1] != "blob" {
			return fail(scanError(ScanFailureRepository, "Git object metadata did not match the tree"))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return fail(scanError(ScanFailureRepository, "Git object size was invalid"))
		}
		if size > MaxBlobMetadataBytes {
			return fail(scanError(ScanFailureContract, "Git blob exceeds the frozen metadata size range"))
		}
		hasher := sha256.New()
		var buffer *bytesBuffer
		writer := io.Writer(hasher)
		if size <= MaxFileBytes && blobNeedsClassificationBytes(entry) {
			buffer = newBytesBuffer(int(size))
			writer = io.MultiWriter(hasher, buffer)
		}
		copied, err := io.CopyN(writer, reader, size)
		if err != nil || copied != size {
			return fail(scanError(ScanFailureRepository, "Git blob content was incomplete"))
		}
		terminator, err := reader.ReadByte()
		if err != nil || terminator != '\n' {
			return fail(scanError(ScanFailureRepository, "Git blob framing was invalid"))
		}
		digest := hex.EncodeToString(hasher.Sum(nil))
		entry.byteSize = size
		entry.contentHash = &digest
		if buffer != nil {
			entry.blob = buffer.Bytes()
		}
	}
	if err := stdin.Close(); err != nil {
		return fail(scanError(ScanFailureRepository, "Git object reader did not close cleanly"))
	}
	if err := cmd.Wait(); err != nil {
		return scanError(ScanFailureRepository, "Git object reader failed")
	}
	return nil
}

// bytesBuffer is deliberately tiny: only blobs at or below MaxFileBytes are
// retained. Larger blobs are streamed into SHA-256 and discarded.
type bytesBuffer struct {
	value []byte
}

func newBytesBuffer(capacity int) *bytesBuffer {
	return &bytesBuffer{value: make([]byte, 0, capacity)}
}

func (buffer *bytesBuffer) Write(value []byte) (int, error) {
	buffer.value = append(buffer.value, value...)
	return len(value), nil
}

func (buffer *bytesBuffer) Bytes() []byte { return buffer.value }

func blobNeedsClassificationBytes(entry *rawTreeEntry) bool {
	if entry.mode != "100644" && entry.mode != "100755" {
		return false
	}
	for _, component := range strings.Split(entry.path, "/") {
		switch component {
		case ".git", ".compair", "build", "dist", "node_modules":
			return false
		}
	}
	return true
}

func utf8Bytes(value []byte) bool {
	return len(value) > 0 && utf8.Valid(value)
}
