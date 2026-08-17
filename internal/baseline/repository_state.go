package baseline

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	repositoryBindingSchemaVersion = "baseline-local-repository-binding.v1"
	repositoryBindingFileLimit     = 32_000
	repositoryLockStaleAfter       = 10 * time.Minute
)

type RepositoryBinding struct {
	SchemaVersion            string  `json:"schema_version"`
	BindingID                string  `json:"binding_id"`
	GroupID                  string  `json:"group_id"`
	RegistrationID           string  `json:"registration_id"`
	RepositoryUID            string  `json:"repository_uid"`
	IdentityDescriptorSHA256 string  `json:"identity_descriptor_sha256"`
	DisplayName              string  `json:"display_name,omitempty"`
	CanonicalPath            string  `json:"canonical_path"`
	PathSHA256               string  `json:"path_sha256"`
	GitSanitySHA256          string  `json:"git_sanity_sha256"`
	GitObjectFormat          string  `json:"git_object_format"`
	SourceDocumentID         *string `json:"source_document_id"`
	CreatedAt                string  `json:"created_at"`
	UpdatedAt                string  `json:"updated_at"`
	IntegrityHMACSHA256      string  `json:"integrity_hmac_sha256"`
}

type repositoryBindingStore struct {
	directory string
	secret    []byte
	now       func() time.Time
}

func newRepositoryBindingStore(override string) (*repositoryBindingStore, error) {
	directory := strings.TrimSpace(override)
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, repositoryError(RepositoryFailureInternal, "state_directory_unavailable")
		}
		base := filepath.Join(home, ".compair")
		if err := ensureProtectedDirectory(base); err != nil {
			return nil, repositoryError(RepositoryFailureContract, "unsafe_state_path")
		}
		stateRoot := filepath.Join(base, "state")
		if err := ensureProtectedDirectory(stateRoot); err != nil {
			return nil, repositoryError(RepositoryFailureContract, "unsafe_state_path")
		}
		directory = filepath.Join(stateRoot, "baseline-repositories")
	}
	if err := ensureProtectedDirectory(directory); err != nil {
		return nil, repositoryError(RepositoryFailureContract, "unsafe_state_path")
	}
	secret, err := loadOrCreateInstallSecret(filepath.Join(filepath.Dir(directory), "baseline-upload-install-secret.v1"))
	if err != nil {
		return nil, repositoryError(RepositoryFailureContract, "install_secret_unavailable")
	}
	return &repositoryBindingStore{directory: directory, secret: secret, now: time.Now}, nil
}

func (store *repositoryBindingStore) withLock(ctx context.Context, operation func() error) error {
	if store == nil || len(store.secret) != installSecretBytes {
		return repositoryError(RepositoryFailureInternal, "binding_store_unavailable")
	}
	lockPath := filepath.Join(store.directory, ".bindings.lock")
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			defer func() { _ = os.Remove(lockPath) }()
			return operation()
		}
		if !errors.Is(err, os.ErrExist) {
			return repositoryError(RepositoryFailureInternal, "binding_lock_unavailable")
		}
		info, statErr := os.Lstat(lockPath)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return repositoryError(RepositoryFailureContract, "unsafe_binding_lock")
		}
		if time.Since(info.ModTime()) > repositoryLockStaleAfter {
			if removeErr := os.Remove(lockPath); removeErr == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			return repositoryError(RepositoryFailureRetryable, "binding_lock_timeout")
		}
		select {
		case <-ctx.Done():
			return repositoryError(RepositoryFailureRetryable, "binding_lock_cancelled")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (store *repositoryBindingStore) loadAll() ([]RepositoryBinding, error) {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return nil, repositoryError(RepositoryFailureInternal, "binding_state_unavailable")
	}
	bindings := make([]RepositoryBinding, 0)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filename := filepath.Join(store.directory, entry.Name())
		info, statErr := os.Lstat(filename)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !privateFilePermissions(info) || info.Size() > repositoryBindingFileLimit {
			return nil, repositoryError(RepositoryFailureContract, "unsafe_or_corrupt_binding_state")
		}
		value, readErr := os.ReadFile(filename)
		if readErr != nil {
			return nil, repositoryError(RepositoryFailureInternal, "binding_state_unavailable")
		}
		var binding RepositoryBinding
		if decodeErr := decodeStrictResponseJSON(value, &binding); decodeErr != nil {
			return nil, repositoryError(RepositoryFailureContract, "corrupt_binding_state")
		}
		provided := binding.IntegrityHMACSHA256
		binding.IntegrityHMACSHA256 = ""
		expected, macErr := store.bindingMAC(&binding)
		if macErr != nil || !hmac.Equal([]byte(provided), []byte(expected)) {
			return nil, repositoryError(RepositoryFailureContract, "corrupt_binding_state")
		}
		binding.IntegrityHMACSHA256 = provided
		if err := validateStoredBinding(binding, entry.Name()); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].BindingID < bindings[right].BindingID })
	return bindings, nil
}

func validateStoredBinding(binding RepositoryBinding, filename string) error {
	if binding.SchemaVersion != repositoryBindingSchemaVersion || !validUUID(binding.BindingID) || filename != binding.BindingID+".json" || !validGroupID(binding.GroupID) || !validUUID(binding.RegistrationID) || !repositoryUIDPattern.MatchString(binding.RepositoryUID) || !validSHA256(binding.IdentityDescriptorSHA256) || binding.CanonicalPath == "" || binding.PathSHA256 != sha256String(binding.CanonicalPath) || !validSHA256(binding.GitSanitySHA256) || (binding.GitObjectFormat != "sha1" && binding.GitObjectFormat != "sha256") || (binding.SourceDocumentID != nil && !validUUID(*binding.SourceDocumentID)) || binding.CreatedAt == "" || binding.UpdatedAt == "" || !validSHA256(binding.IntegrityHMACSHA256) {
		return repositoryError(RepositoryFailureContract, "corrupt_binding_state")
	}
	return nil
}

func (store *repositoryBindingStore) save(binding *RepositoryBinding) error {
	if store == nil || binding == nil {
		return repositoryError(RepositoryFailureInternal, "binding_store_unavailable")
	}
	binding.SchemaVersion = repositoryBindingSchemaVersion
	binding.IntegrityHMACSHA256 = ""
	macValue, err := store.bindingMAC(binding)
	if err != nil {
		return repositoryError(RepositoryFailureInternal, "binding_integrity_failed")
	}
	binding.IntegrityHMACSHA256 = macValue
	encoded, err := json.Marshal(binding)
	if err != nil || len(encoded) > repositoryBindingFileLimit {
		return repositoryError(RepositoryFailureInternal, "binding_encoding_failed")
	}
	target := filepath.Join(store.directory, binding.BindingID+".json")
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return repositoryError(RepositoryFailureContract, "unsafe_binding_state")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return repositoryError(RepositoryFailureInternal, "binding_write_failed")
	}
	temporary := filepath.Join(store.directory, fmt.Sprintf(".%s.%d.tmp", binding.BindingID, store.now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return repositoryError(RepositoryFailureInternal, "binding_write_failed")
	}
	writeErr := writeAndSync(file, encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return repositoryError(RepositoryFailureInternal, "binding_write_failed")
	}
	if err := replaceFileAtomically(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return repositoryError(RepositoryFailureInternal, "binding_write_failed")
	}
	if err := syncDirectory(store.directory); err != nil {
		return repositoryError(RepositoryFailureInternal, "binding_write_failed")
	}
	return nil
}

func (store *repositoryBindingStore) bindingMAC(binding *RepositoryBinding) (string, error) {
	canonical, err := canonicalJSONBytes(binding)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, store.secret)
	_, _ = mac.Write([]byte(repositoryBindingSchemaVersion + "\x00"))
	_, _ = mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func sha256String(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
