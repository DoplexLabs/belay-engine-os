package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
)

const maxEvidenceEpisodePayloadBytes = 1 << 20

var ErrEvidenceEpisodeConflict = errors.New(
	"evidence episode identity conflicts with persisted record",
)

type EvidenceEpisodeQuery struct {
	ProjectIdentity string
	SessionKey      string
	Kind            string
	Limit           int
}

func (s *Store) ReadEvidenceEpisodeAudit(
	ctx context.Context,
) (evidenceepisode.Audit, error) {
	result := evidenceepisode.Audit{ByKind: make(map[string]int)}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, complete, COUNT(*)
		FROM evidence_episodes
		GROUP BY kind, complete
		ORDER BY kind, complete`)
	if err != nil {
		return result, errors.New("query evidence episode audit")
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var complete, count int
		if err := rows.Scan(&kind, &complete, &count); err != nil {
			return result, errors.New("read evidence episode audit")
		}
		result.Total += count
		result.ByKind[kind] += count
		if complete == 1 {
			result.Complete += count
		} else {
			result.Incomplete += count
		}
	}
	if err := rows.Err(); err != nil {
		return result, errors.New("query evidence episode audit")
	}
	return result, nil
}

func (s *Store) InsertEvidenceEpisode(
	ctx context.Context,
	value evidenceepisode.Episode,
) (bool, error) {
	if err := value.Validate(); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"evidence_episode",
		value.EpisodeID,
		"payload",
		value,
		maxEvidenceEpisodePayloadBytes,
	)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin evidence episode persistence")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationEvidenceEpisode, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO evidence_episodes (
				episode_id, project_identity, session_key, kind, started_at,
				ended_at, confidence, complete, derivation_version, input_hash,
				payload, payload_encoding, inserted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(episode_id) DO NOTHING`,
			value.EpisodeID,
			value.ProjectIdentity,
			value.SessionKey,
			value.Kind,
			formatProjectionTime(value.StartedAt),
			formatProjectionTime(value.EndedAt),
			value.Confidence,
			boolInt(evidenceEpisodeComplete(value)),
			value.DerivationVersion,
			value.InputHash,
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert evidence episode: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect evidence episode persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}
		return s.compareExistingPayloadTx(
			ctx,
			tx,
			"evidence_episodes",
			"episode_id",
			value.EpisodeID,
			"evidence_episode",
			value.EpisodeID,
			plaintext,
			ErrEvidenceEpisodeConflict,
		)
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit evidence episode persistence")
	}
	return inserted, nil
}

func (s *Store) QueryEvidenceEpisodes(
	ctx context.Context,
	query EvidenceEpisodeQuery,
) ([]evidenceepisode.Episode, error) {
	limit, err := boundedRepositoryLimit(query.Limit)
	if err != nil {
		return nil, err
	}
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 4)
	if query.ProjectIdentity != "" {
		if err := validateProjectIdentity(query.ProjectIdentity); err != nil {
			return nil, err
		}
		clauses = append(clauses, "project_identity = ?")
		args = append(args, query.ProjectIdentity)
	}
	if query.SessionKey != "" {
		if err := validateStorageIdentifier(
			"evidence episode session key",
			query.SessionKey,
		); err != nil {
			return nil, err
		}
		clauses = append(clauses, "session_key = ?")
		args = append(args, query.SessionKey)
	}
	if query.Kind != "" {
		if err := validateStorageIdentifier(
			"evidence episode kind",
			query.Kind,
		); err != nil {
			return nil, err
		}
		clauses = append(clauses, "kind = ?")
		args = append(args, query.Kind)
	}
	if len(clauses) == 1 {
		return nil, errors.New(
			"evidence episode query requires project, session, or kind",
		)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT episode_id, project_identity, session_key, kind, started_at,
			ended_at, confidence, complete, derivation_version, input_hash,
			payload, payload_encoding
		FROM evidence_episodes
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY started_at, episode_id
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query evidence episodes")
	}
	defer rows.Close()
	var result []evidenceepisode.Episode
	for rows.Next() {
		value, err := s.scanEvidenceEpisode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query evidence episodes")
	}
	return result, nil
}

func (s *Store) QuerySessionEvidenceEpisodes(
	ctx context.Context,
	sessionKey string,
	limit int,
) ([]evidenceepisode.Episode, error) {
	return s.QueryEvidenceEpisodes(ctx, EvidenceEpisodeQuery{
		SessionKey: sessionKey,
		Limit:      limit,
	})
}

func (s *Store) QueryEvidenceEpisodesForSessions(
	ctx context.Context,
	sessionKeys []string,
	limit int,
) (map[string][]evidenceepisode.Episode, error) {
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(sessionKeys))
	placeholders := make([]string, 0, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)+1)
	for _, sessionKey := range sessionKeys {
		sessionKey = strings.TrimSpace(sessionKey)
		if sessionKey == "" || seen[sessionKey] {
			continue
		}
		if err := validateStorageIdentifier(
			"evidence episode session key",
			sessionKey,
		); err != nil {
			return nil, err
		}
		seen[sessionKey] = true
		placeholders = append(placeholders, "?")
		args = append(args, sessionKey)
	}
	result := make(map[string][]evidenceepisode.Episode, len(placeholders))
	if len(placeholders) == 0 {
		return result, nil
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT episode_id, project_identity, session_key, kind, started_at,
			ended_at, confidence, complete, derivation_version, input_hash,
			payload, payload_encoding
		FROM evidence_episodes
		WHERE session_key IN (`+strings.Join(placeholders, ", ")+`)
		ORDER BY ended_at DESC, episode_id
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query evidence episodes for sessions")
	}
	defer rows.Close()
	for rows.Next() {
		value, err := s.scanEvidenceEpisode(rows)
		if err != nil {
			return nil, err
		}
		result[value.SessionKey] = append(result[value.SessionKey], value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query evidence episodes for sessions")
	}
	return result, nil
}

func (s *Store) scanEvidenceEpisode(
	scanner rowScanner,
) (evidenceepisode.Episode, error) {
	var episodeID, projectIdentity, sessionKey, kind string
	var startedAtValue, endedAtValue, confidence string
	var complete int
	var derivationVersion, inputHash, encoding string
	var payload []byte
	if err := scanner.Scan(
		&episodeID,
		&projectIdentity,
		&sessionKey,
		&kind,
		&startedAtValue,
		&endedAtValue,
		&confidence,
		&complete,
		&derivationVersion,
		&inputHash,
		&payload,
		&encoding,
	); err != nil {
		return evidenceepisode.Episode{}, err
	}
	if err := validateSealedPayloadSize(
		"evidence episode",
		payload,
		maxEvidenceEpisodePayloadBytes,
	); err != nil {
		return evidenceepisode.Episode{}, err
	}
	plaintext, err := s.cipher.open(
		"evidence_episode",
		episodeID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return evidenceepisode.Episode{}, err
	}
	var value evidenceepisode.Episode
	if err := json.Unmarshal(plaintext, &value); err != nil {
		return evidenceepisode.Episode{}, errors.New(
			"decode evidence episode payload",
		)
	}
	startedAt, startErr := parseProjectionTime(startedAtValue)
	endedAt, endErr := parseProjectionTime(endedAtValue)
	if startErr != nil || endErr != nil ||
		value.EpisodeID != episodeID ||
		value.ProjectIdentity != projectIdentity ||
		value.SessionKey != sessionKey ||
		value.Kind != kind ||
		!value.StartedAt.Equal(startedAt) ||
		!value.EndedAt.Equal(endedAt) ||
		string(value.Confidence) != confidence ||
		boolInt(evidenceEpisodeComplete(value)) != complete ||
		value.DerivationVersion != derivationVersion ||
		value.InputHash != inputHash {
		return evidenceepisode.Episode{}, errors.New(
			"evidence episode index does not match payload",
		)
	}
	if err := value.Validate(); err != nil {
		return evidenceepisode.Episode{}, errors.New(
			"invalid persisted evidence episode",
		)
	}
	return value, nil
}

func evidenceEpisodeComplete(value evidenceepisode.Episode) bool {
	return value.Coverage.Bounded &&
		value.Coverage.Transcript == evidenceepisode.CoverageComplete &&
		value.Coverage.ToolCallLinks == evidenceepisode.CoverageComplete
}
