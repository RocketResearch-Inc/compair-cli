package baseline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanCreationProducesExactScannerInputAndHonorsActiveState(t *testing.T) {
	fixture := newRepositoryTestServer(t)
	toy := newScannerToy(t)
	state := t.TempDir()
	options := fixture.options(state)
	changed, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, toy.changedRoot, testRepositorySource, "changed", options)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, toy.siblingRoot, "", "sibling", options)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "plan with spaces.json")
	result, err := CreateLocalScanPlan(context.Background(), PlanCreateInput{GroupID: testRepositoryGroup, ChangedPath: toy.changedRoot, Base: toy.base, Head: toy.head, SiblingPaths: []string{toy.siblingRoot}, OutputPath: output}, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangedRepositoryRegistration != changed.RegistrationID || len(result.SiblingRepositoryRegistrations) != 1 || result.SiblingRepositoryRegistrations[0] != sibling.RegistrationID || result.PlanSHA256 == "" {
		t.Fatalf("plan result = %#v", result)
	}
	encodedResult, _ := json.Marshal(result)
	if strings.Contains(string(encodedResult), toy.changedRoot) || strings.Contains(string(encodedResult), toy.siblingRoot) {
		t.Fatalf("safe result leaked local path: %s", encodedResult)
	}
	input, err := LoadScanInput(output)
	if err != nil {
		t.Fatal(err)
	}
	if input.Changed.RepositoryID != changed.RegistrationID || input.Changed.RepositoryName != changed.RegistrationID || input.Changed.SourceDocumentID != testRepositorySource || input.Siblings[0].RepositoryID != sibling.RegistrationID || !input.DryRun || !input.JSON {
		t.Fatalf("scanner input = %#v", input)
	}
	scan, err := NewScanner().Scan(context.Background(), testRepositoryGroup, input)
	if err != nil {
		t.Fatal(err)
	}
	scan.ClearProtected()
	if _, err := CreateLocalScanPlan(context.Background(), PlanCreateInput{GroupID: testRepositoryGroup, ChangedPath: toy.changedRoot, Base: toy.base, Head: toy.head, SiblingPaths: []string{toy.siblingRoot}, OutputPath: output}, options); err == nil || SafeRepositoryReason(err) != "output_exists_requires_overwrite" {
		t.Fatalf("implicit overwrite accepted: %v", err)
	}
	if _, err := SetRepositoryRegistrationState(context.Background(), testRepositoryGroup, sibling.RegistrationID, false, options); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocalScanPlan(context.Background(), PlanCreateInput{GroupID: testRepositoryGroup, ChangedPath: toy.changedRoot, Base: toy.base, Head: toy.head, SiblingPaths: []string{toy.siblingRoot}, OutputPath: output, Overwrite: true}, options); err == nil || SafeRepositoryReason(err) != "repository_registration_disabled" {
		t.Fatalf("disabled sibling accepted: %v", err)
	}
	if _, err := SetRepositoryRegistrationState(context.Background(), testRepositoryGroup, sibling.RegistrationID, true, options); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocalScanPlan(context.Background(), PlanCreateInput{GroupID: testRepositoryGroup, ChangedPath: toy.changedRoot, Base: toy.base, Head: toy.head, SiblingPaths: []string{toy.siblingRoot}, OutputPath: output, Overwrite: true}, options); err != nil {
		t.Fatalf("reactivated plan failed: %v", err)
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("plan permissions = %v, %v", info, err)
	}
}

func TestPlanRejectsWrongGroupDuplicatesRoleConflictsAndMissingSource(t *testing.T) {
	fixture := newRepositoryTestServer(t)
	toy := newScannerToy(t)
	state := t.TempDir()
	options := fixture.options(state)
	changed, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, toy.changedRoot, "", "", options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, toy.siblingRoot, "", "", options); err != nil {
		t.Fatal(err)
	}
	baseInput := PlanCreateInput{GroupID: testRepositoryGroup, ChangedPath: toy.changedRoot, Base: toy.base, Head: toy.head, SiblingPaths: []string{toy.siblingRoot}, OutputPath: filepath.Join(t.TempDir(), "plan.json")}
	if _, err := CreateLocalScanPlan(context.Background(), baseInput, options); err == nil || SafeRepositoryReason(err) != "changed_repository_source_document_required" {
		t.Fatalf("missing source accepted: %v", err)
	}
	store, _ := newRepositoryBindingStore(state)
	bindings, _ := store.loadAll()
	for index := range bindings {
		if bindings[index].RegistrationID == changed.RegistrationID {
			bindings[index].SourceDocumentID = nullableString(testRepositorySource)
			bindings[index].UpdatedAt = timestamp(store.now())
			if err := store.save(&bindings[index]); err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			fixture.byRegistration(changed.RegistrationID).SourceDocument = nullableString(testRepositorySource)
			fixture.mu.Unlock()
		}
	}
	secondCopy := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(secondCopy, "copy.txt"), []byte("copy\n"), 0o644)
	gitTest(t, secondCopy, "add", "--", "copy.txt")
	gitTest(t, secondCopy, "commit", "-m", "copy")
	siblingRegistration := ""
	for _, binding := range bindings {
		if binding.RegistrationID != changed.RegistrationID {
			siblingRegistration = binding.RegistrationID
		}
	}
	if _, err := BindLocalRepository(context.Background(), testRepositoryGroup, siblingRegistration, secondCopy, "second copy", options); err != nil {
		t.Fatal(err)
	}
	baseInput.SiblingPaths = []string{toy.siblingRoot, secondCopy}
	if _, err := CreateLocalScanPlan(context.Background(), baseInput, options); err == nil || SafeRepositoryReason(err) != "repository_role_conflict" {
		t.Fatalf("registration reuse accepted: %v", err)
	}
	if _, err := BindLocalRepository(context.Background(), testRepositoryGroup, siblingRegistration, toy.changedRoot, "wrong", options); err == nil || SafeRepositoryReason(err) != "path_already_bound" {
		t.Fatalf("wrong registration/path accepted: %v", err)
	}
	baseInput.SiblingPaths = []string{toy.siblingRoot, toy.siblingRoot}
	if _, err := CreateLocalScanPlan(context.Background(), baseInput, options); err == nil || SafeRepositoryReason(err) != "duplicate_repository_path" {
		t.Fatalf("duplicate sibling accepted: %v", err)
	}
	baseInput.SiblingPaths = []string{toy.changedRoot}
	if _, err := CreateLocalScanPlan(context.Background(), baseInput, options); err == nil || SafeRepositoryReason(err) != "duplicate_repository_path" {
		t.Fatalf("role conflict accepted: %v", err)
	}
	baseInput.GroupID = "00000000-0000-4000-8000-000000000199"
	baseInput.SiblingPaths = []string{toy.siblingRoot}
	if _, err := CreateLocalScanPlan(context.Background(), baseInput, options); err == nil || SafeRepositoryReason(err) != "binding_group_mismatch" {
		t.Fatalf("wrong group accepted: %v", err)
	}
}

func TestProtectedPlanNoReplaceIsAtomicUnderRace(t *testing.T) {
	output := filepath.Join(t.TempDir(), "plan.json")
	start := make(chan struct{})
	errors := make(chan error, 2)
	for _, value := range [][]byte{[]byte("one\n"), []byte("two\n")} {
		value := value
		go func() {
			<-start
			_, err := writeProtectedPlan(output, value, false)
			errors <- err
		}()
	}
	close(start)
	first, second := <-errors, <-errors
	if (first == nil) == (second == nil) {
		t.Fatalf("atomic no-replace results = %v, %v", first, second)
	}
	failure := first
	if failure == nil {
		failure = second
	}
	if SafeRepositoryReason(failure) != "output_exists_requires_overwrite" {
		t.Fatalf("race failure = %v", failure)
	}
	value, err := os.ReadFile(output)
	if err != nil || (string(value) != "one\n" && string(value) != "two\n") {
		t.Fatalf("atomic output = %q, %v", value, err)
	}
}
