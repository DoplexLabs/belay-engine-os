package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	defaultExperienceQueryLimit = 100
	maxExperienceQueryLimit     = 500
	maxExperiencePayloadBytes   = 2 << 20
	maxEvidencePayloadBytes     = 64 << 10
)

var (
	ErrExperienceCandidateConflict  = errors.New("experience candidate identity conflicts with persisted record")
	ErrExperienceVersionConflict    = errors.New("experience version identity conflicts with persisted record")
	ErrExperienceTransitionConflict = errors.New("experience transition identity conflicts with persisted record")
	ErrExperienceLifecycleConflict  = errors.New("experience lifecycle projection conflicts with transition history")
)

type StoredExperience struct {
	Experience       experience.Experience
	CurrentLifecycle experience.LifecycleState
	ActivatedAt      *time.Time
	ExpiresAt        *time.Time
}

type storedEvidencePayload struct {
	EvidenceSetID string                   `json:"evidence_set_id"`
	EvidenceIndex int                      `json:"evidence_index"`
	Experience    experience.ExperienceRef `json:"experience"`
	Ref           experience.EvidenceRef   `json:"ref"`
}

type sealedExperienceEvidence struct {
	index   int
	ref     experience.EvidenceRef
	payload []byte
}

type preparedExperiencePersistence struct {
	value        experience.Experience
	recordID     string
	plaintext    []byte
	payload      []byte
	evidenceRows []sealedExperienceEvidence
}

func (s *Store) InsertExperienceCandidate(
	ctx context.Context,
	candidate experience.Candidate,
) (bool, error) {
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"experience_candidate",
		candidate.CandidateID,
		"payload",
		candidate,
		maxExperiencePayloadBytes,
	)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin experience candidate persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationExperienceCandidate, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO experience_candidates (
				candidate_id, project_identity, family, lifecycle_state,
				instruction_authority, created_at, payload, payload_encoding,
				inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(candidate_id) DO NOTHING`,
			candidate.CandidateID,
			candidate.ProjectIdentity,
			candidate.Family,
			candidate.LifecycleState,
			candidate.Authority,
			formatProjectionTime(candidate.CreatedAt),
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert experience candidate: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect experience candidate persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}
		var existingPayload []byte
		var encoding string
		if err := tx.QueryRowContext(ctx, `
			SELECT payload, payload_encoding
			FROM experience_candidates
			WHERE candidate_id = ?`,
			candidate.CandidateID,
		).Scan(&existingPayload, &encoding); err != nil {
			return errors.New("read duplicate experience candidate")
		}
		existingPayload, err = s.cipher.open(
			"experience_candidate",
			candidate.CandidateID,
			"payload",
			encoding,
			existingPayload,
		)
		if err != nil {
			return err
		}
		if !bytes.Equal(existingPayload, plaintext) {
			return ErrExperienceCandidateConflict
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit experience candidate persistence")
	}
	return inserted, nil
}

func (s *Store) GetExperienceCandidate(
	ctx context.Context,
	candidateID string,
) (experience.Candidate, error) {
	if err := validateStorageIdentifier("experience candidate ID", candidateID); err != nil {
		return experience.Candidate{}, err
	}
	return s.scanExperienceCandidate(s.db.QueryRowContext(
		ctx,
		experienceCandidateSelectSQL+` WHERE candidate_id = ?`,
		candidateID,
	))
}

func (s *Store) QueryExperienceCandidates(
	ctx context.Context,
	projectIdentity string,
	limit int,
) ([]experience.Candidate, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, experienceCandidateSelectSQL+`
		WHERE project_identity = ?
		ORDER BY created_at DESC, candidate_id
		LIMIT ?`,
		projectIdentity,
		limit,
	)
	if err != nil {
		return nil, errors.New("query experience candidates")
	}
	defer rows.Close()
	result := make([]experience.Candidate, 0)
	for rows.Next() {
		candidate, err := s.scanExperienceCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query experience candidates")
	}
	return result, nil
}

const experienceCandidateSelectSQL = `
	SELECT candidate_id, project_identity, family, lifecycle_state,
		instruction_authority, created_at, payload, payload_encoding
	FROM experience_candidates`

func (s *Store) scanExperienceCandidate(scanner rowScanner) (experience.Candidate, error) {
	var candidateID, projectIdentity, family, lifecycle, authority string
	var createdAtValue, encoding string
	var payload []byte
	if err := scanner.Scan(
		&candidateID,
		&projectIdentity,
		&family,
		&lifecycle,
		&authority,
		&createdAtValue,
		&payload,
		&encoding,
	); err != nil {
		return experience.Candidate{}, err
	}
	if err := validateSealedPayloadSize(
		"experience candidate",
		payload,
		maxExperiencePayloadBytes,
	); err != nil {
		return experience.Candidate{}, err
	}
	payload, err := s.cipher.open(
		"experience_candidate",
		candidateID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return experience.Candidate{}, err
	}
	var candidate experience.Candidate
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return experience.Candidate{}, errors.New("decode experience candidate payload")
	}
	createdAt, err := parseProjectionTime(createdAtValue)
	if err != nil ||
		candidate.CandidateID != candidateID ||
		candidate.ProjectIdentity != projectIdentity ||
		string(candidate.Family) != family ||
		string(candidate.LifecycleState) != lifecycle ||
		string(candidate.Authority) != authority ||
		!candidate.CreatedAt.Equal(createdAt) {
		return experience.Candidate{}, errors.New("experience candidate index does not match payload")
	}
	if err := candidate.Validate(); err != nil {
		return experience.Candidate{}, errors.New("invalid persisted experience candidate")
	}
	return candidate, nil
}

func (s *Store) InsertExperience(
	ctx context.Context,
	value experience.Experience,
) (bool, error) {
	prepared, err := s.prepareExperiencePersistence(value)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin experience persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationExperienceRegistry, func() error {
		var persistErr error
		inserted, persistErr = s.insertPreparedExperienceTx(
			ctx,
			tx,
			prepared,
		)
		return persistErr
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit experience persistence")
	}
	return inserted, nil
}

func (s *Store) prepareExperiencePersistence(
	value experience.Experience,
) (preparedExperiencePersistence, error) {
	if err := value.Validate(); err != nil {
		return preparedExperiencePersistence{}, err
	}
	recordID := experienceRecordID(value.ExperienceID, value.Version)
	plaintext, payload, err := s.sealDomainJSON(
		"experience",
		recordID,
		"payload",
		value,
		maxExperiencePayloadBytes,
	)
	if err != nil {
		return preparedExperiencePersistence{}, err
	}
	evidenceRows := make(
		[]sealedExperienceEvidence,
		0,
		len(value.Evidence.Refs),
	)
	for index, ref := range value.Evidence.Refs {
		envelope := storedEvidencePayload{
			EvidenceSetID: value.Evidence.EvidenceSetID,
			EvidenceIndex: index,
			Experience: experience.ExperienceRef{
				ExperienceID: value.ExperienceID,
				Version:      value.Version,
			},
			Ref: ref,
		}
		_, sealed, err := s.sealDomainJSON(
			"experience_evidence",
			evidenceRecordID(
				value.ExperienceID,
				value.Version,
				value.Evidence.EvidenceSetID,
				index,
			),
			"payload",
			envelope,
			maxEvidencePayloadBytes,
		)
		if err != nil {
			return preparedExperiencePersistence{}, err
		}
		evidenceRows = append(evidenceRows, sealedExperienceEvidence{
			index:   index,
			ref:     ref,
			payload: sealed,
		})
	}
	return preparedExperiencePersistence{
		value:        value,
		recordID:     recordID,
		plaintext:    plaintext,
		payload:      payload,
		evidenceRows: evidenceRows,
	}, nil
}

func (s *Store) insertPreparedExperienceTx(
	ctx context.Context,
	tx *sql.Tx,
	prepared preparedExperiencePersistence,
) (bool, error) {
	value := prepared.value
	var activatedAt any
	if value.Governance.LifecycleState == experience.LifecycleActive {
		activatedAt = formatProjectionTime(
			value.Governance.Approval.ApprovedAt,
		)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO experiences (
			experience_id, version, origin_candidate_id, project_identity,
			experience_type, initial_lifecycle_state, lifecycle_state,
			intervention_strength, content_hash, previous_experience_id,
			previous_version, approved_at, activated_at, expires_at,
			created_at, updated_at, payload, payload_encoding
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(experience_id, version) DO NOTHING`,
		value.ExperienceID,
		value.Version,
		value.OriginCandidateID,
		value.Scope.ProjectIdentity,
		value.Type,
		value.Governance.LifecycleState,
		value.Governance.LifecycleState,
		value.Guidance.InterventionStrength,
		value.ContentHash,
		nullableExperienceID(value.PreviousVersion),
		nullableExperienceVersion(value.PreviousVersion),
		formatProjectionTime(value.Governance.Approval.ApprovedAt),
		activatedAt,
		nullableTime(value.Applicability.ExpiresAt),
		formatProjectionTime(value.CreatedAt),
		formatProjectionTime(value.CreatedAt),
		prepared.payload,
		payloadEncodingAESGCM,
	)
	if err != nil {
		return false, fmt.Errorf("insert experience version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, errors.New("inspect experience persistence")
	}
	if affected == 0 {
		var existingPayload []byte
		var encoding string
		if err := tx.QueryRowContext(ctx, `
			SELECT payload, payload_encoding
			FROM experiences
			WHERE experience_id = ? AND version = ?`,
			value.ExperienceID,
			value.Version,
		).Scan(&existingPayload, &encoding); err != nil {
			return false, errors.New("read duplicate experience version")
		}
		existingPayload, err = s.cipher.open(
			"experience",
			prepared.recordID,
			"payload",
			encoding,
			existingPayload,
		)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(existingPayload, prepared.plaintext) {
			return false, ErrExperienceVersionConflict
		}
		return false, nil
	}
	for _, row := range prepared.evidenceRows {
		sessionKey, turnIndex, eventID, outcomeID, occurredAt :=
			evidenceIndexValues(row.ref)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO experience_evidence (
				evidence_set_id, evidence_index, experience_id,
				experience_version, source_kind, session_key, turn_index,
				event_id, outcome_id, occurred_at, payload,
				payload_encoding, inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			value.Evidence.EvidenceSetID,
			row.index,
			value.ExperienceID,
			value.Version,
			row.ref.Kind,
			sessionKey,
			turnIndex,
			eventID,
			outcomeID,
			occurredAt,
			row.payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		); err != nil {
			return false, fmt.Errorf("insert experience evidence: %w", err)
		}
	}
	return true, nil
}

func (s *Store) GetExperience(
	ctx context.Context,
	ref experience.ExperienceRef,
) (StoredExperience, error) {
	if err := ref.Validate(); err != nil {
		return StoredExperience{}, err
	}
	return s.scanStoredExperience(s.db.QueryRowContext(ctx, experienceSelectSQL+`
		WHERE value.experience_id = ? AND value.version = ?`,
		ref.ExperienceID,
		ref.Version,
	))
}

func (s *Store) QueryExperiences(
	ctx context.Context,
	projectIdentity string,
	limit int,
) ([]StoredExperience, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, experienceSelectSQL+`
		WHERE value.project_identity = ?
		ORDER BY value.updated_at DESC, value.experience_id, value.version DESC
		LIMIT ?`,
		projectIdentity,
		limit,
	)
	if err != nil {
		return nil, errors.New("query experiences")
	}
	defer rows.Close()
	result := make([]StoredExperience, 0)
	for rows.Next() {
		value, err := s.scanStoredExperience(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query experiences")
	}
	return result, nil
}

// QueryActiveExperiences applies the rebuilt lifecycle filter before LIMIT so
// inactive historical versions cannot starve active records from a bounded
// conflict-preflight read.
func (s *Store) QueryActiveExperiences(
	ctx context.Context,
	projectIdentity string,
	limit int,
) ([]StoredExperience, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, experienceSelectSQL+`
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
		ORDER BY value.experience_id, value.version
		LIMIT ?`,
		projectIdentity,
		limit,
	)
	if err != nil {
		return nil, errors.New("query active experiences")
	}
	defer rows.Close()
	result := make([]StoredExperience, 0)
	for rows.Next() {
		value, err := s.scanStoredExperience(rows)
		if err != nil {
			return nil, err
		}
		if value.CurrentLifecycle != experience.LifecycleActive {
			return nil, ErrExperienceLifecycleConflict
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query active experiences")
	}
	return result, nil
}

const experienceSelectSQL = `
	SELECT value.experience_id, value.version, value.origin_candidate_id,
		value.project_identity, value.experience_type,
		value.initial_lifecycle_state, value.lifecycle_state,
		COALESCE((
			SELECT transition.to_state
			FROM experience_transitions transition
			WHERE transition.experience_id = value.experience_id
				AND transition.experience_version = value.version
			ORDER BY transition.occurred_at DESC, transition.transition_id DESC
			LIMIT 1
		), value.initial_lifecycle_state),
		CASE
			WHEN value.lifecycle_state = 'active' THEN COALESCE((
				SELECT transition.occurred_at
				FROM experience_transitions transition
				WHERE transition.experience_id = value.experience_id
					AND transition.experience_version = value.version
				ORDER BY transition.occurred_at DESC, transition.transition_id DESC
				LIMIT 1
			), value.approved_at)
			ELSE NULL
		END,
		value.intervention_strength, value.content_hash,
		value.previous_experience_id, value.previous_version,
		value.approved_at, value.activated_at, value.expires_at,
		value.created_at, value.payload,
		value.payload_encoding
	FROM experiences value`

func (s *Store) scanStoredExperience(scanner rowScanner) (StoredExperience, error) {
	var experienceID, originCandidateID, projectIdentity, experienceType string
	var initialLifecycle, currentLifecycle, rebuiltLifecycle string
	var intervention, contentHash, approvedAtValue, createdAtValue, encoding string
	var version int
	var previousID, rebuiltActivatedAtValue, activatedAtValue, expiresAtValue sql.NullString
	var previousVersion sql.NullInt64
	var payload []byte
	if err := scanner.Scan(
		&experienceID,
		&version,
		&originCandidateID,
		&projectIdentity,
		&experienceType,
		&initialLifecycle,
		&currentLifecycle,
		&rebuiltLifecycle,
		&rebuiltActivatedAtValue,
		&intervention,
		&contentHash,
		&previousID,
		&previousVersion,
		&approvedAtValue,
		&activatedAtValue,
		&expiresAtValue,
		&createdAtValue,
		&payload,
		&encoding,
	); err != nil {
		return StoredExperience{}, err
	}
	if currentLifecycle != rebuiltLifecycle {
		return StoredExperience{}, ErrExperienceLifecycleConflict
	}
	if err := validateSealedPayloadSize(
		"experience",
		payload,
		maxExperiencePayloadBytes,
	); err != nil {
		return StoredExperience{}, err
	}
	recordID := experienceRecordID(experienceID, version)
	payload, err := s.cipher.open(
		"experience",
		recordID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return StoredExperience{}, err
	}
	var value experience.Experience
	if err := json.Unmarshal(payload, &value); err != nil {
		return StoredExperience{}, errors.New("decode experience payload")
	}
	if value.Governance.Approval == nil {
		return StoredExperience{}, errors.New("invalid persisted experience")
	}
	approvedAt, approvedErr := parseProjectionTime(approvedAtValue)
	createdAt, createdErr := parseProjectionTime(createdAtValue)
	activatedAt, activatedErr := parseNullableProjectionTime(activatedAtValue)
	rebuiltActivatedAt, rebuiltActivatedErr := parseNullableProjectionTime(rebuiltActivatedAtValue)
	expiresAt, expiresErr := parseNullableProjectionTime(expiresAtValue)
	if approvedErr != nil || createdErr != nil ||
		activatedErr != nil || rebuiltActivatedErr != nil || expiresErr != nil ||
		value.ExperienceID != experienceID ||
		value.Version != version ||
		value.OriginCandidateID != originCandidateID ||
		value.Scope.ProjectIdentity != projectIdentity ||
		string(value.Type) != experienceType ||
		string(value.Governance.LifecycleState) != initialLifecycle ||
		string(value.Guidance.InterventionStrength) != intervention ||
		value.ContentHash != contentHash ||
		!value.Governance.Approval.ApprovedAt.Equal(approvedAt) ||
		!value.CreatedAt.Equal(createdAt) ||
		!optionalTimeEqual(value.Applicability.ExpiresAt, expiresAt) ||
		!optionalTimeEqual(activatedAt, rebuiltActivatedAt) ||
		!samePreviousVersion(value.PreviousVersion, previousID, previousVersion) {
		return StoredExperience{}, errors.New("experience index does not match payload")
	}
	if err := value.Validate(); err != nil {
		return StoredExperience{}, errors.New("invalid persisted experience")
	}
	return StoredExperience{
		Experience:       value,
		CurrentLifecycle: experience.LifecycleState(currentLifecycle),
		ActivatedAt:      activatedAt,
		ExpiresAt:        expiresAt,
	}, nil
}

func (s *Store) GetExperienceEvidence(
	ctx context.Context,
	ref experience.ExperienceRef,
) (experience.EvidenceSet, error) {
	if err := ref.Validate(); err != nil {
		return experience.EvidenceSet{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT evidence_set_id, evidence_index, experience_id,
			experience_version, source_kind, session_key, turn_index,
			event_id, outcome_id, occurred_at, payload, payload_encoding
		FROM experience_evidence
		WHERE experience_id = ? AND experience_version = ?
		ORDER BY evidence_index`,
		ref.ExperienceID,
		ref.Version,
	)
	if err != nil {
		return experience.EvidenceSet{}, errors.New("query experience evidence")
	}
	defer rows.Close()
	result := experience.EvidenceSet{Availability: experience.EvidenceAvailable}
	index := 0
	for rows.Next() {
		var evidenceSetID, experienceID, sourceKind, encoding string
		var evidenceIndex, experienceVersion int
		var sessionKey, eventID, outcomeID, occurredAt sql.NullString
		var turnIndex sql.NullInt64
		var payload []byte
		if err := rows.Scan(
			&evidenceSetID,
			&evidenceIndex,
			&experienceID,
			&experienceVersion,
			&sourceKind,
			&sessionKey,
			&turnIndex,
			&eventID,
			&outcomeID,
			&occurredAt,
			&payload,
			&encoding,
		); err != nil {
			return experience.EvidenceSet{}, errors.New("read experience evidence")
		}
		if evidenceIndex != index {
			return experience.EvidenceSet{}, errors.New("experience evidence index is not contiguous")
		}
		if err := validateSealedPayloadSize(
			"experience evidence",
			payload,
			maxEvidencePayloadBytes,
		); err != nil {
			return experience.EvidenceSet{}, err
		}
		payload, err = s.cipher.open(
			"experience_evidence",
			evidenceRecordID(
				experienceID,
				experienceVersion,
				evidenceSetID,
				evidenceIndex,
			),
			"payload",
			encoding,
			payload,
		)
		if err != nil {
			return experience.EvidenceSet{}, err
		}
		var envelope storedEvidencePayload
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return experience.EvidenceSet{}, errors.New("decode experience evidence payload")
		}
		if envelope.EvidenceSetID != evidenceSetID ||
			envelope.EvidenceIndex != evidenceIndex ||
			envelope.Experience.ExperienceID != experienceID ||
			envelope.Experience.Version != experienceVersion ||
			envelope.Experience != ref ||
			string(envelope.Ref.Kind) != sourceKind ||
			!evidenceIndexesMatch(envelope.Ref, sessionKey, turnIndex, eventID, outcomeID, occurredAt) {
			return experience.EvidenceSet{}, errors.New("experience evidence index does not match payload")
		}
		if result.EvidenceSetID == "" {
			result.EvidenceSetID = evidenceSetID
		} else if result.EvidenceSetID != evidenceSetID {
			return experience.EvidenceSet{}, errors.New("experience evidence set is inconsistent")
		}
		result.Refs = append(result.Refs, envelope.Ref)
		index++
	}
	if err := rows.Err(); err != nil {
		return experience.EvidenceSet{}, errors.New("query experience evidence")
	}
	if len(result.Refs) == 0 {
		return experience.EvidenceSet{}, sql.ErrNoRows
	}
	if err := rows.Close(); err != nil {
		return experience.EvidenceSet{}, errors.New("close experience evidence query")
	}
	stored, err := s.GetExperience(ctx, ref)
	if err != nil {
		return experience.EvidenceSet{}, err
	}
	result.Availability = stored.Experience.Evidence.Availability
	if result.EvidenceSetID != stored.Experience.Evidence.EvidenceSetID ||
		len(result.Refs) != len(stored.Experience.Evidence.Refs) {
		return experience.EvidenceSet{}, errors.New("experience evidence does not match experience payload")
	}
	if err := result.Validate(); err != nil {
		return experience.EvidenceSet{}, errors.New("invalid persisted experience evidence")
	}
	return result, nil
}

func (s *Store) AppendExperienceTransition(
	ctx context.Context,
	transition experience.LifecycleTransition,
) (bool, error) {
	plaintext, payload, err := s.prepareExperienceTransition(transition)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin experience transition")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationExperienceRegistry, func() error {
		var appendErr error
		inserted, appendErr = s.appendExperienceTransitionTx(
			ctx,
			tx,
			transition,
			plaintext,
			payload,
		)
		return appendErr
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit experience transition")
	}
	return inserted, nil
}

func (s *Store) prepareExperienceTransition(
	transition experience.LifecycleTransition,
) ([]byte, []byte, error) {
	if err := transition.Validate(); err != nil {
		return nil, nil, err
	}
	return s.sealDomainJSON(
		"experience_transition",
		transition.TransitionID,
		"payload",
		transition,
		maxEvidencePayloadBytes,
	)
}

func (s *Store) appendExperienceTransitionTx(
	ctx context.Context,
	tx *sql.Tx,
	transition experience.LifecycleTransition,
	plaintext []byte,
	payload []byte,
) (bool, error) {
	var existingPayload []byte
	var existingEncoding string
	err := tx.QueryRowContext(ctx, `
		SELECT payload, payload_encoding
		FROM experience_transitions
		WHERE transition_id = ?`,
		transition.TransitionID,
	).Scan(&existingPayload, &existingEncoding)
	if err == nil {
		existingPayload, err = s.cipher.open(
			"experience_transition",
			transition.TransitionID,
			"payload",
			existingEncoding,
			existingPayload,
		)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(existingPayload, plaintext) {
			return false, ErrExperienceTransitionConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("inspect existing experience transition")
	}

	var currentState, rebuiltState string
	var latestOccurredAt sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT value.lifecycle_state,
			COALESCE((
				SELECT prior.to_state
				FROM experience_transitions prior
				WHERE prior.experience_id = value.experience_id
					AND prior.experience_version = value.version
				ORDER BY prior.occurred_at DESC, prior.transition_id DESC
				LIMIT 1
			), value.initial_lifecycle_state),
			(
				SELECT MAX(prior.occurred_at)
				FROM experience_transitions prior
				WHERE prior.experience_id = value.experience_id
					AND prior.experience_version = value.version
			)
		FROM experiences value
		WHERE value.experience_id = ? AND value.version = ?`,
		transition.Experience.ExperienceID,
		transition.Experience.Version,
	).Scan(&currentState, &rebuiltState, &latestOccurredAt); err != nil {
		return false, errors.New("read experience lifecycle projection")
	}
	if currentState != rebuiltState {
		return false, ErrExperienceLifecycleConflict
	}
	if currentState != string(transition.FromState) {
		return false, ErrExperienceLifecycleConflict
	}
	if latestOccurredAt.Valid {
		latest, err := parseProjectionTime(latestOccurredAt.String)
		if err != nil || !transition.OccurredAt.After(latest) {
			return false, errors.New(
				"experience transition time must follow prior transition",
			)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO experience_transitions (
			transition_id, experience_id, experience_version, from_state,
			to_state, reason_code, actor_kind, occurred_at, payload,
			payload_encoding, inserted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		transition.TransitionID,
		transition.Experience.ExperienceID,
		transition.Experience.Version,
		transition.FromState,
		transition.ToState,
		transition.ReasonCode,
		transition.ActorKind,
		formatProjectionTime(transition.OccurredAt),
		payload,
		payloadEncodingAESGCM,
		formatProjectionTime(s.nowUTC()),
	); err != nil {
		return false, fmt.Errorf("insert experience transition: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE experiences
		SET lifecycle_state = ?, activated_at = ?, updated_at = ?
		WHERE experience_id = ? AND version = ?
			AND lifecycle_state = ?`,
		transition.ToState,
		activationTimeForTransition(transition),
		formatProjectionTime(s.nowUTC()),
		transition.Experience.ExperienceID,
		transition.Experience.Version,
		transition.FromState,
	)
	if err != nil {
		return false, errors.New("update experience lifecycle projection")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, ErrExperienceLifecycleConflict
	}
	return true, nil
}

func (s *Store) ListExperienceTransitions(
	ctx context.Context,
	ref experience.ExperienceRef,
	limit int,
) ([]experience.LifecycleTransition, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT transition_id, experience_id, experience_version, from_state,
			to_state, reason_code, actor_kind, occurred_at, payload,
			payload_encoding
		FROM experience_transitions
		WHERE experience_id = ? AND experience_version = ?
		ORDER BY occurred_at, transition_id
		LIMIT ?`,
		ref.ExperienceID,
		ref.Version,
		limit,
	)
	if err != nil {
		return nil, errors.New("query experience transitions")
	}
	defer rows.Close()
	result := make([]experience.LifecycleTransition, 0)
	for rows.Next() {
		var transitionID, experienceID, fromState, toState string
		var reasonCode, actorKind, occurredAtValue, encoding string
		var version int
		var payload []byte
		if err := rows.Scan(
			&transitionID,
			&experienceID,
			&version,
			&fromState,
			&toState,
			&reasonCode,
			&actorKind,
			&occurredAtValue,
			&payload,
			&encoding,
		); err != nil {
			return nil, errors.New("read experience transition")
		}
		if err := validateSealedPayloadSize(
			"experience transition",
			payload,
			maxEvidencePayloadBytes,
		); err != nil {
			return nil, err
		}
		payload, err = s.cipher.open(
			"experience_transition",
			transitionID,
			"payload",
			encoding,
			payload,
		)
		if err != nil {
			return nil, err
		}
		var transition experience.LifecycleTransition
		if err := json.Unmarshal(payload, &transition); err != nil {
			return nil, errors.New("decode experience transition payload")
		}
		occurredAt, parseErr := parseProjectionTime(occurredAtValue)
		if parseErr != nil ||
			transition.TransitionID != transitionID ||
			transition.Experience.ExperienceID != experienceID ||
			transition.Experience.Version != version ||
			string(transition.FromState) != fromState ||
			string(transition.ToState) != toState ||
			transition.ReasonCode != reasonCode ||
			string(transition.ActorKind) != actorKind ||
			!transition.OccurredAt.Equal(occurredAt) {
			return nil, errors.New("experience transition index does not match payload")
		}
		if err := transition.Validate(); err != nil {
			return nil, errors.New("invalid persisted experience transition")
		}
		result = append(result, transition)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query experience transitions")
	}
	return result, nil
}

func (s *Store) sealDomainJSON(
	recordType string,
	recordID string,
	column string,
	value any,
	limit int,
) ([]byte, []byte, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("encode %s payload", recordType)
	}
	if len(plaintext) > limit {
		return nil, nil, fmt.Errorf("%s payload exceeds safety limit", recordType)
	}
	payload, err := s.cipher.seal(recordType, recordID, column, plaintext)
	if err != nil {
		return nil, nil, err
	}
	return plaintext, payload, nil
}

const sealedPayloadOverheadAllowance = 1024

func validateSealedPayloadSize(recordType string, payload []byte, plaintextLimit int) error {
	if plaintextLimit < 1 || len(payload) > plaintextLimit+sealedPayloadOverheadAllowance {
		return fmt.Errorf("%s payload exceeds safety limit", recordType)
	}
	return nil
}

func boundedRepositoryLimit(limit int) (int, error) {
	if limit <= 0 {
		return defaultExperienceQueryLimit, nil
	}
	if limit > maxExperienceQueryLimit {
		return 0, errors.New("repository query limit exceeds safety limit")
	}
	return limit, nil
}

func validateProjectIdentity(value string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len(value) > 4096 {
		return errors.New("invalid project identity")
	}
	return nil
}

func validateStorageIdentifier(name, value string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len(value) > 512 {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func experienceRecordID(experienceID string, version int) string {
	return fmt.Sprintf("%s:%d", experienceID, version)
}

func evidenceRecordID(
	experienceID string,
	version int,
	evidenceSetID string,
	index int,
) string {
	return fmt.Sprintf("%s:%d:%s:%d", experienceID, version, evidenceSetID, index)
}

func nullableExperienceID(value *experience.ExperienceRef) any {
	if value == nil {
		return nil
	}
	return value.ExperienceID
}

func nullableExperienceVersion(value *experience.ExperienceRef) any {
	if value == nil {
		return nil
	}
	return value.Version
}

func samePreviousVersion(
	value *experience.ExperienceRef,
	indexedID sql.NullString,
	indexedVersion sql.NullInt64,
) bool {
	if value == nil {
		return !indexedID.Valid && !indexedVersion.Valid
	}
	return indexedID.Valid &&
		indexedVersion.Valid &&
		value.ExperienceID == indexedID.String &&
		value.Version == int(indexedVersion.Int64)
}

func parseNullableProjectionTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseProjectionTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func optionalTimeEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func activationTimeForTransition(transition experience.LifecycleTransition) any {
	if transition.ToState == experience.LifecycleActive {
		return formatProjectionTime(transition.OccurredAt)
	}
	return nil
}

func evidenceIndexValues(ref experience.EvidenceRef) (any, any, any, any, any) {
	var sessionKey, turnIndex, eventID, outcomeID, occurredAt any
	if ref.SessionKey != "" {
		sessionKey = ref.SessionKey
	}
	if ref.TurnIndex != nil {
		turnIndex = *ref.TurnIndex
	}
	if ref.EventID != "" {
		eventID = ref.EventID
	}
	if ref.OutcomeID != "" {
		outcomeID = ref.OutcomeID
	}
	if ref.OccurredAt != nil {
		occurredAt = formatProjectionTime(*ref.OccurredAt)
	}
	return sessionKey, turnIndex, eventID, outcomeID, occurredAt
}

func evidenceIndexesMatch(
	ref experience.EvidenceRef,
	sessionKey sql.NullString,
	turnIndex sql.NullInt64,
	eventID sql.NullString,
	outcomeID sql.NullString,
	occurredAt sql.NullString,
) bool {
	if (ref.SessionKey == "") != !sessionKey.Valid ||
		sessionKey.Valid && ref.SessionKey != sessionKey.String ||
		(ref.TurnIndex == nil) != !turnIndex.Valid ||
		turnIndex.Valid && *ref.TurnIndex != turnIndex.Int64 ||
		(ref.EventID == "") != !eventID.Valid ||
		eventID.Valid && ref.EventID != eventID.String ||
		(ref.OutcomeID == "") != !outcomeID.Valid ||
		outcomeID.Valid && ref.OutcomeID != outcomeID.String ||
		(ref.OccurredAt == nil) != !occurredAt.Valid {
		return false
	}
	if occurredAt.Valid {
		indexed, err := parseProjectionTime(occurredAt.String)
		return err == nil && ref.OccurredAt.Equal(indexed)
	}
	return true
}
