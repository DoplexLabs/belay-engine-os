package localapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/impact"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type missionPackStatusTestStore struct {
	receipt      local.MissionPackReceipt
	receiptErr   error
	progress     []local.MissionPackReceiptApplicationProgress
	progressErr  error
	session      transcript.Session
	experiences  map[experience.ExperienceRef]local.StoredExperience
	applications map[string]experience.Application
	evaluations  map[string]*experience.Evaluation
	impacts      map[string][]local.ExperienceImpactObservation
}

func (store *missionPackStatusTestStore) GetMissionPackReceipt(
	_ context.Context,
	_ string,
) (local.MissionPackReceipt, error) {
	return store.receipt, store.receiptErr
}

func (store *missionPackStatusTestStore) GetMissionPackReceiptApplicationProgress(
	_ context.Context,
	_ string,
) ([]local.MissionPackReceiptApplicationProgress, error) {
	return append(
		[]local.MissionPackReceiptApplicationProgress(nil),
		store.progress...,
	), store.progressErr
}

func (store *missionPackStatusTestStore) GetTranscriptSession(
	_ context.Context,
	_ string,
) (transcript.Session, error) {
	return store.session, nil
}

func (store *missionPackStatusTestStore) GetExperience(
	_ context.Context,
	ref experience.ExperienceRef,
) (local.StoredExperience, error) {
	value, found := store.experiences[ref]
	if !found {
		return local.StoredExperience{}, errors.New("experience not found")
	}
	return value, nil
}

func (store *missionPackStatusTestStore) GetExperienceApplication(
	_ context.Context,
	applicationID string,
) (experience.Application, error) {
	value, found := store.applications[applicationID]
	if !found {
		return experience.Application{}, errors.New("application not found")
	}
	return value, nil
}

func (store *missionPackStatusTestStore) GetExperienceApplicationEvaluation(
	_ context.Context,
	applicationID string,
) (*experience.Evaluation, error) {
	value := store.evaluations[applicationID]
	if value == nil {
		return nil, nil
	}
	copy := *value
	return &copy, nil
}

func (store *missionPackStatusTestStore) QueryExperienceImpactObservations(
	_ context.Context,
	applicationID string,
	limit int,
) ([]local.ExperienceImpactObservation, error) {
	values := append(
		[]local.ExperienceImpactObservation(nil),
		store.impacts[applicationID]...,
	)
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func TestMissionPackStatusReturnsExactEvaluatedReceiptInReceiptOrder(
	t *testing.T,
) {
	acceptedAt := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	first := statusTestExperience(t, "status-first")
	second := statusTestExperience(t, "status-second")
	first.Guidance.Instruction = "Run the focused verifier after editing."
	compilerTestRehash(t, &first)
	second.Guidance.Instruction = "Keep generated files unchanged."
	compilerTestRehash(t, &second)
	receipt := statusTestReceipt(
		acceptedAt,
		compilerTestRef(second),
		compilerTestRef(first),
	)
	store := newMissionPackStatusTestStore(receipt, first, second)

	application := statusTestApplication(
		receipt,
		compilerTestRef(first),
	)
	application.OpportunityState = experience.OpportunityObserved
	application.ApplicabilityState = experience.ApplicabilityApplicable
	application.VerifierState = experience.VerifierUnknown
	application.TaskOutcomeState = experience.TaskOutcomeUnknown
	application.ApplicationID = application.DeterministicID()
	store.applications[application.ApplicationID] = application
	store.progress = []local.MissionPackReceiptApplicationProgress{
		{
			ReceiptID:       receipt.ReceiptID,
			Experience:      compilerTestRef(first),
			ProgressPresent: true,
			ApplicationID:   application.ApplicationID,
		},
		{
			ReceiptID:       receipt.ReceiptID,
			Experience:      compilerTestRef(second),
			ProgressPresent: true,
		},
	}
	evaluatedAt := acceptedAt.Add(2 * time.Minute)
	turn := int64(7)
	evaluation := experience.Evaluation{
		SchemaVersion:      experience.EvaluationSchemaVersion,
		ApplicationID:      application.ApplicationID,
		Experience:         application.Experience,
		EvaluatedAt:        evaluatedAt,
		OpportunityState:   application.OpportunityState,
		ApplicabilityState: application.ApplicabilityState,
		VerifierState:      application.VerifierState,
		TaskOutcomeState:   application.TaskOutcomeState,
		Coverage: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		SourceEvidence: []experience.EvidenceRef{
			{
				Kind:       experience.EvidenceTranscriptTurn,
				SessionKey: receipt.BoundSessionKey,
				TurnIndex:  &turn,
				Excerpt:    "go test ./internal/localapp passed",
			},
			{
				Kind:    experience.EvidenceCanonicalEvent,
				EventID: "evt_status_1",
			},
			{
				Kind:      experience.EvidenceOutcomeObservation,
				OutcomeID: "out_status_1",
			},
			{
				Kind:    experience.EvidenceWorkspaceHash,
				Path:    "internal/localapp/status.go",
				SHA256:  "sha256:" + strings.Repeat("a", 64),
				Excerpt: "workspace capture",
			},
			{
				Kind:       experience.EvidenceUserRecorded,
				RecordedBy: "local_user",
				Excerpt:    "user recorded observation",
			},
			{
				Kind:    experience.EvidenceCanonicalEvent,
				EventID: "evt_status_2",
			},
		},
		DerivationVersion: "evaluation.status.v1",
	}
	evaluation.EvaluationID = evaluation.DeterministicID()
	if err := evaluation.Validate(); err != nil {
		t.Fatal(err)
	}
	store.evaluations[application.ApplicationID] = &evaluation
	priorCorrections := 2.0
	correctionDelta := -1.0
	priorFailures := 3.0
	failureDelta := -2.0
	store.impacts[application.ApplicationID] = []local.ExperienceImpactObservation{{
		ObservedAt: evaluatedAt.Add(time.Minute),
		Observation: impact.Observation{
			ApplicationID:   application.ApplicationID,
			Experience:      application.Experience,
			ProjectIdentity: application.ProjectIdentity,
			SessionKey:      application.SessionKey,
			Evidence: impact.EvidenceRange{
				SessionKey: application.SessionKey,
				StartTurn:  3,
				EndTurn:    9,
			},
			Current: impact.Metrics{
				ExplicitCorrections:        1,
				FailedToolResults:          1,
				VerificationAfterFinalEdit: impact.VerificationObserved,
				TaskOutcomeState:           experience.TaskOutcomeUnknown,
			},
			Comparison: impact.Comparison{
				State:              impact.ComparisonMatched,
				ComparableSessions: 4,
				MatchBasis: []string{
					impact.MatchProject,
					impact.MatchHarness,
				},
				BaselineMedian: impact.BaselineMetrics{
					ExplicitCorrections: &priorCorrections,
					FailedToolResults:   &priorFailures,
				},
				Delta: impact.MetricDeltas{
					ExplicitCorrections: &correctionDelta,
					FailedToolResults:   &failureDelta,
				},
			},
			Coverage: impact.Coverage{
				CurrentTranscript: transcript.CoverageComplete,
			},
		},
	}}

	service := newMissionPackStatusTestService(t, store)
	result, err := service.Get(context.Background(), receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceiptState != local.ReceiptBound ||
		result.DestinationHarness != experience.HarnessCodex ||
		result.BoundSession != receipt.BoundSessionKey ||
		len(result.Items) != 2 {
		t.Fatalf("status result = %+v", result)
	}
	if result.Items[0].Instruction != second.Guidance.Instruction ||
		result.Items[0].Status != MissionPackItemWaitingForDelivery {
		t.Fatalf("first ordered item = %+v", result.Items[0])
	}
	evaluated := result.Items[1]
	if evaluated.Instruction != first.Guidance.Instruction ||
		evaluated.Status != MissionPackItemEvaluated ||
		evaluated.DeliveredAt == nil ||
		!evaluated.DeliveredAt.Equal(acceptedAt) ||
		evaluated.EvaluatedAt == nil ||
		!evaluated.EvaluatedAt.Equal(evaluatedAt) ||
		evaluated.OpportunityState != experience.OpportunityObserved ||
		evaluated.ApplicabilityState != experience.ApplicabilityApplicable ||
		evaluated.VerifierState != experience.VerifierUnknown ||
		evaluated.TaskOutcomeState != experience.TaskOutcomeUnknown {
		t.Fatalf("evaluated item = %+v", evaluated)
	}
	if evaluated.ObservedAfter == nil ||
		evaluated.ObservedAfter.MatchedSessions != 4 ||
		evaluated.ObservedAfter.FailedAttempts.Delta == nil ||
		*evaluated.ObservedAfter.FailedAttempts.Delta != -2 ||
		evaluated.ObservedAfter.EvidenceStartTurn != 3 ||
		evaluated.ObservedAfter.EvidenceEndTurn != 9 {
		t.Fatalf("observed impact = %+v", evaluated.ObservedAfter)
	}
	if !reflect.DeepEqual(
		evaluated.CoverageGaps,
		[]experience.CoverageRequirement{
			experience.CoverageWorkspaceCaptured,
		},
	) {
		t.Fatalf("coverage gaps = %+v", evaluated.CoverageGaps)
	}
	if len(evaluated.Evidence) != maxMissionPackStatusEvidence {
		t.Fatalf("evidence count = %d, want %d", len(evaluated.Evidence), maxMissionPackStatusEvidence)
	}
	for _, item := range evaluated.Evidence {
		if item.Kind == experience.EvidenceWorkspaceHash &&
			item.Path != "internal/localapp/status.go" {
			t.Fatalf("workspace evidence = %+v", item)
		}
	}
}

func TestMissionPackStatusPendingReceiptMakesNoDeliveryClaims(t *testing.T) {
	acceptedAt := time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC)
	value := statusTestExperience(t, "status-pending")
	receipt := statusTestReceipt(acceptedAt, compilerTestRef(value))
	receipt.BindingState = local.ReceiptPending
	receipt.BoundSessionKey = ""
	store := newMissionPackStatusTestStore(receipt, value)
	store.progress = []local.MissionPackReceiptApplicationProgress{{
		ReceiptID:       receipt.ReceiptID,
		Experience:      compilerTestRef(value),
		ProgressPresent: true,
	}}

	result, err := newMissionPackStatusTestService(t, store).Get(
		context.Background(),
		receipt.ReceiptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	item := result.Items[0]
	if result.ReceiptState != local.ReceiptPending ||
		result.BoundSession != "" ||
		item.Status != MissionPackItemWaitingForDelivery ||
		item.DeliveredAt != nil ||
		item.EvaluatedAt != nil ||
		item.OpportunityState != "" ||
		item.ApplicabilityState != "" ||
		item.VerifierState != "" ||
		item.TaskOutcomeState != "" ||
		len(item.CoverageGaps) != 0 ||
		len(item.Evidence) != 0 {
		t.Fatalf("pending result made a delivery claim: %+v", result)
	}
}

func TestMissionPackStatusBoundReceiptWithoutLinkWaitsForDelivery(
	t *testing.T,
) {
	acceptedAt := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC)
	value := statusTestExperience(t, "status-unlinked")
	receipt := statusTestReceipt(acceptedAt, compilerTestRef(value))
	for _, test := range []struct {
		name     string
		progress []local.MissionPackReceiptApplicationProgress
	}{
		{name: "missing progress"},
		{
			name: "unlinked progress",
			progress: []local.MissionPackReceiptApplicationProgress{{
				ReceiptID:       receipt.ReceiptID,
				Experience:      compilerTestRef(value),
				ProgressPresent: true,
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMissionPackStatusTestStore(receipt, value)
			store.progress = test.progress
			result, err := newMissionPackStatusTestService(t, store).Get(
				context.Background(),
				receipt.ReceiptID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.BoundSession != receipt.BoundSessionKey ||
				result.Items[0].Status != MissionPackItemWaitingForDelivery ||
				result.Items[0].DeliveredAt != nil {
				t.Fatalf("bound unlinked result = %+v", result)
			}
		})
	}
}

func TestMissionPackStatusFailsClosedOnReceiptLinkMismatch(t *testing.T) {
	acceptedAt := time.Date(2026, 9, 10, 21, 30, 0, 0, time.UTC)
	value := statusTestExperience(t, "status-mismatch")
	ref := compilerTestRef(value)
	receipt := statusTestReceipt(acceptedAt, ref)
	store := newMissionPackStatusTestStore(receipt, value)
	application := statusTestApplication(receipt, ref)
	application.SessionKey = "ses_foreign"
	application.ApplicationID = application.DeterministicID()
	store.applications[application.ApplicationID] = application
	store.progress = []local.MissionPackReceiptApplicationProgress{{
		ReceiptID:       receipt.ReceiptID,
		Experience:      ref,
		ProgressPresent: true,
		ApplicationID:   application.ApplicationID,
	}}

	_, err := newMissionPackStatusTestService(t, store).Get(
		context.Background(),
		receipt.ReceiptID,
	)
	if !errors.Is(err, ErrMissionPackStatusMismatch) {
		t.Fatalf("Get() error = %v, want mismatch", err)
	}
}

func statusTestExperience(
	t *testing.T,
	seed string,
) experience.Experience {
	t.Helper()
	value := compilerTestExperience(t, seed)
	value.Verifier = experience.Verifier{
		Kind: experience.VerifierFileNotModified,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
			experience.CoverageWorkspaceCaptured,
		},
		File: &experience.FileVerifierSpec{Path: "generated/output.go"},
	}
	compilerTestRehash(t, &value)
	return value
}

func statusTestReceipt(
	acceptedAt time.Time,
	refs ...experience.ExperienceRef,
) local.MissionPackReceipt {
	return local.MissionPackReceipt{
		ReceiptID:       "mpr_" + strings.Repeat("a", 64),
		PackID:          "mpk_" + strings.Repeat("b", 52),
		ProjectIdentity: compilerTestProjectIdentity(),
		Harness:         experience.HarnessCodex,
		ExperienceRefs:  append([]experience.ExperienceRef(nil), refs...),
		Generation:      7,
		AcceptedAt:      acceptedAt,
		ExpiresAt:       acceptedAt.Add(5 * time.Minute),
		BindingState:    local.ReceiptBound,
		BoundSessionKey: "ses_status_bound",
	}
}

func statusTestApplication(
	receipt local.MissionPackReceipt,
	ref experience.ExperienceRef,
) experience.Application {
	deliveredAt := receipt.AcceptedAt
	value := experience.Application{
		SchemaVersion:      experience.ApplicationSchemaVersion,
		Experience:         ref,
		ProjectIdentity:    receipt.ProjectIdentity,
		SessionKey:         receipt.BoundSessionKey,
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryDelivered,
		DeliveredAt:        &deliveredAt,
		OpportunityState:   experience.OpportunityUnknown,
		ApplicabilityState: experience.ApplicabilityUnknown,
		VerifierState:      experience.VerifierNotEvaluated,
		TaskOutcomeState:   experience.TaskOutcomeNotObserved,
	}
	value.ApplicationID = value.DeterministicID()
	return value
}

func newMissionPackStatusTestStore(
	receipt local.MissionPackReceipt,
	values ...experience.Experience,
) *missionPackStatusTestStore {
	experiences := make(
		map[experience.ExperienceRef]local.StoredExperience,
		len(values),
	)
	for _, value := range values {
		experiences[compilerTestRef(value)] = local.StoredExperience{
			Experience:       value,
			CurrentLifecycle: value.Governance.LifecycleState,
		}
	}
	return &missionPackStatusTestStore{
		receipt: receipt,
		session: transcript.Session{
			SessionKey:      receipt.BoundSessionKey,
			Agent:           "codex",
			ProjectIdentity: receipt.ProjectIdentity,
			Coverage:        transcript.CoverageComplete,
		},
		experiences:  experiences,
		applications: make(map[string]experience.Application),
		evaluations:  make(map[string]*experience.Evaluation),
		impacts:      make(map[string][]local.ExperienceImpactObservation),
	}
}

func newMissionPackStatusTestService(
	t *testing.T,
	store MissionPackStatusRepository,
) *MissionPackStatusService {
	t.Helper()
	service, err := NewMissionPackStatusService(store)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
