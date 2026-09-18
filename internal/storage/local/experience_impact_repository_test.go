package local

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/impact"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestExperienceImpactPersistenceReplayAndQueue(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return base }

	candidate := storageExperienceCandidate("impact observation")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}

	sessionKey := "ses_impact_observation"
	session := transcriptTestSession(sessionKey, transcript.CoverageComplete)
	session.Agent = "codex"
	session.ProjectIdentity = value.Scope.ProjectIdentity
	session.ProjectPath = "/tmp/impact-observation"
	session.GitRemoteURL = value.Scope.ProjectIdentity
	session.StartedAt = base.Add(-time.Hour)
	session.EndedAt = base.Add(-time.Minute)
	turn := transcriptTestTurn(
		"turn-impact-observation",
		"source-impact-observation",
		sessionKey,
		0,
		base.Add(-30*time.Minute),
		transcript.RoleAssistant,
		transcript.Payload{Text: "Done.", JSONLByteOffset: 12},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}

	deliveredAt := base.Add(-45 * time.Minute)
	application := storageExperienceApplication(value)
	application.SessionKey = sessionKey
	application.DeliveryState = experience.DeliveryDelivered
	application.DeliveredAt = &deliveredAt
	application.ApplicationID = application.DeterministicID()
	if _, err := store.InsertExperienceApplication(ctx, application); err != nil {
		t.Fatal(err)
	}
	evidenceTurn := int64(0)
	evaluation := experience.Evaluation{
		SchemaVersion:      experience.EvaluationSchemaVersion,
		ApplicationID:      application.ApplicationID,
		Experience:         application.Experience,
		EvaluatedAt:        base.Add(time.Minute),
		OpportunityState:   experience.OpportunityObserved,
		ApplicabilityState: experience.ApplicabilityApplicable,
		VerifierState:      experience.VerifierSatisfied,
		TaskOutcomeState:   experience.TaskOutcomeSucceeded,
		Coverage: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		SourceEvidence: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: sessionKey,
			TurnIndex:  &evidenceTurn,
			Excerpt:    "Done.",
		}},
		DerivationVersion: "impact.repository.evaluation.v1",
	}
	evaluation.EvaluationID = evaluation.DeterministicID()
	evaluated, applied, err := store.ApplyExperienceEvaluation(ctx, evaluation)
	if err != nil || !applied {
		t.Fatalf("ApplyExperienceEvaluation() = %+v, %v, %v", evaluated, applied, err)
	}

	queued, err := store.QueryExperienceApplicationsNeedingImpact(
		ctx,
		impact.DerivationVersion,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(queued, []experience.Application{evaluated}) {
		t.Fatalf("impact queue before observation = %+v", queued)
	}

	observation, err := impact.Derive(impact.Input{
		Application: evaluated,
		Evaluation:  &evaluation,
		Current: impact.SessionInput{
			Session:                 session,
			Turns:                   []transcript.Turn{turn},
			TaskOutcomeState:        evaluated.TaskOutcomeState,
			OutcomeCoverageComplete: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return base.Add(2 * time.Minute) }
	if inserted, err := store.InsertExperienceImpactObservation(
		ctx,
		observation,
	); err != nil || !inserted {
		t.Fatalf("InsertExperienceImpactObservation() = %v, %v", inserted, err)
	}
	if inserted, err := store.InsertExperienceImpactObservation(
		ctx,
		observation,
	); err != nil || inserted {
		t.Fatalf("replay = %v, %v", inserted, err)
	}

	got, err := store.GetExperienceImpactObservation(
		ctx,
		observation.ObservationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Observation, observation) ||
		!got.ObservedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("persisted observation = %+v", got)
	}
	var sealed []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload
		FROM experience_impact_observations
		WHERE observation_id = ?`,
		observation.ObservationID,
	).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(impact.SchemaVersion)) {
		t.Fatal("impact observation payload was not encrypted")
	}

	queued, err = store.QueryExperienceApplicationsNeedingImpact(
		ctx,
		impact.DerivationVersion,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("impact queue after observation = %+v", queued)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE experience_impact_observations
		SET input_hash = ?
		WHERE observation_id = ?`,
		storageSHA256("unauthorized"),
		observation.ObservationID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("unauthorized impact update error = %v", err)
	}
}
