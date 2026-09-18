package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

const (
	maxHabitDebriefPayloadBytes = 512 << 10
	maxHabitDebriefQueryKeys    = 100
)

// ErrHabitDebriefNotFound is returned when a session has no stored debrief.
var ErrHabitDebriefNotFound = errors.New("habit debrief not found")

func validateHabitDebriefRecord(record userinsights.DebriefRecord) error {
	switch {
	case strings.TrimSpace(record.SessionKey) == "" || len(record.SessionKey) > 256:
		return errors.New("invalid habit debrief session key")
	case strings.TrimSpace(record.ProjectIdentity) == "" ||
		len(record.ProjectIdentity) > maxCostIssueProjectBytes:
		return errors.New("invalid habit debrief project identity")
	case strings.TrimSpace(record.Harness) == "" || len(record.Harness) > 64:
		return errors.New("invalid habit debrief harness")
	case strings.TrimSpace(record.PromptVersion) == "" || len(record.PromptVersion) > 128:
		return errors.New("invalid habit debrief prompt version")
	case strings.TrimSpace(record.InputHash) == "" || len(record.InputHash) > 128:
		return errors.New("invalid habit debrief input hash")
	case record.GeneratedAt.IsZero():
		return errors.New("invalid habit debrief generated time")
	case strings.TrimSpace(record.Debrief.Headline) == "":
		return errors.New("habit debrief requires a headline")
	}
	return nil
}

// ReplaceHabitDebrief stores one session's debrief, replacing any earlier
// generation for the same session.
func (s *Store) ReplaceHabitDebrief(
	ctx context.Context,
	record userinsights.DebriefRecord,
) error {
	if err := validateHabitDebriefRecord(record); err != nil {
		return err
	}
	record.SchemaVersion = userinsights.DebriefSchemaVersion
	plaintext, err := json.Marshal(record)
	if err != nil {
		return errors.New("encode habit debrief payload")
	}
	if len(plaintext) > maxHabitDebriefPayloadBytes {
		return errors.New("habit debrief payload exceeds safety limit")
	}
	payload, err := s.cipher.seal("habit_debrief", record.SessionKey, "payload", plaintext)
	if err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin habit debrief replacement")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationHabitDebrief, func() error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO habit_debriefs (
				session_key, project_identity, harness, model,
				prompt_version, input_hash, generated_at, payload,
				payload_encoding, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_key) DO UPDATE SET
				project_identity = excluded.project_identity,
				harness = excluded.harness,
				model = excluded.model,
				prompt_version = excluded.prompt_version,
				input_hash = excluded.input_hash,
				generated_at = excluded.generated_at,
				payload = excluded.payload,
				payload_encoding = excluded.payload_encoding,
				updated_at = excluded.updated_at`,
			record.SessionKey,
			record.ProjectIdentity,
			record.Harness,
			record.Model,
			record.PromptVersion,
			record.InputHash,
			formatProjectionTime(record.GeneratedAt.UTC()),
			payload,
			payloadEncodingAESGCM,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("persist habit debrief: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit habit debrief replacement")
	}
	return nil
}

// GetHabitDebrief reads one session's stored debrief.
func (s *Store) GetHabitDebrief(
	ctx context.Context,
	sessionKey string,
) (userinsights.DebriefRecord, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(sessionKey) > 256 {
		return userinsights.DebriefRecord{}, errors.New("invalid habit debrief session key")
	}
	records, err := s.QueryHabitDebriefs(ctx, []string{sessionKey})
	if err != nil {
		return userinsights.DebriefRecord{}, err
	}
	record, ok := records[sessionKey]
	if !ok {
		return userinsights.DebriefRecord{}, ErrHabitDebriefNotFound
	}
	return record, nil
}

// QueryHabitDebriefs reads stored debriefs for up to 100 session keys and
// returns them keyed by session. Missing sessions are simply absent.
func (s *Store) QueryHabitDebriefs(
	ctx context.Context,
	sessionKeys []string,
) (map[string]userinsights.DebriefRecord, error) {
	result := make(map[string]userinsights.DebriefRecord, len(sessionKeys))
	if len(sessionKeys) == 0 {
		return result, nil
	}
	if len(sessionKeys) > maxHabitDebriefQueryKeys {
		return nil, errors.New("habit debrief query exceeds key limit")
	}
	placeholders := make([]string, 0, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys))
	for _, key := range sessionKeys {
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 256 {
			return nil, errors.New("invalid habit debrief session key")
		}
		placeholders = append(placeholders, "?")
		args = append(args, key)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_key, payload, payload_encoding
		FROM habit_debriefs
		WHERE session_key IN (`+strings.Join(placeholders, ", ")+`)`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query habit debriefs")
	}
	defer rows.Close()
	for rows.Next() {
		var sessionKey, encoding string
		var payload []byte
		if err := rows.Scan(&sessionKey, &payload, &encoding); err != nil {
			return nil, errors.New("read habit debrief")
		}
		plaintext, err := s.cipher.open("habit_debrief", sessionKey, "payload", encoding, payload)
		if err != nil {
			return nil, err
		}
		var record userinsights.DebriefRecord
		if err := json.Unmarshal(plaintext, &record); err != nil {
			return nil, errors.New("decode habit debrief payload")
		}
		record.GeneratedAt = record.GeneratedAt.UTC()
		result[sessionKey] = record
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query habit debriefs")
	}
	return result, nil
}

// DeleteHabitDebrief removes one session's debrief. Callers use it when a
// session is pruned from the transcript store.
func (s *Store) DeleteHabitDebrief(ctx context.Context, sessionKey string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(sessionKey) > 256 {
		return errors.New("invalid habit debrief session key")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin habit debrief deletion")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationHabitDebrief, func() error {
		_, err := tx.ExecContext(ctx, "DELETE FROM habit_debriefs WHERE session_key = ?", sessionKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.New("delete habit debrief")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit habit debrief deletion")
	}
	return nil
}

var _ = time.Now
