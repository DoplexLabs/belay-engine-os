package localapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type transcriptIssueAnalysisTestStore struct {
	dirty       []local.DirtyTranscriptProject
	inputs      map[string]issueintel.ProjectInput
	replaced    []transcriptIssueAnalysisReplacement
	replaceErrs map[string]error
}

type transcriptIssueAnalysisReplacement struct {
	project    issueintel.Project
	generation int64
	analysis   issueintel.Analysis
}

func (s *transcriptIssueAnalysisTestStore) ListDirtyTranscriptProjects(
	_ context.Context,
	_ ...int,
) ([]local.DirtyTranscriptProject, error) {
	return append([]local.DirtyTranscriptProject(nil), s.dirty...), nil
}

func (s *transcriptIssueAnalysisTestStore) LoadTranscriptProjectData(
	_ context.Context,
	identity string,
) (issueintel.ProjectInput, error) {
	value, ok := s.inputs[identity]
	if !ok {
		return issueintel.ProjectInput{}, errors.New("missing project")
	}
	return value, nil
}

func (s *transcriptIssueAnalysisTestStore) ReplaceProjectIssueAnalysis(
	_ context.Context,
	project issueintel.Project,
	generation int64,
	analysis issueintel.Analysis,
) error {
	if err := s.replaceErrs[project.Identity]; err != nil {
		return err
	}
	s.replaced = append(s.replaced, transcriptIssueAnalysisReplacement{
		project:    project,
		generation: generation,
		analysis:   analysis,
	})
	return nil
}

func (s *transcriptIssueAnalysisTestStore) QueryTrajectoryEdges(
	_ context.Context,
	_ local.TrajectoryEdgeQuery,
) ([]trajectory.Edge, error) {
	return nil, nil
}

func (s *transcriptIssueAnalysisTestStore) QueryOutcomes(
	_ context.Context,
	_ local.OutcomeQuery,
) ([]trajectory.Outcome, error) {
	return nil, nil
}

func (s *transcriptIssueAnalysisTestStore) InsertEvidenceEpisode(
	_ context.Context,
	_ evidenceepisode.Episode,
) (bool, error) {
	return true, nil
}

func TestAnalyzeTranscriptIssuesOnceLoadsProjectConfigAndPersistsGeneration(
	t *testing.T,
) {
	projectPath := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectPath, "CLAUDE.md"),
		[]byte("# instructions\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectPath, "package.json"),
		[]byte(`{"scripts":{"check":"eslint . && tsc --noEmit"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	project := issueintel.Project{Identity: "project-a", Path: projectPath}
	session := transcript.Session{
		SessionKey:      "ses_analysis",
		Agent:           "claude",
		ProjectIdentity: project.Identity,
		ProjectPath:     project.Path,
		StartedAt:       now.Add(-time.Minute),
		EndedAt:         now,
		Coverage:        transcript.CoverageComplete,
	}
	store := &transcriptIssueAnalysisTestStore{
		dirty: []local.DirtyTranscriptProject{{
			Project:              project,
			TranscriptGeneration: 7,
		}},
		inputs: map[string]issueintel.ProjectInput{
			project.Identity: {
				Project: project,
				Now:     now,
				Sessions: []issueintel.Session{{
					Metadata: session,
				}},
			},
		},
	}
	report, err := AnalyzeTranscriptIssuesOnce(context.Background(), store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Projects != 1 || len(store.replaced) != 1 {
		t.Fatalf("report/replacements = %+v/%+v", report, store.replaced)
	}
	replacement := store.replaced[0]
	if replacement.generation != 7 ||
		replacement.project != project {
		t.Fatalf("replacement = %+v", replacement)
	}
}

func TestAnalyzeTranscriptIssuesOnceLeavesGenerationRaceDirty(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	project := issueintel.Project{Identity: "project-race", Path: t.TempDir()}
	store := &transcriptIssueAnalysisTestStore{
		dirty: []local.DirtyTranscriptProject{{
			Project:              project,
			TranscriptGeneration: 3,
		}},
		inputs: map[string]issueintel.ProjectInput{
			project.Identity: {
				Project: project,
				Now:     now,
			},
		},
		replaceErrs: map[string]error{
			project.Identity: local.ErrTranscriptProjectGenerationChanged,
		},
	}
	report, err := AnalyzeTranscriptIssuesOnce(context.Background(), store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Stale != 1 || report.Projects != 0 {
		t.Fatalf("race report = %+v", report)
	}
}
