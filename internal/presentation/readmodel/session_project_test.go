package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/sessionidentity"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type sessionProjectCoreRepository struct {
	issueTestCoreRepository
	now time.Time
}

func (repository sessionProjectCoreRepository) sessions() []model.SessionSummary {
	return []model.SessionSummary{
		{SessionID: "ses_with_transcript", Harness: "claude-code", StartedAt: repository.now.Add(-time.Hour), EndedAt: repository.now, EventCount: 12, Outcome: "succeeded", History: "live"},
		{SessionID: "ses_without", Harness: "codex", StartedAt: repository.now.Add(-2 * time.Hour), EndedAt: repository.now.Add(-time.Hour), EventCount: 3, Outcome: "unknown", History: "historical"},
	}
}

func (repository sessionProjectCoreRepository) QuerySessions(context.Context, model.SessionQuery) (model.SessionPage, error) {
	return model.SessionPage{Data: repository.sessions(), Snapshot: 1, DataThrough: repository.now}, nil
}

func (repository sessionProjectCoreRepository) GetSession(context.Context, string) (model.SessionSummary, time.Time, error) {
	return repository.sessions()[0], repository.now, nil
}

type sessionProjectTranscriptRepository struct {
	keys    []string
	fail    bool
	aliases map[string]sessionidentity.ActiveAlias
}

func (r *sessionProjectTranscriptRepository) TranscriptCoverage(context.Context) (transcript.CoverageCounts, error) {
	return transcript.CoverageCounts{}, nil
}

func (r *sessionProjectTranscriptRepository) QueryTranscriptSessions(context.Context, transcript.SessionQuery) ([]transcript.Session, error) {
	return nil, nil
}

func (r *sessionProjectTranscriptRepository) QueryTranscriptSessionsByKeys(_ context.Context, keys []string) (map[string]transcript.Session, error) {
	r.keys = keys
	if r.fail {
		return nil, context.DeadlineExceeded
	}
	cost := 19.4
	return map[string]transcript.Session{
		"ses_with_transcript": {
			SessionKey:      "ses_with_transcript",
			ProjectPath:     "/Users/private/work/billing-service",
			ProjectIdentity: "/Users/private/work/billing-service",
			WallDurationMS:  (130 * time.Minute).Milliseconds(),
			TotalCostUSD:    &cost,
		},
	}, nil
}

func (r *sessionProjectTranscriptRepository) QueryActiveSessionIdentityAliases(
	_ context.Context,
	keys []string,
) (map[string]sessionidentity.ActiveAlias, error) {
	result := make(map[string]sessionidentity.ActiveAlias)
	for _, key := range keys {
		if alias, ok := r.aliases[key]; ok {
			result[key] = alias
		}
	}
	return result, nil
}

func TestSessionListJoinsProjectLabelDurationAndCost(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	transcripts := &sessionProjectTranscriptRepository{}
	service := New(sessionProjectCoreRepository{now: now}, WithTranscriptRepository(transcripts))
	list, err := service.ListSessionsPage(context.Background(), SessionListRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(transcripts.keys) != 2 {
		t.Fatalf("both session keys should be looked up once: %v", transcripts.keys)
	}
	first, second := list.Data[0], list.Data[1]
	if first.Project != "billing-service" || first.DurationMS != (130*time.Minute).Milliseconds() ||
		first.CostUSD == nil || *first.CostUSD != 19.4 {
		t.Fatalf("session with transcript was not decorated: %+v", first)
	}
	if second.Project != "" || second.DurationMS != 0 || second.CostUSD != nil {
		t.Fatalf("session without transcript must stay untouched: %+v", second)
	}
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), "/Users/private") {
		t.Fatal("session list leaked the raw project path")
	}
	detail, err := service.GetSession(context.Background(), "ses_with_transcript")
	if err != nil || detail.Data.Project != "billing-service" {
		t.Fatalf("session detail should carry the project label: %+v %v", detail.Data, err)
	}

	failing := &sessionProjectTranscriptRepository{fail: true}
	service = New(sessionProjectCoreRepository{now: now}, WithTranscriptRepository(failing))
	list, err = service.ListSessionsPage(context.Background(), SessionListRequest{Limit: 10})
	if err != nil || list.Data[0].Project != "" {
		t.Fatalf("a failed join must leave the list intact: %+v %v", list.Data[0], err)
	}

	fused := &sessionProjectTranscriptRepository{
		aliases: map[string]sessionidentity.ActiveAlias{
			"ses_without": {
				SessionKey:       "ses_without",
				LinkedSessionKey: "ses_with_transcript",
				Bases:            []string{sessionidentity.BasisExactNativeID},
			},
		},
	}
	service = New(
		sessionProjectCoreRepository{now: now},
		WithTranscriptRepository(fused),
		WithFusedSessionReads(true),
	)
	list, err = service.ListSessionsPage(
		context.Background(),
		SessionListRequest{Limit: 10},
	)
	if err != nil || list.Data[1].Project != "billing-service" {
		t.Fatalf(
			"active fused alias should decorate the event session: %+v %v",
			list.Data[1],
			err,
		)
	}
}
