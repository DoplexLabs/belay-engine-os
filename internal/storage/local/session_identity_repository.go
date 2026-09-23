package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/sessionidentity"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const maxSessionIdentityPayloadBytes = 64 << 10

type sessionIdentityObservationIDRepair struct {
	oldID string
	newID string
	value sessionidentity.Observation
}

func (s *Store) UpsertSessionIdentityObservation(
	ctx context.Context,
	value sessionidentity.Observation,
) error {
	value, err := sessionidentity.NewObservation(s.storeID, value)
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode session identity observation")
	}
	if len(plaintext) > maxSessionIdentityPayloadBytes {
		return errors.New("session identity observation exceeds safety limit")
	}
	payload, err := s.cipher.seal(
		"session_identity_observation",
		value.ObservationID,
		"payload",
		plaintext,
	)
	if err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin session identity observation")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationSessionIdentity, func() error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO session_identity_observations (
				observation_id, source_kind, source_agent, source_session_key,
				native_namespace, native_id_hash, project_identity,
				artifact_type, source_run_id, started_at, ended_at, coverage,
				derivation_version, observed_at, payload, payload_encoding,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_kind, source_agent, source_session_key, native_namespace)
			DO UPDATE SET
				observation_id = excluded.observation_id,
				native_id_hash = excluded.native_id_hash,
				project_identity = excluded.project_identity,
				artifact_type = excluded.artifact_type,
				source_run_id = excluded.source_run_id,
				started_at = COALESCE(
					session_identity_observations.started_at,
					excluded.started_at
				),
				ended_at = COALESCE(
					excluded.ended_at,
					session_identity_observations.ended_at
				),
				coverage = excluded.coverage,
				derivation_version = excluded.derivation_version,
				observed_at = excluded.observed_at,
				payload = excluded.payload,
				payload_encoding = excluded.payload_encoding,
				updated_at = excluded.updated_at`,
			value.ObservationID,
			value.SourceKind,
			value.SourceAgent,
			value.SourceSessionKey,
			value.NativeNamespace,
			value.NativeIDHash,
			value.ProjectIdentity,
			value.ArtifactType,
			value.SourceRunID,
			nullableIdentityTime(value.StartedAt),
			nullableIdentityTime(value.EndedAt),
			value.Coverage,
			value.DerivationVersion,
			formatProjectionTime(value.ObservedAt.UTC()),
			payload,
			payloadEncodingAESGCM,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("persist session identity observation: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit session identity observation")
	}
	return nil
}

func (s *Store) QuerySessionIdentityObservations(
	ctx context.Context,
	sourceKind string,
) ([]sessionidentity.Observation, error) {
	sourceKind = strings.TrimSpace(sourceKind)
	args := []any{}
	filter := ""
	if sourceKind != "" {
		filter = " WHERE source_kind = ?"
		args = append(args, sourceKind)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT observation_id, source_kind, source_agent, source_session_key,
			native_namespace, payload, payload_encoding
		FROM session_identity_observations`+filter+`
		ORDER BY observed_at, observation_id`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query session identity observations")
	}
	defer rows.Close()
	var result []sessionidentity.Observation
	var repairs []sessionIdentityObservationIDRepair
	for rows.Next() {
		var id, sourceKind, sourceAgent, sourceSessionKey string
		var nativeNamespace, encoding string
		var payload []byte
		if err := rows.Scan(
			&id,
			&sourceKind,
			&sourceAgent,
			&sourceSessionKey,
			&nativeNamespace,
			&payload,
			&encoding,
		); err != nil {
			return nil, errors.New("read session identity observation")
		}
		expectedID := sessionidentity.StableObservationID(
			sourceKind,
			sourceAgent,
			sourceSessionKey,
			nativeNamespace,
		)
		plaintext, err := s.cipher.open(
			"session_identity_observation",
			id,
			"payload",
			encoding,
			payload,
		)
		legacyAAD := false
		if err != nil {
			if expectedID == id {
				return nil, err
			}
			plaintext, err = s.cipher.open(
				"session_identity_observation",
				expectedID,
				"payload",
				encoding,
				payload,
			)
			if err != nil {
				return nil, err
			}
			legacyAAD = true
		}
		var value sessionidentity.Observation
		if err := json.Unmarshal(plaintext, &value); err != nil {
			return nil, errors.New("decode session identity observation")
		}
		switch {
		case value.SourceKind != sourceKind:
			return nil, errors.New(
				"session identity source kind does not match payload",
			)
		case value.SourceAgent != sourceAgent:
			return nil, errors.New(
				"session identity source agent does not match payload",
			)
		case value.SourceSessionKey != sourceSessionKey:
			return nil, errors.New(
				"session identity source session does not match payload",
			)
		case value.NativeNamespace != nativeNamespace:
			return nil, errors.New(
				"session identity namespace does not match payload",
			)
		}
		if legacyAAD || value.ObservationID != expectedID {
			value.ObservationID = expectedID
			repairs = append(repairs, sessionIdentityObservationIDRepair{
				oldID: id,
				newID: expectedID,
				value: value,
			})
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query session identity observations")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("close session identity observations")
	}
	if len(repairs) > 0 {
		if err := s.repairSessionIdentityObservationIDs(
			ctx,
			repairs,
		); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) repairSessionIdentityObservationIDs(
	ctx context.Context,
	repairs []sessionIdentityObservationIDRepair,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin session identity observation repair")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationSessionIdentity, func() error {
		for _, repair := range repairs {
			plaintext, err := json.Marshal(repair.value)
			if err != nil {
				return errors.New(
					"encode repaired session identity observation",
				)
			}
			payload, err := s.cipher.seal(
				"session_identity_observation",
				repair.newID,
				"payload",
				plaintext,
			)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `
				UPDATE session_identity_observations
				SET observation_id = ?, payload = ?, payload_encoding = ?,
					updated_at = ?
				WHERE observation_id = ?`,
				repair.newID,
				payload,
				payloadEncodingAESGCM,
				formatProjectionTime(s.nowUTC()),
				repair.oldID,
			)
			if err != nil {
				return errors.New("repair session identity observation ID")
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				return errors.New(
					"session identity observation ID was not repaired",
				)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit session identity observation repair")
	}
	return nil
}

func (s *Store) ReadSessionIdentityAudit(
	ctx context.Context,
) (sessionidentity.Audit, error) {
	values, err := s.QuerySessionIdentityObservations(ctx, "")
	if err != nil {
		return sessionidentity.Audit{}, err
	}
	result := sessionidentity.AuditObservations(values)
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM session_identity_links
		WHERE state = ?`,
		sessionidentity.StateActive,
	).Scan(&result.ActiveLinks); err != nil {
		return sessionidentity.Audit{}, errors.New("count active session identity links")
	}
	return result, nil
}

func (s *Store) QueryActiveSessionIdentityAliases(
	ctx context.Context,
	sessionKeys []string,
) (map[string]sessionidentity.ActiveAlias, error) {
	result := make(map[string]sessionidentity.ActiveAlias, len(sessionKeys))
	if len(sessionKeys) == 0 {
		return result, nil
	}
	if len(sessionKeys) > 100 {
		return nil, errors.New("session identity alias query exceeds limit")
	}
	requested := make(map[string]bool, len(sessionKeys))
	for _, key := range sessionKeys {
		if err := validateStorageIdentifier(
			"session identity alias key",
			key,
		); err != nil {
			return nil, err
		}
		requested[key] = true
	}
	links, err := s.querySessionIdentityLinks(ctx)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		counterparts map[string]bool
		bases        map[string]bool
	}
	candidates := make(map[string]*candidate)
	add := func(key, counterpart, basis string) {
		if !requested[key] {
			return
		}
		value := candidates[key]
		if value == nil {
			value = &candidate{
				counterparts: make(map[string]bool),
				bases:        make(map[string]bool),
			}
			candidates[key] = value
		}
		value.counterparts[counterpart] = true
		value.bases[basis] = true
	}
	for _, link := range links {
		if link.State != sessionidentity.StateActive {
			continue
		}
		add(link.LeftSessionKey, link.RightSessionKey, link.Basis)
		add(link.RightSessionKey, link.LeftSessionKey, link.Basis)
	}
	for key, value := range candidates {
		if len(value.counterparts) != 1 {
			continue
		}
		alias := sessionidentity.ActiveAlias{SessionKey: key}
		for counterpart := range value.counterparts {
			alias.LinkedSessionKey = counterpart
		}
		for basis := range value.bases {
			alias.Bases = append(alias.Bases, basis)
		}
		sort.Strings(alias.Bases)
		result[key] = alias
	}
	return result, nil
}

func (s *Store) ReconcileSessionIdentityLinks(
	ctx context.Context,
) (int, error) {
	if err := s.backfillTranscriptSessionIdentityObservations(ctx); err != nil {
		return 0, err
	}
	observations, err := s.QuerySessionIdentityObservations(ctx, "")
	if err != nil {
		return 0, err
	}
	now := s.nowUTC()
	inserted, err := s.reconcileSessionIdentityLinkBasis(
		ctx,
		sessionidentity.BasisExactNativeID,
		sessionidentity.ResolveExactNativeLinks(observations, now),
	)
	if err != nil {
		return inserted, err
	}
	exactCovered := sessionidentity.ExactNativeObservationIDs(observations)
	evidence := make(map[string]*sessionidentity.SessionEvidence)
	for _, value := range observations {
		if exactCovered[value.ObservationID] {
			continue
		}
		key := value.SourceKind + "\x00" + value.SourceSessionKey
		if _, exists := evidence[key]; exists {
			continue
		}
		evidence[key] = &sessionidentity.SessionEvidence{
			SourceKind:      value.SourceKind,
			SourceAgent:     value.SourceAgent,
			SessionKey:      value.SourceSessionKey,
			ProjectIdentity: value.ProjectIdentity,
		}
	}
	if err := s.addTranscriptToolCallEvidence(ctx, evidence); err != nil {
		return 0, err
	}
	if err := s.addCanonicalToolCallEvidence(ctx, evidence); err != nil {
		return 0, err
	}
	input := make([]sessionidentity.SessionEvidence, 0, len(evidence))
	for _, value := range evidence {
		input = append(input, *value)
	}
	toolInserted, err := s.reconcileSessionIdentityLinkBasis(
		ctx,
		sessionidentity.BasisSharedToolCalls,
		sessionidentity.ResolveToolCallLinks(input, now),
	)
	return inserted + toolInserted, err
}

func (s *Store) backfillTranscriptSessionIdentityObservations(
	ctx context.Context,
) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts.session_key, ts.agent, ts.native_session_id, ts.project_path,
			ts.git_remote_url, ts.project_identity, ts.started_at, ts.ended_at,
			ts.wall_duration_ms, ts.total_input_tokens, ts.total_output_tokens,
			ts.total_tokens, ts.total_cache_read_tokens,
			ts.total_cache_write_tokens, ts.total_cost_usd, ts.turn_count,
			ts.user_turn_count, ts.assistant_turn_count, ts.tool_call_count,
			ts.tool_result_count, ts.system_turn_count,
			ts.compaction_summary_count, ts.coverage
		FROM transcript_sessions ts
		WHERE NOT EXISTS (
			SELECT 1
			FROM session_identity_observations sio
			WHERE sio.source_kind = ?
				AND sio.source_agent = ts.agent
				AND sio.source_session_key = ts.session_key
				AND sio.native_namespace = ts.agent || '_transcript'
		)
		ORDER BY ts.session_key`,
		sessionidentity.SourceTranscript,
	)
	if err != nil {
		return errors.New("query transcript identity backfill")
	}
	var sessions []transcript.Session
	for rows.Next() {
		session, scanErr := scanTranscriptSession(rows)
		if scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return errors.New("query transcript identity backfill")
	}
	if err := rows.Close(); err != nil {
		return errors.New("close transcript identity backfill")
	}
	for _, session := range sessions {
		if err := s.UpsertSessionIdentityObservation(
			ctx,
			sessionidentity.Observation{
				SourceKind:       sessionidentity.SourceTranscript,
				SourceAgent:      session.Agent,
				SourceSessionKey: session.SessionKey,
				NativeNamespace:  session.Agent + "_transcript",
				NativeSessionID:  session.NativeSessionID,
				ProjectIdentity:  session.ProjectIdentity,
				StartedAt:        session.StartedAt,
				EndedAt:          session.EndedAt,
				Coverage:         string(session.Coverage),
				ObservedAt:       s.nowUTC(),
			},
		); err != nil {
			return fmt.Errorf("backfill transcript identity observation: %w", err)
		}
	}
	return nil
}

func (s *Store) reconcileSessionIdentityLinkBasis(
	ctx context.Context,
	basis string,
	links []sessionidentity.Link,
) (int, error) {
	desired := make(map[string]sessionidentity.Link, len(links))
	for _, link := range links {
		if link.Basis != basis {
			return 0, errors.New("session identity link basis mismatch")
		}
		desired[link.LinkID] = link
	}
	existing, err := s.querySessionIdentityLinks(ctx)
	if err != nil {
		return 0, err
	}
	inserted := 0
	for _, link := range desired {
		if current, ok := existing[link.LinkID]; ok {
			if current.State == sessionidentity.StateActive {
				continue
			}
			link.CreatedAt = current.CreatedAt
			if err := s.setSessionIdentityLinkState(ctx, link); err != nil {
				return inserted, err
			}
			continue
		}
		added, err := s.upsertSessionIdentityLink(ctx, link)
		if err != nil {
			return inserted, err
		}
		if added {
			inserted++
		}
	}
	for id, link := range existing {
		if link.Basis != basis ||
			link.State != sessionidentity.StateActive {
			continue
		}
		if _, ok := desired[id]; ok {
			continue
		}
		link.State = sessionidentity.StateSuperseded
		if err := s.setSessionIdentityLinkState(ctx, link); err != nil {
			return inserted, err
		}
	}
	return inserted, nil
}

func (s *Store) addTranscriptToolCallEvidence(
	ctx context.Context,
	evidence map[string]*sessionidentity.SessionEvidence,
) error {
	keys := sessionIdentityEvidenceSessionKeys(evidence, true)
	for start := 0; start < len(keys); start += 400 {
		end := min(start+400, len(keys))
		if err := s.addTranscriptToolCallEvidenceChunk(
			ctx,
			evidence,
			keys[start:end],
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addTranscriptToolCallEvidenceChunk(
	ctx context.Context,
	evidence map[string]*sessionidentity.SessionEvidence,
	sessionKeys []string,
) error {
	if len(sessionKeys) == 0 {
		return nil
	}
	args := make([]any, len(sessionKeys))
	for index, key := range sessionKeys {
		args[index] = key
	}
	query := `
		SELECT turn_id, session_key, payload, payload_encoding
		FROM transcript_turns
		WHERE role IN ('tool_call', 'tool_result')
			AND session_key IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(sessionKeys)), ",") +
		`)
		ORDER BY session_key, turn_index`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return errors.New("query transcript identity evidence")
	}
	for rows.Next() {
		var turnID, sessionKey, encoding string
		var payload []byte
		if err := rows.Scan(&turnID, &sessionKey, &payload, &encoding); err != nil {
			rows.Close()
			return errors.New("read transcript identity evidence")
		}
		value := evidence[sessionidentity.SourceTranscript+"\x00"+sessionKey]
		if value == nil {
			continue
		}
		plaintext, err := s.cipher.open(
			"transcript_turn",
			turnID,
			"payload",
			encoding,
			payload,
		)
		if err != nil {
			rows.Close()
			return err
		}
		var body transcript.Payload
		if err := json.Unmarshal(plaintext, &body); err != nil {
			rows.Close()
			return errors.New("decode transcript identity evidence")
		}
		if id := strings.TrimSpace(body.ToolCallID); id != "" {
			value.ToolCallIDs = append(value.ToolCallIDs, id)
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}

func (s *Store) addCanonicalToolCallEvidence(
	ctx context.Context,
	evidence map[string]*sessionidentity.SessionEvidence,
) error {
	keys := sessionIdentityEvidenceSessionKeys(evidence, false)
	for start := 0; start < len(keys); start += 400 {
		end := min(start+400, len(keys))
		if err := s.addCanonicalToolCallEvidenceChunk(
			ctx,
			evidence,
			keys[start:end],
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addCanonicalToolCallEvidenceChunk(
	ctx context.Context,
	evidence map[string]*sessionidentity.SessionEvidence,
	sessionKeys []string,
) error {
	if len(sessionKeys) == 0 {
		return nil
	}
	args := make([]any, len(sessionKeys))
	for index, key := range sessionKeys {
		args[index] = key
	}
	query := `
		SELECT event_id, source_kind, session_key, canonical_json,
			canonical_encoding
		FROM events
		WHERE event_type IN (
			'tool.call',
			'tool.result',
			'command.exec',
			'command.result'
		)
			AND session_key IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(sessionKeys)), ",") +
		`)
		ORDER BY session_key, source_sequence, event_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return errors.New("query canonical identity evidence")
	}
	for rows.Next() {
		var eventID, sourceKind, sessionKey, encoding string
		var payload []byte
		if err := rows.Scan(
			&eventID,
			&sourceKind,
			&sessionKey,
			&payload,
			&encoding,
		); err != nil {
			rows.Close()
			return errors.New("read canonical identity evidence")
		}
		kind := sessionIdentitySourceKindFromCanonical(sourceKind)
		value := evidence[kind+"\x00"+sessionKey]
		if value == nil {
			continue
		}
		event, err := s.decodeEvent(eventID, encoding, payload)
		if err != nil {
			rows.Close()
			return err
		}
		if event.Observation.Details == nil {
			continue
		}
		if id := strings.TrimSpace(event.Observation.Details.ToolCallID); id != "" {
			value.ToolCallIDs = append(value.ToolCallIDs, id)
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}

func sessionIdentityEvidenceSessionKeys(
	evidence map[string]*sessionidentity.SessionEvidence,
	transcript bool,
) []string {
	unique := make(map[string]bool)
	for _, value := range evidence {
		isTranscript := value.SourceKind == sessionidentity.SourceTranscript
		if isTranscript != transcript || strings.TrimSpace(value.SessionKey) == "" {
			continue
		}
		unique[value.SessionKey] = true
	}
	result := make([]string, 0, len(unique))
	for key := range unique {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sessionIdentitySourceKindFromCanonical(value string) string {
	switch value {
	case "artifact":
		return sessionidentity.SourceNumbatArtifact
	case "hook":
		return sessionidentity.SourceNumbatHook
	case "otel":
		return sessionidentity.SourceOTLP
	default:
		return "numbat_" + value
	}
}

func (s *Store) querySessionIdentityLinks(
	ctx context.Context,
) (map[string]sessionidentity.Link, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT link_id, left_session_key, right_session_key, relation, basis,
			confidence, state, derivation_version, payload, payload_encoding,
			created_at
		FROM session_identity_links
		WHERE derivation_version = ?
		ORDER BY link_id`,
		sessionidentity.DerivationVersion,
	)
	if err != nil {
		return nil, errors.New("query session identity links")
	}
	defer rows.Close()
	result := make(map[string]sessionidentity.Link)
	for rows.Next() {
		var id, left, right, relation, basis, confidence, state string
		var derivationVersion, encoding, createdAtValue string
		var payload []byte
		if err := rows.Scan(
			&id,
			&left,
			&right,
			&relation,
			&basis,
			&confidence,
			&state,
			&derivationVersion,
			&payload,
			&encoding,
			&createdAtValue,
		); err != nil {
			return nil, errors.New("read session identity link")
		}
		plaintext, err := s.cipher.open(
			"session_identity_link",
			id,
			"payload",
			encoding,
			payload,
		)
		if err != nil {
			return nil, err
		}
		var value sessionidentity.Link
		if err := json.Unmarshal(plaintext, &value); err != nil {
			return nil, errors.New("decode session identity link")
		}
		createdAt, err := parseProjectionTime(createdAtValue)
		if err != nil ||
			value.LinkID != id ||
			value.LeftSessionKey != left ||
			value.RightSessionKey != right ||
			value.Relation != relation ||
			value.Basis != basis ||
			value.Confidence != confidence ||
			value.State != state ||
			value.DerivationVersion != derivationVersion ||
			!value.CreatedAt.Equal(createdAt) {
			return nil, errors.New(
				"session identity link index does not match payload",
			)
		}
		result[id] = value
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query session identity links")
	}
	return result, nil
}

func (s *Store) upsertSessionIdentityLink(
	ctx context.Context,
	link sessionidentity.Link,
) (bool, error) {
	plaintext, err := json.Marshal(link)
	if err != nil {
		return false, errors.New("encode session identity link")
	}
	payload, err := s.cipher.seal(
		"session_identity_link",
		link.LinkID,
		"payload",
		plaintext,
	)
	if err != nil {
		return false, err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin session identity link")
	}
	defer tx.Rollback()
	inserted := false
	err = withMutationTx(ctx, tx, mutationSessionIdentity, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO session_identity_links (
				link_id, left_session_key, right_session_key, relation,
				basis, confidence, state, derivation_version, payload,
				payload_encoding, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(
				left_session_key,
				right_session_key,
				basis,
				derivation_version
			) DO NOTHING`,
			link.LinkID,
			link.LeftSessionKey,
			link.RightSessionKey,
			link.Relation,
			link.Basis,
			link.Confidence,
			link.State,
			link.DerivationVersion,
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(link.CreatedAt),
			now,
		)
		if err != nil {
			return fmt.Errorf("persist session identity link: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect session identity link persistence")
		}
		inserted = affected > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit session identity link")
	}
	return inserted, nil
}

func (s *Store) UpsertSessionIdentityLink(
	ctx context.Context,
	link sessionidentity.Link,
) (bool, error) {
	return s.upsertSessionIdentityLink(ctx, link)
}

func (s *Store) setSessionIdentityLinkState(
	ctx context.Context,
	link sessionidentity.Link,
) error {
	plaintext, err := json.Marshal(link)
	if err != nil {
		return errors.New("encode session identity link state")
	}
	payload, err := s.cipher.seal(
		"session_identity_link",
		link.LinkID,
		"payload",
		plaintext,
	)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin session identity link state")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationSessionIdentity, func() error {
		result, err := tx.ExecContext(ctx, `
			UPDATE session_identity_links
			SET state = ?, payload = ?, payload_encoding = ?, updated_at = ?
			WHERE link_id = ? AND derivation_version = ?`,
			link.State,
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
			link.LinkID,
			sessionidentity.DerivationVersion,
		)
		if err != nil {
			return fmt.Errorf("update session identity link state: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return errors.New("session identity link state was not updated")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit session identity link state")
	}
	return nil
}

func nullableIdentityTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatProjectionTime(value.UTC())
}
