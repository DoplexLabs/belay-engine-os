package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	maxApplicationPayloadBytes   = 1 << 20
	maxGenerationPayloadBytes    = 256 << 10
	maxReceiptPayloadBytes       = 256 << 10
	maxCompiledExperienceRefs    = 500
	compiledExperienceProbeLimit = maxCompiledExperienceRefs + 1
)

var (
	ErrExperienceApplicationConflict         = errors.New("experience application identity conflicts with persisted record")
	ErrExperienceEvaluationConflict          = errors.New("experience evaluation identity conflicts with persisted record")
	ErrExperienceEvaluationStale             = errors.New("experience evaluation is stale")
	ErrExperienceGenerationConflict          = errors.New("experience generation identity conflicts with persisted record")
	ErrExperienceGenerationLimit             = errors.New("active experience generation exceeds safety limit")
	ErrExperienceGenerationNotFound          = errors.New("experience generation was not found")
	ErrMissionPackReceiptConflict            = errors.New("mission pack receipt identity conflicts with persisted record")
	ErrMissionPackReceiptApplicationConflict = errors.New(
		"mission pack receipt application link conflicts with persisted record",
	)
	ErrMissionPackReceiptApplicationCorrupt = errors.New(
		"mission pack receipt application progress is corrupt",
	)
)

type GenerationState string

const (
	GenerationActive   GenerationState = "active"
	GenerationInactive GenerationState = "inactive"
)

type ExperienceGeneration struct {
	ProjectIdentity    string                     `json:"project_identity"`
	Generation         int64                      `json:"generation"`
	CompiledHash       string                     `json:"compiled_hash"`
	ExperienceRefs     []experience.ExperienceRef `json:"experience_refs"`
	PreviousGeneration *int64                     `json:"previous_generation,omitempty"`
	State              GenerationState            `json:"state"`
	CompiledAt         time.Time                  `json:"compiled_at"`
	ActivatedAt        time.Time                  `json:"activated_at"`
}

type CompileExperienceGenerationResult struct {
	Generation ExperienceGeneration
	Replayed   bool
}

type ReceiptBindingState string

const (
	ReceiptPending   ReceiptBindingState = "pending"
	ReceiptBound     ReceiptBindingState = "bound"
	ReceiptAmbiguous ReceiptBindingState = "ambiguous"
	ReceiptExpired   ReceiptBindingState = "expired"
	ReceiptCancelled ReceiptBindingState = "cancelled"
)

type MissionPackReceipt struct {
	ReceiptID       string                     `json:"receipt_id"`
	PackID          string                     `json:"pack_id"`
	ProjectIdentity string                     `json:"project_identity"`
	Harness         experience.Harness         `json:"harness"`
	ExperienceRefs  []experience.ExperienceRef `json:"experience_refs"`
	Generation      int64                      `json:"generation"`
	AcceptedAt      time.Time                  `json:"accepted_at"`
	ExpiresAt       time.Time                  `json:"expires_at"`
	TaskHintHash    string                     `json:"task_hint_hash,omitempty"`
	BindingState    ReceiptBindingState        `json:"binding_state"`
	BoundSessionKey string                     `json:"bound_session_key,omitempty"`
}

type MissionPackReceiptApplicationProgress struct {
	ReceiptID       string
	Experience      experience.ExperienceRef
	ProgressPresent bool
	ApplicationID   string
}

type storedApplicationPayload struct {
	Application experience.Application `json:"application"`
	Evaluation  *experience.Evaluation `json:"evaluation,omitempty"`
}

func (s *Store) InsertExperienceApplication(
	ctx context.Context,
	application experience.Application,
) (bool, error) {
	if err := application.Validate(); err != nil {
		return false, err
	}
	envelope := storedApplicationPayload{Application: application}
	_, payload, err := s.sealDomainJSON(
		"experience_application",
		application.ApplicationID,
		"payload",
		envelope,
		maxApplicationPayloadBytes,
	)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin experience application persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationExperienceApplication, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO experience_applications (
				application_id, experience_id, experience_version,
				project_identity, session_key, delivery_kind, delivery_state,
				opportunity_state, applicability_state, verifier_state,
				task_outcome_state, delivered_at, evaluated_at, payload,
				payload_encoding, inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)
			ON CONFLICT(application_id) DO NOTHING`,
			application.ApplicationID,
			application.Experience.ExperienceID,
			application.Experience.Version,
			application.ProjectIdentity,
			nullable(application.SessionKey),
			application.DeliveryKind,
			application.DeliveryState,
			application.OpportunityState,
			application.ApplicabilityState,
			application.VerifierState,
			application.TaskOutcomeState,
			nullableTime(application.DeliveredAt),
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert experience application: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect experience application persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}
		existing, err := s.scanStoredExperienceApplication(tx.QueryRowContext(
			ctx,
			applicationSelectSQL+` WHERE application_id = ?`,
			application.ApplicationID,
		))
		if err != nil {
			return errors.New("read duplicate experience application")
		}
		if !sameApplicationDeliveryIdentity(existing.Application, application) {
			return ErrExperienceApplicationConflict
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit experience application persistence")
	}
	return inserted, nil
}

func (s *Store) ApplyExperienceEvaluation(
	ctx context.Context,
	evaluation experience.Evaluation,
) (experience.Application, bool, error) {
	if err := evaluation.Validate(); err != nil {
		return experience.Application{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return experience.Application{}, false, errors.New("begin experience evaluation")
	}
	defer tx.Rollback()
	var result experience.Application
	applied := false
	err = withMutationTx(ctx, tx, mutationExperienceApplication, func() error {
		stored, err := s.scanStoredExperienceApplication(tx.QueryRowContext(
			ctx,
			applicationSelectSQL+` WHERE application_id = ?`,
			evaluation.ApplicationID,
		))
		if err != nil {
			return err
		}
		if stored.Application.ApplicationID != evaluation.ApplicationID ||
			stored.Application.Experience != evaluation.Experience {
			return errors.New("experience evaluation does not match application")
		}
		if stored.Evaluation != nil {
			if stored.Evaluation.EvaluationID == evaluation.EvaluationID {
				if !sameJSONValue(*stored.Evaluation, evaluation) {
					return ErrExperienceEvaluationConflict
				}
				result = stored.Application
				return nil
			}
			if !evaluation.EvaluatedAt.After(stored.Evaluation.EvaluatedAt) {
				return ErrExperienceEvaluationStale
			}
		}

		updated := stored.Application
		updated.OpportunityState = evaluation.OpportunityState
		updated.ApplicabilityState = evaluation.ApplicabilityState
		updated.VerifierState = evaluation.VerifierState
		updated.TaskOutcomeState = evaluation.TaskOutcomeState
		if err := updated.Validate(); err != nil {
			return errors.New("evaluation produced invalid experience application")
		}
		envelope := storedApplicationPayload{
			Application: updated,
			Evaluation:  &evaluation,
		}
		_, payload, err := s.sealDomainJSON(
			"experience_application",
			updated.ApplicationID,
			"payload",
			envelope,
			maxApplicationPayloadBytes,
		)
		if err != nil {
			return err
		}
		expectedEvaluatedAt := nullableEvaluationTime(stored.Evaluation)
		updateResult, err := tx.ExecContext(ctx, `
			UPDATE experience_applications
			SET opportunity_state = ?, applicability_state = ?,
				verifier_state = ?, task_outcome_state = ?,
				evaluated_at = ?, payload = ?
			WHERE application_id = ?
				AND (
					(evaluated_at IS NULL AND ? IS NULL)
					OR evaluated_at = ?
				)`,
			updated.OpportunityState,
			updated.ApplicabilityState,
			updated.VerifierState,
			updated.TaskOutcomeState,
			formatProjectionTime(evaluation.EvaluatedAt),
			payload,
			updated.ApplicationID,
			expectedEvaluatedAt,
			expectedEvaluatedAt,
		)
		if err != nil {
			return fmt.Errorf("update experience application evaluation: %w", err)
		}
		affected, err := updateResult.RowsAffected()
		if err != nil || affected != 1 {
			return ErrExperienceEvaluationStale
		}
		result = updated
		applied = true
		return nil
	})
	if err != nil {
		return experience.Application{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return experience.Application{}, false, errors.New("commit experience evaluation")
	}
	return result, applied, nil
}

func (s *Store) GetExperienceApplication(
	ctx context.Context,
	applicationID string,
) (experience.Application, error) {
	if err := validateStorageIdentifier("experience application ID", applicationID); err != nil {
		return experience.Application{}, err
	}
	return s.scanExperienceApplication(s.db.QueryRowContext(ctx, applicationSelectSQL+`
		WHERE application_id = ?`,
		applicationID,
	))
}

func (s *Store) GetExperienceApplicationEvaluation(
	ctx context.Context,
	applicationID string,
) (*experience.Evaluation, error) {
	if err := validateStorageIdentifier(
		"experience application ID",
		applicationID,
	); err != nil {
		return nil, err
	}
	stored, err := s.scanStoredExperienceApplication(s.db.QueryRowContext(
		ctx,
		applicationSelectSQL+` WHERE application_id = ?`,
		applicationID,
	))
	if err != nil {
		return nil, err
	}
	if stored.Evaluation == nil {
		return nil, nil
	}
	value := *stored.Evaluation
	return &value, nil
}

func (s *Store) QueryExperienceApplications(
	ctx context.Context,
	ref experience.ExperienceRef,
	limit int,
) ([]experience.Application, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, applicationSelectSQL+`
		WHERE experience_id = ? AND experience_version = ?
		ORDER BY delivered_at, application_id
		LIMIT ?`,
		ref.ExperienceID,
		ref.Version,
		limit,
	)
	if err != nil {
		return nil, errors.New("query experience applications")
	}
	defer rows.Close()
	result := make([]experience.Application, 0)
	for rows.Next() {
		application, err := s.scanExperienceApplication(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, application)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query experience applications")
	}
	return result, nil
}

// QueryExperienceApplicationsNeedingEvaluation returns delivered
// applications whose exact bound session has transcript or trajectory
// derivation evidence newer than the application's last evaluation.
func (s *Store) QueryExperienceApplicationsNeedingEvaluation(
	ctx context.Context,
	limit int,
) ([]experience.Application, error) {
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT application.application_id, application.experience_id,
			application.experience_version, application.project_identity,
			application.session_key, application.delivery_kind,
			application.delivery_state, application.opportunity_state,
			application.applicability_state, application.verifier_state,
			application.task_outcome_state, application.delivered_at,
			application.evaluated_at, application.payload,
			application.payload_encoding
		FROM transcript_sessions AS transcript
		JOIN experience_applications AS application
			ON application.session_key = transcript.session_key
			AND application.project_identity = transcript.project_identity
		WHERE application.delivery_state = ?
			AND application.session_key IS NOT NULL
			AND application.session_key != ''
			AND (
				application.evaluated_at IS NULL
				OR transcript.updated_at > application.evaluated_at
				OR EXISTS (
					SELECT 1
					FROM trajectory_derivation_state AS derivation
					WHERE derivation.session_key = application.session_key
						AND derivation.updated_at > application.evaluated_at
				)
			)
		ORDER BY
			COALESCE(
				application.evaluated_at,
				application.delivered_at,
				application.inserted_at
			),
			application.application_id
		LIMIT ?`,
		experience.DeliveryDelivered,
		limit,
	)
	if err != nil {
		return nil, errors.New(
			"query experience applications needing evaluation",
		)
	}
	defer rows.Close()

	result := make([]experience.Application, 0)
	for rows.Next() {
		application, err := s.scanExperienceApplication(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, application)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New(
			"query experience applications needing evaluation",
		)
	}
	return result, nil
}

const applicationSelectSQL = `
	SELECT application_id, experience_id, experience_version,
		project_identity, session_key, delivery_kind, delivery_state,
		opportunity_state, applicability_state, verifier_state,
		task_outcome_state, delivered_at, evaluated_at, payload,
		payload_encoding
	FROM experience_applications`

func (s *Store) scanExperienceApplication(scanner rowScanner) (experience.Application, error) {
	stored, err := s.scanStoredExperienceApplication(scanner)
	return stored.Application, err
}

func (s *Store) scanStoredExperienceApplication(
	scanner rowScanner,
) (storedApplicationPayload, error) {
	var applicationID, experienceID, projectIdentity string
	var deliveryKind, deliveryState, opportunityState, applicabilityState string
	var verifierState, taskOutcomeState, encoding string
	var version int
	var sessionKey, deliveredAtValue, evaluatedAtValue sql.NullString
	var payload []byte
	if err := scanner.Scan(
		&applicationID,
		&experienceID,
		&version,
		&projectIdentity,
		&sessionKey,
		&deliveryKind,
		&deliveryState,
		&opportunityState,
		&applicabilityState,
		&verifierState,
		&taskOutcomeState,
		&deliveredAtValue,
		&evaluatedAtValue,
		&payload,
		&encoding,
	); err != nil {
		return storedApplicationPayload{}, err
	}
	if err := validateSealedPayloadSize(
		"experience application",
		payload,
		maxApplicationPayloadBytes,
	); err != nil {
		return storedApplicationPayload{}, err
	}
	payload, err := s.cipher.open(
		"experience_application",
		applicationID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return storedApplicationPayload{}, err
	}
	var stored storedApplicationPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return storedApplicationPayload{}, errors.New("decode experience application payload")
	}
	application := stored.Application
	if application.ApplicationID != applicationID ||
		application.Experience.ExperienceID != experienceID ||
		application.Experience.Version != version ||
		application.ProjectIdentity != projectIdentity ||
		application.SessionKey != nullStringValue(sessionKey) ||
		string(application.DeliveryKind) != deliveryKind ||
		string(application.DeliveryState) != deliveryState ||
		string(application.OpportunityState) != opportunityState ||
		string(application.ApplicabilityState) != applicabilityState ||
		string(application.VerifierState) != verifierState ||
		string(application.TaskOutcomeState) != taskOutcomeState ||
		!optionalTimeMatches(application.DeliveredAt, deliveredAtValue) {
		return storedApplicationPayload{}, errors.New("experience application index does not match payload")
	}
	if err := application.Validate(); err != nil {
		return storedApplicationPayload{}, errors.New("invalid persisted experience application")
	}
	if stored.Evaluation == nil {
		if evaluatedAtValue.Valid {
			return storedApplicationPayload{}, errors.New("experience application evaluation index does not match payload")
		}
		return stored, nil
	}
	evaluation := stored.Evaluation
	evaluatedAt, parseErr := parseNullableProjectionTime(evaluatedAtValue)
	if parseErr != nil || evaluatedAt == nil ||
		!evaluation.EvaluatedAt.Equal(*evaluatedAt) ||
		evaluation.ApplicationID != application.ApplicationID ||
		evaluation.Experience != application.Experience ||
		evaluation.OpportunityState != application.OpportunityState ||
		evaluation.ApplicabilityState != application.ApplicabilityState ||
		evaluation.VerifierState != application.VerifierState ||
		evaluation.TaskOutcomeState != application.TaskOutcomeState {
		return storedApplicationPayload{}, errors.New("experience application evaluation index does not match payload")
	}
	if err := evaluation.Validate(); err != nil {
		return storedApplicationPayload{}, errors.New("invalid persisted experience evaluation")
	}
	return stored, nil
}

// CompileActiveExperienceGeneration snapshots the exact active, unexpired
// experience membership for one project. Lifecycle reads, replay detection,
// generation allocation, deactivation, and activation occur in one mutation
// transaction. Lifecycle actions intentionally do not call this method.
func (s *Store) CompileActiveExperienceGeneration(
	ctx context.Context,
	projectIdentity string,
	compiledAt time.Time,
) (CompileExperienceGenerationResult, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return CompileExperienceGenerationResult{}, err
	}
	compiledAt = compiledAt.UTC()
	if compiledAt.IsZero() {
		return CompileExperienceGenerationResult{},
			errors.New("experience generation compile time is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompileExperienceGenerationResult{},
			errors.New("begin active experience generation compilation")
	}
	defer tx.Rollback()

	var result CompileExperienceGenerationResult
	err = withMutationTx(
		ctx,
		tx,
		mutationExperienceGeneration,
		func() error {
			values, err := s.queryCompilableExperiencesTx(
				ctx,
				tx,
				projectIdentity,
				compiledAt,
			)
			if err != nil {
				return err
			}
			refs := make(
				[]experience.ExperienceRef,
				len(values),
			)
			for index, value := range values {
				refs[index] = experience.ExperienceRef{
					ExperienceID: value.Experience.ExperienceID,
					Version:      value.Experience.Version,
				}
			}
			compiledHash, err := compileExperienceGenerationHash(
				projectIdentity,
				values,
			)
			if err != nil {
				return err
			}

			current, currentFound, err := s.loadActiveGenerationTx(
				ctx,
				tx,
				projectIdentity,
			)
			if err != nil {
				return err
			}
			if currentFound &&
				current.CompiledHash == compiledHash &&
				sameExperienceRefs(current.ExperienceRefs, refs) {
				result = CompileExperienceGenerationResult{
					Generation: current,
					Replayed:   true,
				}
				return nil
			}

			var maximum int64
			if err := tx.QueryRowContext(ctx, `
				SELECT COALESCE(MAX(generation), 0)
				FROM experience_generations
				WHERE project_identity = ?`,
				projectIdentity,
			).Scan(&maximum); err != nil {
				return errors.New("read maximum experience generation")
			}
			if maximum == math.MaxInt64 {
				return errors.New("experience generation counter exhausted")
			}

			next := ExperienceGeneration{
				ProjectIdentity: projectIdentity,
				Generation:      maximum + 1,
				CompiledHash:    compiledHash,
				ExperienceRefs:  refs,
				State:           GenerationActive,
				CompiledAt:      compiledAt,
				ActivatedAt:     compiledAt,
			}
			if currentFound {
				current.State = GenerationInactive
				if err := s.writeGenerationTx(
					ctx,
					tx,
					current,
					false,
				); err != nil {
					return err
				}
				previous := current.Generation
				next.PreviousGeneration = &previous
			}
			if err := s.writeGenerationTx(ctx, tx, next, true); err != nil {
				return err
			}
			result = CompileExperienceGenerationResult{
				Generation: next,
			}
			return nil
		},
	)
	if err != nil {
		return CompileExperienceGenerationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompileExperienceGenerationResult{},
			errors.New("commit active experience generation compilation")
	}
	return result, nil
}

func (s *Store) queryCompilableExperiencesTx(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	compiledAt time.Time,
) ([]StoredExperience, error) {
	rows, err := tx.QueryContext(ctx, experienceSelectSQL+`
		WHERE value.project_identity = ?
			AND COALESCE((
				SELECT transition.to_state
				FROM experience_transitions transition
				WHERE transition.experience_id = value.experience_id
					AND transition.experience_version = value.version
				ORDER BY transition.occurred_at DESC,
					transition.transition_id DESC
				LIMIT 1
			), value.initial_lifecycle_state) = 'active'
			AND (value.expires_at IS NULL OR value.expires_at > ?)
		LIMIT ?`,
		projectIdentity,
		formatProjectionTime(compiledAt),
		compiledExperienceProbeLimit,
	)
	if err != nil {
		return nil, errors.New("query compilable active experiences")
	}
	defer rows.Close()

	values := make([]StoredExperience, 0)
	for rows.Next() {
		value, err := s.scanStoredExperience(rows)
		if err != nil {
			return nil, err
		}
		if value.CurrentLifecycle != experience.LifecycleActive {
			return nil, ErrExperienceLifecycleConflict
		}
		if value.ExpiresAt != nil && !value.ExpiresAt.After(compiledAt) {
			return nil, errors.New(
				"expired experience escaped generation compile filter",
			)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query compilable active experiences")
	}
	if len(values) > maxCompiledExperienceRefs {
		return nil, ErrExperienceGenerationLimit
	}
	sort.Slice(values, func(first, second int) bool {
		left := values[first].Experience
		right := values[second].Experience
		if left.ExperienceID != right.ExperienceID {
			return left.ExperienceID < right.ExperienceID
		}
		return left.Version < right.Version
	})
	return values, nil
}

func compileExperienceGenerationHash(
	projectIdentity string,
	values []StoredExperience,
) (string, error) {
	type hashEntry struct {
		ExperienceID string `json:"experience_id"`
		Version      int    `json:"version"`
		ContentHash  string `json:"content_hash"`
	}
	payload := struct {
		ProjectIdentity string      `json:"project_identity"`
		Experiences     []hashEntry `json:"experiences"`
	}{
		ProjectIdentity: projectIdentity,
		Experiences:     make([]hashEntry, len(values)),
	}
	for index, value := range values {
		payload.Experiences[index] = hashEntry{
			ExperienceID: value.Experience.ExperienceID,
			Version:      value.Experience.Version,
			ContentHash:  value.Experience.ContentHash,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("encode active experience generation hash")
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sameExperienceRefs(
	first []experience.ExperienceRef,
	second []experience.ExperienceRef,
) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func (s *Store) ActivateExperienceGeneration(
	ctx context.Context,
	value ExperienceGeneration,
) (ExperienceGeneration, error) {
	if value.State != GenerationActive {
		return ExperienceGeneration{}, errors.New("generation activation requires active state")
	}
	if err := validateExperienceGeneration(value); err != nil {
		return ExperienceGeneration{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExperienceGeneration{}, errors.New("begin experience generation activation")
	}
	defer tx.Rollback()
	var activated ExperienceGeneration
	err = withMutationTx(ctx, tx, mutationExperienceGeneration, func() error {
		current, currentFound, err := s.loadActiveGenerationTx(ctx, tx, value.ProjectIdentity)
		if err != nil {
			return err
		}
		if currentFound && current.Generation == value.Generation {
			if current.CompiledHash != value.CompiledHash ||
				!sameExperienceRefs(
					current.ExperienceRefs,
					value.ExperienceRefs,
				) {
				return ErrExperienceGenerationConflict
			}
			activated = current
			return nil
		}
		target, targetFound, err := s.loadGenerationTx(
			ctx,
			tx,
			value.ProjectIdentity,
			value.Generation,
		)
		if err != nil {
			return err
		}
		if targetFound &&
			(target.CompiledHash != value.CompiledHash ||
				!sameExperienceRefs(
					target.ExperienceRefs,
					value.ExperienceRefs,
				)) {
			return ErrExperienceGenerationConflict
		}
		if currentFound {
			current.State = GenerationInactive
			if err := s.writeGenerationTx(ctx, tx, current, false); err != nil {
				return err
			}
			previous := current.Generation
			value.PreviousGeneration = &previous
		} else {
			value.PreviousGeneration = nil
		}
		value.State = GenerationActive
		if targetFound {
			target.State = GenerationActive
			target.PreviousGeneration = value.PreviousGeneration
			target.ActivatedAt = value.ActivatedAt
			if err := s.writeGenerationTx(ctx, tx, target, false); err != nil {
				return err
			}
			activated = target
			return nil
		}
		if err := s.writeGenerationTx(ctx, tx, value, true); err != nil {
			return err
		}
		activated = value
		return nil
	})
	if err != nil {
		return ExperienceGeneration{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExperienceGeneration{}, errors.New("commit experience generation activation")
	}
	return activated, nil
}

func (s *Store) RollbackExperienceGeneration(
	ctx context.Context,
	projectIdentity string,
) (ExperienceGeneration, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return ExperienceGeneration{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExperienceGeneration{}, errors.New("begin experience generation rollback")
	}
	defer tx.Rollback()
	var activated ExperienceGeneration
	err = withMutationTx(ctx, tx, mutationExperienceGeneration, func() error {
		current, found, err := s.loadActiveGenerationTx(ctx, tx, projectIdentity)
		if err != nil {
			return err
		}
		if !found || current.PreviousGeneration == nil {
			return ErrExperienceGenerationNotFound
		}
		target, found, err := s.loadGenerationTx(
			ctx,
			tx,
			projectIdentity,
			*current.PreviousGeneration,
		)
		if err != nil {
			return err
		}
		if !found {
			return ErrExperienceGenerationNotFound
		}
		current.State = GenerationInactive
		if err := s.writeGenerationTx(ctx, tx, current, false); err != nil {
			return err
		}
		target.State = GenerationActive
		previous := current.Generation
		target.PreviousGeneration = &previous
		target.ActivatedAt = s.nowUTC()
		if err := s.writeGenerationTx(ctx, tx, target, false); err != nil {
			return err
		}
		activated = target
		return nil
	})
	if err != nil {
		return ExperienceGeneration{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExperienceGeneration{}, errors.New("commit experience generation rollback")
	}
	return activated, nil
}

func (s *Store) GetActiveExperienceGeneration(
	ctx context.Context,
	projectIdentity string,
) (ExperienceGeneration, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return ExperienceGeneration{}, err
	}
	value, found, err := s.loadActiveGeneration(ctx, projectIdentity)
	if err != nil {
		return ExperienceGeneration{}, err
	}
	if !found {
		return ExperienceGeneration{}, sql.ErrNoRows
	}
	return value, nil
}

func (s *Store) loadActiveGeneration(
	ctx context.Context,
	projectIdentity string,
) (ExperienceGeneration, bool, error) {
	value, err := s.scanExperienceGeneration(s.db.QueryRowContext(ctx, generationSelectSQL+`
		WHERE project_identity = ? AND state = 'active'`,
		projectIdentity,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ExperienceGeneration{}, false, nil
	}
	return value, err == nil, err
}

func (s *Store) loadActiveGenerationTx(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
) (ExperienceGeneration, bool, error) {
	value, err := s.scanExperienceGeneration(tx.QueryRowContext(ctx, generationSelectSQL+`
		WHERE project_identity = ? AND state = 'active'`,
		projectIdentity,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ExperienceGeneration{}, false, nil
	}
	return value, err == nil, err
}

func (s *Store) loadGenerationTx(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	generation int64,
) (ExperienceGeneration, bool, error) {
	value, err := s.scanExperienceGeneration(tx.QueryRowContext(ctx, generationSelectSQL+`
		WHERE project_identity = ? AND generation = ?`,
		projectIdentity,
		generation,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ExperienceGeneration{}, false, nil
	}
	return value, err == nil, err
}

const generationSelectSQL = `
	SELECT project_identity, generation, compiled_hash, previous_generation,
		state, compiled_at, activated_at, payload, payload_encoding
	FROM experience_generations`

func (s *Store) scanExperienceGeneration(scanner rowScanner) (ExperienceGeneration, error) {
	var projectIdentity, compiledHash, state, compiledAtValue, activatedAtValue string
	var generation int64
	var previous sql.NullInt64
	var payload []byte
	var encoding string
	if err := scanner.Scan(
		&projectIdentity,
		&generation,
		&compiledHash,
		&previous,
		&state,
		&compiledAtValue,
		&activatedAtValue,
		&payload,
		&encoding,
	); err != nil {
		return ExperienceGeneration{}, err
	}
	if err := validateSealedPayloadSize(
		"experience generation",
		payload,
		maxGenerationPayloadBytes,
	); err != nil {
		return ExperienceGeneration{}, err
	}
	payload, err := s.cipher.open(
		"experience_generation",
		generationRecordID(projectIdentity, generation),
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return ExperienceGeneration{}, err
	}
	var value ExperienceGeneration
	if err := json.Unmarshal(payload, &value); err != nil {
		return ExperienceGeneration{}, errors.New("decode experience generation payload")
	}
	compiledAt, compiledErr := parseProjectionTime(compiledAtValue)
	activatedAt, activatedErr := parseProjectionTime(activatedAtValue)
	if compiledErr != nil || activatedErr != nil ||
		value.ProjectIdentity != projectIdentity ||
		value.Generation != generation ||
		value.CompiledHash != compiledHash ||
		value.State != GenerationState(state) ||
		!value.CompiledAt.Equal(compiledAt) ||
		!value.ActivatedAt.Equal(activatedAt) ||
		!optionalInt64Matches(value.PreviousGeneration, previous) {
		return ExperienceGeneration{}, errors.New("experience generation index does not match payload")
	}
	if err := validateExperienceGeneration(value); err != nil {
		return ExperienceGeneration{}, errors.New("invalid persisted experience generation")
	}
	return value, nil
}

func (s *Store) writeGenerationTx(
	ctx context.Context,
	tx *sql.Tx,
	value ExperienceGeneration,
	insert bool,
) error {
	if err := validateExperienceGeneration(value); err != nil {
		return err
	}
	_, payload, err := s.sealDomainJSON(
		"experience_generation",
		generationRecordID(value.ProjectIdentity, value.Generation),
		"payload",
		value,
		maxGenerationPayloadBytes,
	)
	if err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	var result sql.Result
	if insert {
		result, err = tx.ExecContext(ctx, `
			INSERT INTO experience_generations (
				project_identity, generation, compiled_hash,
				previous_generation, state, compiled_at, activated_at,
				payload, payload_encoding, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			value.ProjectIdentity,
			value.Generation,
			value.CompiledHash,
			nullableInt64Value(value.PreviousGeneration),
			value.State,
			formatProjectionTime(value.CompiledAt),
			formatProjectionTime(value.ActivatedAt),
			payload,
			payloadEncodingAESGCM,
			now,
			now,
		)
	} else {
		result, err = tx.ExecContext(ctx, `
			UPDATE experience_generations
			SET previous_generation = ?, state = ?, activated_at = ?,
				payload = ?, payload_encoding = ?, updated_at = ?
			WHERE project_identity = ? AND generation = ?`,
			nullableInt64Value(value.PreviousGeneration),
			value.State,
			formatProjectionTime(value.ActivatedAt),
			payload,
			payloadEncodingAESGCM,
			now,
			value.ProjectIdentity,
			value.Generation,
		)
	}
	if err != nil {
		return fmt.Errorf("write experience generation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return errors.New("inspect experience generation persistence")
	}
	if affected != 1 {
		return errors.New("experience generation write did not affect exactly one row")
	}
	return nil
}

func (s *Store) RecordPendingMissionPackReceipt(
	ctx context.Context,
	receipt MissionPackReceipt,
) (bool, error) {
	if receipt.BindingState != ReceiptPending || receipt.BoundSessionKey != "" {
		return false, errors.New("new mission pack receipt must be pending and unbound")
	}
	if err := validateMissionPackReceipt(receipt); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"mission_pack_receipt",
		receipt.ReceiptID,
		"payload",
		receipt,
		maxReceiptPayloadBytes,
	)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin mission pack receipt persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationMissionPackReceipt, func() error {
		if err := s.validateMissionPackReceiptReferencesTx(ctx, tx, receipt); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO mission_pack_receipts (
				receipt_id, pack_id, project_identity, harness, generation,
				accepted_at, expires_at, task_hint_hash, binding_state,
				bound_session_key, payload, payload_encoding, created_at,
				updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)
			ON CONFLICT(receipt_id) DO NOTHING`,
			receipt.ReceiptID,
			receipt.PackID,
			receipt.ProjectIdentity,
			receipt.Harness,
			receipt.Generation,
			formatProjectionTime(receipt.AcceptedAt),
			formatProjectionTime(receipt.ExpiresAt),
			receipt.TaskHintHash,
			receipt.BindingState,
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert mission pack receipt: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect mission pack receipt persistence")
		}
		inserted = affected == 1
		if !inserted {
			var existingPayload []byte
			var encoding string
			if err := tx.QueryRowContext(ctx, `
				SELECT payload, payload_encoding
				FROM mission_pack_receipts
				WHERE receipt_id = ?`,
				receipt.ReceiptID,
			).Scan(&existingPayload, &encoding); err != nil {
				return errors.New("read duplicate mission pack receipt")
			}
			if err := validateSealedPayloadSize(
				"mission pack receipt",
				existingPayload,
				maxReceiptPayloadBytes,
			); err != nil {
				return err
			}
			existingPayload, err = s.cipher.open(
				"mission_pack_receipt",
				receipt.ReceiptID,
				"payload",
				encoding,
				existingPayload,
			)
			if err != nil {
				return err
			}
			if !bytes.Equal(existingPayload, plaintext) {
				return ErrMissionPackReceiptConflict
			}
		}
		return s.ensureMissionPackReceiptApplicationProgressTx(
			ctx,
			tx,
			receipt,
		)
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit mission pack receipt persistence")
	}
	return inserted, nil
}

func (s *Store) GetMissionPackReceipt(
	ctx context.Context,
	receiptID string,
) (MissionPackReceipt, error) {
	if err := validateStorageIdentifier("mission pack receipt ID", receiptID); err != nil {
		return MissionPackReceipt{}, err
	}
	return s.scanMissionPackReceipt(s.db.QueryRowContext(ctx, receiptSelectSQL+`
		WHERE receipt_id = ?`,
		receiptID,
	))
}

func (s *Store) QueryPendingMissionPackReceipts(
	ctx context.Context,
	projectIdentity string,
	harness experience.Harness,
	observedAt time.Time,
	limit int,
) ([]MissionPackReceipt, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	if !harness.Valid() {
		return nil, errors.New("invalid receipt harness")
	}
	if observedAt.IsZero() {
		return nil, errors.New("receipt observation time is required")
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, receiptSelectSQL+`
		WHERE project_identity = ? AND harness = ?
			AND binding_state = 'pending'
			AND accepted_at <= ? AND expires_at > ?
		ORDER BY accepted_at, receipt_id
		LIMIT ?`,
		projectIdentity,
		harness,
		formatProjectionTime(observedAt),
		formatProjectionTime(observedAt),
		limit,
	)
	if err != nil {
		return nil, errors.New("query pending mission pack receipts")
	}
	defer rows.Close()
	result := make([]MissionPackReceipt, 0)
	for rows.Next() {
		receipt, err := s.scanMissionPackReceipt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query pending mission pack receipts")
	}
	return result, nil
}

// QueryBoundMissionPackReceiptsNeedingApplications returns a bounded set of
// bound receipts that have no initialized progress rows or at least one
// expected experience reference without an exact receipt-to-application link.
// Receipt payloads are decrypted only after the bounded plaintext query has
// selected them.
//
// Distinct receipts with the same delivery tuple and experience ref may share
// the deterministic Application record, but each retains its own progress row
// and link so receipt traceability is not conflated.
func (s *Store) QueryBoundMissionPackReceiptsNeedingApplications(
	ctx context.Context,
	limit int,
) ([]MissionPackReceipt, error) {
	if limit < 1 {
		return nil, errors.New(
			"mission pack application receipt query limit is required",
		)
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.receipt_id, r.pack_id, r.project_identity, r.harness,
			r.generation, r.accepted_at, r.expires_at, r.task_hint_hash,
			r.binding_state, r.bound_session_key, r.payload,
			r.payload_encoding
		FROM mission_pack_receipts AS r
		WHERE r.binding_state = 'bound'
			AND (
				NOT EXISTS (
					SELECT 1
					FROM mission_pack_receipt_applications AS p
					WHERE p.receipt_id = r.receipt_id
				)
				OR EXISTS (
					SELECT 1
					FROM mission_pack_receipt_applications AS p
					WHERE p.receipt_id = r.receipt_id
						AND p.application_id IS NULL
				)
			)
		ORDER BY r.accepted_at, r.receipt_id
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, errors.New(
			"query bound mission pack receipts needing applications",
		)
	}
	defer rows.Close()

	result := make([]MissionPackReceipt, 0)
	for rows.Next() {
		receipt, err := s.scanMissionPackReceipt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New(
			"query bound mission pack receipts needing applications",
		)
	}
	return result, nil
}

func (s *Store) EnsureMissionPackReceiptApplicationProgress(
	ctx context.Context,
	receipt MissionPackReceipt,
) error {
	if err := validateMissionPackReceipt(receipt); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New(
			"begin mission pack receipt application progress initialization",
		)
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationMissionPackReceipt, func() error {
		persisted, err := s.scanMissionPackReceipt(tx.QueryRowContext(
			ctx,
			receiptSelectSQL+` WHERE receipt_id = ?`,
			receipt.ReceiptID,
		))
		if err != nil {
			return errors.New(
				"read mission pack receipt for application progress",
			)
		}
		if !sameJSONValue(persisted, receipt) {
			return ErrMissionPackReceiptConflict
		}
		return s.ensureMissionPackReceiptApplicationProgressTx(
			ctx,
			tx,
			receipt,
		)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New(
			"commit mission pack receipt application progress initialization",
		)
	}
	return nil
}

// GetMissionPackReceiptApplicationProgress returns one ordered result for each
// experience reference in one exact receipt. ProgressPresent distinguishes a
// missing row from an initialized-but-unlinked row.
func (s *Store) GetMissionPackReceiptApplicationProgress(
	ctx context.Context,
	receiptID string,
) ([]MissionPackReceiptApplicationProgress, error) {
	if err := validateStorageIdentifier(
		"mission pack receipt ID",
		receiptID,
	); err != nil {
		return nil, err
	}
	const receiptExperienceLimit = 3
	receipt, err := s.GetMissionPackReceipt(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	expected := make(
		map[experience.ExperienceRef]struct{},
		len(receipt.ExperienceRefs),
	)
	for _, ref := range receipt.ExperienceRefs {
		expected[ref] = struct{}{}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT receipt_id, experience_id, experience_version,
			application_id, linked_at
		FROM mission_pack_receipt_applications
		WHERE receipt_id = ?
		ORDER BY experience_id, experience_version
		LIMIT ?`,
		receiptID,
		receiptExperienceLimit+1,
	)
	if err != nil {
		return nil, errors.New(
			"query mission pack receipt application progress",
		)
	}
	defer rows.Close()

	persisted := make(
		map[experience.ExperienceRef]MissionPackReceiptApplicationProgress,
		len(receipt.ExperienceRefs),
	)
	seenRefs := make(map[experience.ExperienceRef]struct{}, receiptExperienceLimit)
	seenApplications := make(map[string]struct{}, receiptExperienceLimit)
	for rows.Next() {
		var rowReceiptID, experienceID string
		var version int
		var applicationID, linkedAt sql.NullString
		if err := rows.Scan(
			&rowReceiptID,
			&experienceID,
			&version,
			&applicationID,
			&linkedAt,
		); err != nil {
			return nil, errors.New(
				"read mission pack receipt application progress",
			)
		}
		if len(persisted) == receiptExperienceLimit {
			return nil, ErrMissionPackReceiptApplicationCorrupt
		}
		ref := experience.ExperienceRef{
			ExperienceID: experienceID,
			Version:      version,
		}
		if rowReceiptID != receiptID {
			return nil, ErrMissionPackReceiptApplicationCorrupt
		}
		if err := ref.Validate(); err != nil {
			return nil, ErrMissionPackReceiptApplicationCorrupt
		}
		if _, found := expected[ref]; !found {
			return nil, ErrMissionPackReceiptApplicationCorrupt
		}
		if _, duplicate := seenRefs[ref]; duplicate {
			return nil, ErrMissionPackReceiptApplicationCorrupt
		}
		seenRefs[ref] = struct{}{}

		progress := MissionPackReceiptApplicationProgress{
			ReceiptID:       rowReceiptID,
			Experience:      ref,
			ProgressPresent: true,
		}
		switch {
		case applicationID.Valid != linkedAt.Valid:
			return nil, ErrMissionPackReceiptApplicationCorrupt
		case applicationID.Valid:
			if err := validateStorageIdentifier(
				"experience application ID",
				applicationID.String,
			); err != nil {
				return nil, ErrMissionPackReceiptApplicationCorrupt
			}
			if _, err := parseProjectionTime(linkedAt.String); err != nil {
				return nil, ErrMissionPackReceiptApplicationCorrupt
			}
			if _, duplicate := seenApplications[applicationID.String]; duplicate {
				return nil, ErrMissionPackReceiptApplicationCorrupt
			}
			seenApplications[applicationID.String] = struct{}{}
			progress.ApplicationID = applicationID.String
		}
		persisted[ref] = progress
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New(
			"query mission pack receipt application progress",
		)
	}
	result := make(
		[]MissionPackReceiptApplicationProgress,
		0,
		len(receipt.ExperienceRefs),
	)
	for _, ref := range receipt.ExperienceRefs {
		progress, found := persisted[ref]
		if !found {
			progress = MissionPackReceiptApplicationProgress{
				ReceiptID:  receiptID,
				Experience: ref,
			}
		}
		result = append(result, progress)
	}
	return result, nil
}

func (s *Store) LinkMissionPackReceiptApplication(
	ctx context.Context,
	receiptID string,
	ref experience.ExperienceRef,
	applicationID string,
) error {
	if err := validateStorageIdentifier(
		"mission pack receipt ID",
		receiptID,
	); err != nil {
		return err
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := validateStorageIdentifier(
		"experience application ID",
		applicationID,
	); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin mission pack receipt application link")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationMissionPackReceipt, func() error {
		receipt, err := s.scanMissionPackReceipt(tx.QueryRowContext(
			ctx,
			receiptSelectSQL+` WHERE receipt_id = ?`,
			receiptID,
		))
		if err != nil {
			return errors.New("read mission pack receipt for application link")
		}
		if receipt.BindingState != ReceiptBound {
			return errors.New(
				"mission pack receipt application link requires a bound receipt",
			)
		}

		var experienceID, projectIdentity, sessionKey string
		var version int
		var deliveryKind, deliveryState, deliveredAtValue string
		if err := tx.QueryRowContext(ctx, `
			SELECT experience_id, experience_version, project_identity,
				session_key, delivery_kind, delivery_state, delivered_at
			FROM experience_applications
			WHERE application_id = ?`,
			applicationID,
		).Scan(
			&experienceID,
			&version,
			&projectIdentity,
			&sessionKey,
			&deliveryKind,
			&deliveryState,
			&deliveredAtValue,
		); err != nil {
			return errors.New(
				"read experience application for mission pack receipt link",
			)
		}
		deliveredAt, err := parseProjectionTime(deliveredAtValue)
		if err != nil ||
			experienceID != ref.ExperienceID ||
			version != ref.Version ||
			projectIdentity != receipt.ProjectIdentity ||
			sessionKey != receipt.BoundSessionKey ||
			deliveryKind != string(experience.DeliveryMissionPack) ||
			deliveryState != string(experience.DeliveryDelivered) ||
			!deliveredAt.Equal(receipt.AcceptedAt) {
			return ErrMissionPackReceiptApplicationConflict
		}

		var linkedApplicationID sql.NullString
		if err := tx.QueryRowContext(ctx, `
			SELECT application_id
			FROM mission_pack_receipt_applications
			WHERE receipt_id = ? AND experience_id = ?
				AND experience_version = ?`,
			receiptID,
			ref.ExperienceID,
			ref.Version,
		).Scan(&linkedApplicationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New(
					"mission pack receipt application progress is not initialized",
				)
			}
			return errors.New("read mission pack receipt application link")
		}
		if linkedApplicationID.Valid {
			if linkedApplicationID.String != applicationID {
				return ErrMissionPackReceiptApplicationConflict
			}
			return nil
		}

		now := formatProjectionTime(s.nowUTC())
		result, err := tx.ExecContext(ctx, `
			UPDATE mission_pack_receipt_applications
			SET application_id = ?, linked_at = ?, updated_at = ?
			WHERE receipt_id = ? AND experience_id = ?
				AND experience_version = ? AND application_id IS NULL`,
			applicationID,
			now,
			now,
			receiptID,
			ref.ExperienceID,
			ref.Version,
		)
		if err != nil {
			return errors.New("link mission pack receipt application")
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return ErrMissionPackReceiptApplicationConflict
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit mission pack receipt application link")
	}
	return nil
}

func (s *Store) ensureMissionPackReceiptApplicationProgressTx(
	ctx context.Context,
	tx *sql.Tx,
	receipt MissionPackReceipt,
) error {
	now := formatProjectionTime(s.nowUTC())
	for _, ref := range receipt.ExperienceRefs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mission_pack_receipt_applications (
				receipt_id, experience_id, experience_version,
				application_id, linked_at, created_at, updated_at
			) VALUES (?, ?, ?, NULL, NULL, ?, ?)
			ON CONFLICT(receipt_id, experience_id, experience_version)
			DO NOTHING`,
			receipt.ReceiptID,
			ref.ExperienceID,
			ref.Version,
			now,
			now,
		); err != nil {
			return errors.New(
				"initialize mission pack receipt application progress",
			)
		}
	}
	return nil
}

func (s *Store) ResolveMissionPackReceipt(
	ctx context.Context,
	receiptID string,
	compatibleSessionKeys []string,
	observedAt time.Time,
) (MissionPackReceipt, error) {
	if err := validateStorageIdentifier("mission pack receipt ID", receiptID); err != nil {
		return MissionPackReceipt{}, err
	}
	if observedAt.IsZero() {
		return MissionPackReceipt{}, errors.New("receipt resolution time is required")
	}
	sessionKeys, err := normalizeSessionKeys(compatibleSessionKeys)
	if err != nil {
		return MissionPackReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MissionPackReceipt{}, errors.New("begin mission pack receipt resolution")
	}
	defer tx.Rollback()
	var result MissionPackReceipt
	err = withMutationTx(ctx, tx, mutationMissionPackReceipt, func() error {
		receipt, err := s.scanMissionPackReceipt(tx.QueryRowContext(ctx, receiptSelectSQL+`
			WHERE receipt_id = ?`,
			receiptID,
		))
		if err != nil {
			return err
		}
		if receipt.BindingState != ReceiptPending {
			result = receipt
			return nil
		}
		switch {
		case !observedAt.Before(receipt.ExpiresAt):
			receipt.BindingState = ReceiptExpired
		case len(sessionKeys) == 0:
			result = receipt
			return nil
		case len(sessionKeys) == 1:
			receipt.BindingState = ReceiptBound
			receipt.BoundSessionKey = sessionKeys[0]
		default:
			receipt.BindingState = ReceiptAmbiguous
		}
		if err := s.updateMissionPackReceiptTx(ctx, tx, receipt); err != nil {
			return err
		}
		result = receipt
		return nil
	})
	if err != nil {
		return MissionPackReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return MissionPackReceipt{}, errors.New("commit mission pack receipt resolution")
	}
	return result, nil
}

func (s *Store) CancelMissionPackReceipt(
	ctx context.Context,
	receiptID string,
) (MissionPackReceipt, error) {
	if err := validateStorageIdentifier("mission pack receipt ID", receiptID); err != nil {
		return MissionPackReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MissionPackReceipt{}, errors.New("begin mission pack receipt cancellation")
	}
	defer tx.Rollback()
	var result MissionPackReceipt
	err = withMutationTx(ctx, tx, mutationMissionPackReceipt, func() error {
		receipt, err := s.scanMissionPackReceipt(tx.QueryRowContext(ctx, receiptSelectSQL+`
			WHERE receipt_id = ?`,
			receiptID,
		))
		if err != nil {
			return err
		}
		if receipt.BindingState != ReceiptPending {
			return errors.New("only pending mission pack receipt can be cancelled")
		}
		receipt.BindingState = ReceiptCancelled
		if err := s.updateMissionPackReceiptTx(ctx, tx, receipt); err != nil {
			return err
		}
		result = receipt
		return nil
	})
	if err != nil {
		return MissionPackReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return MissionPackReceipt{}, errors.New("commit mission pack receipt cancellation")
	}
	return result, nil
}

const receiptSelectSQL = `
	SELECT receipt_id, pack_id, project_identity, harness, generation,
		accepted_at, expires_at, task_hint_hash, binding_state,
		bound_session_key, payload, payload_encoding
	FROM mission_pack_receipts`

func (s *Store) scanMissionPackReceipt(scanner rowScanner) (MissionPackReceipt, error) {
	var receiptID, packID, projectIdentity, harness string
	var acceptedAtValue, expiresAtValue, taskHintHash, bindingState string
	var generation int64
	var boundSessionKey sql.NullString
	var payload []byte
	var encoding string
	if err := scanner.Scan(
		&receiptID,
		&packID,
		&projectIdentity,
		&harness,
		&generation,
		&acceptedAtValue,
		&expiresAtValue,
		&taskHintHash,
		&bindingState,
		&boundSessionKey,
		&payload,
		&encoding,
	); err != nil {
		return MissionPackReceipt{}, err
	}
	if err := validateSealedPayloadSize(
		"mission pack receipt",
		payload,
		maxReceiptPayloadBytes,
	); err != nil {
		return MissionPackReceipt{}, err
	}
	payload, err := s.cipher.open(
		"mission_pack_receipt",
		receiptID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return MissionPackReceipt{}, err
	}
	var receipt MissionPackReceipt
	if err := json.Unmarshal(payload, &receipt); err != nil {
		return MissionPackReceipt{}, errors.New("decode mission pack receipt payload")
	}
	acceptedAt, acceptedErr := parseProjectionTime(acceptedAtValue)
	expiresAt, expiresErr := parseProjectionTime(expiresAtValue)
	if acceptedErr != nil || expiresErr != nil ||
		receipt.ReceiptID != receiptID ||
		receipt.PackID != packID ||
		receipt.ProjectIdentity != projectIdentity ||
		string(receipt.Harness) != harness ||
		receipt.Generation != generation ||
		!receipt.AcceptedAt.Equal(acceptedAt) ||
		!receipt.ExpiresAt.Equal(expiresAt) ||
		receipt.TaskHintHash != taskHintHash ||
		string(receipt.BindingState) != bindingState ||
		receipt.BoundSessionKey != nullStringValue(boundSessionKey) {
		return MissionPackReceipt{}, errors.New("mission pack receipt index does not match payload")
	}
	if err := validateMissionPackReceipt(receipt); err != nil {
		return MissionPackReceipt{}, errors.New("invalid persisted mission pack receipt")
	}
	return receipt, nil
}

func (s *Store) updateMissionPackReceiptTx(
	ctx context.Context,
	tx *sql.Tx,
	receipt MissionPackReceipt,
) error {
	if err := validateMissionPackReceipt(receipt); err != nil {
		return err
	}
	_, payload, err := s.sealDomainJSON(
		"mission_pack_receipt",
		receipt.ReceiptID,
		"payload",
		receipt,
		maxReceiptPayloadBytes,
	)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE mission_pack_receipts
		SET binding_state = ?, bound_session_key = ?, payload = ?,
			payload_encoding = ?, updated_at = ?
		WHERE receipt_id = ?`,
		receipt.BindingState,
		nullable(receipt.BoundSessionKey),
		payload,
		payloadEncodingAESGCM,
		formatProjectionTime(s.nowUTC()),
		receipt.ReceiptID,
	)
	if err != nil {
		return errors.New("update mission pack receipt")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("mission pack receipt was not updated")
	}
	return nil
}

func (s *Store) validateMissionPackReceiptReferencesTx(
	ctx context.Context,
	tx *sql.Tx,
	receipt MissionPackReceipt,
) error {
	generation, found, err := s.loadGenerationTx(
		ctx,
		tx,
		receipt.ProjectIdentity,
		receipt.Generation,
	)
	if err != nil {
		return errors.New("read mission pack receipt generation")
	}
	if !found {
		return errors.New("mission pack receipt generation was not found")
	}
	if generation.State != GenerationActive {
		return errors.New("mission pack receipt generation is not active")
	}
	members := make(
		map[experience.ExperienceRef]struct{},
		len(generation.ExperienceRefs),
	)
	for _, ref := range generation.ExperienceRefs {
		members[ref] = struct{}{}
	}
	for _, ref := range receipt.ExperienceRefs {
		var projectIdentity, lifecycleState string
		if err := tx.QueryRowContext(ctx, `
			SELECT project_identity, lifecycle_state
			FROM experiences
			WHERE experience_id = ? AND version = ?`,
			ref.ExperienceID,
			ref.Version,
		).Scan(&projectIdentity, &lifecycleState); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("mission pack receipt experience was not found")
			}
			return errors.New("read mission pack receipt experience")
		}
		if projectIdentity != receipt.ProjectIdentity {
			return errors.New("mission pack receipt experience project does not match")
		}
		if lifecycleState != string(experience.LifecycleActive) {
			return errors.New("mission pack receipt experience is not active")
		}
		if _, member := members[ref]; !member {
			return errors.New(
				"mission pack receipt experience is not a member of generation",
			)
		}
	}
	return nil
}

func validateExperienceGeneration(value ExperienceGeneration) error {
	if err := validateProjectIdentity(value.ProjectIdentity); err != nil {
		return err
	}
	if value.Generation < 1 || !validStorageSHA256(value.CompiledHash) ||
		value.CompiledAt.IsZero() || value.ActivatedAt.IsZero() {
		return errors.New("invalid experience generation")
	}
	if value.PreviousGeneration != nil && *value.PreviousGeneration < 1 {
		return errors.New("invalid previous experience generation")
	}
	if value.State != GenerationActive && value.State != GenerationInactive {
		return errors.New("invalid experience generation state")
	}
	if err := validateGenerationExperienceRefs(value.ExperienceRefs); err != nil {
		return err
	}
	return nil
}

func validateGenerationExperienceRefs(
	refs []experience.ExperienceRef,
) error {
	if len(refs) > maxCompiledExperienceRefs {
		return ErrExperienceGenerationLimit
	}
	for index, ref := range refs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf(
				"invalid experience generation reference: %w",
				err,
			)
		}
		if index == 0 {
			continue
		}
		previous := refs[index-1]
		switch {
		case previous == ref:
			return errors.New(
				"experience generation contains duplicate reference",
			)
		case previous.ExperienceID > ref.ExperienceID:
			return errors.New(
				"experience generation references are not sorted",
			)
		case previous.ExperienceID == ref.ExperienceID &&
			previous.Version > ref.Version:
			return errors.New(
				"experience generation references are not sorted",
			)
		}
	}
	return nil
}

func validateMissionPackReceipt(receipt MissionPackReceipt) error {
	if err := validateStorageIdentifier("receipt ID", receipt.ReceiptID); err != nil {
		return err
	}
	if err := validateStorageIdentifier("receipt pack ID", receipt.PackID); err != nil {
		return err
	}
	if err := validateProjectIdentity(receipt.ProjectIdentity); err != nil {
		return err
	}
	if !receipt.Harness.Valid() || receipt.Generation < 1 ||
		receipt.AcceptedAt.IsZero() || receipt.ExpiresAt.IsZero() ||
		!receipt.ExpiresAt.After(receipt.AcceptedAt) ||
		len(receipt.ExperienceRefs) == 0 || len(receipt.ExperienceRefs) > 3 ||
		len(receipt.TaskHintHash) > 128 {
		return errors.New("invalid mission pack receipt")
	}
	seenRefs := make(map[experience.ExperienceRef]struct{}, len(receipt.ExperienceRefs))
	for _, ref := range receipt.ExperienceRefs {
		if err := ref.Validate(); err != nil {
			return err
		}
		if _, duplicate := seenRefs[ref]; duplicate {
			return errors.New("mission pack receipt contains duplicate experience reference")
		}
		seenRefs[ref] = struct{}{}
	}
	switch receipt.BindingState {
	case ReceiptPending, ReceiptAmbiguous, ReceiptExpired, ReceiptCancelled:
		if receipt.BoundSessionKey != "" {
			return errors.New("unbound receipt state cannot contain session key")
		}
	case ReceiptBound:
		if err := validateStorageIdentifier("receipt session key", receipt.BoundSessionKey); err != nil {
			return err
		}
	default:
		return errors.New("invalid mission pack receipt binding state")
	}
	return nil
}

func validStorageSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil && strings.ToLower(value) == value
}

func generationRecordID(projectIdentity string, generation int64) string {
	return fmt.Sprintf("%s:%d", projectIdentity, generation)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatProjectionTime(*value)
}

func nullStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func optionalTimeMatches(value *time.Time, indexed sql.NullString) bool {
	if value == nil {
		return !indexed.Valid
	}
	if !indexed.Valid {
		return false
	}
	parsed, err := parseProjectionTime(indexed.String)
	return err == nil && value.Equal(parsed)
}

func nullableInt64Value(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalInt64Matches(value *int64, indexed sql.NullInt64) bool {
	if value == nil {
		return !indexed.Valid
	}
	return indexed.Valid && *value == indexed.Int64
}

func nullableEvaluationTime(value *experience.Evaluation) any {
	if value == nil {
		return nil
	}
	return formatProjectionTime(value.EvaluatedAt)
}

func sameJSONValue(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func sameApplicationDeliveryIdentity(
	left experience.Application,
	right experience.Application,
) bool {
	left.OpportunityState = right.OpportunityState
	left.ApplicabilityState = right.ApplicabilityState
	left.VerifierState = right.VerifierState
	left.TaskOutcomeState = right.TaskOutcomeState
	return sameJSONValue(left, right)
}

func normalizeSessionKeys(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if err := validateStorageIdentifier("compatible session key", value); err != nil {
			return nil, err
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result, nil
}
