package localapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type missionPackTestRepository struct {
	issueProject missionpack.ResolvedProject
	cwdProject   missionpack.ResolvedProject
	evidence     missionpack.EvidenceSnapshot
	selectors    []missionpack.ProjectSelector
	limits       missionpack.Limits
	readCalls    int
}

var _ MissionPackPreviewRepository = (*local.Store)(nil)

func (r *missionPackTestRepository) ResolveMissionPackProject(
	_ context.Context,
	selector missionpack.ProjectSelector,
) (missionpack.ResolvedProject, error) {
	r.selectors = append(r.selectors, selector)
	if selector.IssueID != "" {
		return r.issueProject, nil
	}
	return r.cwdProject, nil
}

func (r *missionPackTestRepository) ReadMissionPackEvidence(
	_ context.Context,
	_ string,
	limits missionpack.Limits,
) (missionpack.EvidenceSnapshot, error) {
	r.readCalls++
	r.limits = limits
	return r.evidence, nil
}

type missionPackTestExperienceSelector struct {
	result       ExperienceSelectionResult
	err          error
	requests     []ExperienceSelectionRequest
	compileCalls int
}

type missionPackPreviewCall struct {
	packID          string
	projectIdentity string
	harness         experience.Harness
	generation      int64
	refs            []experience.ExperienceRef
	taskHintHash    string
	generatedAt     time.Time
	expiresAt       time.Time
}

type missionPackTestPreviewRepository struct {
	calls []missionPackPreviewCall
	err   error
}

func (r *missionPackTestPreviewRepository) RegisterMissionPackPreview(
	_ context.Context,
	packID string,
	projectIdentity string,
	harness experience.Harness,
	generation int64,
	refs []experience.ExperienceRef,
	taskHintHash string,
	generatedAt time.Time,
	expiresAt time.Time,
) error {
	r.calls = append(r.calls, missionPackPreviewCall{
		packID:          packID,
		projectIdentity: projectIdentity,
		harness:         harness,
		generation:      generation,
		refs:            append([]experience.ExperienceRef(nil), refs...),
		taskHintHash:    taskHintHash,
		generatedAt:     generatedAt,
		expiresAt:       expiresAt,
	})
	return r.err
}

func (s *missionPackTestExperienceSelector) Select(
	_ context.Context,
	request ExperienceSelectionRequest,
) (ExperienceSelectionResult, error) {
	s.requests = append(s.requests, request)
	return s.result, s.err
}

func (s *missionPackTestExperienceSelector) Compile(
	_ context.Context,
	_ string,
) (local.CompileExperienceGenerationResult, error) {
	s.compileCalls++
	return local.CompileExperienceGenerationResult{}, nil
}

func TestMissionPackServiceGeneratesFromBoundedEvidenceAndWorkspace(
	t *testing.T,
) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	runMissionPackTestCommand(
		t,
		root,
		"git",
		"remote",
		"add",
		"origin",
		"https://user:secret@example.test/team/project.git?token=private",
	)
	if err := os.WriteFile(
		filepath.Join(root, "package.json"),
		[]byte(`{
			"packageManager": "pnpm@9.12.0",
			"scripts": {"check": "tsc --noEmit && eslint ."}
		}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "pnpm-lock.yaml"),
		[]byte("lockfileVersion: '9.0'\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     "https://example.test/team/project.git",
			IdentityKind: "remote",
			Path:         root,
		},
		evidence: missionpack.EvidenceSnapshot{
			SourceState: missionpack.SourceState{
				TranscriptGeneration: 4,
				AnalyzedGeneration:   4,
				AnalysisStatus:       missionpack.AnalysisStatusCurrent,
				DataThrough:          now,
			},
			Sessions: []missionpack.EvidenceSession{{
				SessionKey:  "ses_one",
				Harness:     "codex",
				ProjectPath: root,
				Coverage:    missionpack.TranscriptCoverageComplete,
			}},
			SuccessfulCommands: []missionpack.SuccessfulCommand{{
				Command:     "pnpm run check",
				SucceededAt: now,
				Source: missionpack.SourceRef{
					Kind:       "transcript_turn",
					SessionKey: "ses_one",
				},
			}},
			Coverage: missionpack.Coverage{
				TranscriptStatus:          missionpack.TranscriptCoverageComplete,
				CanonicalContextAvailable: false,
			},
		},
	}
	service, err := NewMissionPackService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	pack, err := service.Generate(context.Background(), missionpack.Request{
		CWD:     root,
		Intent:  missionpack.IntentImplement,
		Harness: missionpack.HarnessCodex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.selectors) != 1 ||
		repository.selectors[0].RemoteIdentity !=
			"https://example.test/team/project.git" {
		t.Fatalf("project selectors = %#v", repository.selectors)
	}
	if repository.limits.Sessions != 20 ||
		repository.limits.CommandTurns != 500 ||
		repository.limits.Events != 500 {
		t.Fatalf("evidence limits = %#v", repository.limits)
	}
	if pack.Project.Label != "project" ||
		pack.Project.Branch != "main" ||
		pack.Harness != missionpack.HarnessCodex ||
		len(pack.Verification) != 1 ||
		pack.Verification[0].Command != "pnpm run check" ||
		!pack.Verification[0].Configured ||
		!pack.Verification[0].Observed {
		t.Fatalf("generated pack = %#v", pack)
	}
}

func TestMissionPackServiceSelectsWithNormalizedPackStartInput(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     "project_one",
			IdentityKind: "path",
			Path:         root,
		},
	}
	selector := &missionPackTestExperienceSelector{}
	service, err := NewMissionPackService(
		repository,
		WithMissionPackExperienceSelector(selector),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	}
	_, err = service.Generate(context.Background(), missionpack.Request{
		CWD:      root,
		Intent:   missionpack.IntentImplement,
		Harness:  missionpack.HarnessCodex,
		TaskHint: "  update   the cache\nsafely  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ExperienceSelectionRequest{
		ProjectIdentity: "project_one",
		Harness:         experience.HarnessCodex,
		TaskFamily:      "implement",
		TaskHint:        "update the cache safely",
		RepositoryPaths: []string{},
	}
	if !reflect.DeepEqual(selector.requests, []ExperienceSelectionRequest{want}) {
		t.Fatalf("selection requests = %#v, want %#v", selector.requests, want)
	}
	if selector.compileCalls != 0 {
		t.Fatalf("Compile() calls = %d, want 0", selector.compileCalls)
	}
}

func TestMissionPackServiceNoGenerationAndEmptySelectionPreservePack(
	t *testing.T,
) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     "project_one",
			IdentityKind: "path",
			Path:         root,
		},
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	request := missionpack.Request{
		CWD:      root,
		Intent:   missionpack.IntentGeneral,
		Harness:  missionpack.HarnessClaude,
		TaskHint: "inspect the project",
	}
	baselineService, err := NewMissionPackService(repository)
	if err != nil {
		t.Fatal(err)
	}
	baselineService.now = func() time.Time { return now }
	baseline, err := baselineService.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		selector *missionPackTestExperienceSelector
	}{
		{
			name: "sql no rows",
			selector: &missionPackTestExperienceSelector{
				err: sql.ErrNoRows,
			},
		},
		{
			name: "empty selection",
			selector: &missionPackTestExperienceSelector{
				result: ExperienceSelectionResult{
					Generation: local.ExperienceGeneration{
						Generation: 19,
					},
					Selected: []SelectedExperience{},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewMissionPackService(
				repository,
				WithMissionPackExperienceSelector(test.selector),
			)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return now }
			got, err := service.Generate(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, baseline) {
				t.Fatalf(
					"pack changed without selected experience:\n got: %#v\nwant: %#v",
					got,
					baseline,
				)
			}
		})
	}
}

func TestMissionPackServiceMapsSelectedExperiencesInStableOrder(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	observedAt := time.Date(
		2026,
		9,
		10,
		11,
		30,
		0,
		0,
		time.FixedZone("fixture", -7*60*60),
	)
	turnIndex := int64(4)
	first := missionPackSelectedExperience(
		"exp_second",
		2,
		"candidate_second",
		experience.VerifierCommandSucceeded,
	)
	first.Experience.Evidence.Refs = []experience.EvidenceRef{
		{
			Kind:       experience.EvidenceWorkspaceHash,
			Path:       "generated/client.go",
			SHA256:     "sha256:workspace",
			OccurredAt: &observedAt,
			Excerpt:    "must not enter the Mission Pack",
		},
		{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "session_one",
			TurnIndex:  &turnIndex,
			OccurredAt: &observedAt,
			Excerpt:    "must not enter the Mission Pack",
		},
		{
			Kind:       experience.EvidenceCanonicalEvent,
			EventID:    "event_one",
			OccurredAt: &observedAt,
			Excerpt:    "must not enter the Mission Pack",
		},
	}
	second := missionPackSelectedExperience(
		"exp_first",
		1,
		"candidate_first",
		experience.VerifierCommandSucceeded,
	)
	selector := &missionPackTestExperienceSelector{
		result: ExperienceSelectionResult{
			Generation: local.ExperienceGeneration{
				Generation: 7,
			},
			Selected: []SelectedExperience{first, second},
		},
	}
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     "project_one",
			IdentityKind: "path",
			Path:         root,
		},
	}
	service, err := NewMissionPackService(
		repository,
		WithMissionPackExperienceSelector(selector),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	}
	pack, err := service.Generate(context.Background(), missionpack.Request{
		CWD:     root,
		Intent:  missionpack.IntentImplement,
		Harness: missionpack.HarnessCodex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pack.ExperienceGeneration != 7 ||
		len(pack.Experiences) != 2 ||
		pack.Experiences[0].ExperienceID != "exp_second" ||
		pack.Experiences[1].ExperienceID != "exp_first" {
		t.Fatalf("mapped experiences = %#v", pack.Experiences)
	}
	if pack.Experiences[0].Applicability !=
		"Use this workflow when changing project code." ||
		!reflect.DeepEqual(
			pack.Experiences[0].Exceptions,
			[]string{"Do not apply it to read-only review tasks."},
		) {
		t.Fatalf(
			"mapped experience boundary = %#v",
			pack.Experiences[0],
		)
	}
	sources := pack.Experiences[0].Sources
	if len(sources) != 2 {
		t.Fatalf("mapped sources = %#v, want two", sources)
	}
	wantSources := []missionpack.SourceRef{
		{
			Kind:        "canonical_event",
			CandidateID: "candidate_second",
			EventID:     "event_one",
			ObservedAt:  timePointerForMissionPackTest(observedAt.UTC()),
		},
		{
			Kind:        "transcript_turn",
			CandidateID: "candidate_second",
			SessionKey:  "session_one",
			TurnIndex:   int64PointerForMissionPackTest(turnIndex),
			ObservedAt:  timePointerForMissionPackTest(observedAt.UTC()),
		},
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("mapped sources = %#v, want %#v", sources, wantSources)
	}
}

func TestMissionPackServiceRegistersSuccessfulExperiencePreview(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	selector := &missionPackTestExperienceSelector{
		result: ExperienceSelectionResult{
			Generation: local.ExperienceGeneration{Generation: 11},
			Selected: []SelectedExperience{
				missionPackSelectedExperience(
					"exp_first",
					2,
					"candidate_first",
					experience.VerifierCommandSucceeded,
				),
				missionPackSelectedExperience(
					"exp_second",
					4,
					"candidate_second",
					experience.VerifierCommandSucceeded,
				),
			},
		},
	}
	previewRepository := &missionPackTestPreviewRepository{}
	service, err := NewMissionPackService(
		&missionPackTestRepository{
			cwdProject: missionpack.ResolvedProject{
				Identity:     "project_one",
				IdentityKind: "path",
				Path:         root,
			},
		},
		WithMissionPackExperienceSelector(selector),
		WithMissionPackPreviewRepository(previewRepository),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	pack, err := service.Generate(context.Background(), missionpack.Request{
		CWD:      root,
		Intent:   missionpack.IntentImplement,
		Harness:  missionpack.HarnessCodex,
		TaskHint: "  update the cache safely  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(previewRepository.calls) != 1 {
		t.Fatalf("preview calls = %#v, want one", previewRepository.calls)
	}
	taskHintSum := sha256.Sum256([]byte("update the cache safely"))
	want := missionPackPreviewCall{
		packID:          pack.PackID,
		projectIdentity: "project_one",
		harness:         experience.HarnessCodex,
		generation:      11,
		refs: []experience.ExperienceRef{
			{ExperienceID: "exp_first", Version: 2},
			{ExperienceID: "exp_second", Version: 4},
		},
		taskHintHash: "sha256:" + hex.EncodeToString(taskHintSum[:]),
		generatedAt:  now,
		expiresAt:    now.Add(10 * time.Minute),
	}
	if !reflect.DeepEqual(previewRepository.calls[0], want) {
		t.Fatalf(
			"preview call = %#v, want %#v",
			previewRepository.calls[0],
			want,
		)
	}
}

func TestMissionPackServiceDoesNotRegisterLegacyPreview(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	previewRepository := &missionPackTestPreviewRepository{}
	service, err := NewMissionPackService(
		&missionPackTestRepository{
			cwdProject: missionpack.ResolvedProject{
				Identity:     "project_one",
				IdentityKind: "path",
				Path:         root,
			},
		},
		WithMissionPackPreviewRepository(previewRepository),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	}
	if _, err := service.Generate(
		context.Background(),
		missionpack.Request{
			CWD:     root,
			Intent:  missionpack.IntentGeneral,
			Harness: missionpack.HarnessClaude,
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(previewRepository.calls) != 0 {
		t.Fatalf(
			"legacy preview calls = %#v, want none",
			previewRepository.calls,
		)
	}
}

func TestMissionPackVerifierSummariesCoverCurrentKinds(t *testing.T) {
	tests := []struct {
		name     string
		verifier experience.Verifier
		want     string
	}{
		{
			name: "command observed",
			verifier: experience.Verifier{
				Kind: experience.VerifierCommandObserved,
				Command: &experience.CommandVerifierSpec{
					Command:          "make check",
					ScrubbingVersion: "belay.redaction.v1",
				},
			},
			want: "Run make check.",
		},
		{
			name: "command succeeded",
			verifier: experience.Verifier{
				Kind: experience.VerifierCommandSucceeded,
				Command: &experience.CommandVerifierSpec{
					Command:          "go test ./...",
					ScrubbingVersion: "belay.redaction.v1",
				},
			},
			want: "Run go test ./... successfully.",
		},
		{
			name: "file not modified",
			verifier: experience.Verifier{
				Kind: experience.VerifierFileNotModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageWorkspaceCaptured,
				},
				File: &experience.FileVerifierSpec{
					Path: "generated/client.go",
				},
			},
			want: "Confirm generated/client.go was not modified.",
		},
		{
			name: "file modified",
			verifier: experience.Verifier{
				Kind: experience.VerifierFileModified,
				File: &experience.FileVerifierSpec{
					Path: "internal/service.go",
				},
			},
			want: "Confirm internal/service.go was modified.",
		},
		{
			name: "path pattern not modified",
			verifier: experience.Verifier{
				Kind: experience.VerifierPathPatternNotModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageWorkspaceCaptured,
				},
				PathPattern: &experience.PathPatternVerifierSpec{
					Patterns: []string{"generated/**", "vendor/**"},
				},
			},
			want: "No files matching generated/**, vendor/** changed.",
		},
		{
			name: "verification after last edit",
			verifier: experience.Verifier{
				Kind: experience.VerifierVerificationAfterLastEdit,
				VerificationAfterLastEdit: &experience.VerificationAfterLastEditSpec{
					RequireSuccess: true,
				},
			},
			want: "Run verification successfully after the final edit.",
		},
		{
			name: "no repeat failure",
			verifier: experience.Verifier{
				Kind: experience.VerifierNoRepeatFailure,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				NoRepeatFailure: &experience.NoRepeatFailureSpec{
					CommandClass:      "test",
					NormalizedPattern: "the generated client was edited directly",
					WindowTurns:       20,
				},
			},
			want: "Do not repeat this failure: the generated client was edited directly.",
		},
		{
			name: "user correction absent",
			verifier: experience.Verifier{
				Kind: experience.VerifierUserCorrectionAbsent,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				UserCorrectionAbsent: &experience.UserCorrectionAbsentSpec{
					MarkerFamilies: []string{"explicit_correction"},
				},
			},
			want: "Complete the task without needing another user correction.",
		},
		{
			name: "observation only",
			verifier: experience.Verifier{
				Kind: experience.VerifierObservationOnly,
				ObservationOnly: &experience.ObservationOnlySpec{
					Explanation: "Keep the cited observation\nfor later review.",
				},
			},
			want: "Keep the cited observation for later review.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := missionPackVerifierSummary(test.verifier)
			if err != nil {
				t.Fatal(err)
			}
			if got.Summary != test.want {
				t.Fatalf("summary = %q, want %q", got.Summary, test.want)
			}
			if strings.Contains(got.Summary, string(test.verifier.Kind)) ||
				strings.Contains(got.Summary, "_") ||
				strings.ContainsAny(got.Summary, "\r\n") {
				t.Fatalf("summary exposes internal jargon: %q", got.Summary)
			}
		})
	}

	_, err := missionPackVerifierSummary(experience.Verifier{
		Kind: experience.VerifierCommandSucceeded,
		File: &experience.FileVerifierSpec{Path: "go.mod"},
	})
	if err == nil {
		t.Fatal("malformed verifier produced a summary")
	}
}

func TestMissionPackServicePropagatesExperienceGenerationNotFound(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	wantErr := local.ErrExperienceGenerationNotFound
	selector := &missionPackTestExperienceSelector{err: wantErr}
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     "project_one",
			IdentityKind: "path",
			Path:         root,
		},
	}
	service, err := NewMissionPackService(
		repository,
		WithMissionPackExperienceSelector(selector),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Generate(context.Background(), missionpack.Request{
		CWD:    root,
		Intent: missionpack.IntentGeneral,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Generate() error = %v, want %v", err, wantErr)
	}
	if repository.readCalls != 0 {
		t.Fatalf("evidence reads = %d, want 0", repository.readCalls)
	}
}

func TestMissionPackServiceRejectsInvalidHarness(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     root,
			IdentityKind: "path",
			Path:         root,
		},
	}
	service, err := NewMissionPackService(repository)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Generate(context.Background(), missionpack.Request{
		CWD:     root,
		Intent:  missionpack.IntentImplement,
		Harness: missionpack.Harness("cursor"),
	})
	if err == nil || !strings.Contains(err.Error(), "harness") {
		t.Fatalf("Generate() error = %v", err)
	}
	if repository.readCalls != 0 {
		t.Fatalf("evidence reads = %d, want 0", repository.readCalls)
	}
}

func TestMissionPackServiceRequestTimeoutIsFiveSeconds(t *testing.T) {
	if missionPackServiceRequestTimeout != 5*time.Second {
		t.Fatalf(
			"Mission Pack service timeout = %s, want 5s",
			missionPackServiceRequestTimeout,
		)
	}
}

func TestMissionPackServiceRejectsIssueCWDProjectMismatch(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	repository := &missionPackTestRepository{
		issueProject: missionpack.ResolvedProject{
			Identity:     "project-a",
			IdentityKind: "path",
			Path:         root,
		},
		cwdProject: missionpack.ResolvedProject{
			Identity:     "project-b",
			IdentityKind: "path",
			Path:         root,
		},
	}
	service, err := NewMissionPackService(repository)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Generate(context.Background(), missionpack.Request{
		CWD:     root,
		IssueID: "issue_one",
		Intent:  missionpack.IntentDebug,
	})
	if !errors.Is(err, missionpack.ErrProjectMismatch) {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestMissionPackServiceRejectsChangedRemoteForIssueOnly(t *testing.T) {
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	runMissionPackTestCommand(
		t,
		root,
		"git",
		"remote",
		"add",
		"origin",
		"https://example.test/team/current.git",
	)
	repository := &missionPackTestRepository{
		issueProject: missionpack.ResolvedProject{
			Identity:     "https://example.test/team/previous.git",
			IdentityKind: "remote",
			Path:         root,
		},
	}
	service, err := NewMissionPackService(repository)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Generate(context.Background(), missionpack.Request{
		IssueID: "issue_previous",
		Intent:  missionpack.IntentDebug,
	})
	if !errors.Is(err, missionpack.ErrProjectNotFound) {
		t.Fatalf("Generate() error = %v", err)
	}
	if repository.readCalls != 0 {
		t.Fatalf("evidence reads = %d, want 0", repository.readCalls)
	}
}

func missionPackSelectedExperience(
	experienceID string,
	version int,
	candidateID string,
	verifierKind experience.VerifierKind,
) SelectedExperience {
	return SelectedExperience{
		Experience: experience.Experience{
			ExperienceID: experienceID,
			Version:      version,
			Type:         experience.ExperienceProcedure,
			Applicability: experience.Applicability{
				SemanticDescription: "Use this workflow when changing project code.",
			},
			Guidance: experience.Guidance{
				Instruction: "Use the project verification workflow.",
				Rationale:   "The approved experience requires it.",
				Exceptions: []string{
					"Do not apply it to read-only review tasks.",
				},
			},
			Verifier: experience.Verifier{
				Kind: verifierKind,
				Command: &experience.CommandVerifierSpec{
					Command:          "go test ./...",
					ScrubbingVersion: "belay.redaction.v1",
				},
			},
			Provenance: experience.Provenance{
				SourceCandidateID: candidateID,
			},
		},
	}
}

func timePointerForMissionPackTest(value time.Time) *time.Time {
	return &value
}

func int64PointerForMissionPackTest(value int64) *int64 {
	return &value
}

func TestObservedMissionPackCommandsRequireRecognizedSuccess(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	values := observedMissionPackCommands(
		[]missionpack.SuccessfulCommand{
			{Command: "go test ./...", SucceededAt: now},
			{Command: "git status --short", SucceededAt: now},
		},
		issueintel.ProjectConfig{},
	)
	if len(values) != 1 || values[0].Command != "go test ./..." {
		t.Fatalf("observed commands = %#v", values)
	}
}

func TestObservedMissionPackCommandsRejectShellPrograms(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	values := observedMissionPackCommands(
		[]missionpack.SuccessfulCommand{
			{
				Command: "go test -count=1 ./cmd/belay " +
					"./internal/presentation/localhttp 2>&1 | tail -30",
				SucceededAt: now,
			},
			{
				Command: "gofmt -d internal/pipeline/edge_acceptance_test.go; " +
					"git diff --check; git status --short; " +
					"wc -l internal/pipeline/edge_acceptance_test.go",
				SucceededAt: now,
			},
			{
				Command: "gofmt -w internal/pipeline/edge_acceptance_test.go && " +
					"git diff --check -- internal/pipeline/edge_acceptance_test.go",
				SucceededAt: now,
			},
			{Command: "go test ./...", SucceededAt: now},
			{Command: "make test", SucceededAt: now},
			{Command: "pnpm run check", SucceededAt: now},
			{Command: "./scripts/verify", SucceededAt: now},
		},
		issueintel.ProjectConfig{
			VerificationCommands: []string{"./scripts/verify"},
		},
	)
	got := make(map[string]bool, len(values))
	for _, value := range values {
		got[value.Command] = true
	}
	want := []string{
		"go test ./...",
		"make test",
		"pnpm run check",
		"./scripts/verify",
	}
	if len(got) != len(want) {
		t.Fatalf("observed commands = %#v", values)
	}
	for _, command := range want {
		if !got[command] {
			t.Fatalf("observed commands missing %q: %#v", command, values)
		}
	}
}

func runMissionPackTestCommand(
	t *testing.T,
	workdir string,
	name string,
	args ...string,
) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = workdir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}
