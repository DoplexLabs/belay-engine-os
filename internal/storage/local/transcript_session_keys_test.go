package local

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestQueryTranscriptSessionsByKeysReturnsOnlyStoredSessions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "belay.sqlite"), newMemoryKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	project := issueintel.Project{Identity: "git@example.test:team/project.git", Path: "/work/project"}
	session := costIssueTestSession("ses_keys_a", project, time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC))
	turn := transcriptTestTurn("turn-keys-a", "source-keys-a", session.SessionKey, 0, session.StartedAt,
		transcript.RoleUser, transcript.Payload{Text: "hello", SourceFileID: "source", JSONLByteOffset: 1})
	if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
		t.Fatal(err)
	}
	records, err := store.QueryTranscriptSessionsByKeys(ctx, []string{"ses_keys_a", "ses_missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records["ses_keys_a"].ProjectIdentity != project.Identity {
		t.Fatalf("unexpected records: %+v", records)
	}
	if empty, err := store.QueryTranscriptSessionsByKeys(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty key list should be a no-op: %v %v", empty, err)
	}
	if _, err := store.QueryTranscriptSessionsByKeys(ctx, []string{""}); err == nil {
		t.Fatal("blank keys must be rejected")
	}
}
