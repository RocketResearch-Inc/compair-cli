package baseline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const LocalPlanResultSchemaVersion = "baseline-local-scan-plan-result.v1"

var planRevisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@{}~^+-]{0,255}$`)

type PlanCreateInput struct {
	GroupID      string
	ChangedPath  string
	Base         string
	Head         string
	SiblingPaths []string
	OutputPath   string
	Overwrite    bool
}

type PlanCreateResult struct {
	SchemaVersion                  string   `json:"schema_version"`
	GroupID                        string   `json:"group_id"`
	ChangedRepositoryRegistration  string   `json:"changed_repository_registration_id"`
	SiblingRepositoryRegistrations []string `json:"sibling_repository_registration_ids"`
	BaseRevision                   string   `json:"base_revision"`
	HeadRevision                   string   `json:"head_revision"`
	OutputPathSHA256               string   `json:"output_path_sha256"`
	PlanSHA256                     string   `json:"plan_sha256"`
	CreatedAt                      string   `json:"created_at"`
}

type resolvedPlanRepository struct {
	binding  RepositoryBinding
	metadata RepositoryMetadata
	local    localRepositoryInfo
}

func CreateLocalScanPlan(ctx context.Context, input PlanCreateInput, options RepositoryOptions) (PlanCreateResult, error) {
	if !validGroupID(input.GroupID) || strings.TrimSpace(input.ChangedPath) == "" || !validPlanRevision(input.Base) || !validPlanRevision(input.Head) || len(input.SiblingPaths) < 1 || len(input.SiblingPaths) > MaxSiblingRepositories || strings.TrimSpace(input.OutputPath) == "" {
		return PlanCreateResult{}, repositoryError(RepositoryFailureUsage, "invalid_plan_input")
	}
	store, err := newRepositoryBindingStore(options.StateDirectory)
	if err != nil {
		return PlanCreateResult{}, err
	}
	bindings, err := store.loadAll()
	if err != nil {
		return PlanCreateResult{}, err
	}
	changed, err := resolvePlanRepository(ctx, input.GroupID, input.ChangedPath, bindings, options)
	if err != nil {
		return PlanCreateResult{}, err
	}
	if changed.metadata.SourceDocumentID == nil {
		return PlanCreateResult{}, repositoryError(RepositoryFailureContract, "changed_repository_source_document_required")
	}
	siblings := make([]resolvedPlanRepository, 0, len(input.SiblingPaths))
	seenPaths := map[string]struct{}{changed.local.CanonicalPath: {}}
	seenRegistrations := map[string]struct{}{changed.binding.RegistrationID: {}}
	for _, siblingPath := range input.SiblingPaths {
		sibling, resolveErr := resolvePlanRepository(ctx, input.GroupID, siblingPath, bindings, options)
		if resolveErr != nil {
			return PlanCreateResult{}, resolveErr
		}
		if _, duplicate := seenPaths[sibling.local.CanonicalPath]; duplicate {
			return PlanCreateResult{}, repositoryError(RepositoryFailureContract, "duplicate_repository_path")
		}
		if _, duplicate := seenRegistrations[sibling.binding.RegistrationID]; duplicate {
			return PlanCreateResult{}, repositoryError(RepositoryFailureContract, "repository_role_conflict")
		}
		seenPaths[sibling.local.CanonicalPath] = struct{}{}
		seenRegistrations[sibling.binding.RegistrationID] = struct{}{}
		siblings = append(siblings, sibling)
	}
	sort.Slice(siblings, func(left, right int) bool {
		return siblings[left].binding.RegistrationID < siblings[right].binding.RegistrationID
	})

	scanner := NewScanner()
	baseRevision, err := scanner.resolveCommit(ctx, changed.local.CanonicalPath, input.Base)
	if err != nil {
		return PlanCreateResult{}, repositoryError(RepositoryFailureRepository, "base_revision_unavailable")
	}
	headRevision, err := scanner.resolveCommit(ctx, changed.local.CanonicalPath, input.Head)
	if err != nil {
		return PlanCreateResult{}, repositoryError(RepositoryFailureRepository, "head_revision_unavailable")
	}
	if baseRevision == headRevision {
		return PlanCreateResult{}, repositoryError(RepositoryFailureContract, "distinct_revisions_required")
	}
	if err := scanner.requireAncestor(ctx, changed.local.CanonicalPath, baseRevision, headRevision); err != nil {
		return PlanCreateResult{}, repositoryError(RepositoryFailureRepository, "base_not_ancestor")
	}

	plan := ScanInput{
		Changed: ChangedRepositoryInput{
			SchemaVersion: "baseline-changed-repository-input.v1", LocalPath: changed.local.CanonicalPath,
			RepositoryID: changed.binding.RegistrationID, RepositoryName: changed.binding.RegistrationID,
			RepositoryRevision: headRevision, SourceDocumentID: *changed.metadata.SourceDocumentID,
		},
		BaseRevision: baseRevision, HeadRevision: headRevision, GroupID: input.GroupID, DryRun: true, JSON: true,
	}
	for _, sibling := range siblings {
		revision, resolveErr := resolveLocalHead(ctx, scanner, sibling.local.CanonicalPath)
		if resolveErr != nil {
			return PlanCreateResult{}, repositoryError(RepositoryFailureRepository, "sibling_revision_unavailable")
		}
		plan.Siblings = append(plan.Siblings, SiblingRepositoryInput{
			SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: sibling.local.CanonicalPath,
			RepositoryID: sibling.binding.RegistrationID, RepositoryName: sibling.binding.RegistrationID,
			RepositoryRevision: revision,
		})
	}
	if err := validateScanInput(input.GroupID, plan); err != nil {
		return PlanCreateResult{}, repositoryError(RepositoryFailureContract, "scanner_input_contract_failed")
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil || len(encoded)+1 > MaxControlRequest {
		return PlanCreateResult{}, repositoryError(RepositoryFailureContract, "plan_encoding_limit")
	}
	encoded = append(encoded, '\n')
	outputPath, err := writeProtectedPlan(input.OutputPath, encoded, input.Overwrite)
	if err != nil {
		return PlanCreateResult{}, err
	}
	siblingIDs := make([]string, len(siblings))
	for index, sibling := range siblings {
		siblingIDs[index] = sibling.binding.RegistrationID
	}
	return PlanCreateResult{
		SchemaVersion: LocalPlanResultSchemaVersion, GroupID: input.GroupID,
		ChangedRepositoryRegistration:  changed.binding.RegistrationID,
		SiblingRepositoryRegistrations: siblingIDs, BaseRevision: baseRevision, HeadRevision: headRevision,
		OutputPathSHA256: sha256String(outputPath), PlanSHA256: sha256Bytes(encoded), CreatedAt: timestamp(time.Now()),
	}, nil
}

func resolveLocalHead(ctx context.Context, scanner *Scanner, localPath string) (string, error) {
	output, err := scanner.gitOutput(ctx, localPath, maximumGitScalarBytes, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(output))
	if !validGitRevision(revision) {
		return "", repositoryError(RepositoryFailureContract, "repository_head_invalid")
	}
	return revision, nil
}

func resolvePlanRepository(ctx context.Context, groupID, localPath string, bindings []RepositoryBinding, options RepositoryOptions) (resolvedPlanRepository, error) {
	local, err := inspectLocalRepository(ctx, localPath)
	if err != nil {
		return resolvedPlanRepository{}, err
	}
	var selected *RepositoryBinding
	wrongGroup := false
	for index := range bindings {
		binding := &bindings[index]
		if binding.CanonicalPath != local.CanonicalPath {
			continue
		}
		if binding.GroupID != groupID {
			wrongGroup = true
			continue
		}
		if selected != nil {
			return resolvedPlanRepository{}, repositoryError(RepositoryFailureContract, "duplicate_local_binding")
		}
		selected = binding
	}
	if selected == nil {
		if wrongGroup {
			return resolvedPlanRepository{}, repositoryError(RepositoryFailureAuthentication, "binding_group_mismatch")
		}
		return resolvedPlanRepository{}, repositoryError(RepositoryFailureContract, "repository_binding_missing")
	}
	if selected.GitSanitySHA256 != local.SanitySHA256 || selected.GitObjectFormat != local.GitObjectFormat || selected.PathSHA256 != local.PathSHA256 {
		return resolvedPlanRepository{}, repositoryError(RepositoryFailureContract, "repository_binding_sanity_mismatch")
	}
	inspection, err := InspectRepositoryRegistration(ctx, groupID, selected.RegistrationID, options)
	if err != nil {
		return resolvedPlanRepository{}, err
	}
	metadata := inspection.Repository
	if metadata.State != "active" {
		return resolvedPlanRepository{}, repositoryError(RepositoryFailureAuthentication, "repository_registration_disabled")
	}
	if metadata.IdentityDescriptor.Version != RepositoryIdentityVersion || metadata.IdentityDescriptor.Authority != LocalRepositoryAuthority || metadata.IdentityDescriptor.RepositoryUID != selected.RepositoryUID || metadata.IdentityDescriptorSHA256 != selected.IdentityDescriptorSHA256 || stringPointerValue(metadata.SourceDocumentID) != stringPointerValue(selected.SourceDocumentID) {
		return resolvedPlanRepository{}, repositoryError(RepositoryFailureAuthentication, "repository_binding_authority_mismatch")
	}
	return resolvedPlanRepository{binding: *selected, metadata: metadata, local: local}, nil
}

func validPlanRevision(value string) bool {
	return planRevisionPattern.MatchString(strings.TrimSpace(value))
}

func writeProtectedPlan(requestedPath string, value []byte, overwrite bool) (string, error) {
	abs, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", repositoryError(RepositoryFailureContract, "output_path_invalid")
	}
	parent := filepath.Dir(abs)
	parentInfo, err := os.Lstat(parent)
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return "", repositoryError(RepositoryFailureContract, "output_directory_unsafe")
	}
	if info, statErr := os.Lstat(abs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", repositoryError(RepositoryFailureContract, "output_path_unsafe")
		}
		if !overwrite {
			return "", repositoryError(RepositoryFailureContract, "output_exists_requires_overwrite")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", repositoryError(RepositoryFailureContract, "output_path_unavailable")
	}
	temporary := filepath.Join(parent, fmt.Sprintf(".%s.%d.tmp", filepath.Base(abs), time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", repositoryError(RepositoryFailureInternal, "output_write_failed")
	}
	writeErr := writeAndSync(file, value)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return "", repositoryError(RepositoryFailureInternal, "output_write_failed")
	}
	if overwrite {
		err = replaceFileAtomically(temporary, abs)
	} else {
		err = installFileAtomicallyNoReplace(temporary, abs)
	}
	if err != nil {
		_ = os.Remove(temporary)
		if errors.Is(err, os.ErrExist) {
			return "", repositoryError(RepositoryFailureContract, "output_exists_requires_overwrite")
		}
		return "", repositoryError(RepositoryFailureInternal, "output_write_failed")
	}
	if err := syncDirectory(parent); err != nil {
		return "", repositoryError(RepositoryFailureInternal, "output_write_failed")
	}
	return abs, nil
}
