package baseline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"golang.org/x/text/unicode/norm"
)

const (
	ControlProtocolVersion = "baseline-control-plane.v1"
	ControlProtocolSHA256  = "3b45287a54d04cea751e9cc3209c5f0783192de13062e682eadcae40af322650"
	DryRunSchemaVersion    = "baseline-scan-dry-run.v1"

	MaxSiblingRepositories = 128
	MaxFileRecords         = 50_000
	MaxFileBytes           = 200_000
	MaxBlobMetadataBytes   = 512_000_000
	MaxSupportedBytes      = 512_000_000
	MaxRawDiffBytes        = 8_000_000
	MaxManifestRequest     = 32_000_000
	MaxPartRequest         = 8_000_000
	MaxPartBytes           = 1_000_000
	MaxPartItems           = 1_000
	MaxContentParts        = 512
	MaxControlRequest      = 64_000
	MaxPathBytes           = 4_096
)

const (
	placeholderRequestID     = "00000000-0000-4000-8000-000000000000"
	placeholderJobID         = "00000000-0000-4000-8000-000000000000"
	placeholderIdempotency   = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	maximumTreeListingBytes  = 256_000_000
	maximumGitScalarBytes    = 8_192
	maximumBatchHeaderBytes  = 512
	finalRevisionDriftReason = "repository revision changed or became unavailable during scan"
)

var (
	groupPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)
	repositoryIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)
	repositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	gitRevisionPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	uuidPattern           = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
)

type ScanFailureKind string

const (
	ScanFailureContract   ScanFailureKind = "contract"
	ScanFailureRepository ScanFailureKind = "repository"
	ScanFailureInternal   ScanFailureKind = "internal"
)

// ScanError intentionally carries only a safe classification and fixed
// diagnostic. Git stderr and scanner inputs are never retained in the error.
type ScanError struct {
	Kind    ScanFailureKind
	Message string
}

func (e *ScanError) Error() string { return e.Message }

func scanError(kind ScanFailureKind, message string) error {
	return &ScanError{Kind: kind, Message: message}
}

func ScanFailure(err error) ScanFailureKind {
	var target *ScanError
	if errors.As(err, &target) {
		return target.Kind
	}
	return ScanFailureInternal
}

type ChangedRepositoryInput struct {
	SchemaVersion      string `json:"schema_version"`
	LocalPath          string `json:"local_path"`
	RepositoryID       string `json:"repository_id"`
	RepositoryName     string `json:"repository_name"`
	RepositoryRevision string `json:"repository_revision"`
	SourceDocumentID   string `json:"source_document_id"`
}

type SiblingRepositoryInput struct {
	SchemaVersion      string `json:"schema_version"`
	LocalPath          string `json:"local_path"`
	RepositoryID       string `json:"repository_id"`
	RepositoryName     string `json:"repository_name"`
	RepositoryRevision string `json:"repository_revision"`
}

// ScanInput is byte-for-byte compatible in shape with the frozen
// baseline-scanner-inputs.v1 fixture. The command-level --group is separately
// required and must equal GroupID.
type ScanInput struct {
	Changed      ChangedRepositoryInput   `json:"changed"`
	Siblings     []SiblingRepositoryInput `json:"siblings"`
	BaseRevision string                   `json:"base_revision"`
	HeadRevision string                   `json:"head_revision"`
	GroupID      string                   `json:"group_id"`
	DryRun       bool                     `json:"dry_run"`
	JSON         bool                     `json:"json"`
}

type ChangedRepository struct {
	RepositoryID     string `json:"repository_id"`
	RepositoryName   string `json:"repository_name"`
	Revision         string `json:"repository_revision"`
	Role             string `json:"role"`
	BaseRevision     string `json:"base_revision"`
	HeadRevision     string `json:"head_revision"`
	SourceDocumentID string `json:"source_document_id"`
	ExpectedFiles    int    `json:"expected_file_count"`
}

type SiblingRepository struct {
	RepositoryID   string `json:"repository_id"`
	RepositoryName string `json:"repository_name"`
	Revision       string `json:"repository_revision"`
	Role           string `json:"role"`
	ExpectedFiles  int    `json:"expected_file_count"`
}

type FileRecord struct {
	Ordinal         int     `json:"ordinal"`
	RepositoryID    string  `json:"repository_id"`
	RepositoryName  string  `json:"repository_name"`
	Revision        string  `json:"repository_revision"`
	RelativePath    string  `json:"relative_path"`
	GitMode         string  `json:"git_mode"`
	GitObjectID     string  `json:"git_object_id"`
	FileState       string  `json:"file_state"`
	SkipReason      *string `json:"skip_reason"`
	ByteSize        int64   `json:"byte_size"`
	ContentSHA256   *string `json:"content_sha256"`
	ContentRequired bool    `json:"content_required"`
}

type SnapshotManifest struct {
	SchemaVersion         string              `json:"schema_version"`
	GroupID               string              `json:"group_id"`
	ChangedRepository     ChangedRepository   `json:"changed_repository"`
	SiblingRepositories   []SiblingRepository `json:"sibling_repositories"`
	Files                 []FileRecord        `json:"files"`
	RepositoryCount       int                 `json:"repository_count"`
	TotalFileCount        int                 `json:"total_file_count"`
	SupportedFileCount    int                 `json:"supported_file_count"`
	SupportedContentBytes int64               `json:"supported_content_bytes"`
	CanonicalManifestHash string              `json:"canonical_manifest_hash"`
	SnapshotID            string              `json:"snapshot_id"`
}

type RawDiffMetadata struct {
	Representation string `json:"representation"`
	BaseRevision   string `json:"base_revision"`
	HeadRevision   string `json:"head_revision"`
	ByteSize       int    `json:"byte_size"`
	SHA256         string `json:"sha256"`
}

type ScanPlan struct {
	ProtocolVersion string           `json:"protocol_version"`
	MessageType     string           `json:"message_type"`
	GroupID         string           `json:"group_id"`
	Snapshot        SnapshotManifest `json:"snapshot"`
	RawDiff         RawDiffMetadata  `json:"raw_diff"`
}

type ContentItem struct {
	FileOrdinal int    `json:"file_ordinal"`
	ByteSize    int    `json:"byte_size"`
	ContentHash string `json:"content_sha256"`
	ContentUTF8 string `json:"content_utf8"`
}

type PartDescriptor struct {
	PartOrdinal int    `json:"part_ordinal"`
	PartSHA256  string `json:"part_sha256"`
}

type ContentPart struct {
	Descriptor         PartDescriptor
	Items              []ContentItem
	DecodedContentByte int
	RequestBytes       int
}

type DryRunRepository struct {
	RegistrationID string `json:"repository_registration_id"`
	Revision       string `json:"revision"`
}

type DryRunChangedRepository struct {
	RegistrationID string `json:"repository_registration_id"`
	BaseRevision   string `json:"base_revision"`
	HeadRevision   string `json:"head_revision"`
	SourceDocument string `json:"source_document_id"`
}

type SkipReasonCounts struct {
	NonUTF8             int `json:"non_utf8"`
	Oversized           int `json:"oversized"`
	Symlink             int `json:"symlink"`
	ExcludedDirectory   int `json:"excluded_directory"`
	UnsupportedFileType int `json:"unsupported_file_type"`
	Unreadable          int `json:"unreadable"`
}

type DryRunCounts struct {
	RepositoryCount      int `json:"repository_count"`
	FileCount            int `json:"file_count"`
	SupportedFileCount   int `json:"supported_file_count"`
	SkippedFileCount     int `json:"skipped_file_count"`
	SupportedContentByte int `json:"supported_content_bytes"`
}

type DryRunPart struct {
	Ordinal            int    `json:"part_ordinal"`
	SHA256             string `json:"part_sha256"`
	FileCount          int    `json:"file_count"`
	DecodedContentByte int    `json:"decoded_content_bytes"`
	RequestBytes       int    `json:"request_bytes"`
}

type DryRunReport struct {
	SchemaVersion             string                  `json:"schema_version"`
	ProtocolVersion           string                  `json:"protocol_version"`
	ProtocolSHA256            string                  `json:"protocol_sha256"`
	GroupID                   string                  `json:"group_id"`
	ChangedRepository         DryRunChangedRepository `json:"changed_repository"`
	SiblingRepositories       []DryRunRepository      `json:"sibling_repositories"`
	SnapshotID                string                  `json:"snapshot_id"`
	CanonicalManifestHash     string                  `json:"canonical_manifest_hash"`
	ScanPlanJCSSHA256         string                  `json:"scan_plan_jcs_sha256"`
	ContentManifestHash       string                  `json:"content_manifest_hash"`
	Counts                    DryRunCounts            `json:"counts"`
	SkipReasonCounts          SkipReasonCounts        `json:"skip_reason_counts"`
	RawDiff                   RawDiffMetadata         `json:"raw_diff"`
	Parts                     []DryRunPart            `json:"parts"`
	ManifestRequestBytes      int                     `json:"manifest_request_bytes"`
	CommitRequestBytes        int                     `json:"commit_request_bytes"`
	MaximumPlannedUploadBytes int64                   `json:"maximum_planned_upload_bytes"`
	DeterministicFingerprint  string                  `json:"scan_fingerprint"`
	Warnings                  []string                `json:"warnings"`
	Errors                    []string                `json:"errors"`
}

type ScanResult struct {
	Plan                ScanPlan
	Parts               []ContentPart
	ContentManifestHash string
	RawDiff             []byte
	Report              DryRunReport
	blobs               [][]byte
}

// ClearProtected drops all reachable raw content as soon as the dry-run report
// has been written. It does not claim that Go's allocator provides secure
// memory erasure.
func (result *ScanResult) ClearProtected() {
	if result == nil {
		return
	}
	for index := range result.RawDiff {
		result.RawDiff[index] = 0
	}
	result.RawDiff = nil
	for _, blob := range result.blobs {
		for index := range blob {
			blob[index] = 0
		}
	}
	result.blobs = nil
	for partIndex := range result.Parts {
		for itemIndex := range result.Parts[partIndex].Items {
			result.Parts[partIndex].Items[itemIndex].ContentUTF8 = ""
		}
		result.Parts[partIndex].Items = nil
	}
}

type Scanner struct {
	gitBinary         string
	temporaryRoot     string
	beforeFinalVerify func()
}

func NewScanner() *Scanner {
	return &Scanner{gitBinary: "git"}
}

func LoadScanInput(filename string) (ScanInput, error) {
	file, err := os.Open(filename)
	if err != nil {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan could not be read")
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, MaxControlRequest+1))
	if err != nil {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan could not be read")
	}
	if len(value) > MaxControlRequest {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan exceeds the frozen control limit")
	}
	return DecodeScanInput(value)
}

func DecodeScanInput(value []byte) (ScanInput, error) {
	if !utf8.Valid(value) {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan must be UTF-8 JSON")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan is not strict JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var input ScanInput
	if err := decoder.Decode(&input); err != nil {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan does not match baseline-scanner-inputs.v1")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ScanInput{}, scanError(ScanFailureContract, "scan plan must contain exactly one JSON value")
	}
	return input, nil
}

func rejectDuplicateJSONKeys(value []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := readUniqueJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func readUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := readUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := readUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

type rawTreeEntry struct {
	mode        string
	objectType  string
	objectID    string
	path        string
	byteSize    int64
	contentHash *string
	blob        []byte
}

type scannedRepository struct {
	input   SiblingRepositoryInput
	records []FileRecord
	blobs   map[string][]byte
}

func (scanner *Scanner) Scan(ctx context.Context, explicitGroup string, input ScanInput) (*ScanResult, error) {
	if scanner == nil {
		return nil, scanError(ScanFailureInternal, "scanner is unavailable")
	}
	if err := validateScanInput(explicitGroup, input); err != nil {
		return nil, err
	}
	if scanner.gitBinary == "" {
		scanner.gitBinary = "git"
	}

	siblings := append([]SiblingRepositoryInput(nil), input.Siblings...)
	sort.Slice(siblings, func(left, right int) bool {
		if siblings[left].RepositoryName != siblings[right].RepositoryName {
			return siblings[left].RepositoryName < siblings[right].RepositoryName
		}
		return siblings[left].RepositoryID < siblings[right].RepositoryID
	})
	if err := scanner.validateRepositoryRoots(ctx, input.Changed, siblings); err != nil {
		return nil, err
	}

	baseRevision, err := scanner.resolveCommit(ctx, input.Changed.LocalPath, input.BaseRevision)
	if err != nil {
		return nil, err
	}
	headRevision, err := scanner.resolveCommit(ctx, input.Changed.LocalPath, input.HeadRevision)
	if err != nil {
		return nil, err
	}
	if baseRevision != input.BaseRevision || headRevision != input.HeadRevision || headRevision != input.Changed.RepositoryRevision {
		return nil, scanError(ScanFailureRepository, "changed repository revisions do not match the immutable plan")
	}
	if err := scanner.requireAncestor(ctx, input.Changed.LocalPath, baseRevision, headRevision); err != nil {
		return nil, err
	}
	rawDiff, err := scanner.rawDiff(ctx, input.Changed.LocalPath, baseRevision, headRevision)
	if err != nil {
		return nil, err
	}

	repositories := make([]scannedRepository, 0, len(siblings))
	allRecords := make([]FileRecord, 0)
	allBlobs := make(map[string][]byte)
	for _, sibling := range siblings {
		resolved, err := scanner.resolveCommit(ctx, sibling.LocalPath, sibling.RepositoryRevision)
		if err != nil {
			return nil, err
		}
		if resolved != sibling.RepositoryRevision {
			return nil, scanError(ScanFailureRepository, "sibling repository revision does not match the immutable plan")
		}
		repository, err := scanner.scanRepository(ctx, sibling)
		if err != nil {
			return nil, err
		}
		if len(allRecords)+len(repository.records) > MaxFileRecords {
			return nil, scanError(ScanFailureContract, "snapshot exceeds the frozen file-record limit")
		}
		for key, blob := range repository.blobs {
			if _, duplicate := allBlobs[key]; duplicate {
				return nil, scanError(ScanFailureContract, "snapshot contains a duplicate repository/path identity")
			}
			allBlobs[key] = blob
		}
		allRecords = append(allRecords, repository.records...)
		repositories = append(repositories, repository)
	}

	sort.Slice(allRecords, func(left, right int) bool {
		a, b := allRecords[left], allRecords[right]
		if a.RepositoryName != b.RepositoryName {
			return a.RepositoryName < b.RepositoryName
		}
		if a.RelativePath != b.RelativePath {
			return a.RelativePath < b.RelativePath
		}
		return a.RepositoryID < b.RepositoryID
	})
	for index := range allRecords {
		allRecords[index].Ordinal = index + 1
	}

	result, err := buildScanResult(input, repositories, allRecords, allBlobs, rawDiff)
	if err != nil {
		return nil, err
	}
	if scanner.beforeFinalVerify != nil {
		scanner.beforeFinalVerify()
	}
	if err := scanner.finalVerify(ctx, input, siblings); err != nil {
		result.ClearProtected()
		return nil, err
	}
	return result, nil
}

func validateScanInput(explicitGroup string, input ScanInput) error {
	if !validGroupID(explicitGroup) || explicitGroup != input.GroupID {
		return scanError(ScanFailureContract, "an explicit matching group ID is required")
	}
	if input.Changed.SchemaVersion != "baseline-changed-repository-input.v1" ||
		!validRepositoryID(input.Changed.RepositoryID) ||
		!validRepositoryNameValue(input.Changed.RepositoryName) ||
		!validGitRevision(input.Changed.RepositoryRevision) ||
		!validUUID(input.Changed.SourceDocumentID) || !validLocalPath(input.Changed.LocalPath) {
		return scanError(ScanFailureContract, "changed repository input is invalid")
	}
	if !validGitRevision(input.BaseRevision) || !validGitRevision(input.HeadRevision) ||
		input.BaseRevision == input.HeadRevision || input.Changed.RepositoryRevision != input.HeadRevision {
		return scanError(ScanFailureContract, "distinct immutable base and head revisions are required")
	}
	if !input.DryRun || !input.JSON {
		return scanError(ScanFailureContract, "the frozen scanner input must be dry-run JSON only")
	}
	if len(input.Siblings) < 1 || len(input.Siblings) > MaxSiblingRepositories {
		return scanError(ScanFailureContract, "the sibling repository count violates the frozen limit")
	}
	seenIDs := map[string]struct{}{input.Changed.RepositoryID: {}}
	seenPairs := make(map[string]struct{})
	for _, sibling := range input.Siblings {
		if sibling.SchemaVersion != "baseline-sibling-repository-input.v1" ||
			!validRepositoryID(sibling.RepositoryID) ||
			!validRepositoryNameValue(sibling.RepositoryName) ||
			!validGitRevision(sibling.RepositoryRevision) || !validLocalPath(sibling.LocalPath) {
			return scanError(ScanFailureContract, "sibling repository input is invalid")
		}
		if _, duplicate := seenIDs[sibling.RepositoryID]; duplicate {
			return scanError(ScanFailureContract, "repository registration identities must be unique")
		}
		seenIDs[sibling.RepositoryID] = struct{}{}
		pair := sibling.RepositoryName + "\x00" + sibling.RepositoryID
		if _, duplicate := seenPairs[pair]; duplicate {
			return scanError(ScanFailureContract, "repository identities must be unique")
		}
		seenPairs[pair] = struct{}{}
	}
	return nil
}

func validGroupID(value string) bool {
	return len(value) >= 1 && len(value) <= 64 && groupPattern.MatchString(value)
}

func validRepositoryID(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && repositoryIDPattern.MatchString(value)
}

func validRepositoryNameValue(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && repositoryNamePattern.MatchString(value)
}

func validGitRevision(value string) bool { return gitRevisionPattern.MatchString(value) }
func validUUID(value string) bool        { return uuidPattern.MatchString(value) }

func validLocalPath(value string) bool {
	return value != "" && utf8.ValidString(value) && len([]byte(value)) <= MaxPathBytes && !strings.ContainsRune(value, 0)
}

func validatePOSIXPath(value string) error {
	if value == "" || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) || len([]byte(value)) > MaxPathBytes {
		return scanError(ScanFailureContract, "Git tree contains a non-canonical path")
	}
	if path.IsAbs(value) || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) ||
		strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
		return scanError(ScanFailureContract, "Git tree contains a non-canonical path")
	}
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' {
		return scanError(ScanFailureContract, "Git tree contains a non-canonical path")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return scanError(ScanFailureContract, "Git tree contains a path alias or traversal")
		}
	}
	if path.Clean(value) != value {
		return scanError(ScanFailureContract, "Git tree contains a path alias or traversal")
	}
	return nil
}

func (scanner *Scanner) scanRepository(ctx context.Context, input SiblingRepositoryInput) (scannedRepository, error) {
	isolation, cleanup, err := scanner.isolatedObjectEnvironment(ctx, input.LocalPath)
	if err != nil {
		return scannedRepository{}, err
	}
	defer cleanup()
	listing, err := scanner.gitOutputWithEnvironment(ctx, input.LocalPath, maximumTreeListingBytes, isolation,
		"ls-tree", "-r", "-z", "--full-tree", input.RepositoryRevision)
	if errors.Is(err, errGitOutputLimit) {
		return scannedRepository{}, scanError(ScanFailureContract, "Git tree listing exceeds the bounded manifest range")
	}
	if err != nil {
		return scannedRepository{}, err
	}
	entries, err := parseTreeListing(listing)
	if err != nil {
		return scannedRepository{}, err
	}
	if len(entries) > MaxFileRecords {
		return scannedRepository{}, scanError(ScanFailureContract, "repository exceeds the frozen file-record limit")
	}
	if err := scanner.readBlobMetadata(ctx, input.LocalPath, isolation, entries); err != nil {
		return scannedRepository{}, err
	}

	records := make([]FileRecord, 0, len(entries))
	blobs := make(map[string][]byte)
	seenPaths := make(map[string]struct{})
	seenAliases := make(map[string]string)
	for _, entry := range entries {
		if err := validatePOSIXPath(entry.path); err != nil {
			return scannedRepository{}, err
		}
		key := input.RepositoryID + "\x00" + entry.path
		if _, duplicate := seenPaths[key]; duplicate {
			return scannedRepository{}, scanError(ScanFailureContract, "Git tree contains a duplicate repository/path")
		}
		seenPaths[key] = struct{}{}
		alias := input.RepositoryID + "\x00" + norm.NFC.String(entry.path)
		if raw, duplicate := seenAliases[alias]; duplicate && raw != entry.path {
			return scannedRepository{}, scanError(ScanFailureContract, "Git tree contains a Unicode path alias")
		}
		seenAliases[alias] = entry.path

		record, err := classifyTreeEntry(input, entry)
		if err != nil {
			return scannedRepository{}, err
		}
		records = append(records, record)
		if record.ContentRequired {
			blobKey := input.RepositoryID + "\x00" + entry.path
			blobs[blobKey] = entry.blob
		}
	}
	return scannedRepository{input: input, records: records, blobs: blobs}, nil
}

func parseTreeListing(value []byte) ([]*rawTreeEntry, error) {
	if len(value) == 0 {
		return nil, nil
	}
	parts := bytes.Split(value, []byte{0})
	if len(parts[len(parts)-1]) != 0 {
		return nil, scanError(ScanFailureRepository, "Git tree enumeration was incomplete")
	}
	entries := make([]*rawTreeEntry, 0, len(parts)-1)
	for _, item := range parts[:len(parts)-1] {
		tab := bytes.IndexByte(item, '\t')
		if tab <= 0 {
			return nil, scanError(ScanFailureRepository, "Git tree enumeration was malformed")
		}
		header := strings.Fields(string(item[:tab]))
		if len(header) != 3 || !validGitRevision(header[2]) {
			return nil, scanError(ScanFailureRepository, "Git tree enumeration was malformed")
		}
		pathBytes := item[tab+1:]
		if !utf8.Valid(pathBytes) {
			return nil, scanError(ScanFailureContract, "Git tree contains a non-UTF-8 path")
		}
		entries = append(entries, &rawTreeEntry{mode: header[0], objectType: header[1], objectID: header[2], path: string(pathBytes)})
	}
	return entries, nil
}

func classifyTreeEntry(input SiblingRepositoryInput, entry *rawTreeEntry) (FileRecord, error) {
	record := FileRecord{
		RepositoryID: input.RepositoryID, RepositoryName: input.RepositoryName,
		Revision: input.RepositoryRevision, RelativePath: entry.path, GitMode: entry.mode,
		GitObjectID: entry.objectID, ByteSize: entry.byteSize, ContentSHA256: entry.contentHash,
	}
	reason := func(value string) *string { return &value }
	switch entry.mode {
	case "120000":
		if entry.objectType != "blob" || entry.contentHash == nil {
			return FileRecord{}, scanError(ScanFailureRepository, "symlink Git object is unavailable")
		}
		record.FileState, record.SkipReason = "symlink_rejected", reason("symlink")
		return record, nil
	case "160000":
		if entry.objectType != "commit" {
			return FileRecord{}, scanError(ScanFailureRepository, "submodule Git object has an incompatible type")
		}
		record.FileState, record.SkipReason = "excluded", reason("unsupported_file_type")
		return record, nil
	case "100644", "100755":
		if entry.objectType != "blob" || entry.contentHash == nil {
			return FileRecord{}, scanError(ScanFailureRepository, "file Git object is unavailable")
		}
	default:
		return FileRecord{}, scanError(ScanFailureContract, "Git tree contains a mode outside the frozen contract")
	}
	for _, component := range strings.Split(entry.path, "/") {
		switch component {
		case ".git", ".compair", "build", "dist", "node_modules":
			record.FileState, record.SkipReason = "excluded", reason("excluded_directory")
			return record, nil
		}
	}
	if entry.byteSize > MaxFileBytes {
		record.FileState, record.SkipReason = "oversized", reason("oversized")
		return record, nil
	}
	if !utf8.Valid(entry.blob) {
		record.FileState, record.SkipReason = "unsupported_utf8", reason("non_utf8")
		return record, nil
	}
	record.FileState = "supported"
	record.ContentRequired = true
	return record, nil
}

func buildScanResult(input ScanInput, repositories []scannedRepository, records []FileRecord, blobs map[string][]byte, rawDiff []byte) (*ScanResult, error) {
	changed := ChangedRepository{
		RepositoryID: input.Changed.RepositoryID, RepositoryName: input.Changed.RepositoryName,
		Revision: input.HeadRevision, Role: "changed", BaseRevision: input.BaseRevision,
		HeadRevision: input.HeadRevision, SourceDocumentID: input.Changed.SourceDocumentID,
		ExpectedFiles: 0,
	}
	siblingRecords := make([]SiblingRepository, 0, len(repositories))
	for _, repository := range repositories {
		siblingRecords = append(siblingRecords, SiblingRepository{
			RepositoryID: repository.input.RepositoryID, RepositoryName: repository.input.RepositoryName,
			Revision: repository.input.RepositoryRevision, Role: "sibling", ExpectedFiles: len(repository.records),
		})
	}

	supportedCount, supportedBytes, err := validateSnapshotAggregate(records)
	if err != nil {
		return nil, err
	}

	manifest := SnapshotManifest{
		SchemaVersion: "baseline-snapshot.v1", GroupID: input.GroupID,
		ChangedRepository: changed, SiblingRepositories: siblingRecords, Files: records,
		RepositoryCount: len(siblingRecords), TotalFileCount: len(records),
		SupportedFileCount: supportedCount, SupportedContentBytes: supportedBytes,
	}
	canonicalManifest := struct {
		SchemaVersion       string              `json:"schema_version"`
		ChangedRepository   ChangedRepository   `json:"changed_repository"`
		SiblingRepositories []SiblingRepository `json:"sibling_repositories"`
		Files               []FileRecord        `json:"files"`
	}{manifest.SchemaVersion, manifest.ChangedRepository, manifest.SiblingRepositories, manifest.Files}
	manifestBytes, err := canonicalJSONBytes(canonicalManifest)
	if err != nil {
		return nil, scanError(ScanFailureInternal, "manifest canonicalization failed")
	}
	manifest.CanonicalManifestHash = sha256Hex(manifestBytes)
	manifest.SnapshotID = "bsnap_" + manifest.CanonicalManifestHash

	rawDiffMetadata := RawDiffMetadata{
		Representation: "raw_git_diff_v1", BaseRevision: input.BaseRevision,
		HeadRevision: input.HeadRevision, ByteSize: len(rawDiff), SHA256: sha256Hex(rawDiff),
	}
	plan := ScanPlan{ProtocolVersion: ControlProtocolVersion, MessageType: "scan_plan", GroupID: input.GroupID, Snapshot: manifest, RawDiff: rawDiffMetadata}
	planBytes, err := canonicalJSONBytes(plan)
	if err != nil {
		return nil, scanError(ScanFailureInternal, "scan plan canonicalization failed")
	}

	parts, descriptors, err := buildContentParts(records, blobs, input.GroupID, manifest.SnapshotID)
	if err != nil {
		return nil, err
	}
	descriptorBytes, err := canonicalJSONBytes(descriptors)
	if err != nil {
		return nil, scanError(ScanFailureInternal, "content manifest canonicalization failed")
	}
	contentManifestHash := sha256Hex(descriptorBytes)
	manifestRequestBytes, commitRequestBytes, totalUploadBytes, err := calculateUploadSizes(input.GroupID, manifest, parts, descriptors, contentManifestHash)
	if err != nil {
		return nil, err
	}

	report := buildDryRunReport(input, plan, parts, contentManifestHash, sha256Hex(planBytes), manifestRequestBytes, commitRequestBytes, totalUploadBytes)
	if report.DeterministicFingerprint == "" {
		return nil, scanError(ScanFailureInternal, "dry-run contract construction failed")
	}
	if err := ValidateDryRunReportContract(report); err != nil {
		return nil, err
	}
	protectedBlobs := make([][]byte, 0, len(blobs))
	for _, blob := range blobs {
		protectedBlobs = append(protectedBlobs, blob)
	}
	return &ScanResult{Plan: plan, Parts: parts, ContentManifestHash: contentManifestHash, RawDiff: rawDiff, Report: report, blobs: protectedBlobs}, nil
}

func validateSnapshotAggregate(records []FileRecord) (int, int64, error) {
	if len(records) > MaxFileRecords {
		return 0, 0, scanError(ScanFailureContract, "snapshot exceeds the frozen file-record limit")
	}
	supportedCount := 0
	var supportedBytes int64
	for _, record := range records {
		if !record.ContentRequired {
			continue
		}
		if record.ByteSize < 0 || record.ByteSize > MaxFileBytes {
			return 0, 0, scanError(ScanFailureContract, "supported file violates the frozen file-size limit")
		}
		supportedCount++
		supportedBytes += record.ByteSize
	}
	if supportedBytes > MaxSupportedBytes {
		return 0, 0, scanError(ScanFailureContract, "snapshot exceeds the frozen supported-content limit")
	}
	return supportedCount, supportedBytes, nil
}

func buildContentParts(records []FileRecord, blobs map[string][]byte, groupID string, snapshotID string) ([]ContentPart, []PartDescriptor, error) {
	var parts []ContentPart
	for _, record := range records {
		if !record.ContentRequired {
			continue
		}
		key := record.RepositoryID + "\x00" + record.RelativePath
		blob, ok := blobs[key]
		if !ok || len(blob) != int(record.ByteSize) || record.ContentSHA256 == nil || sha256Hex(blob) != *record.ContentSHA256 {
			return nil, nil, scanError(ScanFailureInternal, "supported content no longer matches its manifest")
		}
		if len(parts) == 0 || len(parts[len(parts)-1].Items) >= MaxPartItems ||
			parts[len(parts)-1].DecodedContentByte+len(blob) > MaxPartBytes {
			parts = append(parts, ContentPart{Descriptor: PartDescriptor{PartOrdinal: len(parts) + 1}})
		}
		part := &parts[len(parts)-1]
		part.Items = append(part.Items, ContentItem{FileOrdinal: record.Ordinal, ByteSize: len(blob), ContentHash: *record.ContentSHA256, ContentUTF8: string(blob)})
		part.DecodedContentByte += len(blob)
	}
	if len(parts) > MaxContentParts {
		return nil, nil, scanError(ScanFailureContract, "snapshot exceeds the frozen content-part limit")
	}
	descriptors := make([]PartDescriptor, len(parts))
	for index := range parts {
		canonical, err := canonicalJSONBytes(parts[index].Items)
		if err != nil {
			return nil, nil, scanError(ScanFailureInternal, "content part canonicalization failed")
		}
		parts[index].Descriptor.PartSHA256 = sha256Hex(canonical)
		descriptors[index] = parts[index].Descriptor
		request := contentPartRequest(inputRequestContext(groupID, snapshotID), parts[index])
		requestBytes, err := canonicalJSONBytes(request)
		if err != nil {
			return nil, nil, scanError(ScanFailureInternal, "content part request canonicalization failed")
		}
		parts[index].RequestBytes = len(requestBytes)
		if parts[index].DecodedContentByte > MaxPartBytes || len(parts[index].Items) > MaxPartItems || len(requestBytes) > MaxPartRequest {
			return nil, nil, scanError(ScanFailureContract, "content part violates a frozen upload limit")
		}
	}
	return parts, descriptors, nil
}

type requestContext struct {
	groupID    string
	snapshotID string
}

func inputRequestContext(groupID, snapshotID string) requestContext {
	return requestContext{groupID: groupID, snapshotID: snapshotID}
}

func contentPartRequest(context requestContext, part ContentPart) any {
	return struct {
		ProtocolVersion string        `json:"protocol_version"`
		ProtocolSHA256  string        `json:"protocol_sha256"`
		MessageType     string        `json:"message_type"`
		RequestID       string        `json:"request_id"`
		GroupID         string        `json:"group_id"`
		JobID           string        `json:"job_id"`
		SnapshotID      string        `json:"snapshot_id"`
		PartOrdinal     int           `json:"part_ordinal"`
		PartSHA256      string        `json:"part_sha256"`
		ContentItems    []ContentItem `json:"content_items"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "snapshot_content_part", placeholderRequestID, context.groupID, placeholderJobID, context.snapshotID, part.Descriptor.PartOrdinal, part.Descriptor.PartSHA256, part.Items}
}

func calculateUploadSizes(groupID string, manifest SnapshotManifest, parts []ContentPart, descriptors []PartDescriptor, contentManifestHash string) (int, int, int64, error) {
	begin := struct {
		ProtocolVersion string           `json:"protocol_version"`
		ProtocolSHA256  string           `json:"protocol_sha256"`
		MessageType     string           `json:"message_type"`
		RequestID       string           `json:"request_id"`
		GroupID         string           `json:"group_id"`
		IdempotencyKey  string           `json:"idempotency_key"`
		Snapshot        SnapshotManifest `json:"snapshot"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "snapshot_begin", placeholderRequestID, groupID, placeholderIdempotency, manifest}
	beginBytes, err := canonicalJSONBytes(begin)
	if err != nil {
		return 0, 0, 0, scanError(ScanFailureInternal, "manifest request canonicalization failed")
	}
	if len(beginBytes) > MaxManifestRequest {
		return 0, 0, 0, scanError(ScanFailureContract, "snapshot manifest cannot fit the frozen request limit")
	}
	commit := struct {
		ProtocolVersion     string           `json:"protocol_version"`
		ProtocolSHA256      string           `json:"protocol_sha256"`
		MessageType         string           `json:"message_type"`
		RequestID           string           `json:"request_id"`
		GroupID             string           `json:"group_id"`
		JobID               string           `json:"job_id"`
		SnapshotID          string           `json:"snapshot_id"`
		Parts               []PartDescriptor `json:"parts"`
		ContentManifestHash string           `json:"content_manifest_hash"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "snapshot_commit", placeholderRequestID, groupID, placeholderJobID, manifest.SnapshotID, descriptors, contentManifestHash}
	commitBytes, err := canonicalJSONBytes(commit)
	if err != nil {
		return 0, 0, 0, scanError(ScanFailureInternal, "commit request canonicalization failed")
	}
	if len(commitBytes) > MaxControlRequest {
		return 0, 0, 0, scanError(ScanFailureContract, "snapshot commit cannot fit the frozen request limit")
	}
	total := int64(len(beginBytes) + len(commitBytes))
	for _, part := range parts {
		total += int64(part.RequestBytes)
	}
	return len(beginBytes), len(commitBytes), total, nil
}

func buildDryRunReport(input ScanInput, plan ScanPlan, parts []ContentPart, contentManifestHash, planHash string, manifestRequestBytes, commitRequestBytes int, maximumPlannedUploadBytes int64) DryRunReport {
	reportParts := make([]DryRunPart, len(parts))
	for index, part := range parts {
		reportParts[index] = DryRunPart{Ordinal: part.Descriptor.PartOrdinal, SHA256: part.Descriptor.PartSHA256, FileCount: len(part.Items), DecodedContentByte: part.DecodedContentByte, RequestBytes: part.RequestBytes}
	}
	siblings := make([]DryRunRepository, len(plan.Snapshot.SiblingRepositories))
	for index, repository := range plan.Snapshot.SiblingRepositories {
		siblings[index] = DryRunRepository{RegistrationID: repository.RepositoryID, Revision: repository.Revision}
	}
	skips := countSkips(plan.Snapshot.Files)
	report := DryRunReport{
		SchemaVersion: DryRunSchemaVersion, ProtocolVersion: ControlProtocolVersion, ProtocolSHA256: ControlProtocolSHA256,
		GroupID:             input.GroupID,
		ChangedRepository:   DryRunChangedRepository{RegistrationID: input.Changed.RepositoryID, BaseRevision: input.BaseRevision, HeadRevision: input.HeadRevision, SourceDocument: input.Changed.SourceDocumentID},
		SiblingRepositories: siblings, SnapshotID: plan.Snapshot.SnapshotID,
		CanonicalManifestHash: plan.Snapshot.CanonicalManifestHash, ScanPlanJCSSHA256: planHash,
		ContentManifestHash: contentManifestHash,
		Counts:              DryRunCounts{RepositoryCount: plan.Snapshot.RepositoryCount, FileCount: plan.Snapshot.TotalFileCount, SupportedFileCount: plan.Snapshot.SupportedFileCount, SkippedFileCount: plan.Snapshot.TotalFileCount - plan.Snapshot.SupportedFileCount, SupportedContentByte: int(plan.Snapshot.SupportedContentBytes)},
		SkipReasonCounts:    skips, RawDiff: plan.RawDiff, Parts: reportParts,
		ManifestRequestBytes: manifestRequestBytes, CommitRequestBytes: commitRequestBytes, MaximumPlannedUploadBytes: maximumPlannedUploadBytes,
		Warnings: []string{"dry_run_only", "no_network_or_persistence"}, Errors: []string{},
	}
	fingerprintValue := struct {
		ProtocolSHA256            string          `json:"protocol_sha256"`
		GroupID                   string          `json:"group_id"`
		SnapshotID                string          `json:"snapshot_id"`
		ScanPlanJCSSHA256         string          `json:"scan_plan_jcs_sha256"`
		ContentManifestHash       string          `json:"content_manifest_hash"`
		RawDiff                   RawDiffMetadata `json:"raw_diff"`
		Parts                     []DryRunPart    `json:"parts"`
		MaximumPlannedUploadBytes int64           `json:"maximum_planned_upload_bytes"`
	}{ControlProtocolSHA256, input.GroupID, report.SnapshotID, report.ScanPlanJCSSHA256, report.ContentManifestHash, report.RawDiff, report.Parts, report.MaximumPlannedUploadBytes}
	canonical, err := canonicalJSONBytes(fingerprintValue)
	if err == nil {
		report.DeterministicFingerprint = sha256Hex(canonical)
	}
	return report
}

func countSkips(records []FileRecord) SkipReasonCounts {
	var counts SkipReasonCounts
	for _, record := range records {
		if record.SkipReason == nil {
			continue
		}
		switch *record.SkipReason {
		case "non_utf8":
			counts.NonUTF8++
		case "oversized":
			counts.Oversized++
		case "symlink":
			counts.Symlink++
		case "excluded_directory":
			counts.ExcludedDirectory++
		case "unsupported_file_type":
			counts.UnsupportedFileType++
		case "unreadable":
			counts.Unreadable++
		}
	}
	return counts
}

func canonicalJSONBytes(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jsoncanonicalizer.Transform(encoded)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func safeScannerDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	var scanErr *ScanError
	if errors.As(err, &scanErr) {
		return scanErr.Message
	}
	return "baseline scan failed internally"
}

func encodeDryRunReport(writer io.Writer, report DryRunReport) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}

// The following symbols are exported narrowly for the Cobra adapter without
// exposing protected artifacts in its output or errors.
func EncodeDryRunReport(writer io.Writer, report DryRunReport) error {
	return encodeDryRunReport(writer, report)
}

func SafeScannerDiagnostic(err error) string { return safeScannerDiagnostic(err) }

// ValidateDryRunReportContract enforces the arithmetic and ordering invariants
// that JSON Schema cannot express for the public baseline-scan-dry-run.v1
// report. It never accepts or returns protected scanner content.
func ValidateDryRunReportContract(report DryRunReport) error {
	if report.SchemaVersion != DryRunSchemaVersion || report.ProtocolVersion != ControlProtocolVersion || report.ProtocolSHA256 != ControlProtocolSHA256 ||
		!validGroupID(report.GroupID) || !validRepositoryID(report.ChangedRepository.RegistrationID) || !validGitRevision(report.ChangedRepository.BaseRevision) ||
		!validGitRevision(report.ChangedRepository.HeadRevision) || !validUUID(report.ChangedRepository.SourceDocument) || !validSnapshotID(report.SnapshotID) ||
		!validHash(report.CanonicalManifestHash) || !validHash(report.ScanPlanJCSSHA256) || !validHash(report.ContentManifestHash) || !validHash(report.DeterministicFingerprint) {
		return scanError(ScanFailureContract, "dry-run report identity is invalid")
	}
	if report.Counts.RepositoryCount != len(report.SiblingRepositories) || report.Counts.FileCount < 0 || report.Counts.SupportedFileCount < 0 ||
		report.Counts.SupportedFileCount > report.Counts.FileCount || report.Counts.SkippedFileCount != report.Counts.FileCount-report.Counts.SupportedFileCount ||
		report.Counts.SupportedContentByte < 0 {
		return scanError(ScanFailureContract, "dry-run report counts are inconsistent")
	}
	seenRepositories := map[string]struct{}{report.ChangedRepository.RegistrationID: {}}
	for _, repository := range report.SiblingRepositories {
		if !validRepositoryID(repository.RegistrationID) || !validGitRevision(repository.Revision) {
			return scanError(ScanFailureContract, "dry-run repository identity is invalid")
		}
		if _, duplicate := seenRepositories[repository.RegistrationID]; duplicate {
			return scanError(ScanFailureContract, "dry-run repository identity is duplicated")
		}
		seenRepositories[repository.RegistrationID] = struct{}{}
	}
	skipped := report.SkipReasonCounts.NonUTF8 + report.SkipReasonCounts.Oversized + report.SkipReasonCounts.Symlink + report.SkipReasonCounts.ExcludedDirectory + report.SkipReasonCounts.UnsupportedFileType + report.SkipReasonCounts.Unreadable
	if skipped != report.Counts.SkippedFileCount {
		return scanError(ScanFailureContract, "dry-run skip counts are inconsistent")
	}
	if report.RawDiff.Representation != "raw_git_diff_v1" || !validGitRevision(report.RawDiff.BaseRevision) || !validGitRevision(report.RawDiff.HeadRevision) || report.RawDiff.ByteSize < 0 || !validHash(report.RawDiff.SHA256) {
		return scanError(ScanFailureContract, "dry-run diff metadata is invalid")
	}
	partFiles, partBytes, requestBytes := 0, 0, int64(report.ManifestRequestBytes+report.CommitRequestBytes)
	if report.ManifestRequestBytes < 0 || report.ManifestRequestBytes > MaxManifestRequest || report.CommitRequestBytes < 0 || report.CommitRequestBytes > MaxControlRequest {
		return scanError(ScanFailureContract, "dry-run request bounds are invalid")
	}
	for index, part := range report.Parts {
		if part.Ordinal != index+1 || !validHash(part.SHA256) || part.FileCount < 1 || part.FileCount > MaxPartItems || part.DecodedContentByte < 0 || part.DecodedContentByte > MaxPartBytes || part.RequestBytes < 0 || part.RequestBytes > MaxPartRequest {
			return scanError(ScanFailureContract, "dry-run part descriptor is invalid")
		}
		partFiles += part.FileCount
		partBytes += part.DecodedContentByte
		requestBytes += int64(part.RequestBytes)
	}
	if partFiles != report.Counts.SupportedFileCount || partBytes != report.Counts.SupportedContentByte || requestBytes != report.MaximumPlannedUploadBytes {
		return scanError(ScanFailureContract, "dry-run upload totals are inconsistent")
	}
	if len(report.Warnings) != 2 || report.Warnings[0] != "dry_run_only" || report.Warnings[1] != "no_network_or_persistence" || len(report.Errors) != 0 {
		return scanError(ScanFailureContract, "dry-run diagnostics are invalid")
	}
	return nil
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validSnapshotID(value string) bool {
	return strings.HasPrefix(value, "bsnap_") && validHash(strings.TrimPrefix(value, "bsnap_"))
}
