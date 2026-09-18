package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type transcriptStatusRepository struct {
	coverage transcript.CoverageCounts
	sessions []transcript.Session
	query    transcript.SessionQuery
}

func (repository *transcriptStatusRepository) TranscriptCoverage(
	context.Context,
) (transcript.CoverageCounts, error) {
	return repository.coverage, nil
}

func (repository *transcriptStatusRepository) QueryTranscriptSessions(
	_ context.Context,
	query transcript.SessionQuery,
) ([]transcript.Session, error) {
	repository.query = query
	return repository.sessions, nil
}

func TestTranscriptStatusProjectsExactCoverageAndSafeRecentSessions(
	t *testing.T,
) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	repository := &transcriptStatusRepository{
		coverage: transcript.CoverageCounts{
			CanonicalCompleteOrLiveWithTranscript: 4,
			CanonicalPartial:                      2,
			CanonicalWithoutTranscript:            3,
			TranscriptOnly:                        1,
		},
		sessions: []transcript.Session{
			{
				SessionKey:      "ses_live",
				Agent:           "codex",
				ProjectPath:     "/private/source",
				GitRemoteURL:    "git@example.invalid:private/repo.git",
				ProjectIdentity: "git@example.invalid:private/repo.git",
				StartedAt:       now.Add(-time.Minute),
				EndedAt:         now.Add(-time.Second),
				Coverage:        transcript.CoverageLive,
				TurnCount:       7,
			},
			{
				SessionKey:      "ses_recent",
				Agent:           "claude-code",
				ProjectPath:     "/Users/private/work/project-two",
				ProjectIdentity: "/Users/private/work/project-two",
				StartedAt:       now.Add(-time.Hour),
				EndedAt:         now.Add(-30 * time.Minute),
				Coverage:        transcript.CoverageComplete,
				TurnCount:       3,
			},
			{
				SessionKey:      "ses_stale_live",
				Agent:           "codex",
				ProjectIdentity: "/Users/private/work/stale",
				StartedAt:       now.Add(-time.Hour),
				EndedAt:         now.Add(-10 * time.Minute),
				Coverage:        transcript.CoverageLive,
			},
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithClock(func() time.Time { return now }),
		WithTranscriptRepository(repository),
	)
	if service.transcriptRepository != repository {
		t.Fatal("transcript repository option did not populate Service")
	}
	status, err := service.GetTranscriptStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != TranscriptStatusSchemaVersion ||
		status.Coverage != (TranscriptCoverageStatus{
			WithTranscript: 4,
			Partial:        2,
			Without:        3,
			TranscriptOnly: 1,
		}) ||
		len(status.Sessions) != 3 ||
		!status.Sessions[0].Active ||
		status.Sessions[0].SessionKey != "ses_live" ||
		status.Sessions[0].Project != "repo" ||
		status.Sessions[1].SessionKey != "ses_stale_live" ||
		status.Sessions[1].Active ||
		status.Sessions[2].SessionKey != "ses_recent" ||
		status.Sessions[2].Project != "project-two" ||
		status.Sessions[2].Active {
		t.Fatalf("transcript status = %+v", status)
	}
	if repository.query != (transcript.SessionQuery{Limit: 10}) {
		t.Fatalf("transcript session query = %+v", repository.query)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{
		"/private/source",
		"/Users/private/work",
		"git@example.invalid",
		"project_identity",
		"project_path",
		"git_remote",
		"payload",
		"excerpt",
	} {
		if strings.Contains(string(encoded), prohibited) {
			t.Fatalf("transcript status leaked %q: %s", prohibited, encoded)
		}
	}
}

func TestTranscriptProjectLabelUsesBasenameAndHarnessFallback(t *testing.T) {
	tests := []struct {
		name    string
		session transcript.Session
		want    string
	}{
		{
			name: "URL remote",
			session: transcript.Session{
				Agent:           "codex",
				GitRemoteURL:    "https://example.invalid/owner/belay-engine.git",
				ProjectIdentity: "https://example.invalid/owner/belay-engine.git",
			},
			want: "belay-engine",
		},
		{
			name: "SCP remote",
			session: transcript.Session{
				Agent:           "codex",
				GitRemoteURL:    "git@example.invalid:owner/service.git",
				ProjectIdentity: "git@example.invalid:owner/service.git",
			},
			want: "service",
		},
		{
			name: "local path",
			session: transcript.Session{
				Agent:           "codex",
				ProjectIdentity: "/Users/private/source/local-project",
			},
			want: "local-project",
		},
		{
			name:    "harness fallback",
			session: transcript.Session{Agent: "claude-code"},
			want:    "Claude Code",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := transcriptProjectLabel(test.session); got != test.want {
				t.Fatalf("project label = %q, want %q", got, test.want)
			}
		})
	}
	long := transcriptProjectLabel(transcript.Session{
		Agent:           "codex",
		ProjectIdentity: "/tmp/" + strings.Repeat("x", 100),
	})
	if len([]rune(long)) != maxProjectLabelRunes {
		t.Fatalf("bounded project label length = %d", len([]rune(long)))
	}
}

func TestTranscriptStatusRequiresRepository(t *testing.T) {
	service := New(issueTestCoreRepository{})
	if _, err := service.GetTranscriptStatus(context.Background()); err == nil {
		t.Fatal("transcript status unexpectedly available without repository")
	}
}
