package local

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestEvidenceEpisodeEncryptedRoundTripIsReplaySafe(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	value := storageFailureRepairEpisode(t)

	inserted, err := store.InsertEvidenceEpisode(ctx, value)
	if err != nil || !inserted {
		t.Fatalf("InsertEvidenceEpisode() = %v, %v", inserted, err)
	}
	inserted, err = store.InsertEvidenceEpisode(ctx, value)
	if err != nil || inserted {
		t.Fatalf("replayed InsertEvidenceEpisode() = %v, %v", inserted, err)
	}
	values, err := store.QueryEvidenceEpisodes(ctx, EvidenceEpisodeQuery{
		SessionKey: value.SessionKey,
		Limit:      10,
	})
	if err != nil || len(values) != 1 || !reflect.DeepEqual(values[0], value) {
		t.Fatalf("QueryEvidenceEpisodes() = %+v, %v", values, err)
	}
	grouped, err := store.QueryEvidenceEpisodesForSessions(
		ctx,
		[]string{"ses_missing_episode", value.SessionKey, value.SessionKey},
		10,
	)
	if err != nil || len(grouped) != 1 ||
		len(grouped[value.SessionKey]) != 1 ||
		!reflect.DeepEqual(grouped[value.SessionKey][0], value) {
		t.Fatalf("QueryEvidenceEpisodesForSessions() = %+v, %v", grouped, err)
	}
}

func storageFailureRepairEpisode(t *testing.T) evidenceepisode.Episode {
	t.Helper()
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	session := transcript.Session{
		SessionKey:      "ses_storage_episode",
		ProjectIdentity: "project-storage-episode",
		Coverage:        transcript.CoverageComplete,
	}
	turn := func(index int64, role transcript.Role, callID string) transcript.Turn {
		return transcript.Turn{
			TurnID:     callID + string(role),
			SessionKey: session.SessionKey,
			TurnIndex:  index,
			OccurredAt: base.Add(time.Duration(index) * time.Minute),
			Role:       role,
			Payload: transcript.Payload{
				ToolCallID: callID,
			},
		}
	}
	failureCall := turn(0, transcript.RoleToolCall, "failed")
	failureCall.Payload.RawCommand = "go test ./internal/pipeline"
	failureResult := turn(1, transcript.RoleToolResult, "failed")
	exitOne := 1
	failureResult.Payload.ExitCode = &exitOne
	failureResult.Payload.ToolResult = "permission denied"
	successCall := turn(2, transcript.RoleToolCall, "success")
	successCall.Payload.RawCommand =
		"env GOCACHE=/tmp/cache go test ./internal/pipeline"
	successResult := turn(3, transcript.RoleToolResult, "success")
	exitZero := 0
	successResult.Payload.ExitCode = &exitZero
	turns := []transcript.Turn{
		failureCall,
		failureResult,
		successCall,
		successResult,
	}
	value, err := evidenceepisode.NewFailureRepair(
		evidenceepisode.FailureRepairInput{
			ProjectIdentity: session.ProjectIdentity,
			Session:         session,
			SessionTurns:    turns,
			FailureCall:     failureCall,
			FailureResult:   failureResult,
			SuccessCall:     successCall,
			SuccessResult:   successResult,
			OutcomeRefs:     []string{"out_storage_episode"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
