package local

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestOpaqueIdentitiesAreStableDomainSeparatedAndStoreLocal(t *testing.T) {
	first := openStorageTestStore(t)
	second := openStorageTestStore(t)

	scope, err := first.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatalf("DeriveProjectScope() error = %v", err)
	}
	repeated, err := first.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatalf("DeriveProjectScope() repeat error = %v", err)
	}
	if scope.ID == repeated.ID {
		t.Fatal("different paths produced the same project scope")
	}

	command, err := first.DeriveCommandSignature("  pnpm test --filter api  ")
	if err != nil {
		t.Fatalf("DeriveCommandSignature() error = %v", err)
	}
	commandAgain, err := first.DeriveCommandSignature("pnpm test --filter api")
	if err != nil {
		t.Fatalf("DeriveCommandSignature() repeat error = %v", err)
	}
	if command != commandAgain {
		t.Fatalf("trim-equivalent commands differ: %q != %q", command, commandAgain)
	}
	differentTarget, err := first.DeriveCommandSignature("pnpm test --filter web")
	if err != nil {
		t.Fatalf("DeriveCommandSignature() target error = %v", err)
	}
	if command == differentTarget {
		t.Fatal("different positional targets produced the same signature")
	}

	fingerprint, issueID, err := first.DeriveIssueIdentity(
		"issue.v1", "explicit_command_failure", scope.ID, command,
	)
	if err != nil {
		t.Fatalf("DeriveIssueIdentity() error = %v", err)
	}
	if !strings.HasPrefix(fingerprint, "ifp_") || !strings.HasPrefix(issueID, "iss_") ||
		strings.TrimPrefix(fingerprint, "ifp_") != strings.TrimPrefix(issueID, "iss_") {
		t.Fatalf("issue identity = %q / %q", fingerprint, issueID)
	}
	otherStore, _, err := second.DeriveIssueIdentity(
		"issue.v1", "explicit_command_failure", scope.ID, command,
	)
	if err != nil {
		t.Fatalf("second DeriveIssueIdentity() error = %v", err)
	}
	if fingerprint == otherStore {
		t.Fatal("independent stores produced linkable issue fingerprints")
	}

	projectKey, err := first.derivedKey(projectScopeKeyDomain)
	if err != nil {
		t.Fatalf("derive project key: %v", err)
	}
	commandKey, err := first.derivedKey(commandSignatureKeyDomain)
	if err != nil {
		t.Fatalf("derive command key: %v", err)
	}
	if string(projectKey) == string(commandKey) {
		t.Fatal("identity domains produced the same derived key")
	}
	zeroBytes(projectKey)
	zeroBytes(commandKey)
}

func TestRecurrenceIdentitiesAreStableAndDomainSeparated(t *testing.T) {
	store := openStorageTestStore(t)
	annotationID, err := store.deriveFixAnnotationID(
		"00000000-0000-4000-8000-000000040001",
	)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := store.deriveFixRecurrenceID(annotationID, "occ_stable")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.deriveFixRecurrenceID(annotationID, "occ_stable")
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.deriveFixRecurrenceJobID("session-stable", 7)
	if err != nil {
		t.Fatal(err)
	}
	if observation != repeated ||
		!validOpaquePrefixedID(observation, "fxo_") ||
		!validOpaquePrefixedID(job, "fxj_") ||
		observation == job {
		t.Fatalf("recurrence identities = %q / %q / %q",
			observation, repeated, job)
	}
	observationKey, err := store.derivedKey(fixRecurrenceIDKeyDomain)
	if err != nil {
		t.Fatal(err)
	}
	jobKey, err := store.derivedKey(fixRecurrenceJobIDKeyDomain)
	if err != nil {
		zeroBytes(observationKey)
		t.Fatal(err)
	}
	defer zeroBytes(observationKey)
	defer zeroBytes(jobKey)
	if string(observationKey) == string(jobKey) {
		t.Fatal("recurrence observation and job domains share a key")
	}
}

func TestAttentionFamilyIdentityIsStableDomainSeparatedAndStoreLocal(t *testing.T) {
	first := openStorageTestStore(t)
	second := openStorageTestStore(t)

	id, err := first.DeriveAttentionFamilyID(
		"mapped:attention.agent_guardrails_configuration:1:1:1",
		"belay.attention-families.v1",
		"1",
	)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := first.DeriveAttentionFamilyID(
		"mapped:attention.agent_guardrails_configuration:1:1:1",
		"belay.attention-families.v1",
		"1",
	)
	if err != nil {
		t.Fatal(err)
	}
	otherStore, err := second.DeriveAttentionFamilyID(
		"mapped:attention.agent_guardrails_configuration:1:1:1",
		"belay.attention-families.v1",
		"1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != repeated ||
		!validOpaquePrefixedID(id, "atf_") ||
		id == otherStore {
		t.Fatalf("family identities = %q / %q / %q", id, repeated, otherStore)
	}

	familyKey, err := first.derivedKey(attentionFamilyKeyDomain)
	if err != nil {
		t.Fatal(err)
	}
	issueKey, err := first.derivedKey(issueFingerprintKeyDomain)
	if err != nil {
		zeroBytes(familyKey)
		t.Fatal(err)
	}
	defer zeroBytes(familyKey)
	defer zeroBytes(issueKey)
	if string(familyKey) == string(issueKey) {
		t.Fatal("family and exact issue identities share a key domain")
	}
}

func TestProjectPathNormalization(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	got, quality, err := normalizeProjectPath(link + string(filepath.Separator))
	if err != nil {
		t.Fatalf("normalizeProjectPath() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if runtime.GOOS == "darwin" {
		want = strings.TrimPrefix(want, "/private")
	}
	if got != filepath.ToSlash(want) || quality != "resolved" {
		t.Fatalf("normalized path = (%q, %q), want (%q, resolved)", got, quality, want)
	}

	missing := filepath.Join(root, "missing", "..", "project")
	got, quality, err = normalizeProjectPath(missing)
	if err != nil {
		t.Fatalf("normalize missing path error = %v", err)
	}
	if got != filepath.ToSlash(filepath.Clean(missing)) || quality != "lexical" {
		t.Fatalf("lexical path = (%q, %q)", got, quality)
	}
	for _, invalid := range []string{"", "relative/project", "/tmp/bad\x00path"} {
		if _, _, err := normalizeProjectPath(invalid); err == nil {
			t.Errorf("normalizeProjectPath(%q) unexpectedly succeeded", invalid)
		}
	}
	if runtime.GOOS == "darwin" {
		got, _, err = normalizeProjectPath("/private/var/nonexistent-belay-path")
		if err != nil {
			t.Fatalf("normalize Darwin alias error = %v", err)
		}
		if !strings.HasPrefix(got, "/var/") {
			t.Fatalf("Darwin alias = %q, want /var prefix", got)
		}
	}
}

func TestNumbatProjectScopeHashIsValidatedDomainSeparatedAndStoreLocal(t *testing.T) {
	first := openStorageTestStore(t)
	second := openStorageTestStore(t)
	rawHash := strings.Repeat("ab", 32)

	scope, err := first.DeriveNumbatProjectScopeHash(rawHash)
	if err != nil {
		t.Fatalf("DeriveNumbatProjectScopeHash() error = %v", err)
	}
	repeated, err := first.DeriveNumbatProjectScopeHash(rawHash)
	if err != nil {
		t.Fatalf("DeriveNumbatProjectScopeHash() repeat error = %v", err)
	}
	if scope != repeated {
		t.Fatalf("repeat scope = %+v, want %+v", repeated, scope)
	}
	if !validProjectScopeHint(scope.ID) ||
		scope.Quality != model.ScopeLexical ||
		scope.NormalizationVersion != numbatProjectScopeHashVersion {
		t.Fatalf("derived Numbat scope = %+v", scope)
	}
	otherStore, err := second.DeriveNumbatProjectScopeHash(rawHash)
	if err != nil {
		t.Fatalf("second DeriveNumbatProjectScopeHash() error = %v", err)
	}
	if otherStore.ID == scope.ID {
		t.Fatal("independent stores produced linkable Numbat project scopes")
	}

	numbatKey, err := first.derivedKey(numbatProjectScopeHashKeyDomain)
	if err != nil {
		t.Fatalf("derive Numbat project hash key: %v", err)
	}
	projectKey, err := first.derivedKey(projectScopeKeyDomain)
	if err != nil {
		zeroBytes(numbatKey)
		t.Fatalf("derive project path key: %v", err)
	}
	if string(numbatKey) == string(projectKey) {
		t.Fatal("Numbat hash and project path domains produced the same key")
	}
	zeroBytes(numbatKey)
	zeroBytes(projectKey)

	for _, invalid := range []string{
		"",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("AB", 32),
		"sha256:" + rawHash,
		rawHash + "\n",
		strings.Repeat("g0", 32),
	} {
		if _, err := first.DeriveNumbatProjectScopeHash(invalid); err == nil {
			t.Errorf("DeriveNumbatProjectScopeHash(%q) unexpectedly succeeded", invalid)
		} else if strings.Contains(err.Error(), invalid) && invalid != "" {
			t.Errorf("error disclosed invalid hash input: %v", err)
		}
	}
}

func TestIdentityInputsNeverReachDatabaseBytes(t *testing.T) {
	const (
		pathCanary    = "PRIVATE_PROJECT_PATH_CANARY_92ea"
		commandCanary = "SECRET_COMMAND_CANARY_71bf"
	)
	databasePath := filepath.Join(t.TempDir(), "belay.sqlite")
	store, err := Open(databasePath, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	scope, err := store.DeriveProjectScope(filepath.Join(t.TempDir(), pathCanary))
	if err != nil {
		t.Fatalf("DeriveProjectScope() error = %v", err)
	}
	signature, err := store.DeriveCommandSignature("tool --token=" + commandCanary)
	if err != nil {
		t.Fatalf("DeriveCommandSignature() error = %v", err)
	}
	if _, _, err := store.DeriveIssueIdentity("v1", "detector", scope.ID, signature); err != nil {
		t.Fatalf("DeriveIssueIdentity() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertSQLiteFilesExclude(t, databasePath, pathCanary, commandCanary)
}
