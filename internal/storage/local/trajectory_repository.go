package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

const maxTrajectoryPayloadBytes = 1 << 20

var (
	ErrTrajectoryEdgeConflict = errors.New("trajectory edge identity conflicts with persisted record")
	ErrOutcomeConflict        = errors.New("outcome identity conflicts with persisted record")
)

type TrajectoryEdgeQuery struct {
	ProjectIdentity string
	SessionKey      string
	Limit           int
}

type OutcomeQuery struct {
	ProjectIdentity string
	SessionKey      string
	Limit           int
}

func (s *Store) InsertTrajectoryEdge(
	ctx context.Context,
	edge trajectory.Edge,
) (bool, error) {
	if err := edge.Validate(); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"trajectory_edge",
		edge.EdgeID,
		"payload",
		edge,
		maxTrajectoryPayloadBytes,
	)
	if err != nil {
		return false, err
	}
	fromKey, err := trajectoryNodeKey(edge.From)
	if err != nil {
		return false, err
	}
	toKey, err := trajectoryNodeKey(edge.To)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin trajectory edge persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationTrajectory, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO trajectory_edges (
				edge_id, project_identity, session_key, from_kind,
				from_ref_key, relation, to_kind, to_ref_key, evidence_class,
				confidence, derivation_version, occurred_at, payload,
				payload_encoding, inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(edge_id) DO NOTHING`,
			edge.EdgeID,
			edge.ProjectIdentity,
			edge.SessionKey,
			edge.From.Kind,
			fromKey,
			edge.Relation,
			edge.To.Kind,
			toKey,
			edge.EvidenceClass,
			edge.Confidence,
			edge.DerivationVersion,
			formatProjectionTime(edge.OccurredAt),
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert trajectory edge: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect trajectory edge persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}
		return s.compareExistingPayloadTx(
			ctx,
			tx,
			"trajectory_edges",
			"edge_id",
			edge.EdgeID,
			"trajectory_edge",
			edge.EdgeID,
			plaintext,
			ErrTrajectoryEdgeConflict,
		)
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit trajectory edge persistence")
	}
	return inserted, nil
}

func (s *Store) GetTrajectoryEdge(
	ctx context.Context,
	edgeID string,
) (trajectory.Edge, error) {
	if err := validateStorageIdentifier("trajectory edge ID", edgeID); err != nil {
		return trajectory.Edge{}, err
	}
	return s.scanTrajectoryEdge(s.db.QueryRowContext(ctx, trajectoryEdgeSelectSQL+`
		WHERE edge_id = ?`,
		edgeID,
	))
}

func (s *Store) QueryTrajectoryEdges(
	ctx context.Context,
	query TrajectoryEdgeQuery,
) ([]trajectory.Edge, error) {
	limit, err := boundedRepositoryLimit(query.Limit)
	if err != nil {
		return nil, err
	}
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if query.ProjectIdentity != "" {
		if err := validateProjectIdentity(query.ProjectIdentity); err != nil {
			return nil, err
		}
		clauses = append(clauses, "project_identity = ?")
		args = append(args, query.ProjectIdentity)
	}
	if query.SessionKey != "" {
		if err := validateStorageIdentifier("trajectory session key", query.SessionKey); err != nil {
			return nil, err
		}
		clauses = append(clauses, "session_key = ?")
		args = append(args, query.SessionKey)
	}
	if len(clauses) == 1 {
		return nil, errors.New("trajectory edge query requires project or session")
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, trajectoryEdgeSelectSQL+`
		WHERE `+joinSQLClauses(clauses)+`
		ORDER BY occurred_at, edge_id
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query trajectory edges")
	}
	defer rows.Close()
	result := make([]trajectory.Edge, 0)
	for rows.Next() {
		edge, err := s.scanTrajectoryEdge(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query trajectory edges")
	}
	return result, nil
}

const trajectoryEdgeSelectSQL = `
	SELECT edge_id, project_identity, session_key, from_kind, from_ref_key,
		relation, to_kind, to_ref_key, evidence_class, confidence,
		derivation_version, occurred_at, payload, payload_encoding
	FROM trajectory_edges`

func (s *Store) scanTrajectoryEdge(scanner rowScanner) (trajectory.Edge, error) {
	var edgeID, projectIdentity, sessionKey, fromKind, fromKey string
	var relation, toKind, toKey, evidenceClass, confidence string
	var derivationVersion, occurredAtValue, encoding string
	var payload []byte
	if err := scanner.Scan(
		&edgeID,
		&projectIdentity,
		&sessionKey,
		&fromKind,
		&fromKey,
		&relation,
		&toKind,
		&toKey,
		&evidenceClass,
		&confidence,
		&derivationVersion,
		&occurredAtValue,
		&payload,
		&encoding,
	); err != nil {
		return trajectory.Edge{}, err
	}
	if err := validateSealedPayloadSize(
		"trajectory edge",
		payload,
		maxTrajectoryPayloadBytes,
	); err != nil {
		return trajectory.Edge{}, err
	}
	payload, err := s.cipher.open(
		"trajectory_edge",
		edgeID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return trajectory.Edge{}, err
	}
	var edge trajectory.Edge
	if err := json.Unmarshal(payload, &edge); err != nil {
		return trajectory.Edge{}, errors.New("decode trajectory edge payload")
	}
	indexedFrom, fromErr := trajectoryNodeKey(edge.From)
	indexedTo, toErr := trajectoryNodeKey(edge.To)
	occurredAt, timeErr := parseProjectionTime(occurredAtValue)
	if fromErr != nil || toErr != nil || timeErr != nil ||
		edge.EdgeID != edgeID ||
		edge.ProjectIdentity != projectIdentity ||
		edge.SessionKey != sessionKey ||
		string(edge.From.Kind) != fromKind ||
		indexedFrom != fromKey ||
		string(edge.Relation) != relation ||
		string(edge.To.Kind) != toKind ||
		indexedTo != toKey ||
		string(edge.EvidenceClass) != evidenceClass ||
		string(edge.Confidence) != confidence ||
		edge.DerivationVersion != derivationVersion ||
		!edge.OccurredAt.Equal(occurredAt) {
		return trajectory.Edge{}, errors.New("trajectory edge index does not match payload")
	}
	if err := edge.Validate(); err != nil {
		return trajectory.Edge{}, errors.New("invalid persisted trajectory edge")
	}
	return edge, nil
}

func (s *Store) InsertOutcome(
	ctx context.Context,
	outcome trajectory.Outcome,
) (bool, error) {
	if err := outcome.Validate(); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"outcome_observation",
		outcome.OutcomeID,
		"payload",
		outcome,
		maxTrajectoryPayloadBytes,
	)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin outcome persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationOutcome, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO outcome_observations (
				outcome_id, project_identity, session_key, kind, result,
				evidence_class, confidence, derivation_version, occurred_at,
				payload, payload_encoding, inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(outcome_id) DO NOTHING`,
			outcome.OutcomeID,
			outcome.ProjectIdentity,
			outcome.SessionKey,
			outcome.Kind,
			outcome.Result,
			outcome.EvidenceClass,
			outcome.Confidence,
			outcome.DerivationVersion,
			formatProjectionTime(outcome.OccurredAt),
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert outcome observation: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect outcome persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}
		return s.compareExistingPayloadTx(
			ctx,
			tx,
			"outcome_observations",
			"outcome_id",
			outcome.OutcomeID,
			"outcome_observation",
			outcome.OutcomeID,
			plaintext,
			ErrOutcomeConflict,
		)
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit outcome persistence")
	}
	return inserted, nil
}

func (s *Store) GetOutcome(
	ctx context.Context,
	outcomeID string,
) (trajectory.Outcome, error) {
	if err := validateStorageIdentifier("outcome ID", outcomeID); err != nil {
		return trajectory.Outcome{}, err
	}
	return s.scanOutcome(s.db.QueryRowContext(ctx, outcomeSelectSQL+`
		WHERE outcome_id = ?`,
		outcomeID,
	))
}

func (s *Store) QueryOutcomes(
	ctx context.Context,
	query OutcomeQuery,
) ([]trajectory.Outcome, error) {
	limit, err := boundedRepositoryLimit(query.Limit)
	if err != nil {
		return nil, err
	}
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if query.ProjectIdentity != "" {
		if err := validateProjectIdentity(query.ProjectIdentity); err != nil {
			return nil, err
		}
		clauses = append(clauses, "project_identity = ?")
		args = append(args, query.ProjectIdentity)
	}
	if query.SessionKey != "" {
		if err := validateStorageIdentifier("outcome session key", query.SessionKey); err != nil {
			return nil, err
		}
		clauses = append(clauses, "session_key = ?")
		args = append(args, query.SessionKey)
	}
	if len(clauses) == 1 {
		return nil, errors.New("outcome query requires project or session")
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, outcomeSelectSQL+`
		WHERE `+joinSQLClauses(clauses)+`
		ORDER BY occurred_at, outcome_id
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query outcomes")
	}
	defer rows.Close()
	result := make([]trajectory.Outcome, 0)
	for rows.Next() {
		outcome, err := s.scanOutcome(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, outcome)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query outcomes")
	}
	return result, nil
}

const outcomeSelectSQL = `
	SELECT outcome_id, project_identity, session_key, kind, result,
		evidence_class, confidence, derivation_version, occurred_at,
		payload, payload_encoding
	FROM outcome_observations`

func (s *Store) scanOutcome(scanner rowScanner) (trajectory.Outcome, error) {
	var outcomeID, projectIdentity, sessionKey, kind, result string
	var evidenceClass, confidence, derivationVersion, occurredAtValue string
	var encoding string
	var payload []byte
	if err := scanner.Scan(
		&outcomeID,
		&projectIdentity,
		&sessionKey,
		&kind,
		&result,
		&evidenceClass,
		&confidence,
		&derivationVersion,
		&occurredAtValue,
		&payload,
		&encoding,
	); err != nil {
		return trajectory.Outcome{}, err
	}
	if err := validateSealedPayloadSize(
		"outcome observation",
		payload,
		maxTrajectoryPayloadBytes,
	); err != nil {
		return trajectory.Outcome{}, err
	}
	payload, err := s.cipher.open(
		"outcome_observation",
		outcomeID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return trajectory.Outcome{}, err
	}
	var outcome trajectory.Outcome
	if err := json.Unmarshal(payload, &outcome); err != nil {
		return trajectory.Outcome{}, errors.New("decode outcome payload")
	}
	occurredAt, timeErr := parseProjectionTime(occurredAtValue)
	if timeErr != nil ||
		outcome.OutcomeID != outcomeID ||
		outcome.ProjectIdentity != projectIdentity ||
		outcome.SessionKey != sessionKey ||
		string(outcome.Kind) != kind ||
		string(outcome.Result) != result ||
		string(outcome.EvidenceClass) != evidenceClass ||
		string(outcome.Confidence) != confidence ||
		outcome.DerivationVersion != derivationVersion ||
		!outcome.OccurredAt.Equal(occurredAt) {
		return trajectory.Outcome{}, errors.New("outcome index does not match payload")
	}
	if err := outcome.Validate(); err != nil {
		return trajectory.Outcome{}, errors.New("invalid persisted outcome")
	}
	return outcome, nil
}

func (s *Store) compareExistingPayloadTx(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	idColumn string,
	id string,
	recordType string,
	recordID string,
	want []byte,
	conflict error,
) error {
	type comparisonTarget struct {
		idColumn string
		maxBytes int
	}
	allowed := map[string]comparisonTarget{
		"trajectory_edges": {
			idColumn: "edge_id",
			maxBytes: maxTrajectoryPayloadBytes,
		},
		"outcome_observations": {
			idColumn: "outcome_id",
			maxBytes: maxTrajectoryPayloadBytes,
		},
		"evidence_episodes": {
			idColumn: "episode_id",
			maxBytes: maxEvidenceEpisodePayloadBytes,
		},
	}
	target, ok := allowed[table]
	if !ok || target.idColumn != idColumn {
		return errors.New("unsupported domain payload comparison")
	}
	var payload []byte
	var encoding string
	query := "SELECT payload, payload_encoding FROM " + table + " WHERE " + idColumn + " = ?"
	if err := tx.QueryRowContext(ctx, query, id).Scan(&payload, &encoding); err != nil {
		return errors.New("read duplicate domain payload")
	}
	if err := validateSealedPayloadSize(recordType, payload, target.maxBytes); err != nil {
		return err
	}
	payload, err := s.cipher.open(recordType, recordID, "payload", encoding, payload)
	if err != nil {
		return err
	}
	if !bytes.Equal(payload, want) {
		return conflict
	}
	return nil
}

func trajectoryNodeKey(ref trajectory.NodeRef) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	switch ref.Kind {
	case trajectory.NodeCanonicalEvent:
		return ref.EventID, nil
	case trajectory.NodeTranscriptTurn:
		return ref.SessionKey + ":" + strconv.FormatInt(*ref.TurnIndex, 10), nil
	case trajectory.NodeOutcome:
		return ref.OutcomeID, nil
	case trajectory.NodeExperience:
		return ref.ExperienceID + ":" + strconv.Itoa(ref.ExperienceVersion), nil
	case trajectory.NodeApplication:
		return ref.ApplicationID, nil
	case trajectory.NodeGitObject:
		return ref.GitObject, nil
	default:
		return "", errors.New("unsupported trajectory node")
	}
}

func joinSQLClauses(values []string) string {
	result := values[0]
	for _, value := range values[1:] {
		result += " AND " + value
	}
	return result
}
