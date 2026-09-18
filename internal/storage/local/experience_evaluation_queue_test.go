package local

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestQueryExperienceApplicationsNeedingEvaluation(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }

	candidate := storageExperienceCandidate("evaluation queue")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}

	appendSessionTurn := func(
		sessionKey string,
		turnIndex int64,
		at time.Time,
	) {
		t.Helper()
		session := transcriptTestSession(sessionKey, transcript.CoverageLive)
		session.ProjectIdentity = value.Scope.ProjectIdentity
		session.ProjectPath = "/tmp/" + sessionKey
		session.GitRemoteURL = value.Scope.ProjectIdentity
		turn := transcriptTestTurn(
			"turn-"+sessionKey+"-"+time.Unix(turnIndex, 0).UTC().Format("150405"),
			"source-"+sessionKey+"-"+time.Unix(turnIndex, 0).UTC().Format("150405"),
			sessionKey,
			turnIndex,
			at,
			transcript.RoleAssistant,
			transcript.Payload{Text: "bounded evidence", JSONLByteOffset: turnIndex},
		)
		if _, err := store.AppendTranscriptBatch(
			ctx,
			session,
			[]transcript.Turn{turn},
		); err != nil {
			t.Fatalf("AppendTranscriptBatch(%q): %v", sessionKey, err)
		}
	}
	insertDelivered := func(sessionKey string, deliveredAt time.Time) experience.Application {
		t.Helper()
		application := storageExperienceApplication(value)
		application.SessionKey = sessionKey
		application.DeliveryState = experience.DeliveryDelivered
		application.DeliveredAt = &deliveredAt
		application.ApplicationID = application.DeterministicID()
		if _, err := store.InsertExperienceApplication(ctx, application); err != nil {
			t.Fatalf("InsertExperienceApplication(%q): %v", sessionKey, err)
		}
		return application
	}
	evaluateAt := func(application experience.Application, at time.Time) {
		t.Helper()
		evidenceTurn := int64(0)
		evaluation := experience.Evaluation{
			SchemaVersion:      experience.EvaluationSchemaVersion,
			ApplicationID:      application.ApplicationID,
			Experience:         application.Experience,
			EvaluatedAt:        at,
			OpportunityState:   experience.OpportunityObserved,
			ApplicabilityState: experience.ApplicabilityApplicable,
			VerifierState:      experience.VerifierUnknown,
			TaskOutcomeState:   experience.TaskOutcomeUnknown,
			Coverage: []experience.CoverageRequirement{
				experience.CoverageTranscriptComplete,
			},
			SourceEvidence: []experience.EvidenceRef{{
				Kind:       experience.EvidenceTranscriptTurn,
				SessionKey: application.SessionKey,
				TurnIndex:  &evidenceTurn,
				OccurredAt: application.DeliveredAt,
				Excerpt:    "bounded evidence",
			}},
			DerivationVersion: "evaluation.queue.test.v1",
		}
		evaluation.EvaluationID = evaluation.DeterministicID()
		if _, applied, err := store.ApplyExperienceEvaluation(
			ctx,
			evaluation,
		); err != nil || !applied {
			t.Fatalf(
				"ApplyExperienceEvaluation(%q) = %v, %v",
				application.SessionKey,
				applied,
				err,
			)
		}
	}

	t.Run("never evaluated selected and delivered only", func(t *testing.T) {
		sessionKey := "ses_evaluation_queue_never"
		appendSessionTurn(sessionKey, 0, now.Add(-time.Minute))
		delivered := insertDelivered(sessionKey, now.Add(-2*time.Minute))

		pending := storageExperienceApplication(value)
		pending.SessionKey = sessionKey
		pending.ApplicationID = pending.DeterministicID()
		if _, err := store.InsertExperienceApplication(ctx, pending); err != nil {
			t.Fatal(err)
		}

		got, err := store.QueryExperienceApplicationsNeedingEvaluation(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []experience.Application{delivered}) {
			t.Fatalf("queue = %+v, want only never-evaluated delivered application", got)
		}
	})

	t.Run("unchanged evaluated omitted and updated transcript selected", func(t *testing.T) {
		sessionKey := "ses_evaluation_queue_updated"
		now = now.Add(time.Hour)
		appendSessionTurn(sessionKey, 0, now.Add(-time.Minute))
		application := insertDelivered(sessionKey, now.Add(-2*time.Minute))
		evaluatedAt := now.Add(time.Minute)
		evaluateAt(application, evaluatedAt)

		got, err := store.QueryExperienceApplicationsNeedingEvaluation(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, queued := range got {
			if queued.ApplicationID == application.ApplicationID {
				t.Fatalf("unchanged evaluated application was queued: %+v", queued)
			}
		}

		now = evaluatedAt.Add(time.Minute)
		appendSessionTurn(sessionKey, 1, now)
		got, err = store.QueryExperienceApplicationsNeedingEvaluation(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, queued := range got {
			if queued.ApplicationID == application.ApplicationID {
				found = true
				if queued.VerifierState != experience.VerifierUnknown {
					t.Fatalf("reevaluation lost prior verifier state: %+v", queued)
				}
			}
		}
		if !found {
			t.Fatal("updated evaluated application was not queued")
		}
	})
}

func TestQueryExperienceApplicationsNeedingEvaluationBoundedOldestFirst(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }

	candidate := storageExperienceCandidate("evaluation queue ordering")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}

	type queuedApplication struct {
		application experience.Application
		deliveredAt time.Time
	}
	values := make([]queuedApplication, 0, 3)
	for index, offset := range []time.Duration{-3 * time.Hour, -time.Hour, -2 * time.Hour} {
		sessionKey := "ses_evaluation_order_" + string(rune('a'+index))
		session := transcriptTestSession(sessionKey, transcript.CoverageComplete)
		session.ProjectIdentity = value.Scope.ProjectIdentity
		session.GitRemoteURL = value.Scope.ProjectIdentity
		turn := transcriptTestTurn(
			"turn-evaluation-order-"+string(rune('a'+index)),
			"source-evaluation-order-"+string(rune('a'+index)),
			sessionKey,
			0,
			now.Add(offset),
			transcript.RoleAssistant,
			transcript.Payload{Text: "evidence", JSONLByteOffset: 0},
		)
		if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
			t.Fatal(err)
		}
		deliveredAt := now.Add(offset)
		application := storageExperienceApplication(value)
		application.SessionKey = sessionKey
		application.DeliveryState = experience.DeliveryDelivered
		application.DeliveredAt = &deliveredAt
		application.ApplicationID = application.DeterministicID()
		if _, err := store.InsertExperienceApplication(ctx, application); err != nil {
			t.Fatal(err)
		}
		values = append(values, queuedApplication{application: application, deliveredAt: deliveredAt})
	}

	got, err := store.QueryExperienceApplicationsNeedingEvaluation(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []experience.Application{
		values[0].application,
		values[2].application,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded queue = %+v, want oldest first %+v", got, want)
	}
	if _, err := store.QueryExperienceApplicationsNeedingEvaluation(
		ctx,
		maxExperienceQueryLimit+1,
	); err == nil {
		t.Fatal("query accepted a limit above the repository safety bound")
	}
}

func TestQueryExperienceApplicationsNeedingEvaluationAfterTrajectoryDerivation(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }

	candidate := storageExperienceCandidate("evaluation queue trajectory")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}

	const sessionKey = "ses_evaluation_queue_trajectory"
	session := transcriptTestSession(sessionKey, transcript.CoverageComplete)
	session.ProjectIdentity = value.Scope.ProjectIdentity
	session.GitRemoteURL = value.Scope.ProjectIdentity
	turn := transcriptTestTurn(
		"turn-evaluation-queue-trajectory",
		"source-evaluation-queue-trajectory",
		sessionKey,
		0,
		now.Add(-time.Minute),
		transcript.RoleAssistant,
		transcript.Payload{Text: "bounded evidence", JSONLByteOffset: 0},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}

	deliveredAt := now.Add(-2 * time.Minute)
	application := storageExperienceApplication(value)
	application.SessionKey = sessionKey
	application.DeliveryState = experience.DeliveryDelivered
	application.DeliveredAt = &deliveredAt
	application.ApplicationID = application.DeterministicID()
	if _, err := store.InsertExperienceApplication(ctx, application); err != nil {
		t.Fatal(err)
	}

	evidenceTurn := int64(0)
	evaluatedAt := now.Add(time.Minute)
	evaluation := experience.Evaluation{
		SchemaVersion:      experience.EvaluationSchemaVersion,
		ApplicationID:      application.ApplicationID,
		Experience:         application.Experience,
		EvaluatedAt:        evaluatedAt,
		OpportunityState:   experience.OpportunityObserved,
		ApplicabilityState: experience.ApplicabilityApplicable,
		VerifierState:      experience.VerifierUnknown,
		TaskOutcomeState:   experience.TaskOutcomeUnknown,
		Coverage: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		SourceEvidence: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: sessionKey,
			TurnIndex:  &evidenceTurn,
			OccurredAt: &turn.OccurredAt,
			Excerpt:    "bounded evidence",
		}},
		DerivationVersion: "evaluation.queue.test.v1",
	}
	evaluation.EvaluationID = evaluation.DeterministicID()
	evaluatedApplication, applied, err := store.ApplyExperienceEvaluation(
		ctx,
		evaluation,
	)
	if err != nil || !applied {
		t.Fatalf("ApplyExperienceEvaluation() = %v, %v", applied, err)
	}

	queued, err := store.QueryExperienceApplicationsNeedingEvaluation(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("unchanged evaluated application queued before derivation: %+v", queued)
	}

	now = evaluatedAt.Add(time.Minute)
	claims, err := store.ListDirtyTrajectorySessions(
		ctx,
		trajectoryderive.Version,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	claim := requireTrajectoryClaimForSession(t, claims, sessionKey)
	if _, err := store.MarkTrajectoryDerivationCurrent(
		ctx,
		claim,
		completeTrajectoryCoverage(),
		nil,
	); err != nil {
		t.Fatal(err)
	}

	queued, err = store.QueryExperienceApplicationsNeedingEvaluation(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		queued,
		[]experience.Application{evaluatedApplication},
	) {
		t.Fatalf(
			"queue after newer trajectory derivation = %+v, want %+v",
			queued,
			evaluatedApplication,
		)
	}
}
