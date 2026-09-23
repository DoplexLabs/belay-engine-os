package local

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestMigration022ExperienceReviewActionsSchema(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)

	for _, object := range []struct {
		kind string
		name string
	}{
		{kind: "table", name: "experience_review_actions"},
		{
			kind: "index",
			name: "experience_review_actions_proposal_latest_idx",
		},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_schema
			WHERE type = ? AND name = ?`,
			object.kind,
			object.name,
		).Scan(&count); err != nil || count != 1 {
			t.Fatalf(
				"%s %s count/error = %d/%v",
				object.kind,
				object.name,
				count,
				err,
			)
		}
	}
	var migrations int
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM schema_migrations`,
	).Scan(&migrations); err != nil || migrations != 30 {
		t.Fatalf("migration count/error = %d/%v, want 30", migrations, err)
	}
}

func TestRecordExperienceReviewActionEncryptsReplaysConflictsAndGuards(
	t *testing.T,
) {
	const actorCanary = "PRIVATE_REVIEW_ACTOR_CANARY"
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	_, proposal, claims := storageExperienceReviewFixture(
		t,
		store,
		"review persistence",
		now,
	)
	availableAfter := now.Add(time.Hour)
	input := ExperienceReviewActionInput{
		Claims:         claims,
		Actor:          actorCanary,
		Disposition:    ExperienceReviewDefer,
		OccurredAt:     now,
		AvailableAfter: &availableAfter,
	}

	first, err := store.RecordExperienceReviewAction(ctx, input)
	if err != nil || first.Replayed ||
		first.Action.ProposalID != proposal.ProposalID ||
		first.Action.Actor != actorCanary {
		t.Fatalf("record review action = %+v/%v", first, err)
	}
	var payload []byte
	var encoding string
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload, payload_encoding
		FROM experience_review_actions
		WHERE action_id = ?`,
		first.Action.ActionID,
	).Scan(&payload, &encoding); err != nil {
		t.Fatal(err)
	}
	if encoding != payloadEncodingAESGCM ||
		bytes.Contains(payload, []byte(actorCanary)) ||
		bytes.Contains(payload, []byte(claims.ProjectIdentity)) {
		t.Fatal("experience review action payload was not encrypted")
	}
	plaintext, err := store.cipher.open(
		"experience_review_action",
		first.Action.ActionID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeExperienceReviewActionPayload(plaintext)
	if err != nil || decoded.Actor != actorCanary ||
		decoded.Claims != claims {
		t.Fatalf("decoded review action = %+v/%v", decoded, err)
	}

	replayed, err := store.RecordExperienceReviewAction(ctx, input)
	if err != nil || !replayed.Replayed ||
		replayed.Action.ActionID != first.Action.ActionID {
		t.Fatalf("review action replay = %+v/%v", replayed, err)
	}

	now = claims.ExpiresAt.Add(time.Nanosecond)
	replayed, err = store.RecordExperienceReviewAction(ctx, input)
	if err != nil || !replayed.Replayed ||
		replayed.Action.ActionID != first.Action.ActionID {
		t.Fatalf(
			"expired-token exact replay = %+v/%v",
			replayed,
			err,
		)
	}

	conflict := input
	conflict.Actor = "different_user"
	if _, err := store.RecordExperienceReviewAction(
		ctx,
		conflict,
	); !errors.Is(err, ErrExperienceReviewActionConflict) {
		t.Fatalf("review action conflict error = %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO experience_review_actions (
			action_id, proposal_id, candidate_id, project_identity,
			disposition, occurred_at, available_after,
			approval_token_issued_at, payload, payload_encoding,
			inserted_at
		) VALUES (?, ?, ?, ?, 'reject', ?, NULL, ?, X'00',
			'aes256gcm.v1', ?)`,
		"era_direct",
		proposal.ProposalID,
		claims.CandidateID,
		claims.ProjectIdentity,
		formatProjectionTime(now),
		formatProjectionTime(claims.IssuedAt),
		formatProjectionTime(now),
	); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("direct review action insert error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE experience_review_actions
		SET disposition = 'reject', available_after = NULL
		WHERE action_id = ?`,
		first.Action.ActionID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("direct review action update error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM experience_review_actions
		WHERE action_id = ?`,
		first.Action.ActionID,
	); err == nil {
		t.Fatal("direct review action delete bypassed mutation guard")
	}
}

func TestRecordExperienceReviewActionRetriesBusyBegin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review-busy.sqlite")
	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 9, 10, 20, 15, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	_, _, claims := storageExperienceReviewFixture(
		t,
		store,
		"busy review persistence",
		now,
	)
	if _, err := store.db.ExecContext(
		ctx,
		"PRAGMA busy_timeout = 100",
	); err != nil {
		t.Fatal(err)
	}

	writerDB, err := sql.Open("sqlite", mustSQLiteDSN(t, path))
	if err != nil {
		t.Fatal(err)
	}
	defer writerDB.Close()
	writerTx, err := writerDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writerTx.Rollback()
	if _, err := writerTx.ExecContext(ctx, `
		UPDATE local_store_metadata
		SET store_id = store_id
		WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		released <- writerTx.Rollback()
	}()

	result, err := store.RecordExperienceReviewAction(
		ctx,
		ExperienceReviewActionInput{
			Claims:      claims,
			Actor:       "user",
			Disposition: ExperienceReviewReject,
			OccurredAt:  now,
		},
	)
	if releaseErr := <-released; releaseErr != nil &&
		!errors.Is(releaseErr, sql.ErrTxDone) {
		t.Fatal(releaseErr)
	}
	if err != nil || result.Replayed {
		t.Fatalf("busy review action = %+v/%v", result, err)
	}
}

func TestRecordExperienceReviewActionRejectsInvalidStaleAndApproved(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC)

	t.Run("actor", func(t *testing.T) {
		store := openStorageTestStore(t)
		store.clock = func() time.Time { return now }
		_, _, claims := storageExperienceReviewFixture(
			t,
			store,
			"invalid actor",
			now,
		)
		_, err := store.RecordExperienceReviewAction(
			ctx,
			ExperienceReviewActionInput{
				Claims:      claims,
				Disposition: ExperienceReviewReject,
				OccurredAt:  now,
			},
		)
		if !errors.Is(err, ErrExperienceReviewActionInvalid) {
			t.Fatalf("invalid actor error = %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*experience.ApprovalTokenClaims)
		clock  func(experience.ApprovalTokenClaims) time.Time
	}{
		{
			name: "project",
			mutate: func(claims *experience.ApprovalTokenClaims) {
				claims.ProjectIdentity =
					"git@example.test:doplexlabs/other.git"
			},
		},
		{
			name: "evidence",
			mutate: func(claims *experience.ApprovalTokenClaims) {
				claims.EvidenceGeneration =
					storageSHA256("stale evidence")
			},
		},
		{
			name: "expired token",
			clock: func(claims experience.ApprovalTokenClaims) time.Time {
				return claims.ExpiresAt.Add(time.Nanosecond)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openStorageTestStore(t)
			store.clock = func() time.Time { return now }
			_, _, claims := storageExperienceReviewFixture(
				t,
				store,
				test.name,
				now,
			)
			if test.mutate != nil {
				test.mutate(&claims)
			}
			actionAt := now
			if test.clock != nil {
				current := test.clock(claims)
				store.clock = func() time.Time { return current }
				actionAt = claims.ExpiresAt
			}
			_, err := store.RecordExperienceReviewAction(
				ctx,
				ExperienceReviewActionInput{
					Claims:      claims,
					Actor:       "user",
					Disposition: ExperienceReviewReject,
					OccurredAt:  actionAt,
				},
			)
			if !errors.Is(err, ErrExperienceReviewActionStale) {
				t.Fatalf("stale review action error = %v", err)
			}
		})
	}

	t.Run("approved", func(t *testing.T) {
		store := openStorageTestStore(t)
		store.clock = func() time.Time { return now }
		_, _, claims := storageExperienceReviewFixture(
			t,
			store,
			"already approved",
			now,
		)
		if _, err := store.ApproveExperienceProposal(
			ctx,
			ExperienceApprovalInput{
				Claims:     claims,
				ApprovedBy: "user",
				ApprovedAt: now,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordExperienceReviewAction(
			ctx,
			ExperienceReviewActionInput{
				Claims:      claims,
				Actor:       "user",
				Disposition: ExperienceReviewReject,
				OccurredAt:  now,
			},
		); !errors.Is(err, ErrExperienceReviewActionStale) {
			t.Fatalf("approved proposal review action error = %v", err)
		}
	})
}

func TestPendingExperienceSemanticProposalsHonorLatestReviewAction(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	_, rejected, rejectClaims := storageExperienceReviewFixture(
		t,
		store,
		"reject visibility",
		now,
	)
	_, deferred, deferClaims := storageExperienceReviewFixture(
		t,
		store,
		"defer visibility",
		now,
	)

	pending := storagePendingReviewProposalIDs(t, store, false)
	if !pending[rejected.ProposalID] || !pending[deferred.ProposalID] {
		t.Fatalf("initial pending proposals = %+v", pending)
	}
	if _, err := store.RecordExperienceReviewAction(
		ctx,
		ExperienceReviewActionInput{
			Claims:      rejectClaims,
			Actor:       "user",
			Disposition: ExperienceReviewReject,
			OccurredAt:  now,
		},
	); err != nil {
		t.Fatal(err)
	}
	firstAvailable := now.Add(time.Hour)
	if _, err := store.RecordExperienceReviewAction(
		ctx,
		ExperienceReviewActionInput{
			Claims:         deferClaims,
			Actor:          "user",
			Disposition:    ExperienceReviewDefer,
			OccurredAt:     now,
			AvailableAfter: &firstAvailable,
		},
	); err != nil {
		t.Fatal(err)
	}
	if pending := storagePendingReviewProposalIDs(t, store, false); len(pending) != 0 {
		t.Fatalf("hidden pending proposals = %+v", pending)
	}
	pending = storagePendingReviewProposalIDs(t, store, true)
	if pending[rejected.ProposalID] ||
		!pending[deferred.ProposalID] ||
		len(pending) != 1 {
		t.Fatalf("explicit deferred review proposals = %+v", pending)
	}

	now = firstAvailable
	pending = storagePendingReviewProposalIDs(t, store, false)
	if pending[rejected.ProposalID] ||
		!pending[deferred.ProposalID] ||
		len(pending) != 1 {
		t.Fatalf("expired defer pending proposals = %+v", pending)
	}

	secondClaims := deferClaims
	secondClaims.IssuedAt = now
	secondClaims.ExpiresAt = now.Add(experienceApprovalTokenLifetime)
	secondAvailable := now.Add(2 * time.Hour)
	second, err := store.RecordExperienceReviewAction(
		ctx,
		ExperienceReviewActionInput{
			Claims:         secondClaims,
			Actor:          "user",
			Disposition:    ExperienceReviewDefer,
			OccurredAt:     now,
			AvailableAfter: &secondAvailable,
		},
	)
	if err != nil || second.Replayed {
		t.Fatalf("second defer = %+v/%v", second, err)
	}
	if pending := storagePendingReviewProposalIDs(t, store, false); len(pending) != 0 {
		t.Fatalf("second defer pending proposals = %+v", pending)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM experience_review_actions
		WHERE proposal_id = ?`,
		deferred.ProposalID,
	).Scan(&count); err != nil || count != 2 {
		t.Fatalf("defer action count = %d/%v, want 2", count, err)
	}
}

func storageExperienceReviewFixture(
	t *testing.T,
	store *Store,
	label string,
	now time.Time,
) (
	experience.Candidate,
	experience.SemanticProposal,
	experience.ApprovalTokenClaims,
) {
	t.Helper()
	ctx := context.Background()
	candidate := storageExperienceCandidate(label)
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(
		candidate,
		"Use the evidence-backed workflow for "+label+".",
	)
	proposal.Provenance.PromptVersion =
		experience.SemanticProposalPromptVersion
	proposal.Provenance.InputHash =
		storageSHA256("semantic input " + label)
	proposal.Provenance.OutputHash =
		storageSHA256("semantic output " + label)
	proposal.ProposalID = proposal.DeterministicID()
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	); err != nil {
		t.Fatal(err)
	}
	return candidate, proposal,
		storageExperienceApprovalRepositoryClaims(
			now,
			candidate,
			proposal,
		)
}

func storagePendingReviewProposalIDs(
	t *testing.T,
	store *Store,
	includeDeferred bool,
) map[string]bool {
	t.Helper()
	values, err := store.QueryPendingCurrentExperienceSemanticProposals(
		context.Background(),
		storageProjectIdentity,
		experience.HarnessClaude,
		100,
		includeDeferred,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value.ProposalID] = true
	}
	return result
}
