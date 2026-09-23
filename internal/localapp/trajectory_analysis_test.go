package localapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type incrementalTrajectoryTestStore struct {
	claims        []local.TrajectoryDerivationClaim
	sessions      map[string]transcript.Session
	sessionErrors map[string]error
	completed     []string
	failed        []string
	requeued      []string
}

func (store *incrementalTrajectoryTestStore) ListDirtyTrajectorySessions(
	_ context.Context,
	_ string,
	_ int,
) ([]local.TrajectoryDerivationClaim, error) {
	return append([]local.TrajectoryDerivationClaim(nil), store.claims...), nil
}

func (store *incrementalTrajectoryTestStore) GetTranscriptSession(
	_ context.Context,
	sessionKey string,
) (transcript.Session, error) {
	if err := store.sessionErrors[sessionKey]; err != nil {
		return transcript.Session{}, err
	}
	return store.sessions[sessionKey], nil
}

func (store *incrementalTrajectoryTestStore) QueryTranscriptTurns(
	context.Context,
	string,
	int,
) ([]transcript.Turn, error) {
	return nil, nil
}

func (store *incrementalTrajectoryTestStore) QuerySessionTimeline(
	context.Context,
	model.TimelineQuery,
) (model.EventPage, error) {
	return model.EventPage{}, nil
}

func (store *incrementalTrajectoryTestStore) InsertTrajectoryEdge(
	context.Context,
	trajectory.Edge,
) (bool, error) {
	return true, nil
}

func (store *incrementalTrajectoryTestStore) InsertOutcome(
	context.Context,
	trajectory.Outcome,
) (bool, error) {
	return true, nil
}

func (store *incrementalTrajectoryTestStore) MarkTrajectoryDerivationCurrent(
	_ context.Context,
	claim local.TrajectoryDerivationClaim,
	coverage trajectoryderive.Coverage,
	_ []trajectoryderive.Diagnostic,
) (local.TrajectoryDerivationState, error) {
	store.completed = append(store.completed, claim.SessionKey)
	return local.TrajectoryDerivationState{
		TrajectoryDerivationClaim: claim,
		Status:                    local.TrajectoryDerivationComplete,
		Coverage:                  coverage,
	}, nil
}

func (store *incrementalTrajectoryTestStore) RecordTrajectoryDerivationFailure(
	_ context.Context,
	claim local.TrajectoryDerivationClaim,
	_ string,
	_ bool,
) (local.TrajectoryDerivationState, error) {
	store.failed = append(store.failed, claim.SessionKey)
	return local.TrajectoryDerivationState{
		TrajectoryDerivationClaim: claim,
		Status:                    local.TrajectoryDerivationFailed,
	}, nil
}

func (store *incrementalTrajectoryTestStore) MarkTranscriptProjectAnalysisDirty(
	_ context.Context,
	projectIdentity string,
) error {
	store.requeued = append(store.requeued, projectIdentity)
	return nil
}

type trajectoryTestKeyProvider struct {
	key     []byte
	storeID string
}

func (provider *trajectoryTestKeyProvider) Load(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	if len(provider.key) == 0 || provider.storeID != storeID {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *trajectoryTestKeyProvider) Create(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	if len(provider.key) != 0 {
		return nil, local.ErrKeyAlreadyExists
	}
	provider.key = bytes.Repeat([]byte{0x53}, 32)
	provider.storeID = storeID
	return append([]byte(nil), provider.key...), nil
}

func TestDeriveSessionTrajectoryOncePersistsIdempotentReplay(t *testing.T) {
	ctx := context.Background()
	store, err := local.Open(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		&trajectoryTestKeyProvider{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC)
	projectPath := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectPath, "Makefile"),
		[]byte("verify:\n\t@echo verified\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	session := transcript.Session{
		SessionKey:      "ses_trajectory_worker",
		Agent:           "codex",
		NativeSessionID: "native-trajectory-worker",
		ProjectPath:     projectPath,
		ProjectIdentity: "git@example.test:doplexlabs/belay.git",
		Coverage:        transcript.CoverageComplete,
	}
	turns := []transcript.Turn{
		trajectoryWorkerTurn(session.SessionKey, 0, base, transcript.RoleToolCall, "", "call-1"),
		trajectoryWorkerTurn(session.SessionKey, 1, base.Add(time.Second), transcript.RoleToolResult, "", "call-1"),
		trajectoryWorkerTurn(session.SessionKey, 2, base.Add(2*time.Second), transcript.RoleUser, "Wrong, run the focused test.", ""),
	}
	turns[0].Payload.RawCommand = "make verify"
	zero := 0
	turns[1].Payload.ExitCode = &zero
	if inserted, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil || inserted != len(turns) {
		t.Fatalf("AppendTranscriptBatch() = %d, %v", inserted, err)
	}
	eventID, err := model.NewUUIDv7(
		base,
		bytes.NewReader(bytes.Repeat([]byte{0x21}, 32)),
	)
	if err != nil {
		t.Fatal(err)
	}
	event := trajectoryWorkerEvent(eventID, session.SessionKey, base, "call-1")
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent() = %v, %v", inserted, err)
	}

	first, err := DeriveSessionTrajectoryOnce(ctx, store, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if first.DerivationVersion != trajectoryderive.Version ||
		first.EdgesInserted != 4 ||
		first.OutcomesInserted != 2 ||
		first.EdgesReplayed != 0 ||
		first.OutcomesReplayed != 0 ||
		!first.Coverage.FullyDerived {
		t.Fatalf("first derivation = %+v", first)
	}
	second, err := DeriveSessionTrajectoryOnce(ctx, store, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if second.EdgesInserted != 0 ||
		second.OutcomesInserted != 0 ||
		second.EdgesReplayed != first.EdgesInserted ||
		second.OutcomesReplayed != first.OutcomesInserted {
		t.Fatalf("replayed derivation = %+v", second)
	}

	edges, err := store.QueryTrajectoryEdges(ctx, local.TrajectoryEdgeQuery{
		SessionKey: session.SessionKey,
		Limit:      20,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := store.QueryOutcomes(ctx, local.OutcomeQuery{
		SessionKey: session.SessionKey,
		Limit:      20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != first.EdgesInserted || len(outcomes) != first.OutcomesInserted {
		t.Fatalf("persisted edges/outcomes = %d/%d", len(edges), len(outcomes))
	}
	for _, edge := range edges {
		if edge.DerivationVersion != trajectoryderive.Version ||
			edge.SessionKey != session.SessionKey {
			t.Fatalf("persisted edge = %+v", edge)
		}
	}
	kinds := make(map[trajectory.OutcomeKind]bool)
	for _, outcome := range outcomes {
		kinds[outcome.Kind] = true
		if outcome.DerivationVersion != trajectoryderive.Version {
			t.Fatalf("persisted outcome = %+v", outcome)
		}
	}
	if !kinds[trajectory.OutcomeCorrection] ||
		!kinds[trajectory.OutcomeVerificationPass] {
		t.Fatalf("persisted outcome kinds = %+v", kinds)
	}

	batch, err := AnalyzeTrajectorySessionsOnce(ctx, store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Dirty != 1 || batch.Complete != 1 {
		t.Fatalf("incremental batch = %+v", batch)
	}
	clean, err := AnalyzeTrajectorySessionsOnce(ctx, store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Dirty != 0 {
		t.Fatalf("clean incremental rerun = %+v", clean)
	}
}

func TestHighVolumeTrajectoryDiagnosticsAreBoundedAndPublished(
	t *testing.T,
) {
	ctx := context.Background()
	store, err := local.Open(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		&trajectoryTestKeyProvider{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	session := transcript.Session{
		SessionKey:      "ses_trajectory_diagnostics_bound",
		Agent:           "codex",
		NativeSessionID: "native-trajectory-diagnostics-bound",
		ProjectPath:     t.TempDir(),
		ProjectIdentity: "git@example.test:doplexlabs/belay.git",
		Coverage:        transcript.CoveragePartial,
	}
	turns := make([]transcript.Turn, 0, 600)
	for index := 0; index < 600; index++ {
		turn := trajectoryWorkerTurn(
			session.SessionKey,
			int64(index),
			base.Add(time.Duration(index)*time.Second),
			transcript.RoleToolResult,
			"",
			"missing-call-"+fmt.Sprintf("%03d", index),
		)
		turns = append(turns, turn)
	}
	if inserted, err := store.AppendTranscriptBatch(
		ctx,
		session,
		turns,
	); err != nil || inserted != len(turns) {
		t.Fatalf("AppendTranscriptBatch() = %d, %v", inserted, err)
	}

	first, err := DeriveSessionTrajectoryOnce(
		ctx,
		store,
		session.SessionKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeriveSessionTrajectoryOnce(
		ctx,
		store,
		session.SessionKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics) != trajectoryderive.MaxDiagnostics ||
		countTrajectoryDiagnostic(
			first.Diagnostics,
			trajectoryderive.DiagnosticDiagnosticsTruncated,
		) != 1 ||
		first.Diagnostics[len(first.Diagnostics)-1].Code !=
			trajectoryderive.DiagnosticDiagnosticsTruncated ||
		first.Coverage.FullyDerived ||
		first.Coverage.LinksComplete ||
		first.Coverage.Transcript != transcript.CoveragePartial ||
		!reflect.DeepEqual(first.Diagnostics, second.Diagnostics) {
		t.Fatalf(
			"bounded deterministic diagnostics = first %+v second %+v coverage %+v",
			first.Diagnostics,
			second.Diagnostics,
			first.Coverage,
		)
	}

	batch, err := AnalyzeTrajectorySessionsOnce(ctx, store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Dirty != 1 || batch.Partial != 1 || batch.Failed != 0 {
		t.Fatalf("published trajectory batch = %+v", batch)
	}
	state, err := store.GetTrajectoryDerivationState(
		ctx,
		session.SessionKey,
		trajectoryderive.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != local.TrajectoryDerivationPartial ||
		len(state.Diagnostics) != trajectoryderive.MaxDiagnostics ||
		countTrajectoryDiagnostic(
			state.Diagnostics,
			trajectoryderive.DiagnosticDiagnosticsTruncated,
		) != 1 ||
		state.Coverage.FullyDerived ||
		state.Coverage.LinksComplete {
		t.Fatalf("persisted bounded trajectory state = %+v", state)
	}
	clean, err := AnalyzeTrajectorySessionsOnce(ctx, store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Dirty != 0 {
		t.Fatalf("published partial state immediately retried = %+v", clean)
	}
}

func TestAnalyzeTrajectorySessionsOnceIsolatesMalformedSession(t *testing.T) {
	version := trajectoryderive.Version
	store := &incrementalTrajectoryTestStore{
		claims: []local.TrajectoryDerivationClaim{
			{
				SessionKey:            "ses_malformed",
				DerivationVersion:     version,
				TranscriptFingerprint: "sha256:" + string(bytes.Repeat([]byte{'1'}, 64)),
				CanonicalFingerprint:  "sha256:" + string(bytes.Repeat([]byte{'2'}, 64)),
			},
			{
				SessionKey:            "ses_valid",
				DerivationVersion:     version,
				TranscriptFingerprint: "sha256:" + string(bytes.Repeat([]byte{'3'}, 64)),
				CanonicalFingerprint:  "sha256:" + string(bytes.Repeat([]byte{'4'}, 64)),
			},
		},
		sessions: map[string]transcript.Session{
			"ses_valid": {
				SessionKey:      "ses_valid",
				Agent:           "codex",
				ProjectIdentity: "git@example.test:doplexlabs/belay.git",
				Coverage:        transcript.CoverageComplete,
			},
		},
		sessionErrors: map[string]error{
			"ses_malformed": errors.New("malformed encrypted transcript"),
		},
	}
	report, err := AnalyzeTrajectorySessionsOnce(
		context.Background(),
		store,
		10,
	)
	if err == nil {
		t.Fatal("malformed session did not report an error")
	}
	if report.Dirty != 2 ||
		report.Failed != 1 ||
		report.Complete != 1 ||
		len(store.failed) != 1 ||
		store.failed[0] != "ses_malformed" ||
		len(store.completed) != 1 ||
		store.completed[0] != "ses_valid" {
		t.Fatalf(
			"batch report/state = %+v failed=%+v completed=%+v",
			report,
			store.failed,
			store.completed,
		)
	}
}

func TestAnalyzeTrajectorySessionsOnceStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &incrementalTrajectoryTestStore{
		claims: []local.TrajectoryDerivationClaim{{
			SessionKey:        "ses_cancelled",
			DerivationVersion: trajectoryderive.Version,
		}},
		sessions:      map[string]transcript.Session{},
		sessionErrors: map[string]error{},
	}
	report, err := AnalyzeTrajectorySessionsOnce(ctx, store, 10)
	if !errors.Is(err, context.Canceled) ||
		report.Dirty != 1 ||
		len(store.completed) != 0 ||
		len(store.failed) != 0 {
		t.Fatalf(
			"cancelled batch = report %+v error %v completed=%+v failed=%+v",
			report,
			err,
			store.completed,
			store.failed,
		)
	}
}

func trajectoryWorkerTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	role transcript.Role,
	text string,
	callID string,
) transcript.Turn {
	return transcript.Turn{
		TurnID:          "trn-worker-" + occurredAt.Format("150405"),
		SourceRecordKey: "source-worker-" + occurredAt.Format("150405"),
		SessionKey:      sessionKey,
		TurnIndex:       index,
		OccurredAt:      occurredAt,
		Role:            role,
		Payload: transcript.Payload{
			Text:       text,
			ToolCallID: callID,
		},
	}
}

func trajectoryWorkerEvent(
	eventID string,
	sessionKey string,
	occurredAt time.Time,
	callID string,
) model.Event {
	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: "inst-trajectory-worker",
		OccurredAt:     occurredAt,
		ObservedAt:     occurredAt.Add(time.Millisecond),
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "test",
			SchemaVersion:    "0.3.0",
			RecordType:       "event",
			RunID:            "run-trajectory-worker",
			RecordID:         "record-trajectory-worker",
			Kind:             "hook",
			Agent:            "codex",
			AdapterVersion:   "test",
			DeduplicationKey: "sha256:trajectory-worker",
			Sequence:         1,
		},
		Session: model.SessionRef{Key: sessionKey},
		Observation: model.Observation{
			Type:    "command.exec",
			Actor:   "assistant",
			Action:  "command",
			Outcome: "unknown",
			Details: &model.Details{ToolCallID: callID},
		},
		Coverage: model.Coverage{
			Depth:      "tool_call",
			Confidence: "high",
		},
		Redaction: model.Redaction{PolicyVersion: model.RedactionVersion},
	}
}

func countTrajectoryDiagnostic(
	values []trajectoryderive.Diagnostic,
	code trajectoryderive.DiagnosticCode,
) int {
	count := 0
	for _, value := range values {
		if value.Code == code {
			count++
		}
	}
	return count
}
