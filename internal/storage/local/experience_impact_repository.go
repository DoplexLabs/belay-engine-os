package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/impact"
)

const maxExperienceImpactPayloadBytes = 2 << 20

var ErrExperienceImpactConflict = errors.New(
	"experience impact observation identity conflicts with persisted record",
)

type ExperienceImpactObservation struct {
	Observation impact.Observation `json:"observation"`
	ObservedAt  time.Time          `json:"observed_at"`
}

func (s *Store) InsertExperienceImpactObservation(
	ctx context.Context,
	observation impact.Observation,
) (bool, error) {
	if err := observation.Validate(); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"experience_impact_observation",
		observation.ObservationID,
		"payload",
		observation,
		maxExperienceImpactPayloadBytes,
	)
	if err != nil {
		return false, err
	}
	observedAt := s.nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin experience impact persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationExperienceImpact, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO experience_impact_observations (
				observation_id, application_id, experience_id,
				experience_version, project_identity, session_key,
				observed_at, window_start_turn, window_end_turn,
				derivation_version, input_hash, payload, payload_encoding,
				inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(observation_id) DO NOTHING`,
			observation.ObservationID,
			observation.ApplicationID,
			observation.Experience.ExperienceID,
			observation.Experience.Version,
			observation.ProjectIdentity,
			observation.SessionKey,
			formatProjectionTime(observedAt),
			observation.Evidence.StartTurn,
			observation.Evidence.EndTurn,
			observation.DerivationVersion,
			observation.InputHash,
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(observedAt),
		)
		if err != nil {
			return fmt.Errorf("insert experience impact observation: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect experience impact persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}
		var existingPayload []byte
		var encoding string
		if err := tx.QueryRowContext(ctx, `
			SELECT payload, payload_encoding
			FROM experience_impact_observations
			WHERE observation_id = ?`,
			observation.ObservationID,
		).Scan(&existingPayload, &encoding); err != nil {
			return errors.New("read duplicate experience impact observation")
		}
		existingPayload, err = s.cipher.open(
			"experience_impact_observation",
			observation.ObservationID,
			"payload",
			encoding,
			existingPayload,
		)
		if err != nil {
			return err
		}
		if !bytes.Equal(existingPayload, plaintext) {
			return ErrExperienceImpactConflict
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit experience impact persistence")
	}
	return inserted, nil
}

func (s *Store) GetExperienceImpactObservation(
	ctx context.Context,
	observationID string,
) (ExperienceImpactObservation, error) {
	if err := validateStorageIdentifier(
		"experience impact observation ID",
		observationID,
	); err != nil {
		return ExperienceImpactObservation{}, err
	}
	return s.scanExperienceImpactObservation(s.db.QueryRowContext(
		ctx,
		experienceImpactSelectSQL+` WHERE observation_id = ?`,
		observationID,
	))
}

func (s *Store) QueryExperienceImpactObservations(
	ctx context.Context,
	applicationID string,
	limit int,
) ([]ExperienceImpactObservation, error) {
	if err := validateStorageIdentifier(
		"experience application ID",
		applicationID,
	); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, experienceImpactSelectSQL+`
		WHERE application_id = ?
		ORDER BY observed_at DESC, observation_id
		LIMIT ?`,
		applicationID,
		limit,
	)
	if err != nil {
		return nil, errors.New("query experience impact observations")
	}
	defer rows.Close()
	result := make([]ExperienceImpactObservation, 0)
	for rows.Next() {
		observation, err := s.scanExperienceImpactObservation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query experience impact observations")
	}
	return result, nil
}

// QueryExperienceApplicationsNeedingImpact returns evaluated, delivered
// applications whose bound session is no longer live and whose relevant
// transcript projection is newer than the latest observation for this
// derivation version.
func (s *Store) QueryExperienceApplicationsNeedingImpact(
	ctx context.Context,
	derivationVersion string,
	limit int,
) ([]experience.Application, error) {
	if strings.TrimSpace(derivationVersion) == "" ||
		derivationVersion != strings.TrimSpace(derivationVersion) ||
		len(derivationVersion) > 256 {
		return nil, errors.New("invalid impact derivation version")
	}
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
		FROM transcript_sessions AS current_session
		JOIN experience_applications AS application
			ON application.session_key = current_session.session_key
			AND application.project_identity = current_session.project_identity
		WHERE application.delivery_state = ?
			AND application.evaluated_at IS NOT NULL
			AND application.session_key IS NOT NULL
			AND application.session_key != ''
			AND current_session.coverage != 'live'
			AND current_session.ended_at IS NOT NULL
			AND NOT EXISTS (
				SELECT 1
				FROM experience_impact_observations AS observation
				WHERE observation.application_id = application.application_id
					AND observation.derivation_version = ?
					AND observation.observed_at >= application.evaluated_at
					AND observation.observed_at >= current_session.updated_at
					AND NOT EXISTS (
						SELECT 1
						FROM transcript_sessions AS prior_session
						WHERE prior_session.project_identity =
								current_session.project_identity
							AND prior_session.agent = current_session.agent
							AND prior_session.coverage = 'complete'
							AND prior_session.ended_at IS NOT NULL
							AND prior_session.ended_at <
								current_session.started_at
							AND prior_session.session_key IN (
								SELECT bounded_prior.session_key
								FROM transcript_sessions AS bounded_prior
								WHERE bounded_prior.project_identity =
										current_session.project_identity
									AND bounded_prior.agent =
										current_session.agent
									AND bounded_prior.coverage = 'complete'
									AND bounded_prior.ended_at IS NOT NULL
									AND bounded_prior.ended_at <
										current_session.started_at
								ORDER BY
									bounded_prior.ended_at DESC,
									bounded_prior.session_key
								LIMIT 5
							)
							AND prior_session.updated_at >
								observation.observed_at
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
		derivationVersion,
		limit,
	)
	if err != nil {
		return nil, errors.New(
			"query experience applications needing impact observation",
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
			"query experience applications needing impact observation",
		)
	}
	return result, nil
}

const experienceImpactSelectSQL = `
	SELECT observation_id, application_id, experience_id, experience_version,
		project_identity, session_key, observed_at, window_start_turn,
		window_end_turn, derivation_version, input_hash, payload,
		payload_encoding
	FROM experience_impact_observations`

func (s *Store) scanExperienceImpactObservation(
	scanner rowScanner,
) (ExperienceImpactObservation, error) {
	var observationID, applicationID, experienceID, projectIdentity string
	var sessionKey, observedAtValue, derivationVersion, inputHash, encoding string
	var experienceVersion int
	var windowStart, windowEnd int64
	var payload []byte
	if err := scanner.Scan(
		&observationID,
		&applicationID,
		&experienceID,
		&experienceVersion,
		&projectIdentity,
		&sessionKey,
		&observedAtValue,
		&windowStart,
		&windowEnd,
		&derivationVersion,
		&inputHash,
		&payload,
		&encoding,
	); err != nil {
		return ExperienceImpactObservation{}, err
	}
	if err := validateSealedPayloadSize(
		"experience impact observation",
		payload,
		maxExperienceImpactPayloadBytes,
	); err != nil {
		return ExperienceImpactObservation{}, err
	}
	payload, err := s.cipher.open(
		"experience_impact_observation",
		observationID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return ExperienceImpactObservation{}, err
	}
	var observation impact.Observation
	if err := json.Unmarshal(payload, &observation); err != nil {
		return ExperienceImpactObservation{}, errors.New(
			"decode experience impact observation payload",
		)
	}
	observedAt, err := parseProjectionTime(observedAtValue)
	if err != nil ||
		observation.ObservationID != observationID ||
		observation.ApplicationID != applicationID ||
		observation.Experience.ExperienceID != experienceID ||
		observation.Experience.Version != experienceVersion ||
		observation.ProjectIdentity != projectIdentity ||
		observation.SessionKey != sessionKey ||
		observation.Evidence.StartTurn != windowStart ||
		observation.Evidence.EndTurn != windowEnd ||
		observation.DerivationVersion != derivationVersion ||
		observation.InputHash != inputHash {
		return ExperienceImpactObservation{}, errors.New(
			"experience impact index does not match payload",
		)
	}
	if err := observation.Validate(); err != nil {
		return ExperienceImpactObservation{}, errors.New(
			"invalid persisted experience impact observation",
		)
	}
	return ExperienceImpactObservation{
		Observation: observation,
		ObservedAt:  observedAt,
	}, nil
}
