package localapp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type boundedExperienceAnalysisDiscoveryStore struct {
	projects []string
	limits   []int
}

func (store *boundedExperienceAnalysisDiscoveryStore) ListTranscriptProjectIdentities(
	_ context.Context,
	limit int,
) ([]string, error) {
	store.limits = append(store.limits, limit)
	return append([]string(nil), store.projects...), nil
}

func boundedExperienceAnalysisOperations() experienceAnalysisOperations {
	return experienceAnalysisOperations{
		analyzeTrajectories: func(context.Context) (TrajectoryBatchReport, error) {
			return TrajectoryBatchReport{}, nil
		},
		compileProject: func(
			context.Context,
			string,
		) (ExperienceCandidateCompilationReport, error) {
			return ExperienceCandidateCompilationReport{}, nil
		},
		pendingProject: func(
			context.Context,
			string,
		) (experiencePendingCandidateReport, error) {
			return experiencePendingCandidateReport{}, nil
		},
		analyzeProject: func(
			context.Context,
			string,
		) (ExperienceSemanticAnalysisReport, error) {
			return ExperienceSemanticAnalysisReport{}, nil
		},
	}
}

func TestExperienceAnalysisTrajectoryDrainAndPassCap(t *testing.T) {
	store := &boundedExperienceAnalysisDiscoveryStore{}
	operations := boundedExperienceAnalysisOperations()
	pass := 0
	operations.analyzeTrajectories = func(
		context.Context,
	) (TrajectoryBatchReport, error) {
		pass++
		if pass < 3 {
			return TrajectoryBatchReport{
				Dirty:    2,
				Complete: 1,
				Partial:  1,
			}, nil
		}
		return TrajectoryBatchReport{}, nil
	}
	report, err := analyzeExperienceProjectsOnce(
		context.Background(),
		store,
		operations,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.TrajectoryPasses != 3 ||
		report.TrajectoryClaims != 4 ||
		report.TrajectoryComplete != 2 ||
		report.TrajectoryPartial != 2 ||
		report.TrajectoryPassLimitReached {
		t.Fatalf("bounded trajectory drain = %+v", report)
	}

	operations.analyzeTrajectories = func(
		context.Context,
	) (TrajectoryBatchReport, error) {
		return TrajectoryBatchReport{Dirty: 1, Failed: 1, Stale: 1}, nil
	}
	report, err = analyzeExperienceProjectsOnce(
		context.Background(),
		store,
		operations,
	)
	if !errors.Is(err, ErrExperienceAnalysisIncomplete) {
		t.Fatalf("pass-cap error = %v", err)
	}
	if report.TrajectoryPasses != maxExperienceTrajectoryAnalysisPasses ||
		report.TrajectoryClaims != maxExperienceTrajectoryAnalysisPasses ||
		report.TrajectoryFailed != maxExperienceTrajectoryAnalysisPasses ||
		report.TrajectoryStale != maxExperienceTrajectoryAnalysisPasses ||
		!report.TrajectoryPassLimitReached {
		t.Fatalf("trajectory pass-cap report = %+v", report)
	}
}

func TestExperienceAnalysisPendingFalseSkipsAnalysis(t *testing.T) {
	store := &boundedExperienceAnalysisDiscoveryStore{
		projects: []string{"project-a"},
	}
	operations := boundedExperienceAnalysisOperations()
	analyzeCalls := 0
	operations.analyzeProject = func(
		context.Context,
		string,
	) (ExperienceSemanticAnalysisReport, error) {
		analyzeCalls++
		return ExperienceSemanticAnalysisReport{}, nil
	}
	report, err := analyzeExperienceProjectsOnce(
		context.Background(),
		store,
		operations,
	)
	if err != nil {
		t.Fatal(err)
	}
	if analyzeCalls != 0 ||
		report.ProjectsCompiled != 1 ||
		report.ProjectsAnalyzed != 0 {
		t.Fatalf("pending=false report/calls = %+v/%d", report, analyzeCalls)
	}
}

func TestExperienceAnalysisIsolatesPerProjectFailure(t *testing.T) {
	store := &boundedExperienceAnalysisDiscoveryStore{
		projects: []string{"project-a", "project-b"},
	}
	operations := boundedExperienceAnalysisOperations()
	operations.pendingProject = func(
		context.Context,
		string,
	) (experiencePendingCandidateReport, error) {
		return experiencePendingCandidateReport{Pending: true}, nil
	}
	var analyzed []string
	operations.analyzeProject = func(
		_ context.Context,
		projectIdentity string,
	) (ExperienceSemanticAnalysisReport, error) {
		analyzed = append(analyzed, projectIdentity)
		if projectIdentity == "project-a" {
			return ExperienceSemanticAnalysisReport{}, errors.New("failed")
		}
		return ExperienceSemanticAnalysisReport{Proposed: 1}, nil
	}
	report, err := analyzeExperienceProjectsOnce(
		context.Background(),
		store,
		operations,
	)
	if !errors.Is(err, ErrExperienceAnalysisIncomplete) {
		t.Fatalf("project failure error = %v", err)
	}
	if !reflect.DeepEqual(analyzed, []string{"project-a", "project-b"}) ||
		report.ProjectAnalysisFailures != 1 ||
		report.ProjectFailures != 1 ||
		report.Proposals != 1 {
		t.Fatalf("project failure isolation = %+v/%v", report, analyzed)
	}
}

func TestExperienceAnalysisDiscoveryBoundAndNormalization(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		wantCapped bool
	}{
		{name: "exactly 100", count: 100},
		{name: "more than 100", count: 101, wantCapped: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projects := []string{" ", " project-050 ", "project-050"}
			for index := test.count - 1; index >= 0; index-- {
				projects = append(projects, fmt.Sprintf("project-%03d", index))
			}
			store := &boundedExperienceAnalysisDiscoveryStore{projects: projects}
			operations := boundedExperienceAnalysisOperations()
			var compiled []string
			operations.compileProject = func(
				_ context.Context,
				projectIdentity string,
			) (ExperienceCandidateCompilationReport, error) {
				compiled = append(compiled, projectIdentity)
				return ExperienceCandidateCompilationReport{
					SessionsCompiled:             3,
					PartialSessionsCompiled:      1,
					LiveSessionsCompiled:         2,
					SessionsSkippedIncomplete:    1,
					TranscriptIncompleteSessions: 1,
					EdgeCapSessions:              1,
					OutcomeCapSessions:           1,
				}, nil
			}
			report, err := analyzeExperienceProjectsOnce(
				context.Background(),
				store,
				operations,
			)
			if test.wantCapped {
				if !errors.Is(err, ErrExperienceAnalysisIncomplete) {
					t.Fatalf("capped discovery error = %v", err)
				}
			} else if err != nil {
				t.Fatalf("exact-cap discovery error = %v", err)
			}
			if !reflect.DeepEqual(store.limits, []int{101}) {
				t.Fatalf("discovery limits = %v, want [101]", store.limits)
			}
			if report.ProjectCapReached != test.wantCapped ||
				report.ProjectsConsidered != 100 ||
				len(compiled) != 100 ||
				compiled[0] != "project-000" ||
				compiled[99] != "project-099" {
				t.Fatalf("normalized discovery report/order = %+v/%v", report, compiled)
			}
			if report.CompilationSessionsCompiled != 300 ||
				report.CompilationPartialSessionsCompiled != 100 ||
				report.CompilationLiveSessionsCompiled != 200 ||
				report.CompilationSessionsSkipped != 100 ||
				report.TranscriptIncompleteSessions != 100 ||
				report.EdgeCapSessions != 100 ||
				report.OutcomeCapSessions != 100 {
				t.Fatalf("retained incomplete counters = %+v", report)
			}
		})
	}
}
