package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

type userInsightRepository struct {
	sessions   []transcript.Session
	turns      map[string][]transcript.Turn
	turnErrors map[string]error
	turnLimits []int
}

type userInsightEpisodeRepository struct {
	values map[string][]evidenceepisode.Episode
}

func (repository userInsightEpisodeRepository) QuerySessionEvidenceEpisodes(
	_ context.Context,
	sessionKey string,
	_ int,
) ([]evidenceepisode.Episode, error) {
	return repository.values[sessionKey], nil
}

func (repository userInsightEpisodeRepository) QueryEvidenceEpisodesForSessions(
	_ context.Context,
	sessionKeys []string,
	_ int,
) (map[string][]evidenceepisode.Episode, error) {
	result := make(map[string][]evidenceepisode.Episode)
	for _, sessionKey := range sessionKeys {
		if values := repository.values[sessionKey]; len(values) > 0 {
			result[sessionKey] = values
		}
	}
	return result, nil
}

func (repository *userInsightRepository) TranscriptCoverage(
	context.Context,
) (transcript.CoverageCounts, error) {
	return transcript.CoverageCounts{}, nil
}

func (repository *userInsightRepository) QueryTranscriptSessions(
	_ context.Context,
	query transcript.SessionQuery,
) ([]transcript.Session, error) {
	if query.Limit > len(repository.sessions) {
		return repository.sessions, nil
	}
	return repository.sessions[:query.Limit], nil
}

func (repository *userInsightRepository) QueryTranscriptTurns(
	_ context.Context,
	sessionKey string,
	limit int,
) ([]transcript.Turn, error) {
	repository.turnLimits = append(repository.turnLimits, limit)
	if err := repository.turnErrors[sessionKey]; err != nil {
		return nil, err
	}
	return repository.turns[sessionKey], nil
}

func userInsightTurn(
	index int,
	role transcript.Role,
	at time.Time,
	text string,
	tool string,
	command string,
	callID string,
	exit *int,
) transcript.Turn {
	turn := transcript.Turn{
		TurnID:     "turn-" + string(rune('a'+index)),
		SessionKey: "ses_recent",
		TurnIndex:  int64(index),
		OccurredAt: at,
		Role:       role,
		ToolName:   tool,
	}
	turn.Payload.Text = text
	turn.Payload.RawCommand = command
	turn.Payload.ToolCallID = callID
	turn.Payload.ExitCode = exit
	if tool == "Edit" {
		turn.Payload.ToolInput = json.RawMessage(`{"file_path":"/Users/private/project/secret/billing.go"}`)
	}
	return turn
}

func TestGetUserInsightsBuildsSafeDebriefsForCompleteSessions(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	zero := 0
	sessions := []transcript.Session{
		{
			SessionKey:      "ses_recent",
			Agent:           "claude-code",
			ProjectPath:     "/Users/private/work/billing-service",
			ProjectIdentity: "/Users/private/work/billing-service",
			StartedAt:       start,
			EndedAt:         start.Add(90 * time.Minute),
			WallDurationMS:  (90 * time.Minute).Milliseconds(),
			Coverage:        transcript.CoverageComplete,
			UserTurnCount:   3,
		},
		{
			SessionKey:    "ses_live",
			Agent:         "codex",
			Coverage:      transcript.CoverageLive,
			UserTurnCount: 9,
		},
		{
			SessionKey:    "ses_short",
			Agent:         "codex",
			Coverage:      transcript.CoverageComplete,
			UserTurnCount: 1,
		},
	}
	for index := 0; index < 3; index++ {
		sessions = append(sessions, transcript.Session{
			SessionKey:      "ses_baseline_" + string(rune('a'+index)),
			Agent:           "claude-code",
			ProjectIdentity: "/Users/private/work/billing-service",
			WallDurationMS:  (30 * time.Minute).Milliseconds(),
			Coverage:        transcript.CoverageComplete,
			UserTurnCount:   4,
		})
	}
	repository := &userInsightRepository{
		sessions: sessions,
		turns: map[string][]transcript.Turn{
			"ses_recent": {
				userInsightTurn(0, transcript.RoleUser, start, "Refactor billing. SECRET_TOKEN=abc", "", "", "", nil),
				userInsightTurn(1, transcript.RoleToolCall, start.Add(2*time.Minute), "", "Edit", "", "", nil),
				userInsightTurn(2, transcript.RoleToolCall, start.Add(3*time.Minute), "", "Edit", "", "", nil),
				userInsightTurn(3, transcript.RoleToolCall, start.Add(4*time.Minute), "", "Edit", "", "", nil),
				userInsightTurn(4, transcript.RoleAssistant, start.Add(5*time.Minute), "Done.", "", "", "", nil),
				userInsightTurn(5, transcript.RoleUser, start.Add(6*time.Minute), "No, run the tests first.", "", "", "", nil),
				userInsightTurn(6, transcript.RoleToolCall, start.Add(40*time.Minute), "", "Bash", "go test ./secret/...", "call-1", nil),
				userInsightTurn(7, transcript.RoleToolResult, start.Add(41*time.Minute), "", "", "", "call-1", &zero),
			},
			"ses_baseline_a": {},
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithClock(func() time.Time { return now }),
		WithTranscriptRepository(repository),
		WithEvidenceEpisodeRepository(userInsightEpisodeRepository{
			values: map[string][]evidenceepisode.Episode{
				"ses_recent": {
					{
						EpisodeID: "eep_safe_signal",
						Kind:      evidenceepisode.KindFailureRepair,
						FirstTurn: 2,
						LastTurn:  7,
						Cost: issueintel.Cost{
							WastedMinutes: 6,
							WastedTokens:  1200,
						},
						FailureSignature: "SECRET_TOKEN=do-not-expose",
					},
				},
			},
		}),
	)

	response, err := service.GetUserInsights(context.Background(), UserInsightsRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != SchemaVersion ||
		response.ProjectionVersion != UserInsightsProjectionVersion ||
		response.AnalysisVersion != userinsights.AnalysisVersion ||
		!response.GeneratedAt.Equal(now) {
		t.Fatalf("unexpected envelope: %+v", response)
	}
	if len(response.Sessions) != 1 {
		t.Fatalf("expected one debrief, got %d", len(response.Sessions))
	}
	debrief := response.Sessions[0]
	if debrief.SessionKey != "ses_recent" || debrief.Project != "billing-service" ||
		debrief.Agent != "claude-code" {
		t.Fatalf("unexpected debrief identity: %+v", debrief)
	}
	if debrief.Baseline == nil || debrief.Baseline.SessionCount != 3 ||
		debrief.Baseline.Comparison != userinsights.ComparisonLonger {
		t.Fatalf("baseline should compare against the three project sessions: %+v", debrief.Baseline)
	}
	if len(debrief.Findings) == 0 || debrief.Findings[0].Kind != userinsights.FindingLateVerification {
		t.Fatalf("expected the late verification finding first: %+v", debrief.Findings)
	}
	if len(debrief.Signals) != 1 ||
		debrief.Signals[0].EpisodeID != "eep_safe_signal" ||
		debrief.Signals[0].Title != "A failed command was recovered" {
		t.Fatalf("expected one safe deterministic signal: %+v", debrief.Signals)
	}
	if response.Coverage.CandidateSessions != 6 || response.Coverage.EvaluatedSessions != 1 ||
		!response.Coverage.HasMore {
		t.Fatalf("unexpected coverage: %+v", response.Coverage)
	}
	if len(repository.turnLimits) != 1 || repository.turnLimits[0] != userInsightTurnLimit {
		t.Fatalf("turn reads should be bounded once per evaluated session: %v", repository.turnLimits)
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"SECRET_TOKEN",
		"/Users/private",
		"go test",
		"secret/billing.go",
		"do-not-expose",
		"run the tests first",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("user insights leaked %q", forbidden)
		}
	}
}

func TestGetUserInsightsSkipsUnreadableSessionsAndBoundsLimit(t *testing.T) {
	sessions := make([]transcript.Session, 0, 4)
	for index := 0; index < 4; index++ {
		sessions = append(sessions, transcript.Session{
			SessionKey:      "ses_" + string(rune('a'+index)),
			ProjectIdentity: "/Users/private/project",
			Coverage:        transcript.CoverageComplete,
			UserTurnCount:   3,
			WallDurationMS:  (10 * time.Minute).Milliseconds(),
		})
	}
	repository := &userInsightRepository{
		sessions:   sessions,
		turns:      map[string][]transcript.Turn{},
		turnErrors: map[string]error{"ses_b": errors.New("decrypt failed")},
	}
	service := New(issueTestCoreRepository{}, WithUserInsightRepository(repository))

	response, err := service.GetUserInsights(context.Background(), UserInsightsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Sessions) != 3 || response.Coverage.SkippedUnreadable != 1 ||
		response.Coverage.HasMore {
		t.Fatalf("unexpected coverage after an unreadable session: %+v", response.Coverage)
	}
	if _, err := service.GetUserInsights(
		context.Background(),
		UserInsightsRequest{Limit: maxUserInsightSessionLimit + 1},
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request for an oversized limit, got %v", err)
	}
	if _, err := New(issueTestCoreRepository{}).GetUserInsights(
		context.Background(),
		UserInsightsRequest{},
	); !errors.Is(err, ErrUserInsightsUnavailable) {
		t.Fatalf("expected unavailable without a repository, got %v", err)
	}
}
