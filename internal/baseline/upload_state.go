package baseline

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	uploadStateSchemaVersion = "baseline-upload-resume.v2"
	uploadStateFileLimit     = 256_000
	installSecretBytes       = 32
)

type uploadStatePart struct {
	Ordinal int    `json:"part_ordinal"`
	SHA256  string `json:"part_sha256"`
}

type uploadStateCounts struct {
	RepositoryCount    int `json:"repository_count"`
	FileCount          int `json:"file_count"`
	SupportedFileCount int `json:"supported_file_count"`
	PartCount          int `json:"part_count"`
}

type uploadState struct {
	SchemaVersion         string            `json:"schema_version"`
	GroupID               string            `json:"group_id"`
	ServerIdentitySHA256  string            `json:"server_identity_sha256"`
	PlanIdentitySHA256    string            `json:"plan_identity_sha256"`
	ProtocolVersion       string            `json:"protocol_version"`
	ProtocolSHA256        string            `json:"protocol_sha256"`
	ScanFingerprint       string            `json:"scan_fingerprint"`
	RevisionSetSHA256     string            `json:"revision_set_sha256"`
	SnapshotID            string            `json:"snapshot_id"`
	StagingJobID          string            `json:"staging_job_id,omitempty"`
	ContinuationJobID     string            `json:"continuation_job_id,omitempty"`
	CanonicalManifestHash string            `json:"canonical_manifest_hash"`
	ContentManifestHash   string            `json:"content_manifest_hash"`
	Counts                uploadStateCounts `json:"counts"`
	Parts                 []uploadStatePart `json:"parts"`
	CompletedParts        []uploadStatePart `json:"completed_parts"`
	SafeState             string            `json:"safe_state"`
	CreatedAt             string            `json:"created_at"`
	UpdatedAt             string            `json:"updated_at"`
	IntegrityHMACSHA256   string            `json:"integrity_hmac_sha256"`
}

type uploadStateStore struct {
	directory string
	secret    []byte
}

func newUploadStateStore(override string) (*uploadStateStore, error) {
	directory := strings.TrimSpace(override)
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, uploadError(UploadFailureInternal, "state_directory_unavailable")
		}
		base := filepath.Join(home, ".compair")
		if err := ensureProtectedDirectory(base); err != nil {
			return nil, err
		}
		stateRoot := filepath.Join(base, "state")
		if err := ensureProtectedDirectory(stateRoot); err != nil {
			return nil, err
		}
		directory = filepath.Join(stateRoot, "baseline-uploads")
	}
	if err := ensureProtectedDirectory(directory); err != nil {
		return nil, err
	}
	secret, err := loadOrCreateInstallSecret(filepath.Join(filepath.Dir(directory), "baseline-upload-install-secret.v1"))
	if err != nil {
		return nil, err
	}
	return &uploadStateStore{directory: directory, secret: secret}, nil
}

func ensureProtectedDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return uploadError(UploadFailureContract, "unsafe_state_path")
		}
		if err := ensurePrivateDirectoryPermissions(directory, info); err != nil {
			return uploadError(UploadFailureContract, "unsafe_state_permissions")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return uploadError(UploadFailureInternal, "state_directory_unavailable")
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return uploadError(UploadFailureInternal, "state_directory_unavailable")
	}
	return nil
}

func loadOrCreateInstallSecret(filename string) ([]byte, error) {
	for attempts := 0; attempts < 50; attempts++ {
		info, err := os.Lstat(filename)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) {
				return nil, uploadError(UploadFailureContract, "unsafe_install_secret")
			}
			if info.Size() != installSecretBytes {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			value, readErr := os.ReadFile(filename)
			if readErr != nil || len(value) != installSecretBytes {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return value, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, uploadError(UploadFailureInternal, "install_secret_unavailable")
		}
		value := make([]byte, installSecretBytes)
		if _, err := io.ReadFull(rand.Reader, value); err != nil {
			return nil, uploadError(UploadFailureInternal, "install_secret_unavailable")
		}
		file, openErr := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			continue
		}
		if openErr != nil {
			return nil, uploadError(UploadFailureInternal, "install_secret_unavailable")
		}
		writeErr := writeAndSync(file, value)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return nil, uploadError(UploadFailureInternal, "install_secret_unavailable")
		}
		return value, nil
	}
	return nil, uploadError(UploadFailureInternal, "install_secret_unavailable")
}

func writeAndSync(file *os.File, value []byte) error {
	if _, err := file.Write(value); err != nil {
		return err
	}
	return file.Sync()
}

func (store *uploadStateStore) path(identity string) string {
	return filepath.Join(store.directory, identity+".json")
}

func (store *uploadStateStore) save(identity string, state *uploadState) error {
	if store == nil || state == nil || len(store.secret) != installSecretBytes {
		return uploadError(UploadFailureInternal, "state_store_unavailable")
	}
	state.IntegrityHMACSHA256 = ""
	macValue, err := store.stateMAC(state)
	if err != nil {
		return uploadError(UploadFailureInternal, "state_integrity_failed")
	}
	state.IntegrityHMACSHA256 = macValue
	encoded, err := json.Marshal(state)
	if err != nil || len(encoded) > uploadStateFileLimit {
		return uploadError(UploadFailureInternal, "state_encoding_failed")
	}
	target := store.path(identity)
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return uploadError(UploadFailureContract, "unsafe_state_path")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return uploadError(UploadFailureInternal, "state_write_failed")
	}
	temporary := filepath.Join(store.directory, fmt.Sprintf(".%s.%d.tmp", identity, time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return uploadError(UploadFailureInternal, "state_write_failed")
	}
	writeErr := writeAndSync(file, encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return uploadError(UploadFailureInternal, "state_write_failed")
	}
	if err := replaceFileAtomically(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return uploadError(UploadFailureInternal, "state_write_failed")
	}
	return syncDirectory(store.directory)
}

func (store *uploadStateStore) load(identity string) (*uploadState, error) {
	filename := store.path(identity)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) || info.Size() > uploadStateFileLimit {
		return nil, uploadError(UploadFailureContract, "unsafe_or_corrupt_resume_state")
	}
	value, err := os.ReadFile(filename)
	if err != nil {
		return nil, uploadError(UploadFailureInternal, "resume_state_unavailable")
	}
	var state uploadState
	if err := decodeStrictResponseJSON(value, &state); err != nil {
		return nil, uploadError(UploadFailureContract, "corrupt_resume_state")
	}
	provided := state.IntegrityHMACSHA256
	state.IntegrityHMACSHA256 = ""
	expected, err := store.stateMAC(&state)
	if err != nil || !hmac.Equal([]byte(provided), []byte(expected)) {
		return nil, uploadError(UploadFailureContract, "corrupt_resume_state")
	}
	state.IntegrityHMACSHA256 = provided
	return &state, nil
}

func (store *uploadStateStore) delete(identity string) error {
	filename := store.path(identity)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return uploadError(UploadFailureContract, "unsafe_state_path")
	}
	if err := os.Remove(filename); err != nil {
		return uploadError(UploadFailureInternal, "state_cleanup_failed")
	}
	return syncDirectory(store.directory)
}

func (store *uploadStateStore) stateMAC(state *uploadState) (string, error) {
	canonical, err := canonicalJSONBytes(state)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(uploadStateSchemaVersion + "\x00"))
	_, _ = mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (store *uploadStateStore) deriveHex(label, identity string) string {
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(identity))
	return hex.EncodeToString(mac.Sum(nil))
}

func (store *uploadStateStore) deriveUUID(label, identity string) string {
	raw, _ := hex.DecodeString(store.deriveHex(label, identity)[:32])
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	value := hex.EncodeToString(raw)
	return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32]
}

func planIdentitySHA256(groupID, serverIdentity string, input ScanInput) (string, error) {
	siblings := append([]SiblingRepositoryInput(nil), input.Siblings...)
	sort.Slice(siblings, func(left, right int) bool { return siblings[left].RepositoryID < siblings[right].RepositoryID })
	type repositoryIdentity struct {
		RegistrationID string `json:"repository_registration_id"`
		Revision       string `json:"revision"`
	}
	values := make([]repositoryIdentity, len(siblings))
	for index, sibling := range siblings {
		values[index] = repositoryIdentity{RegistrationID: sibling.RepositoryID, Revision: sibling.RepositoryRevision}
	}
	identity := struct {
		SchemaVersion         string               `json:"schema_version"`
		ServerIdentitySHA256  string               `json:"server_identity_sha256"`
		GroupID               string               `json:"group_id"`
		ChangedRegistrationID string               `json:"changed_repository_registration_id"`
		SourceDocumentID      string               `json:"source_document_id"`
		BaseRevision          string               `json:"base_revision"`
		HeadRevision          string               `json:"head_revision"`
		Siblings              []repositoryIdentity `json:"siblings"`
	}{uploadStateSchemaVersion, serverIdentity, groupID, input.Changed.RepositoryID, input.Changed.SourceDocumentID, input.BaseRevision, input.HeadRevision, values}
	canonical, err := canonicalJSONBytes(identity)
	if err != nil {
		return "", err
	}
	return sha256Hex(canonical), nil
}

func revisionSetSHA256(report DryRunReport) (string, error) {
	value := struct {
		Changed DryRunChangedRepository `json:"changed_repository"`
		Sibling []DryRunRepository      `json:"sibling_repositories"`
	}{report.ChangedRepository, report.SiblingRepositories}
	canonical, err := canonicalJSONBytes(value)
	if err != nil {
		return "", err
	}
	return sha256Hex(canonical), nil
}
