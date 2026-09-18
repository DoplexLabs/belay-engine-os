package localapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	maxMissionPackStatusItems    = 3
	maxMissionPackStatusEvidence = 5

	MissionPackItemWaitingForDelivery = "waiting_for_delivery"
	MissionPackItemAwaitingEvaluation = "awaiting_evaluation"
	MissionPackItemEvaluated          = "evaluated"
)

var (
	missionPackReceiptIDPattern = regexp.MustCompile(`^mpr_[0-9a-f]{64}$`)

	ErrMissionPackStatusInvalidInput = errors.New(
		"Mission Pack status requires a valid receipt ID",
	)
	ErrMissionPackStatusNotFound = errors.New(
		"Mission Pack status receipt was not found",
	)
	ErrMissionPackStatusMismatch = errors.New(
		"Mission Pack status data is inconsistent",
	)
)

type MissionPackStatusRepository interface {
	GetMissionPackReceipt(
		context.Context,
		string,
	) (local.MissionPackReceipt, error)
	GetMissionPackReceiptApplicationProgress(
		context.Context,
		string,
	) ([]local.MissionPackReceiptApplicationProgress, error)
	GetTranscriptSession(context.Context, string) (transcript.Session, error)
	GetExperience(
		context.Context,
		experience.ExperienceRef,
	) (local.StoredExperience, error)
	GetExperienceApplication(
		context.Context,
		string,
	) (experience.Application, error)
	GetExperienceApplicationEvaluation(
		context.Context,
		string,
	) (*experience.Evaluation, error)
	QueryExperienceImpactObservations(
		context.Context,
		string,
		int,
	) ([]local.ExperienceImpactObservation, error)
}

type MissionPackStatusResult struct {
	ReceiptState       local.ReceiptBindingState `json:"receipt_state"`
	DestinationHarness experience.Harness        `json:"destination_harness"`
	BoundSession       string                    `json:"bound_session,omitempty"`
	AcceptedAt         time.Time                 `json:"accepted_at"`
	ExpiresAt          time.Time                 `json:"expires_at"`
	Items              []MissionPackStatusItem   `json:"items"`
}

type MissionPackStatusItem struct {
	Instruction        string                           `json:"instruction"`
	Version            int                              `json:"version"`
	Status             string                           `json:"status"`
	DeliveredAt        *time.Time                       `json:"delivered_at,omitempty"`
	EvaluatedAt        *time.Time                       `json:"evaluated_at,omitempty"`
	OpportunityState   experience.OpportunityState      `json:"opportunity_state,omitempty"`
	ApplicabilityState experience.ApplicabilityState    `json:"applicability_state,omitempty"`
	VerifierState      experience.VerifierState         `json:"verifier_state,omitempty"`
	TaskOutcomeState   experience.TaskOutcomeState      `json:"task_outcome_state,omitempty"`
	CoverageGaps       []experience.CoverageRequirement `json:"coverage_gaps"`
	Evidence           []MissionPackStatusEvidence      `json:"evidence"`
	ObservedAfter      *MissionPackObservedImpact       `json:"observed_after,omitempty"`
}

type MissionPackObservedImpact struct {
	ObservedAt                time.Time                   `json:"observed_at"`
	ComparisonState           string                      `json:"comparison_state"`
	MatchedSessions           int                         `json:"matched_sessions"`
	MatchedOn                 []string                    `json:"matched_on"`
	Corrections               MissionPackImpactMetric     `json:"corrections"`
	FailedAttempts            MissionPackImpactMetric     `json:"failed_attempts"`
	VerificationAfterLastEdit string                      `json:"verification_after_last_edit"`
	TaskOutcomeState          experience.TaskOutcomeState `json:"task_outcome_state"`
	TranscriptCoverage        transcript.SessionCoverage  `json:"transcript_coverage"`
	OutcomeCoverageComplete   bool                        `json:"outcome_coverage_complete"`
	EvidenceStartTurn         int64                       `json:"evidence_start_turn"`
	EvidenceEndTurn           int64                       `json:"evidence_end_turn"`
}

type MissionPackImpactMetric struct {
	Current     float64  `json:"current"`
	PriorMedian *float64 `json:"prior_median,omitempty"`
	Delta       *float64 `json:"delta,omitempty"`
}

type MissionPackStatusEvidence struct {
	Kind       experience.EvidenceSourceKind `json:"kind"`
	TurnIndex  *int64                        `json:"turn_index,omitempty"`
	EventID    string                        `json:"event_id,omitempty"`
	OutcomeID  string                        `json:"outcome_id,omitempty"`
	Path       string                        `json:"path,omitempty"`
	OccurredAt *time.Time                    `json:"occurred_at,omitempty"`
	Excerpt    string                        `json:"excerpt,omitempty"`
}

type MissionPackStatusService struct {
	repository MissionPackStatusRepository
}

func NewMissionPackStatusService(
	repository MissionPackStatusRepository,
) (*MissionPackStatusService, error) {
	if repository == nil {
		return nil, errors.New("Mission Pack status service requires a store")
	}
	return &MissionPackStatusService{repository: repository}, nil
}

func (s *MissionPackStatusService) Get(
	ctx context.Context,
	receiptID string,
) (MissionPackStatusResult, error) {
	if ctx == nil {
		return MissionPackStatusResult{}, ErrMissionPackStatusInvalidInput
	}
	receiptID = strings.TrimSpace(receiptID)
	if !missionPackReceiptIDPattern.MatchString(receiptID) {
		return MissionPackStatusResult{}, ErrMissionPackStatusInvalidInput
	}
	if s == nil || s.repository == nil {
		return MissionPackStatusResult{}, errors.New(
			"Mission Pack status service is unavailable",
		)
	}

	receipt, err := s.repository.GetMissionPackReceipt(ctx, receiptID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MissionPackStatusResult{}, ErrMissionPackStatusNotFound
		}
		return MissionPackStatusResult{}, fmt.Errorf(
			"load exact Mission Pack receipt: %w",
			err,
		)
	}
	if err := validateMissionPackStatusReceipt(receiptID, receipt); err != nil {
		return MissionPackStatusResult{}, err
	}
	progressRows, err := s.repository.
		GetMissionPackReceiptApplicationProgress(ctx, receiptID)
	if err != nil {
		return MissionPackStatusResult{}, fmt.Errorf(
			"load exact Mission Pack receipt progress: %w",
			err,
		)
	}
	progress, err := indexMissionPackStatusProgress(receipt, progressRows)
	if err != nil {
		return MissionPackStatusResult{}, err
	}

	var boundSession transcript.Session
	if receipt.BindingState == local.ReceiptBound {
		boundSession, err = s.repository.GetTranscriptSession(
			ctx,
			receipt.BoundSessionKey,
		)
		if err != nil ||
			boundSession.SessionKey != receipt.BoundSessionKey ||
			boundSession.ProjectIdentity != receipt.ProjectIdentity ||
			evaluationHarness(boundSession.Agent) != receipt.Harness {
			return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
		}
	}

	result := MissionPackStatusResult{
		ReceiptState:       receipt.BindingState,
		DestinationHarness: receipt.Harness,
		AcceptedAt:         receipt.AcceptedAt.UTC(),
		ExpiresAt:          receipt.ExpiresAt.UTC(),
		Items:              make([]MissionPackStatusItem, 0, len(receipt.ExperienceRefs)),
	}
	if receipt.BindingState == local.ReceiptBound {
		result.BoundSession = receipt.BoundSessionKey
	}
	for _, ref := range receipt.ExperienceRefs {
		stored, err := s.repository.GetExperience(ctx, ref)
		if err != nil {
			return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
		}
		value := stored.Experience
		if value.ExperienceID != ref.ExperienceID ||
			value.Version != ref.Version ||
			value.Scope.ProjectIdentity != receipt.ProjectIdentity {
			return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
		}
		item := MissionPackStatusItem{
			Instruction:  value.Guidance.Instruction,
			Version:      value.Version,
			Status:       MissionPackItemWaitingForDelivery,
			CoverageGaps: []experience.CoverageRequirement{},
			Evidence:     []MissionPackStatusEvidence{},
		}
		row, present := progress[ref]
		if receipt.BindingState != local.ReceiptBound {
			if present && row.ApplicationID != "" {
				return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
			}
			result.Items = append(result.Items, item)
			continue
		}
		if !present || !row.ProgressPresent || row.ApplicationID == "" {
			result.Items = append(result.Items, item)
			continue
		}

		application, err := s.repository.GetExperienceApplication(
			ctx,
			row.ApplicationID,
		)
		if err != nil || validateMissionPackStatusApplication(
			receipt,
			ref,
			row.ApplicationID,
			application,
			boundSession,
		) != nil {
			return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
		}
		item.Status = MissionPackItemAwaitingEvaluation
		deliveredAt := application.DeliveredAt.UTC()
		item.DeliveredAt = &deliveredAt
		item.OpportunityState = application.OpportunityState
		item.ApplicabilityState = application.ApplicabilityState
		item.VerifierState = application.VerifierState
		item.TaskOutcomeState = application.TaskOutcomeState
		item.Evidence = missionPackStatusEvidence(
			application.SourceEvidence,
			application.SessionKey,
		)

		evaluation, err := s.repository.GetExperienceApplicationEvaluation(
			ctx,
			application.ApplicationID,
		)
		if err != nil {
			return MissionPackStatusResult{}, fmt.Errorf(
				"load exact Mission Pack evaluation: %w",
				err,
			)
		}
		if evaluation == nil {
			if application.OpportunityState != experience.OpportunityUnknown ||
				application.ApplicabilityState != experience.ApplicabilityUnknown ||
				application.VerifierState != experience.VerifierNotEvaluated ||
				application.TaskOutcomeState != experience.TaskOutcomeNotObserved {
				return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
			}
			result.Items = append(result.Items, item)
			continue
		}
		if err := validateMissionPackStatusEvaluation(
			value,
			application,
			*evaluation,
		); err != nil {
			return MissionPackStatusResult{}, ErrMissionPackStatusMismatch
		}
		item.Status = MissionPackItemEvaluated
		evaluatedAt := evaluation.EvaluatedAt.UTC()
		item.EvaluatedAt = &evaluatedAt
		item.CoverageGaps = missionPackCoverageGaps(
			value.Verifier.CoverageRequirements,
			evaluation.Coverage,
		)
		item.Evidence = missionPackStatusEvidence(
			append(
				append(
					[]experience.EvidenceRef(nil),
					application.SourceEvidence...,
				),
				evaluation.SourceEvidence...,
			),
			application.SessionKey,
		)
		observations, err := s.repository.QueryExperienceImpactObservations(
			ctx,
			application.ApplicationID,
			1,
		)
		if err != nil {
			return MissionPackStatusResult{}, fmt.Errorf(
				"load exact Mission Pack impact observation: %w",
				err,
			)
		}
		if len(observations) > 0 {
			item.ObservedAfter, err = missionPackObservedImpact(
				application,
				observations[0],
			)
			if err != nil {
				return MissionPackStatusResult{},
					ErrMissionPackStatusMismatch
			}
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func missionPackObservedImpact(
	application experience.Application,
	record local.ExperienceImpactObservation,
) (*MissionPackObservedImpact, error) {
	observation := record.Observation
	if observation.ApplicationID != application.ApplicationID ||
		observation.Experience != application.Experience ||
		observation.ProjectIdentity != application.ProjectIdentity ||
		observation.SessionKey != application.SessionKey ||
		record.ObservedAt.IsZero() {
		return nil, ErrMissionPackStatusMismatch
	}
	return &MissionPackObservedImpact{
		ObservedAt:      record.ObservedAt.UTC(),
		ComparisonState: observation.Comparison.State,
		MatchedSessions: observation.Comparison.ComparableSessions,
		MatchedOn:       append([]string(nil), observation.Comparison.MatchBasis...),
		Corrections: MissionPackImpactMetric{
			Current: float64(observation.Current.ExplicitCorrections),
			PriorMedian: observation.Comparison.BaselineMedian.
				ExplicitCorrections,
			Delta: observation.Comparison.Delta.ExplicitCorrections,
		},
		FailedAttempts: MissionPackImpactMetric{
			Current: float64(observation.Current.FailedToolResults),
			PriorMedian: observation.Comparison.BaselineMedian.
				FailedToolResults,
			Delta: observation.Comparison.Delta.FailedToolResults,
		},
		VerificationAfterLastEdit: observation.Current.
			VerificationAfterFinalEdit,
		TaskOutcomeState:        observation.Current.TaskOutcomeState,
		TranscriptCoverage:      observation.Coverage.CurrentTranscript,
		OutcomeCoverageComplete: observation.Coverage.OutcomeComplete,
		EvidenceStartTurn:       observation.Evidence.StartTurn,
		EvidenceEndTurn:         observation.Evidence.EndTurn,
	}, nil
}

func validateMissionPackStatusReceipt(
	receiptID string,
	receipt local.MissionPackReceipt,
) error {
	if receipt.ReceiptID != receiptID ||
		strings.TrimSpace(receipt.PackID) == "" ||
		strings.TrimSpace(receipt.ProjectIdentity) == "" ||
		!receipt.Harness.Valid() ||
		receipt.Generation < 1 ||
		receipt.AcceptedAt.IsZero() ||
		receipt.ExpiresAt.IsZero() ||
		!receipt.ExpiresAt.After(receipt.AcceptedAt) ||
		len(receipt.ExperienceRefs) == 0 ||
		len(receipt.ExperienceRefs) > maxMissionPackStatusItems {
		return ErrMissionPackStatusMismatch
	}
	switch receipt.BindingState {
	case local.ReceiptBound:
		if strings.TrimSpace(receipt.BoundSessionKey) == "" {
			return ErrMissionPackStatusMismatch
		}
	case local.ReceiptPending,
		local.ReceiptAmbiguous,
		local.ReceiptExpired,
		local.ReceiptCancelled:
		if receipt.BoundSessionKey != "" {
			return ErrMissionPackStatusMismatch
		}
	default:
		return ErrMissionPackStatusMismatch
	}
	seen := make(map[experience.ExperienceRef]struct{}, len(receipt.ExperienceRefs))
	for _, ref := range receipt.ExperienceRefs {
		if err := ref.Validate(); err != nil {
			return ErrMissionPackStatusMismatch
		}
		if _, duplicate := seen[ref]; duplicate {
			return ErrMissionPackStatusMismatch
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func indexMissionPackStatusProgress(
	receipt local.MissionPackReceipt,
	rows []local.MissionPackReceiptApplicationProgress,
) (map[experience.ExperienceRef]local.MissionPackReceiptApplicationProgress, error) {
	if len(rows) > maxMissionPackStatusItems {
		return nil, ErrMissionPackStatusMismatch
	}
	expected := make(map[experience.ExperienceRef]struct{}, len(receipt.ExperienceRefs))
	for _, ref := range receipt.ExperienceRefs {
		expected[ref] = struct{}{}
	}
	result := make(
		map[experience.ExperienceRef]local.MissionPackReceiptApplicationProgress,
		len(rows),
	)
	applicationIDs := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.ReceiptID != receipt.ReceiptID {
			return nil, ErrMissionPackStatusMismatch
		}
		if err := row.Experience.Validate(); err != nil {
			return nil, ErrMissionPackStatusMismatch
		}
		if _, found := expected[row.Experience]; !found {
			return nil, ErrMissionPackStatusMismatch
		}
		if _, duplicate := result[row.Experience]; duplicate {
			return nil, ErrMissionPackStatusMismatch
		}
		if !row.ProgressPresent && row.ApplicationID != "" {
			return nil, ErrMissionPackStatusMismatch
		}
		if row.ApplicationID != "" {
			if strings.TrimSpace(row.ApplicationID) != row.ApplicationID {
				return nil, ErrMissionPackStatusMismatch
			}
			if _, duplicate := applicationIDs[row.ApplicationID]; duplicate {
				return nil, ErrMissionPackStatusMismatch
			}
			applicationIDs[row.ApplicationID] = struct{}{}
		}
		result[row.Experience] = row
	}
	return result, nil
}

func validateMissionPackStatusApplication(
	receipt local.MissionPackReceipt,
	ref experience.ExperienceRef,
	applicationID string,
	application experience.Application,
	session transcript.Session,
) error {
	if err := application.Validate(); err != nil {
		return err
	}
	if application.ApplicationID != applicationID ||
		application.Experience != ref ||
		application.ProjectIdentity != receipt.ProjectIdentity ||
		application.SessionKey != receipt.BoundSessionKey ||
		session.SessionKey != application.SessionKey ||
		session.ProjectIdentity != application.ProjectIdentity ||
		evaluationHarness(session.Agent) != receipt.Harness ||
		application.DeliveryKind != experience.DeliveryMissionPack ||
		application.DeliveryState != experience.DeliveryDelivered ||
		application.DeliveredAt == nil ||
		!application.DeliveredAt.Equal(receipt.AcceptedAt) {
		return ErrMissionPackStatusMismatch
	}
	return validateMissionPackStatusEvidenceSession(
		application.SourceEvidence,
		application.SessionKey,
	)
}

func validateMissionPackStatusEvaluation(
	value experience.Experience,
	application experience.Application,
	evaluation experience.Evaluation,
) error {
	if err := evaluation.Validate(); err != nil {
		return err
	}
	if evaluation.ApplicationID != application.ApplicationID ||
		evaluation.Experience != application.Experience ||
		evaluation.EvaluatedAt.Before(*application.DeliveredAt) ||
		evaluation.OpportunityState != application.OpportunityState ||
		evaluation.ApplicabilityState != application.ApplicabilityState ||
		evaluation.VerifierState != application.VerifierState ||
		evaluation.TaskOutcomeState != application.TaskOutcomeState {
		return ErrMissionPackStatusMismatch
	}
	declared := make(
		map[experience.CoverageRequirement]struct{},
		len(value.Verifier.CoverageRequirements),
	)
	for _, requirement := range value.Verifier.CoverageRequirements {
		declared[requirement] = struct{}{}
	}
	seen := make(map[experience.CoverageRequirement]struct{}, len(evaluation.Coverage))
	for _, requirement := range evaluation.Coverage {
		if _, found := declared[requirement]; !found {
			return ErrMissionPackStatusMismatch
		}
		if _, duplicate := seen[requirement]; duplicate {
			return ErrMissionPackStatusMismatch
		}
		seen[requirement] = struct{}{}
	}
	if len(missionPackCoverageGaps(
		value.Verifier.CoverageRequirements,
		evaluation.Coverage,
	)) > 0 && evaluation.VerifierState != experience.VerifierUnknown {
		return ErrMissionPackStatusMismatch
	}
	return validateMissionPackStatusEvidenceSession(
		evaluation.SourceEvidence,
		application.SessionKey,
	)
}

func validateMissionPackStatusEvidenceSession(
	values []experience.EvidenceRef,
	sessionKey string,
) error {
	for _, value := range values {
		if err := value.Validate(); err != nil {
			return err
		}
		if value.Kind == experience.EvidenceTranscriptTurn &&
			value.SessionKey != sessionKey {
			return ErrMissionPackStatusMismatch
		}
	}
	return nil
}

func missionPackCoverageGaps(
	declared []experience.CoverageRequirement,
	complete []experience.CoverageRequirement,
) []experience.CoverageRequirement {
	present := make(map[experience.CoverageRequirement]struct{}, len(complete))
	for _, requirement := range complete {
		present[requirement] = struct{}{}
	}
	result := make([]experience.CoverageRequirement, 0, len(declared))
	added := make(map[experience.CoverageRequirement]struct{}, len(declared))
	for _, requirement := range declared {
		if _, found := present[requirement]; found {
			continue
		}
		if _, duplicate := added[requirement]; duplicate {
			continue
		}
		added[requirement] = struct{}{}
		result = append(result, requirement)
	}
	return result
}

func missionPackStatusEvidence(
	values []experience.EvidenceRef,
	sessionKey string,
) []MissionPackStatusEvidence {
	result := make([]MissionPackStatusEvidence, 0, maxMissionPackStatusEvidence)
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if len(result) == maxMissionPackStatusEvidence {
			break
		}
		if err := value.Validate(); err != nil {
			continue
		}
		if value.Kind == experience.EvidenceTranscriptTurn &&
			value.SessionKey != sessionKey {
			continue
		}
		item := MissionPackStatusEvidence{
			Kind:      value.Kind,
			TurnIndex: value.TurnIndex,
			EventID:   value.EventID,
			OutcomeID: value.OutcomeID,
			Path:      value.Path,
			Excerpt:   value.Excerpt,
		}
		if value.OccurredAt != nil {
			occurredAt := value.OccurredAt.UTC()
			item.OccurredAt = &occurredAt
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		key := string(encoded)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}
