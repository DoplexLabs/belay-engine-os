package localapp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/evaluate"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type experienceEvaluationTestStore struct {
	applications []experience.Application
	experiences  map[experience.ExperienceRef]local.StoredExperience
	sessions     map[string]transcript.Session
	turns        map[string][]transcript.Turn
	outcomes     map[string][]trajectory.Outcome
	canonical    map[string][]model.Event
	trajectories map[string]local.TrajectoryDerivationState
	getErrors    map[experience.ExperienceRef]error
	evaluations  map[string]experience.Evaluation
}

func (store *experienceEvaluationTestStore) QueryExperienceApplicationsNeedingEvaluation(
	_ context.Context,
	limit int,
) ([]experience.Application, error) {
	result := append([]experience.Application(nil), store.applications...)
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (store *experienceEvaluationTestStore) GetExperience(
	_ context.Context,
	ref experience.ExperienceRef,
) (local.StoredExperience, error) {
	if err := store.getErrors[ref]; err != nil {
		return local.StoredExperience{}, err
	}
	value, found := store.experiences[ref]
	if !found {
		return local.StoredExperience{}, errors.New("experience not found")
	}
	return value, nil
}

func (store *experienceEvaluationTestStore) GetTranscriptSession(
	_ context.Context,
	sessionKey string,
) (transcript.Session, error) {
	value, found := store.sessions[sessionKey]
	if !found {
		return transcript.Session{}, errors.New("session not found")
	}
	return value, nil
}

func (store *experienceEvaluationTestStore) QueryTranscriptTurns(
	_ context.Context,
	sessionKey string,
	_ int,
) ([]transcript.Turn, error) {
	return append([]transcript.Turn(nil), store.turns[sessionKey]...), nil
}

func (store *experienceEvaluationTestStore) QueryOutcomes(
	_ context.Context,
	query local.OutcomeQuery,
) ([]trajectory.Outcome, error) {
	return append([]trajectory.Outcome(nil), store.outcomes[query.SessionKey]...), nil
}

func (store *experienceEvaluationTestStore) QuerySessionTimeline(
	_ context.Context,
	query model.TimelineQuery,
) (model.EventPage, error) {
	if query.Cursor != nil {
		return model.EventPage{Snapshot: 1}, nil
	}
	events := append([]model.Event(nil), store.canonical[query.SessionID]...)
	if len(events) > query.Limit {
		events = events[:query.Limit]
	}
	return model.EventPage{Data: events, Snapshot: 1}, nil
}

func (store *experienceEvaluationTestStore) GetTrajectoryDerivationState(
	_ context.Context,
	sessionKey string,
	_ string,
) (local.TrajectoryDerivationState, error) {
	value, found := store.trajectories[sessionKey]
	if !found {
		return local.TrajectoryDerivationState{},
			local.ErrTrajectoryDerivationNotFound
	}
	return value, nil
}

func (store *experienceEvaluationTestStore) IsTrajectoryDerivationStateCurrent(
	_ context.Context,
	_ local.TrajectoryDerivationState,
) (bool, error) {
	return true, nil
}

func (store *experienceEvaluationTestStore) GetExperienceApplicationEvaluation(
	_ context.Context,
	applicationID string,
) (*experience.Evaluation, error) {
	value, found := store.evaluations[applicationID]
	if !found {
		return nil, nil
	}
	return &value, nil
}

func (store *experienceEvaluationTestStore) ApplyExperienceEvaluation(
	_ context.Context,
	value experience.Evaluation,
) (experience.Application, bool, error) {
	if err := value.Validate(); err != nil {
		return experience.Application{}, false, err
	}
	previous, found := store.evaluations[value.ApplicationID]
	if found {
		if previous.EvaluationID == value.EvaluationID {
			if !reflect.DeepEqual(previous, value) {
				return experience.Application{}, false,
					local.ErrExperienceEvaluationConflict
			}
			return store.application(value.ApplicationID), false, nil
		}
		if !value.EvaluatedAt.After(previous.EvaluatedAt) {
			return experience.Application{}, false,
				local.ErrExperienceEvaluationStale
		}
	}
	store.evaluations[value.ApplicationID] = value
	return store.application(value.ApplicationID), true, nil
}

func (store *experienceEvaluationTestStore) application(
	applicationID string,
) experience.Application {
	for _, value := range store.applications {
		if value.ApplicationID == applicationID {
			return value
		}
	}
	return experience.Application{}
}

func TestExperienceEvaluationCommandSucceededUsesExactCallAndResult(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	value := evaluationTestExperience(
		t,
		"command succeeded",
		experience.Verifier{
			Kind: experience.VerifierCommandSucceeded,
			CoverageRequirements: []experience.CoverageRequirement{
				experience.CoverageTranscriptComplete,
			},
			Command: &experience.CommandVerifierSpec{
				Command:          "go test ./...",
				CommandClass:     "test",
				ScrubbingVersion: commandScrubbingVersionV1,
			},
		},
	)
	application, session := evaluationTestBinding(value, "ses_eval_command", now)
	exitCode := 0
	turns := []transcript.Turn{
		evaluationTestTurn(
			session.SessionKey,
			0,
			now.Add(-2*time.Minute),
			transcript.RoleToolCall,
			transcript.Payload{
				RawCommand: "go test ./...",
				ToolCallID: "call-test",
			},
		),
		evaluationTestTurn(
			session.SessionKey,
			1,
			now.Add(-time.Minute),
			transcript.RoleToolResult,
			transcript.Payload{
				ToolResult: "ok",
				ToolCallID: "call-test",
				ExitCode:   &exitCode,
			},
		),
	}
	session.TurnCount = len(turns)
	store := evaluationTestStore(value, application, session, turns)
	store.outcomes[session.SessionKey] = []trajectory.Outcome{
		evaluationTestOutcome(
			session,
			turns[1],
			trajectory.OutcomeVerificationPass,
			trajectory.ResultSucceeded,
		),
		evaluationTestOutcome(
			session,
			turns[1],
			trajectory.OutcomeExplicitAcceptance,
			trajectory.ResultObserved,
		),
	}
	coordinator := evaluationTestCoordinator(t, store, func() time.Time {
		return now
	})

	report, err := coordinator.Evaluate(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if report != (ExperienceEvaluationReport{Attempted: 1, Applied: 1}) {
		t.Fatalf("report = %+v", report)
	}
	got := store.evaluations[application.ApplicationID]
	if got.VerifierState != experience.VerifierSatisfied ||
		got.TaskOutcomeState != experience.TaskOutcomeUnknown ||
		!evaluationHasTurnEvidence(got, 0) ||
		!evaluationHasTurnEvidence(got, 1) {
		t.Fatalf("evaluation = %+v", got)
	}

	replay, err := coordinator.Evaluate(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if replay != (ExperienceEvaluationReport{Attempted: 1, Replayed: 1}) {
		t.Fatalf("replay report = %+v", replay)
	}
}

func TestExperienceEvaluationAbsenceRequiresCompleteTranscriptAndCitesCorrection(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 19, 20, 0, 0, time.UTC)
	verifier := experience.Verifier{
		Kind: experience.VerifierUserCorrectionAbsent,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		UserCorrectionAbsent: &experience.UserCorrectionAbsentSpec{},
	}
	t.Run("partial transcript remains unknown", func(t *testing.T) {
		value := evaluationTestExperience(t, "partial correction", verifier)
		application, session := evaluationTestBinding(
			value,
			"ses_eval_partial",
			now,
		)
		session.Coverage = transcript.CoverageLive
		session.TurnCount = 2
		turns := []transcript.Turn{evaluationTestTurn(
			session.SessionKey,
			0,
			now.Add(-time.Minute),
			transcript.RoleAssistant,
			transcript.Payload{Text: "working"},
		)}
		store := evaluationTestStore(value, application, session, turns)
		coordinator := evaluationTestCoordinator(t, store, func() time.Time {
			return now
		})

		if _, err := coordinator.Evaluate(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		got := store.evaluations[application.ApplicationID]
		if got.VerifierState != experience.VerifierUnknown ||
			len(got.Coverage) != 0 {
			t.Fatalf("partial evaluation = %+v", got)
		}
	})

	t.Run("explicit correction violates absence verifier", func(t *testing.T) {
		value := evaluationTestExperience(t, "observed correction", verifier)
		application, session := evaluationTestBinding(
			value,
			"ses_eval_correction",
			now,
		)
		turns := []transcript.Turn{evaluationTestTurn(
			session.SessionKey,
			0,
			now.Add(-time.Minute),
			transcript.RoleUser,
			transcript.Payload{Text: "No, don't edit that generated file."},
		)}
		session.TurnCount = len(turns)
		store := evaluationTestStore(value, application, session, turns)
		coordinator := evaluationTestCoordinator(t, store, func() time.Time {
			return now
		})

		if _, err := coordinator.Evaluate(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		got := store.evaluations[application.ApplicationID]
		if got.VerifierState != experience.VerifierViolated ||
			got.TaskOutcomeState != experience.TaskOutcomeUnknown ||
			len(got.SourceEvidence) != 1 ||
			got.SourceEvidence[0].TurnIndex == nil ||
			*got.SourceEvidence[0].TurnIndex != 0 {
			t.Fatalf("correction evaluation = %+v", got)
		}
	})
}

func TestExperienceEvaluationTrajectoryUpdateReevaluatesWithoutTaskLeakage(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 19, 40, 0, 0, time.UTC)
	value := evaluationTestExperience(
		t,
		"trajectory update",
		experience.Verifier{
			Kind: experience.VerifierFileModified,
			CoverageRequirements: []experience.CoverageRequirement{
				experience.CoverageCanonicalComplete,
			},
			File: &experience.FileVerifierSpec{Path: "internal/service.go"},
		},
	)
	application, session := evaluationTestBinding(
		value,
		"ses_eval_trajectory",
		now,
	)
	turns := []transcript.Turn{evaluationTestTurn(
		session.SessionKey,
		0,
		now.Add(-time.Minute),
		transcript.RoleToolCall,
		transcript.Payload{
			ToolInput:  []byte(`{"path":"internal/service.go"}`),
			ToolCallID: "edit-service",
		},
	)}
	turns[0].ToolName = "Edit"
	session.TurnCount = len(turns)
	store := evaluationTestStore(value, application, session, turns)
	store.canonical[session.SessionKey] = []model.Event{{
		EventID:    "evt_eval_trajectory",
		OccurredAt: turns[0].OccurredAt,
		Session:    model.SessionRef{Key: session.SessionKey},
	}}
	clock := now
	coordinator := evaluationTestCoordinator(t, store, func() time.Time {
		return clock
	})

	if _, err := coordinator.Evaluate(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	first := store.evaluations[application.ApplicationID]
	if first.VerifierState != experience.VerifierUnknown ||
		first.TaskOutcomeState != experience.TaskOutcomeUnknown {
		t.Fatalf("evaluation before trajectory coverage = %+v", first)
	}

	store.trajectories[session.SessionKey] = evaluationTestTrajectoryState(
		session.SessionKey,
	)
	if _, err := coordinator.Evaluate(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	second := store.evaluations[application.ApplicationID]
	if second.VerifierState != experience.VerifierSatisfied ||
		second.TaskOutcomeState != experience.TaskOutcomeUnknown ||
		!second.EvaluatedAt.After(first.EvaluatedAt) {
		t.Fatalf("evaluation after trajectory coverage = %+v", second)
	}
}

func TestExperienceEvaluationDefersWithoutEvidenceAndContinuesAfterFailure(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	verifier := experience.Verifier{
		Kind: experience.VerifierObservationOnly,
		ObservationOnly: &experience.ObservationOnlySpec{
			Explanation: "Observe only.",
		},
	}
	noEvidence := evaluationTestExperience(t, "no evidence", verifier)
	good := evaluationTestExperience(t, "later good", verifier)
	first, firstSession := evaluationTestBinding(
		noEvidence,
		"ses_eval_empty",
		now,
	)
	second, secondSession := evaluationTestBinding(
		good,
		"ses_eval_later",
		now,
	)
	goodTurns := []transcript.Turn{evaluationTestTurn(
		secondSession.SessionKey,
		0,
		now.Add(-time.Minute),
		transcript.RoleAssistant,
		transcript.Payload{Text: "real retained evidence"},
	)}
	secondSession.TurnCount = len(goodTurns)
	store := &experienceEvaluationTestStore{
		applications: []experience.Application{first, second},
		experiences: map[experience.ExperienceRef]local.StoredExperience{
			first.Experience:  {Experience: noEvidence},
			second.Experience: {Experience: good},
		},
		sessions: map[string]transcript.Session{
			firstSession.SessionKey:  firstSession,
			secondSession.SessionKey: secondSession,
		},
		turns: map[string][]transcript.Turn{
			secondSession.SessionKey: goodTurns,
		},
		outcomes:     make(map[string][]trajectory.Outcome),
		canonical:    make(map[string][]model.Event),
		trajectories: make(map[string]local.TrajectoryDerivationState),
		getErrors:    make(map[experience.ExperienceRef]error),
		evaluations:  make(map[string]experience.Evaluation),
	}
	coordinator := evaluationTestCoordinator(t, store, func() time.Time {
		return now
	})

	report, err := coordinator.Evaluate(context.Background(), 10)
	if !errors.Is(err, ErrExperienceEvaluationNoEvidence) {
		t.Fatalf("error = %v, want no-evidence typed error", err)
	}
	if report != (ExperienceEvaluationReport{
		Attempted: 2,
		Applied:   1,
		Deferred:  1,
	}) {
		t.Fatalf("report = %+v", report)
	}
	if _, found := store.evaluations[first.ApplicationID]; found {
		t.Fatal("no-evidence application received fabricated persistence")
	}
	if _, found := store.evaluations[second.ApplicationID]; !found {
		t.Fatal("later application was blocked by the first failure")
	}
}

func TestEvaluationCoverageIsConservativeAtConditionAndOutcomeBounds(
	t *testing.T,
) {
	session := transcript.Session{
		SessionKey: "ses_eval_coverage",
		TurnCount:  1,
		Coverage:   transcript.CoverageComplete,
	}
	turns := []transcript.Turn{evaluationTestTurn(
		session.SessionKey,
		0,
		time.Date(2026, 9, 10, 20, 10, 0, 0, time.UTC),
		transcript.RoleToolCall,
		transcript.Payload{ToolCallID: "call-coverage"},
	)}
	events := []model.Event{{
		EventID:    "evt_eval_coverage",
		OccurredAt: turns[0].OccurredAt,
		Session:    model.SessionRef{Key: session.SessionKey},
	}}
	outcomes := make([]trajectory.Outcome, maxExperienceEvaluationOutcomes)
	for index := range outcomes {
		outcomes[index] = trajectory.Outcome{
			OutcomeID: fmt.Sprintf("out_eval_coverage_%03d", index),
			OccurredAt: turns[0].OccurredAt.Add(
				time.Duration(index) * time.Nanosecond,
			),
		}
	}
	state := evaluationTestTrajectoryState(session.SessionKey)
	coverage, _ := evaluationCoverage(
		session,
		turns,
		events,
		true,
		outcomes,
		state,
		true,
	)
	if !coverage.TranscriptComplete ||
		!coverage.CanonicalEventsComplete ||
		coverage.OutcomeObservationsComplete {
		t.Fatalf("bounded coverage = %+v", coverage)
	}
	for _, item := range coverage.Evidence {
		if item.Requirement != experience.CoverageCanonicalComplete {
			continue
		}
		for _, ref := range item.Evidence {
			if ref.Kind != experience.EvidenceCanonicalEvent {
				t.Fatalf("canonical coverage cited non-canonical ref: %+v", ref)
			}
		}
	}

	kinds := []experience.DeterministicConditionKind{
		experience.ConditionToolName,
		experience.ConditionCommandClass,
		experience.ConditionPathPattern,
	}
	if got := coverageAwareConditionKinds(kinds, evaluate.Coverage{}); len(got) != 0 {
		t.Fatalf("partial coverage completed condition kinds: %+v", got)
	}
	got := coverageAwareConditionKinds(kinds, evaluate.Coverage{
		TranscriptComplete: true,
	})
	if !reflect.DeepEqual(got, []experience.DeterministicConditionKind{
		experience.ConditionToolName,
		experience.ConditionCommandClass,
	}) {
		t.Fatalf("transcript-only complete kinds = %+v", got)
	}
}

func evaluationTestStore(
	value experience.Experience,
	application experience.Application,
	session transcript.Session,
	turns []transcript.Turn,
) *experienceEvaluationTestStore {
	return &experienceEvaluationTestStore{
		applications: []experience.Application{application},
		experiences: map[experience.ExperienceRef]local.StoredExperience{
			application.Experience: {Experience: value},
		},
		sessions: map[string]transcript.Session{
			session.SessionKey: session,
		},
		turns: map[string][]transcript.Turn{
			session.SessionKey: turns,
		},
		outcomes:     make(map[string][]trajectory.Outcome),
		canonical:    make(map[string][]model.Event),
		trajectories: make(map[string]local.TrajectoryDerivationState),
		getErrors:    make(map[experience.ExperienceRef]error),
		evaluations:  make(map[string]experience.Evaluation),
	}
}

func evaluationTestCoordinator(
	t *testing.T,
	store ExperienceEvaluationRepository,
	clock func() time.Time,
) *ExperienceEvaluationCoordinator {
	t.Helper()
	value, err := NewExperienceEvaluationCoordinator(
		store,
		WithExperienceEvaluationClock(clock),
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func evaluationTestExperience(
	t *testing.T,
	seed string,
	verifier experience.Verifier,
) experience.Experience {
	t.Helper()
	value := compilerTestExperience(t, seed)
	value.Verifier = verifier
	compilerTestRehash(t, &value)
	return value
}

func evaluationTestBinding(
	value experience.Experience,
	sessionKey string,
	now time.Time,
) (experience.Application, transcript.Session) {
	deliveredAt := now.Add(-5 * time.Minute)
	application := experience.Application{
		SchemaVersion: experience.ApplicationSchemaVersion,
		Experience: experience.ExperienceRef{
			ExperienceID: value.ExperienceID,
			Version:      value.Version,
		},
		ProjectIdentity:    value.Scope.ProjectIdentity,
		SessionKey:         sessionKey,
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryDelivered,
		DeliveredAt:        &deliveredAt,
		OpportunityState:   experience.OpportunityUnknown,
		ApplicabilityState: experience.ApplicabilityUnknown,
		VerifierState:      experience.VerifierNotEvaluated,
		TaskOutcomeState:   experience.TaskOutcomeNotObserved,
	}
	application.ApplicationID = application.DeterministicID()
	return application, transcript.Session{
		SessionKey:      sessionKey,
		Agent:           "codex",
		NativeSessionID: "native-" + sessionKey,
		ProjectPath:     "/tmp/evaluation-project",
		GitRemoteURL:    value.Scope.ProjectIdentity,
		ProjectIdentity: value.Scope.ProjectIdentity,
		Coverage:        transcript.CoverageComplete,
	}
}

func evaluationTestTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	role transcript.Role,
	payload transcript.Payload,
) transcript.Turn {
	return transcript.Turn{
		TurnID:          "turn-" + sessionKey + "-" + time.Duration(index).String(),
		SourceRecordKey: "source-" + sessionKey + "-" + time.Duration(index).String(),
		SessionKey:      sessionKey,
		TurnIndex:       index,
		OccurredAt:      occurredAt,
		Role:            role,
		Payload:         payload,
	}
}

func evaluationTestTrajectoryState(
	sessionKey string,
) local.TrajectoryDerivationState {
	completedAt := time.Date(2026, 9, 10, 19, 40, 0, 0, time.UTC)
	return local.TrajectoryDerivationState{
		TrajectoryDerivationClaim: local.TrajectoryDerivationClaim{
			SessionKey:        sessionKey,
			DerivationVersion: trajectoryderive.Version,
		},
		Status:      local.TrajectoryDerivationComplete,
		AttemptedAt: completedAt,
		CompletedAt: &completedAt,
		Coverage: trajectoryderive.Coverage{
			Transcript:              transcript.CoverageComplete,
			TranscriptTurnsComplete: true,
			CanonicalEventsComplete: true,
			LinksComplete:           true,
			FullyDerived:            true,
		},
	}
}

func evaluationTestOutcome(
	session transcript.Session,
	turn transcript.Turn,
	kind trajectory.OutcomeKind,
	result trajectory.OutcomeResult,
) trajectory.Outcome {
	index := turn.TurnIndex
	value := trajectory.Outcome{
		SchemaVersion:   trajectory.OutcomeSchemaVersion,
		ProjectIdentity: session.ProjectIdentity,
		SessionKey:      session.SessionKey,
		OccurredAt:      turn.OccurredAt,
		Kind:            kind,
		Result:          result,
		EvidenceClass:   trajectory.EvidenceObserved,
		Confidence:      trajectory.ConfidenceHigh,
		SourceRefs: []trajectory.NodeRef{{
			Kind:       trajectory.NodeTranscriptTurn,
			SessionKey: session.SessionKey,
			TurnIndex:  &index,
		}},
		DerivationVersion: trajectoryderive.Version,
	}
	value.OutcomeID = value.DeterministicID()
	return value
}

func evaluationHasTurnEvidence(
	value experience.Evaluation,
	want int64,
) bool {
	for _, ref := range value.SourceEvidence {
		if ref.Kind == experience.EvidenceTranscriptTurn &&
			ref.TurnIndex != nil &&
			*ref.TurnIndex == want {
			return true
		}
	}
	return false
}
