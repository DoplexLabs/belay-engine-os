package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestTrajectoryDerivationDirtySessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	version := trajectoryderive.Version

	appendTrajectoryTranscriptFixture(
		t,
		ctx,
		store,
		"ses_trajectory_dirty_a",
		base,
		transcript.CoverageComplete,
	)
	claimA := requireOnlyDirtyTrajectoryClaim(t, ctx, store, version)
	state, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		claimA,
		completeTrajectoryCoverage(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TrajectoryDerivationComplete || state.Retryable {
		t.Fatalf("complete state = %+v", state)
	}
	current, err := store.IsTrajectoryDerivationStateCurrent(ctx, state)
	if err != nil || !current {
		t.Fatalf("fresh derivation current = %t, %v", current, err)
	}
	requireNoDirtyTrajectoryClaims(t, ctx, store, version)

	appendTrajectoryTurnFixture(
		t,
		ctx,
		store,
		"ses_trajectory_dirty_a",
		1,
		base.Add(time.Minute),
	)
	current, err = store.IsTrajectoryDerivationStateCurrent(ctx, state)
	if err != nil || current {
		t.Fatalf("stale derivation current = %t, %v", current, err)
	}
	afterTranscript := requireOnlyDirtyTrajectoryClaim(t, ctx, store, version)
	if afterTranscript.TranscriptFingerprint == claimA.TranscriptFingerprint ||
		afterTranscript.CanonicalFingerprint != claimA.CanonicalFingerprint {
		t.Fatalf("transcript append claim = %+v, prior %+v", afterTranscript, claimA)
	}
	if _, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		afterTranscript,
		completeTrajectoryCoverage(),
		nil,
	); err != nil {
		t.Fatal(err)
	}

	appendTrajectoryCanonicalFixture(
		t,
		ctx,
		store,
		"ses_trajectory_dirty_a",
		1,
		base.Add(2*time.Minute),
	)
	afterCanonical := requireOnlyDirtyTrajectoryClaim(t, ctx, store, version)
	if afterCanonical.TranscriptFingerprint != afterTranscript.TranscriptFingerprint ||
		afterCanonical.CanonicalFingerprint == afterTranscript.CanonicalFingerprint {
		t.Fatalf("canonical append claim = %+v, prior %+v", afterCanonical, afterTranscript)
	}
	if _, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		afterCanonical,
		completeTrajectoryCoverage(),
		nil,
	); err != nil {
		t.Fatal(err)
	}

	appendTrajectoryTranscriptFixture(
		t,
		ctx,
		store,
		"ses_trajectory_dirty_b",
		base.Add(3*time.Minute),
		transcript.CoverageComplete,
	)
	dirty, err := store.ListDirtyTrajectorySessions(ctx, version, 10)
	if err != nil {
		t.Fatal(err)
	}
	claimB := requireTrajectoryClaimForSession(
		t,
		dirty,
		"ses_trajectory_dirty_b",
	)
	if _, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		claimB,
		completeTrajectoryCoverage(),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	appendTrajectoryCanonicalFixture(
		t,
		ctx,
		store,
		"ses_trajectory_dirty_b",
		2,
		base.Add(4*time.Minute),
	)
	dirty, err = store.ListDirtyTrajectorySessions(ctx, version, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 || dirty[0].SessionKey != "ses_trajectory_dirty_b" {
		t.Fatalf("unrelated canonical append dirtied sessions = %+v", dirty)
	}

	nextVersion, err := store.ListDirtyTrajectorySessions(ctx, version+".next", 10)
	if err != nil {
		t.Fatal(err)
	}
	requireTrajectoryClaimForSession(
		t,
		nextVersion,
		"ses_trajectory_dirty_a",
	)
}

func TestTrajectoryDerivationRejectsStaleClaimAndRetriesPartial(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	clockTime := time.Date(2026, 9, 10, 18, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return clockTime }
	base := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	sessionKey := "ses_trajectory_stale"
	appendTrajectoryTranscriptFixture(
		t,
		ctx,
		store,
		sessionKey,
		base,
		transcript.CoveragePartial,
	)
	stale := requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	appendTrajectoryTurnFixture(
		t,
		ctx,
		store,
		sessionKey,
		1,
		base.Add(time.Minute),
	)
	if _, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		stale,
		completeTrajectoryCoverage(),
		nil,
	); !errors.Is(err, ErrTrajectoryDerivationStale) {
		t.Fatalf("stale publish error = %v, want stale", err)
	}
	if _, err := store.GetTrajectoryDerivationState(
		ctx,
		sessionKey,
		trajectoryderive.Version,
	); !errors.Is(err, ErrTrajectoryDerivationNotFound) {
		t.Fatalf("state after stale publish = %v, want not found", err)
	}

	current := requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	partialCoverage := trajectoryderive.Coverage{
		Transcript:              transcript.CoveragePartial,
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
		LinksComplete:           true,
		FullyDerived:            false,
	}
	diagnostics := []trajectoryderive.Diagnostic{{
		Code: trajectoryderive.DiagnosticPartialTranscript,
	}}
	state, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		current,
		partialCoverage,
		diagnostics,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TrajectoryDerivationPartial ||
		!state.Retryable ||
		state.AttemptCount != 1 ||
		state.RetryAt == nil ||
		!state.RetryAt.Equal(clockTime.Add(partialTrajectoryRetryDelay)) {
		t.Fatalf("partial state = %+v", state)
	}
	requireNoDirtyTrajectoryClaims(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	clockTime = *state.RetryAt
	retry := requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	if retry != current {
		t.Fatalf("partial retry claim = %+v, want %+v", retry, current)
	}

	var payload []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload FROM trajectory_derivation_state
		WHERE session_key = ? AND derivation_version = ?`,
		sessionKey,
		trajectoryderive.Version,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(trajectoryderive.DiagnosticPartialTranscript)) {
		t.Fatal("trajectory diagnostics were not encrypted")
	}
	roundTrip, err := store.GetTrajectoryDerivationState(
		ctx,
		sessionKey,
		trajectoryderive.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Diagnostics) != 1 ||
		roundTrip.Diagnostics[0].Code !=
			trajectoryderive.DiagnosticPartialTranscript {
		t.Fatalf("round-trip diagnostics = %+v", roundTrip.Diagnostics)
	}

	failed, err := store.RecordTrajectoryDerivationFailure(
		ctx,
		current,
		"trajectory_input_malformed",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != TrajectoryDerivationFailed ||
		!failed.Retryable ||
		failed.AttemptCount != 1 ||
		failed.RetryAt == nil ||
		!failed.RetryAt.Equal(clockTime.Add(failedTrajectoryRetryBaseDelay)) ||
		failed.FailureCode != "trajectory_input_malformed" ||
		failed.CompletedAt != nil {
		t.Fatalf("failed state = %+v", failed)
	}
	failedRoundTrip, err := store.GetTrajectoryDerivationState(
		ctx,
		sessionKey,
		trajectoryderive.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failedRoundTrip.Status != TrajectoryDerivationFailed ||
		failedRoundTrip.FailureCode != failed.FailureCode {
		t.Fatalf("failed round-trip state = %+v", failedRoundTrip)
	}
	requireNoDirtyTrajectoryClaims(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	clockTime = *failed.RetryAt
	retry = requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	if retry != current {
		t.Fatalf("failed retry claim = %+v, want %+v", retry, current)
	}
	secondFailure, err := store.RecordTrajectoryDerivationFailure(
		ctx,
		retry,
		"trajectory_input_malformed",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondFailure.AttemptCount != 2 ||
		secondFailure.RetryAt == nil ||
		!secondFailure.RetryAt.Equal(
			clockTime.Add(2*failedTrajectoryRetryBaseDelay),
		) {
		t.Fatalf("second failed state = %+v", secondFailure)
	}
	requireNoDirtyTrajectoryClaims(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)

	appendTrajectoryTurnFixture(
		t,
		ctx,
		store,
		sessionKey,
		2,
		base.Add(2*time.Minute),
	)
	changed := requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	if changed.TranscriptFingerprint == retry.TranscriptFingerprint {
		t.Fatalf("changed input retained transcript fingerprint: %+v", changed)
	}
	changedFailure, err := store.RecordTrajectoryDerivationFailure(
		ctx,
		changed,
		"trajectory_input_malformed",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if changedFailure.AttemptCount != 1 ||
		changedFailure.RetryAt == nil ||
		!changedFailure.RetryAt.Equal(
			clockTime.Add(failedTrajectoryRetryBaseDelay),
		) {
		t.Fatalf("changed-input failed state = %+v", changedFailure)
	}
}

func TestTrajectoryDerivationIndexMismatchAndMutationGuard(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	sessionKey := "ses_trajectory_integrity"
	appendTrajectoryTranscriptFixture(
		t,
		ctx,
		store,
		sessionKey,
		base,
		transcript.CoverageComplete,
	)
	claim := requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	if _, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		claim,
		completeTrajectoryCoverage(),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE trajectory_derivation_state
		SET attempted_at = '2026-09-10T20:00:00.000000000Z'
		WHERE session_key = ? AND derivation_version = ?`,
		sessionKey,
		trajectoryderive.Version,
	); err == nil {
		t.Fatal("unauthorized trajectory derivation update succeeded")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationTrajectoryDerivation, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE trajectory_derivation_state
			SET attempted_at = '2026-09-10T20:00:00.000000000Z'
			WHERE session_key = ? AND derivation_version = ?`,
			sessionKey,
			trajectoryderive.Version,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTrajectoryDerivationState(
		ctx,
		sessionKey,
		trajectoryderive.Version,
	); err == nil {
		t.Fatal("trajectory derivation index mismatch was accepted")
	}
}

func TestTrajectoryDerivationSuccessResetsFailureBackoff(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	clockTime := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return clockTime }
	sessionKey := "ses_trajectory_retry_reset"
	appendTrajectoryTranscriptFixture(
		t,
		ctx,
		store,
		sessionKey,
		clockTime.Add(-time.Minute),
		transcript.CoverageComplete,
	)
	claim := requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	failed, err := store.RecordTrajectoryDerivationFailure(
		ctx,
		claim,
		"trajectory_derivation_failed",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.AttemptCount != 1 || failed.RetryAt == nil {
		t.Fatalf("failed state = %+v", failed)
	}
	clockTime = *failed.RetryAt
	requireOnlyDirtyTrajectoryClaim(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
	complete, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		claim,
		completeTrajectoryCoverage(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if complete.AttemptCount != 0 ||
		complete.RetryAt != nil ||
		complete.Retryable {
		t.Fatalf("complete retry state = %+v", complete)
	}
	requireNoDirtyTrajectoryClaims(
		t,
		ctx,
		store,
		trajectoryderive.Version,
	)
}

func TestFailedTrajectoryRetryDelayIsBounded(t *testing.T) {
	if got := failedTrajectoryRetryDelay(1); got != failedTrajectoryRetryBaseDelay {
		t.Fatalf("first retry delay = %s", got)
	}
	if got := failedTrajectoryRetryDelay(maxTrajectoryDerivationAttempts); got !=
		failedTrajectoryRetryMaxDelay {
		t.Fatalf("bounded retry delay = %s", got)
	}
}

func completeTrajectoryCoverage() trajectoryderive.Coverage {
	return trajectoryderive.Coverage{
		Transcript:              transcript.CoverageComplete,
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
		LinksComplete:           true,
		FullyDerived:            true,
	}
}

func appendTrajectoryTranscriptFixture(
	t *testing.T,
	ctx context.Context,
	store *Store,
	sessionKey string,
	occurredAt time.Time,
	coverage transcript.SessionCoverage,
) {
	t.Helper()
	session := transcriptTestSession(sessionKey, coverage)
	turn := transcriptTestTurn(
		"turn-"+sessionKey+"-0",
		"source-"+sessionKey+"-0",
		sessionKey,
		0,
		occurredAt,
		transcript.RoleSystem,
		transcript.Payload{JSONLByteOffset: 0},
	)
	if inserted, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil || inserted != 1 {
		t.Fatalf("AppendTranscriptBatch() = %d, %v", inserted, err)
	}
}

func appendTrajectoryTurnFixture(
	t *testing.T,
	ctx context.Context,
	store *Store,
	sessionKey string,
	index int64,
	occurredAt time.Time,
) {
	t.Helper()
	session, err := store.GetTranscriptSession(ctx, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	turn := transcriptTestTurn(
		"turn-"+sessionKey+"-"+occurredAt.Format("150405"),
		"source-"+sessionKey+"-"+occurredAt.Format("150405"),
		sessionKey,
		index,
		occurredAt,
		transcript.RoleAssistant,
		transcript.Payload{
			Text:            "continued",
			JSONLByteOffset: index * 100,
		},
	)
	if inserted, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil || inserted != 1 {
		t.Fatalf("AppendTranscriptBatch() = %d, %v", inserted, err)
	}
}

func appendTrajectoryCanonicalFixture(
	t *testing.T,
	ctx context.Context,
	store *Store,
	sessionKey string,
	unique int,
	occurredAt time.Time,
) {
	t.Helper()
	event := storageTestEvent(
		"00000000-0000-7000-8000-"+formatFixtureSuffix(unique),
		sessionKey,
		int64(unique),
		occurredAt,
	)
	event.Source.RecordID = "trajectory-derivation-record-" + formatFixtureSuffix(unique)
	event.Source.DeduplicationKey = "sha256:" +
		formatFixtureHash(unique)
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent() = %t, %v", inserted, err)
	}
}

func formatFixtureSuffix(value int) string {
	return fmt.Sprintf("%012d", value)
}

func formatFixtureHash(value int) string {
	return fmt.Sprintf("%064x", value)
}

func requireOnlyDirtyTrajectoryClaim(
	t *testing.T,
	ctx context.Context,
	store *Store,
	version string,
) TrajectoryDerivationClaim {
	t.Helper()
	dirty, err := store.ListDirtyTrajectorySessions(ctx, version, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 {
		t.Fatalf("dirty trajectory claims = %+v, want one", dirty)
	}
	return dirty[0]
}

func requireNoDirtyTrajectoryClaims(
	t *testing.T,
	ctx context.Context,
	store *Store,
	version string,
) {
	t.Helper()
	dirty, err := store.ListDirtyTrajectorySessions(ctx, version, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Fatalf("dirty trajectory claims = %+v, want none", dirty)
	}
}

func requireTrajectoryClaimForSession(
	t *testing.T,
	claims []TrajectoryDerivationClaim,
	sessionKey string,
) TrajectoryDerivationClaim {
	t.Helper()
	for _, claim := range claims {
		if claim.SessionKey == sessionKey {
			return claim
		}
	}
	t.Fatalf("missing trajectory claim for %q in %+v", sessionKey, claims)
	return TrajectoryDerivationClaim{}
}
