package baseline

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	runStateSchemaVersion = "baseline-run-resume.v1"
	runStateFileLimit     = 128_000
)

// runState intentionally contains provenance and safe status only. The raw
// retrieval query and all provider/evidence/finding content remain absent.
type runState struct {
	SchemaVersion                   string  `json:"schema_version"`
	GroupID                         string  `json:"group_id"`
	ProtocolVersion                 string  `json:"protocol_version"`
	ProtocolSHA256                  string  `json:"protocol_sha256"`
	ServerIdentitySHA256            string  `json:"server_identity_sha256"`
	ScanFingerprint                 string  `json:"scan_fingerprint"`
	SourceDocumentID                string  `json:"source_document_id"`
	ChangedRepositoryRegistrationID string  `json:"changed_repository_registration_id"`
	BaseRevision                    string  `json:"base_revision"`
	HeadRevision                    string  `json:"head_revision"`
	QuerySHA256                     string  `json:"query_sha256"`
	QueryLength                     int     `json:"query_length"`
	QueryByteSize                   int     `json:"query_byte_size"`
	CorpusGenerationID              string  `json:"corpus_generation_id"`
	CorpusManifestHash              string  `json:"corpus_manifest_hash"`
	IndexPublicationID              string  `json:"index_publication_id"`
	IndexFormatVersion              string  `json:"index_format_version"`
	TokenizerVersion                string  `json:"tokenizer_version"`
	RetrievalConfigFingerprint      string  `json:"retrieval_config_fingerprint"`
	EmbeddingFingerprint            string  `json:"embedding_fingerprint"`
	IndexFingerprint                string  `json:"index_fingerprint"`
	RunIntentFingerprint            string  `json:"run_intent_fingerprint"`
	RunJobID                        string  `json:"run_job_id,omitempty"`
	ProcessingRunID                 string  `json:"processing_run_id,omitempty"`
	PersistedRetrievalRunID         string  `json:"persisted_retrieval_run_id,omitempty"`
	SafeState                       string  `json:"safe_state"`
	CreatedAt                       string  `json:"created_at"`
	UpdatedAt                       string  `json:"updated_at"`
	CompletedAt                     *string `json:"completed_at"`
	IntegrityHMACSHA256             string  `json:"integrity_hmac_sha256"`
}

type runStateStore struct {
	directory string
	secret    []byte
}

func newRunStateStore(override string) (*runStateStore, error) {
	directory := strings.TrimSpace(override)
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, runError(RunFailureInternal, "state_directory_unavailable")
		}
		base := filepath.Join(home, ".compair")
		if err := ensureProtectedDirectory(base); err != nil {
			return nil, translateRunStateError(err)
		}
		root := filepath.Join(base, "state")
		if err := ensureProtectedDirectory(root); err != nil {
			return nil, translateRunStateError(err)
		}
		directory = filepath.Join(root, "baseline-runs")
	}
	if err := ensureProtectedDirectory(directory); err != nil {
		return nil, translateRunStateError(err)
	}
	secret, err := loadOrCreateInstallSecret(filepath.Join(filepath.Dir(directory), "baseline-upload-install-secret.v1"))
	if err != nil {
		return nil, translateRunStateError(err)
	}
	return &runStateStore{directory: directory, secret: secret}, nil
}

func translateRunStateError(err error) error {
	var upload *UploadError
	if errors.As(err, &upload) {
		kind := RunFailureInternal
		if upload.Kind == UploadFailureContract {
			kind = RunFailureInput
		}
		return runError(kind, upload.Reason)
	}
	return runError(RunFailureInternal, "state_store_unavailable")
}

func (store *runStateStore) path(identity string) string {
	return filepath.Join(store.directory, identity+".json")
}

func (store *runStateStore) save(identity string, state *runState) error {
	if store == nil || state == nil || len(store.secret) != installSecretBytes {
		return runError(RunFailureInternal, "state_store_unavailable")
	}
	state.IntegrityHMACSHA256 = ""
	macValue, err := store.stateMAC(state)
	if err != nil {
		return runError(RunFailureInternal, "state_integrity_failed")
	}
	state.IntegrityHMACSHA256 = macValue
	encoded, err := canonicalJSONBytes(state)
	if err != nil || len(encoded) > runStateFileLimit {
		return runError(RunFailureInternal, "state_encoding_failed")
	}
	target := store.path(identity)
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) {
			return runError(RunFailureInput, "unsafe_state_path")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return runError(RunFailureInternal, "state_write_failed")
	}
	temporary := filepath.Join(store.directory, fmt.Sprintf(".%s.%d.tmp", identity, time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return runError(RunFailureInternal, "state_write_failed")
	}
	writeErr := writeAndSync(file, encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return runError(RunFailureInternal, "state_write_failed")
	}
	if err := replaceFileAtomically(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return runError(RunFailureInternal, "state_write_failed")
	}
	if err := syncDirectory(store.directory); err != nil {
		return runError(RunFailureInternal, "state_write_failed")
	}
	return nil
}

func (store *runStateStore) load(identity string) (*runState, error) {
	filename := store.path(identity)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) || info.Size() <= 0 || info.Size() > runStateFileLimit {
		return nil, runError(RunFailureInput, "unsafe_or_corrupt_resume_state")
	}
	value, err := os.ReadFile(filename)
	if err != nil {
		return nil, runError(RunFailureInternal, "resume_state_unavailable")
	}
	var state runState
	if err := decodeStrictResponseJSON(value, &state); err != nil {
		return nil, runError(RunFailureInput, "corrupt_resume_state")
	}
	provided := state.IntegrityHMACSHA256
	state.IntegrityHMACSHA256 = ""
	expected, err := store.stateMAC(&state)
	if err != nil || !hmac.Equal([]byte(provided), []byte(expected)) {
		return nil, runError(RunFailureInput, "corrupt_resume_state")
	}
	state.IntegrityHMACSHA256 = provided
	return &state, nil
}

func (store *runStateStore) delete(identity string) error {
	filename := store.path(identity)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return runError(RunFailureInput, "unsafe_state_path")
	}
	if err := os.Remove(filename); err != nil {
		return runError(RunFailureInternal, "state_cleanup_failed")
	}
	if err := syncDirectory(store.directory); err != nil {
		return runError(RunFailureInternal, "state_cleanup_failed")
	}
	return nil
}

func (store *runStateStore) stateMAC(state *runState) (string, error) {
	canonical, err := canonicalJSONBytes(state)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(runStateSchemaVersion + "\x00"))
	_, _ = mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (store *runStateStore) deriveHex(label, identity string) string {
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(identity))
	return hex.EncodeToString(mac.Sum(nil))
}

func (store *runStateStore) deriveUUID(label, identity string) string {
	raw, _ := hex.DecodeString(store.deriveHex(label, identity)[:32])
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	value := hex.EncodeToString(raw)
	return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32]
}
