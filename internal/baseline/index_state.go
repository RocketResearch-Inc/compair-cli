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
	indexStateSchemaVersion = "baseline-index-resume.v2"
	indexStateFileLimit     = 128_000
)

type indexState struct {
	SchemaVersion                  string `json:"schema_version"`
	GroupID                        string `json:"group_id"`
	ProtocolVersion                string `json:"protocol_version"`
	ProtocolSHA256                 string `json:"protocol_sha256"`
	ScanFingerprint                string `json:"scan_fingerprint"`
	CorpusID                       string `json:"corpus_id"`
	CorpusGenerationID             string `json:"corpus_generation_id"`
	CorpusGenerationVersion        string `json:"corpus_generation_version"`
	IngestionContinuationID        string `json:"ingestion_continuation_id"`
	CanonicalManifestHash          string `json:"canonical_manifest_hash"`
	ContentManifestHash            string `json:"content_manifest_hash"`
	CorpusManifestHash             string `json:"corpus_manifest_hash"`
	IngestionProvenanceFingerprint string `json:"ingestion_provenance_fingerprint"`
	IndexIntentFingerprint         string `json:"index_intent_fingerprint"`
	RetrievalConfigFingerprint     string `json:"retrieval_config_fingerprint"`
	IndexJobID                     string `json:"index_job_id,omitempty"`
	IndexPublicationID             string `json:"index_publication_id,omitempty"`
	SafeState                      string `json:"safe_state"`
	CreatedAt                      string `json:"created_at"`
	UpdatedAt                      string `json:"updated_at"`
	IntegrityHMACSHA256            string `json:"integrity_hmac_sha256"`
}

type indexStateStore struct {
	directory string
	secret    []byte
}

func newIndexStateStore(override string) (*indexStateStore, error) {
	directory := strings.TrimSpace(override)
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, indexError(IndexFailureInternal, "state_directory_unavailable")
		}
		base := filepath.Join(home, ".compair")
		if err := ensureProtectedDirectory(base); err != nil {
			return nil, asIndexStateError(err)
		}
		stateRoot := filepath.Join(base, "state")
		if err := ensureProtectedDirectory(stateRoot); err != nil {
			return nil, asIndexStateError(err)
		}
		directory = filepath.Join(stateRoot, "baseline-indexes")
	}
	if err := ensureProtectedDirectory(directory); err != nil {
		return nil, asIndexStateError(err)
	}
	secret, err := loadOrCreateInstallSecret(filepath.Join(filepath.Dir(directory), "baseline-upload-install-secret.v1"))
	if err != nil {
		return nil, asIndexStateError(err)
	}
	return &indexStateStore{directory: directory, secret: secret}, nil
}

func asIndexStateError(err error) error {
	var upload *UploadError
	if errors.As(err, &upload) {
		kind := IndexFailureInternal
		if upload.Kind == UploadFailureContract {
			kind = IndexFailureInput
		}
		return indexError(kind, upload.Reason)
	}
	return indexError(IndexFailureInternal, "state_store_unavailable")
}

func (store *indexStateStore) path(identity string) string {
	return filepath.Join(store.directory, identity+".json")
}

func (store *indexStateStore) save(identity string, state *indexState) error {
	if store == nil || state == nil || len(store.secret) != installSecretBytes {
		return indexError(IndexFailureInternal, "state_store_unavailable")
	}
	state.IntegrityHMACSHA256 = ""
	macValue, err := store.stateMAC(state)
	if err != nil {
		return indexError(IndexFailureInternal, "state_integrity_failed")
	}
	state.IntegrityHMACSHA256 = macValue
	encoded, err := canonicalJSONBytes(state)
	if err != nil || len(encoded) > indexStateFileLimit {
		return indexError(IndexFailureInternal, "state_encoding_failed")
	}
	target := store.path(identity)
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) {
			return indexError(IndexFailureInput, "unsafe_state_path")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return indexError(IndexFailureInternal, "state_write_failed")
	}
	temporary := filepath.Join(store.directory, fmt.Sprintf(".%s.%d.tmp", identity, time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return indexError(IndexFailureInternal, "state_write_failed")
	}
	writeErr := writeAndSync(file, encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return indexError(IndexFailureInternal, "state_write_failed")
	}
	if err := replaceFileAtomically(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return indexError(IndexFailureInternal, "state_write_failed")
	}
	if err := syncDirectory(store.directory); err != nil {
		return indexError(IndexFailureInternal, "state_write_failed")
	}
	return nil
}

func (store *indexStateStore) load(identity string) (*indexState, error) {
	filename := store.path(identity)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) || info.Size() > indexStateFileLimit {
		return nil, indexError(IndexFailureInput, "unsafe_or_corrupt_resume_state")
	}
	value, err := os.ReadFile(filename)
	if err != nil {
		return nil, indexError(IndexFailureInternal, "resume_state_unavailable")
	}
	var state indexState
	if err := decodeStrictResponseJSON(value, &state); err != nil {
		return nil, indexError(IndexFailureInput, "corrupt_resume_state")
	}
	provided := state.IntegrityHMACSHA256
	state.IntegrityHMACSHA256 = ""
	expected, err := store.stateMAC(&state)
	if err != nil || !hmac.Equal([]byte(provided), []byte(expected)) {
		return nil, indexError(IndexFailureInput, "corrupt_resume_state")
	}
	state.IntegrityHMACSHA256 = provided
	return &state, nil
}

func (store *indexStateStore) delete(identity string) error {
	filename := store.path(identity)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return indexError(IndexFailureInput, "unsafe_state_path")
	}
	if err := os.Remove(filename); err != nil {
		return indexError(IndexFailureInternal, "state_cleanup_failed")
	}
	if err := syncDirectory(store.directory); err != nil {
		return indexError(IndexFailureInternal, "state_cleanup_failed")
	}
	return nil
}

func (store *indexStateStore) stateMAC(state *indexState) (string, error) {
	canonical, err := canonicalJSONBytes(state)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(indexStateSchemaVersion + "\x00"))
	_, _ = mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (store *indexStateStore) deriveHex(label, identity string) string {
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(identity))
	return hex.EncodeToString(mac.Sum(nil))
}

func (store *indexStateStore) deriveUUID(label, identity string) string {
	raw, _ := hex.DecodeString(store.deriveHex(label, identity)[:32])
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	value := hex.EncodeToString(raw)
	return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32]
}
