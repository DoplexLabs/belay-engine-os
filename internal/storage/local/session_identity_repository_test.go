package local

import (
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/sessionidentity"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestSessionIdentityReconciliationActivatesAndSupersedesExactLinks(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	transcriptObservation := sessionidentity.Observation{
		SourceKind:       sessionidentity.SourceTranscript,
		SourceAgent:      "codex",
		SourceSessionKey: "ses_transcript_exact",
		NativeNamespace:  "codex_transcript",
		NativeSessionID:  "native-exact",
		ProjectIdentity:  "project-exact",
		Coverage:         sessionidentity.CoverageComplete,
		ObservedAt:       now,
	}
	artifactObservation := sessionidentity.Observation{
		SourceKind:       sessionidentity.SourceNumbatArtifact,
		SourceAgent:      "codex",
		SourceSessionKey: "ses_artifact_exact",
		NativeNamespace:  "numbat_artifact",
		NativeSessionID:  "native-exact",
		ProjectIdentity:  "project-exact",
		Coverage:         sessionidentity.CoverageObserved,
		ObservedAt:       now,
	}
	if err := store.UpsertSessionIdentityObservation(
		ctx,
		transcriptObservation,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSessionIdentityObservation(
		ctx,
		artifactObservation,
	); err != nil {
		t.Fatal(err)
	}
	var stableObservationID string
	if err := store.db.QueryRow(`
		SELECT observation_id
		FROM session_identity_observations
		WHERE source_session_key = ?`,
		artifactObservation.SourceSessionKey,
	).Scan(&stableObservationID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationSessionIdentity, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE session_identity_observations
			SET observation_id = ?
			WHERE observation_id = ?`,
			"sio_legacy_observation_id",
			stableObservationID,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QuerySessionIdentityObservations(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var repairedObservationID string
	if err := store.db.QueryRow(`
		SELECT observation_id
		FROM session_identity_observations
		WHERE source_session_key = ?`,
		artifactObservation.SourceSessionKey,
	).Scan(&repairedObservationID); err != nil {
		t.Fatal(err)
	}
	if repairedObservationID != stableObservationID {
		t.Fatalf(
			"repaired observation ID = %q, want %q",
			repairedObservationID,
			stableObservationID,
		)
	}
	if inserted, err := store.ReconcileSessionIdentityLinks(ctx); err != nil ||
		inserted != 1 {
		t.Fatalf("first reconciliation = %d, %v", inserted, err)
	}
	assertSessionIdentityLinkState(
		t,
		store,
		sessionidentity.BasisExactNativeID,
		sessionidentity.StateActive,
	)
	assertSessionIdentityAlias(
		t,
		store,
		"ses_artifact_exact",
		"ses_transcript_exact",
	)

	artifactObservation.NativeSessionID = "native-corrected"
	artifactObservation.ObservedAt = now.Add(time.Minute)
	if err := store.UpsertSessionIdentityObservation(
		ctx,
		artifactObservation,
	); err != nil {
		t.Fatal(err)
	}
	if inserted, err := store.ReconcileSessionIdentityLinks(ctx); err != nil ||
		inserted != 0 {
		t.Fatalf("superseding reconciliation = %d, %v", inserted, err)
	}
	assertSessionIdentityLinkState(
		t,
		store,
		sessionidentity.BasisExactNativeID,
		sessionidentity.StateSuperseded,
	)
	assertSessionIdentityAlias(t, store, "ses_artifact_exact", "")

	artifactObservation.NativeSessionID = "native-exact"
	artifactObservation.ObservedAt = now.Add(2 * time.Minute)
	if err := store.UpsertSessionIdentityObservation(
		ctx,
		artifactObservation,
	); err != nil {
		t.Fatal(err)
	}
	if inserted, err := store.ReconcileSessionIdentityLinks(ctx); err != nil ||
		inserted != 0 {
		t.Fatalf("reactivating reconciliation = %d, %v", inserted, err)
	}
	assertSessionIdentityLinkState(
		t,
		store,
		sessionidentity.BasisExactNativeID,
		sessionidentity.StateActive,
	)
	assertSessionIdentityAlias(
		t,
		store,
		"ses_artifact_exact",
		"ses_transcript_exact",
	)
}

func TestSessionIdentityReconciliationBackfillsExistingTranscriptSessions(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		transcript.Session{
			SessionKey:      "ses_transcript_upgrade",
			Agent:           "codex",
			NativeSessionID: "native-upgrade",
			ProjectPath:     "/project",
			ProjectIdentity: "project-upgrade",
			StartedAt:       now,
			EndedAt:         now.Add(time.Minute),
			Coverage:        transcript.CoverageComplete,
		},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationSessionIdentity, func() error {
		_, err := tx.ExecContext(ctx, `
			DELETE FROM session_identity_observations
			WHERE source_kind = ?`,
			sessionidentity.SourceTranscript,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSessionIdentityObservation(
		ctx,
		sessionidentity.Observation{
			SourceKind:       sessionidentity.SourceNumbatArtifact,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_artifact_upgrade",
			NativeNamespace:  "numbat_artifact",
			NativeSessionID:  "native-upgrade",
			ProjectIdentity:  "project-upgrade",
			Coverage:         sessionidentity.CoverageObserved,
			ObservedAt:       now,
		},
	); err != nil {
		t.Fatal(err)
	}
	if inserted, err := store.ReconcileSessionIdentityLinks(ctx); err != nil ||
		inserted != 1 {
		t.Fatalf("upgrade reconciliation = %d, %v", inserted, err)
	}
	audit, err := store.ReadSessionIdentityAudit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Observations != 2 || audit.ExactNativeMatches != 1 ||
		audit.ActiveLinks != 1 {
		t.Fatalf("identity audit = %+v", audit)
	}
	assertSessionIdentityAlias(
		t,
		store,
		"ses_artifact_upgrade",
		"ses_transcript_upgrade",
	)
}

func assertSessionIdentityLinkState(
	t *testing.T,
	store *Store,
	basis string,
	want string,
) {
	t.Helper()
	var state string
	if err := store.db.QueryRow(`
		SELECT state
		FROM session_identity_links
		WHERE basis = ?`,
		basis,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != want {
		t.Fatalf("link state = %q, want %q", state, want)
	}
}

func assertSessionIdentityAlias(
	t *testing.T,
	store *Store,
	sessionKey string,
	wantTarget string,
) {
	t.Helper()
	aliases, err := store.QueryActiveSessionIdentityAliases(
		context.Background(),
		[]string{sessionKey},
	)
	if err != nil {
		t.Fatal(err)
	}
	alias, ok := aliases[sessionKey]
	if ok != (wantTarget != "") {
		t.Fatalf(
			"alias present = %v, want target %q: %+v",
			ok,
			wantTarget,
			aliases,
		)
	}
	if ok && alias.LinkedSessionKey != wantTarget {
		t.Fatalf("alias = %+v", alias)
	}
}
