package local

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RetentionPolicy has no implicit defaults. At least one positive bound must be
// supplied by the caller. MaxPayloadBytes covers encrypted event, finding,
// enrichment, issue-projection, and transcript-turn payload blobs, not indexes
// or page overhead.
type RetentionPolicy struct {
	MaxAge          time.Duration `json:"max_age"`
	MaxEventCount   int           `json:"max_event_count"`
	MaxPayloadBytes int64         `json:"max_payload_bytes"`
}

type RetentionDiagnostics struct {
	Policy                         RetentionPolicy `json:"policy"`
	CurrentEventCount              int             `json:"current_event_count"`
	CurrentFindingCount            int             `json:"current_finding_count"`
	CurrentTranscriptTurnCount     int             `json:"current_transcript_turn_count"`
	CurrentPayloadBytes            int64           `json:"current_payload_bytes"`
	CurrentTranscriptPayloadBytes  int64           `json:"current_transcript_payload_bytes"`
	EligibleEventCount             int             `json:"eligible_event_count"`
	EligibleFindingCount           int             `json:"eligible_finding_count"`
	EligibleTranscriptTurnCount    int             `json:"eligible_transcript_turn_count"`
	EligiblePayloadBytes           int64           `json:"eligible_payload_bytes"`
	EligibleTranscriptPayloadBytes int64           `json:"eligible_transcript_payload_bytes"`
	OldestPayloadAt                *time.Time      `json:"oldest_payload_at,omitempty"`
	NewestPayloadAt                *time.Time      `json:"newest_payload_at,omitempty"`
	EvaluatedAt                    time.Time       `json:"evaluated_at"`
}

type PruneResult struct {
	Before                       RetentionDiagnostics `json:"before"`
	PrunedEventCount             int                  `json:"pruned_event_count"`
	PrunedFindingCount           int                  `json:"pruned_finding_count"`
	PrunedTranscriptTurnCount    int                  `json:"pruned_transcript_turn_count"`
	PrunedEvidenceEpisodeCount   int                  `json:"pruned_evidence_episode_count"`
	PrunedPayloadBytes           int64                `json:"pruned_payload_bytes"`
	PrunedTranscriptPayloadBytes int64                `json:"pruned_transcript_payload_bytes"`
	AfterEventCount              int                  `json:"after_event_count"`
	AfterFindingCount            int                  `json:"after_finding_count"`
	AfterTranscriptTurnCount     int                  `json:"after_transcript_turn_count"`
	AfterPayloadBytes            int64                `json:"after_payload_bytes"`
	AfterTranscriptPayloadBytes  int64                `json:"after_transcript_payload_bytes"`
	Maintenance                  MaintenanceResult    `json:"maintenance"`
}

type MaintenanceResult struct {
	State     string `json:"state"`
	Code      string `json:"code,omitempty"`
	Retryable bool   `json:"retryable"`
}

type retentionItem struct {
	recordType     string
	recordID       string
	occurredAt     time.Time
	sequence       int64
	bytes          int64
	protectedUntil time.Time
	pinned         bool
	selected       bool
}

func (policy RetentionPolicy) validate() error {
	if policy.MaxAge < 0 || policy.MaxEventCount < 0 || policy.MaxPayloadBytes < 0 {
		return errors.New("retention bounds cannot be negative")
	}
	if policy.MaxAge == 0 && policy.MaxEventCount == 0 && policy.MaxPayloadBytes == 0 {
		return errors.New("at least one retention bound is required")
	}
	return nil
}

func (s *Store) RetentionDiagnostics(
	ctx context.Context,
	policy RetentionPolicy,
	now time.Time,
) (RetentionDiagnostics, error) {
	if err := policy.validate(); err != nil {
		return RetentionDiagnostics{}, err
	}
	if now.IsZero() {
		return RetentionDiagnostics{}, errors.New("retention evaluation time is required")
	}
	items, err := readRetentionItems(ctx, s.db)
	if err != nil {
		return RetentionDiagnostics{}, err
	}
	dependencies, err := readCitationDependencies(ctx, s.db)
	if err != nil {
		return RetentionDiagnostics{}, err
	}
	return evaluateRetention(policy, now.UTC(), items, dependencies), nil
}

func (s *Store) Prune(
	ctx context.Context,
	policy RetentionPolicy,
	now time.Time,
) (PruneResult, error) {
	if err := policy.validate(); err != nil {
		return PruneResult{}, err
	}
	if now.IsZero() {
		return PruneResult{}, errors.New("retention evaluation time is required")
	}
	now = now.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneResult{}, errors.New("begin local retention prune")
	}
	defer tx.Rollback()
	items, err := readRetentionItems(ctx, tx)
	if err != nil {
		return PruneResult{}, err
	}
	dependencies, err := readCitationDependencies(ctx, tx)
	if err != nil {
		return PruneResult{}, err
	}
	before := evaluateRetention(policy, now, items, dependencies)
	result := PruneResult{Before: before}
	if err := withMutationTx(ctx, tx, mutationRetentionPrune, func() error {
		affectedSessions, issueRevisionDeleted, err := affectedRetentionSessions(
			ctx,
			tx,
			items,
		)
		if err != nil {
			return err
		}
		affectedIssueIDs := make(map[string]struct{})
		for sessionKey := range affectedSessions {
			sessionIssueIDs, err := activeIssueIDsForSessionTx(ctx, tx, sessionKey)
			if err != nil {
				return err
			}
			mergeIssueIDs(affectedIssueIDs, sessionIssueIDs)
		}
		for sessionKey := range affectedSessions {
			if _, err := s.markSessionDirtyTx(
				ctx,
				tx,
				sessionKey,
				"retention_prune",
				s.sessionScopeQualityTx(ctx, tx, sessionKey),
				formatProjectionTime(now),
			); err != nil {
				return err
			}
		}
		for _, recordType := range []string{"finding", "issue", "event"} {
			for _, item := range items {
				if !item.selected || item.recordType != recordType {
					continue
				}
				var statement string
				switch item.recordType {
				case "event":
					statement = "DELETE FROM events WHERE event_id = ?"
					result.PrunedEventCount++
				case "finding":
					statement = "DELETE FROM findings WHERE finding_id = ?"
					result.PrunedFindingCount++
				case "issue":
					statement = "DELETE FROM issue_occurrences WHERE revision_id = ?"
				default:
					return errors.New("unsupported local retention record")
				}
				deleted, err := tx.ExecContext(ctx, statement, item.recordID)
				if err != nil {
					return errors.New("delete local retention record")
				}
				count, err := deleted.RowsAffected()
				if err != nil || count != 1 {
					return errors.New("local retention record changed during prune")
				}
				result.PrunedPayloadBytes += item.bytes
			}
		}
		for _, item := range items {
			if !item.selected {
				continue
			}
			var statement string
			switch item.recordType {
			case "cost_issue":
				statement = "DELETE FROM cost_issues WHERE issue_id = ?"
			case "correction_candidate":
				statement = "DELETE FROM correction_candidates WHERE candidate_id = ?"
			case "insight":
				statement = "DELETE FROM insights WHERE insight_id = ?"
			case "cost_issue_fix":
				statement = "DELETE FROM cost_issue_fixes WHERE fix_id = ?"
			default:
				continue
			}
			deleted, err := tx.ExecContext(ctx, statement, item.recordID)
			if err != nil {
				return errors.New("delete retained cost issue payload")
			}
			count, err := deleted.RowsAffected()
			if err != nil || count != 1 {
				return errors.New("cost issue payload changed during prune")
			}
			result.PrunedPayloadBytes += item.bytes
		}
		transcriptSessions, err := affectedTranscriptRetentionSessions(
			ctx,
			tx,
			items,
		)
		if err != nil {
			return err
		}
		if len(transcriptSessions) > 0 {
			transcriptProjects := make(map[string]struct{})
			for sessionKey := range transcriptSessions {
				var projectIdentity string
				if err := tx.QueryRowContext(ctx, `
					SELECT project_identity
					FROM transcript_sessions
					WHERE session_key = ?`,
					sessionKey,
				).Scan(&projectIdentity); err != nil {
					return errors.New("resolve retained transcript project")
				}
				transcriptProjects[projectIdentity] = struct{}{}
				var episodeCount int
				var episodeBytes int64
				if err := tx.QueryRowContext(ctx, `
					SELECT COUNT(*), COALESCE(SUM(LENGTH(payload)), 0)
					FROM evidence_episodes
					WHERE session_key = ?`,
					sessionKey,
				).Scan(&episodeCount, &episodeBytes); err != nil {
					return errors.New(
						"inspect retained evidence episodes",
					)
				}
				if episodeCount > 0 {
					if _, err := tx.ExecContext(ctx, `
						DELETE FROM evidence_episodes
						WHERE session_key = ?`,
						sessionKey,
					); err != nil {
						return errors.New(
							"delete invalidated evidence episodes",
						)
					}
					result.PrunedEvidenceEpisodeCount += episodeCount
					result.PrunedPayloadBytes += episodeBytes
				}
			}
			if err := withMutationTx(
				ctx,
				tx,
				mutationTranscriptRetention,
				func() error {
					for _, item := range items {
						if !item.selected || item.recordType != "transcript_turn" {
							continue
						}
						deleted, err := tx.ExecContext(
							ctx,
							"DELETE FROM transcript_turns WHERE turn_id = ?",
							item.recordID,
						)
						if err != nil {
							return errors.New("delete retained transcript turn")
						}
						count, err := deleted.RowsAffected()
						if err != nil || count != 1 {
							return errors.New(
								"transcript turn changed during retention prune",
							)
						}
						result.PrunedTranscriptTurnCount++
						result.PrunedTranscriptPayloadBytes += item.bytes
						result.PrunedPayloadBytes += item.bytes
					}
					for sessionKey := range transcriptSessions {
						var retainedTurns int
						if err := tx.QueryRowContext(ctx, `
							SELECT COUNT(*)
							FROM transcript_turns
							WHERE session_key = ?`,
							sessionKey,
						).Scan(&retainedTurns); err != nil {
							return errors.New("count retained transcript turns")
						}
						if retainedTurns == 0 {
							deleted, err := tx.ExecContext(ctx, `
								DELETE FROM transcript_sessions
								WHERE session_key = ?`,
								sessionKey,
							)
							if err != nil {
								return errors.New("delete empty transcript session")
							}
							count, err := deleted.RowsAffected()
							if err != nil || count != 1 {
								return errors.New(
									"transcript session changed during retention prune",
								)
							}
							continue
						}
						if err := reindexTranscriptSessionTx(
							ctx,
							tx,
							sessionKey,
						); err != nil {
							return err
						}
						if err := recomputeTranscriptSessionTx(
							ctx,
							tx,
							sessionKey,
							formatProjectionTime(now),
						); err != nil {
							return err
						}
					}
					for projectIdentity := range transcriptProjects {
						if err := markTranscriptProjectDirtyTx(
							ctx,
							tx,
							projectIdentity,
							formatProjectionTime(now),
						); err != nil {
							return err
						}
					}
					return nil
				},
			); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM session_scopes
			WHERE NOT EXISTS (
				SELECT 1 FROM events
				WHERE events.session_key = session_scopes.session_key
			)`); err != nil {
			return errors.New("delete orphaned session scopes")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM analysis_diagnostics
			WHERE NOT EXISTS (
				SELECT 1 FROM events
				WHERE events.session_key = analysis_diagnostics.session_key
			)`); err != nil {
			return errors.New("delete orphaned analysis diagnostics")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM session_identity_observations
			WHERE (
				source_kind = 'transcript'
				AND NOT EXISTS (
					SELECT 1
					FROM transcript_sessions
					WHERE transcript_sessions.session_key =
						session_identity_observations.source_session_key
				)
			) OR (
				source_kind <> 'transcript'
				AND NOT EXISTS (
					SELECT 1
					FROM events
					WHERE events.session_key =
						session_identity_observations.source_session_key
				)
			)`); err != nil {
			return errors.New("delete orphaned session identity observations")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM session_identity_links
			WHERE NOT EXISTS (
				SELECT 1
				FROM session_identity_observations
				WHERE source_session_key =
					session_identity_links.left_session_key
			)
				OR NOT EXISTS (
					SELECT 1
					FROM session_identity_observations
					WHERE source_session_key =
						session_identity_links.right_session_key
				)`); err != nil {
			return errors.New("delete orphaned session identity links")
		}
		removedJobs, err := tx.ExecContext(ctx, `
			DELETE FROM fix_recurrence_jobs
			WHERE state = 'complete'
				AND revision_id IN (
					SELECT revision_id
					FROM session_analysis_revisions
					WHERE NOT EXISTS (
						SELECT 1 FROM events
						WHERE events.session_key =
							session_analysis_revisions.session_key
					)
						AND updated_at <= ?
				)`,
			formatProjectionTime(now.Add(-time.Hour)),
		)
		if err != nil {
			return errors.New("delete completed recurrence jobs")
		}
		removedJobCount, _ := removedJobs.RowsAffected()
		var removedAnalysisRevisions int64
		removed, err := tx.ExecContext(ctx, `
					DELETE FROM session_analysis_revisions
					WHERE NOT EXISTS (
						SELECT 1 FROM events
						WHERE events.session_key = session_analysis_revisions.session_key
					)
					AND NOT EXISTS (
						SELECT 1 FROM fix_recurrence_jobs
						WHERE fix_recurrence_jobs.revision_id =
							session_analysis_revisions.revision_id
							AND fix_recurrence_jobs.state <> 'complete'
					)
					AND updated_at <= ?`,
			formatProjectionTime(now.Add(-time.Hour)),
		)
		if err != nil {
			return errors.New("delete orphaned analysis revisions")
		}
		removedAnalysisRevisions, _ = removed.RowsAffected()
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM dirty_sessions
			WHERE NOT EXISTS (
				SELECT 1 FROM events
				WHERE events.session_key = dirty_sessions.session_key
			)`); err != nil {
			return errors.New("delete orphaned dirty-session state")
		}
		relevantPrune := issueRevisionDeleted ||
			removedAnalysisRevisions > 0 ||
			removedJobCount > 0 ||
			hasSelectedCanonicalRetentionItem(items)
		finalProjectionChange := issueRevisionDeleted ||
			removedAnalysisRevisions > 0 ||
			hasSelectedRetentionRecordType(items, "event")
		if finalProjectionChange {
			for sessionKey := range affectedSessions {
				sessionIssueIDs, err := activeIssueIDsForSessionTx(ctx, tx, sessionKey)
				if err != nil {
					return err
				}
				mergeIssueIDs(affectedIssueIDs, sessionIssueIDs)
			}
			generation, err := nextProjectionGenerationTx(ctx, tx)
			if err != nil {
				return err
			}
			if err := s.refreshIssueProjectionIfReadyTx(
				ctx,
				tx,
				generation,
				formatProjectionTime(now),
				affectedIssueIDs,
			); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE issue_projection_metadata
				SET oldest_retained_generation = ?,
					retention_generation = retention_generation + 1
				WHERE singleton = 1`,
				generation,
			); err != nil {
				return errors.New("advance retained issue generation")
			}
		} else if relevantPrune {
			if _, err := tx.ExecContext(ctx, `
				UPDATE issue_projection_metadata
				SET retention_generation = retention_generation + 1
				WHERE singleton = 1`); err != nil {
				return errors.New("advance retention generation")
			}
		}
		return nil
	}); err != nil {
		return PruneResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PruneResult{}, errors.New("commit local retention prune")
	}
	result.AfterEventCount = before.CurrentEventCount - result.PrunedEventCount
	result.AfterFindingCount = before.CurrentFindingCount - result.PrunedFindingCount
	result.AfterTranscriptTurnCount = before.CurrentTranscriptTurnCount -
		result.PrunedTranscriptTurnCount
	result.AfterPayloadBytes = before.CurrentPayloadBytes - result.PrunedPayloadBytes
	result.AfterTranscriptPayloadBytes = before.CurrentTranscriptPayloadBytes -
		result.PrunedTranscriptPayloadBytes
	result.Maintenance = s.runPostPruneMaintenance(ctx)
	return result, nil
}

func hasSelectedCanonicalRetentionItem(items []retentionItem) bool {
	for _, item := range items {
		if item.selected &&
			(item.recordType == "event" ||
				item.recordType == "finding" ||
				item.recordType == "issue") {
			return true
		}
	}
	return false
}

func hasSelectedRetentionRecordType(items []retentionItem, recordType string) bool {
	for _, item := range items {
		if item.selected && item.recordType == recordType {
			return true
		}
	}
	return false
}

type retentionQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readCitationDependencies(
	ctx context.Context,
	querier retentionQuerier,
) (map[string][]string, error) {
	rows, err := querier.QueryContext(ctx, `
		SELECT event_id, dependent_id
		FROM (
			SELECT event_id, finding_id AS dependent_id
			FROM finding_event_citations
			UNION ALL
			SELECT event_id, revision_id AS dependent_id
			FROM issue_occurrence_events
		)
		ORDER BY event_id, dependent_id`)
	if err != nil {
		return nil, errors.New("read finding citation dependencies")
	}
	defer rows.Close()
	result := make(map[string][]string)
	for rows.Next() {
		var eventID, findingID string
		if err := rows.Scan(&eventID, &findingID); err != nil {
			return nil, errors.New("read finding citation dependency")
		}
		result[eventID] = append(result[eventID], findingID)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read finding citation dependencies")
	}
	return result, nil
}

func readRetentionItems(ctx context.Context, querier retentionQuerier) ([]retentionItem, error) {
	rows, err := querier.QueryContext(ctx, `
		SELECT record_type, record_id, occurred_at, source_sequence,
			payload_bytes, protection_base, pinned
		FROM (
			SELECT
				'event' AS record_type,
				e.event_id AS record_id,
				e.occurred_at,
				e.source_sequence,
				LENGTH(e.canonical_json) +
					COALESCE(LENGTH(ee.enrichment_payload), 0) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM events e
			LEFT JOIN event_enrichments ee ON ee.event_id = e.event_id
			UNION ALL
			SELECT
				'finding' AS record_type,
				finding_id AS record_id,
				detected_at AS occurred_at,
				0 AS source_sequence,
				LENGTH(cited_event_ids_json) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM findings
			UNION ALL
			SELECT
				'issue' AS record_type,
				revision_id AS record_id,
				last_observed_at AS occurred_at,
				0 AS source_sequence,
				LENGTH(evidence_payload) AS payload_bytes,
				updated_at AS protection_base,
				EXISTS (
					SELECT 1
					FROM fix_recurrence_jobs frj
					JOIN session_analysis_revisions sar
						ON sar.revision_id = frj.revision_id
					WHERE sar.session_key = issue_occurrences.session_key
						AND sar.visible_from_generation =
							issue_occurrences.visible_from_generation
						AND frj.state <> 'complete'
				) AS pinned
			FROM issue_occurrences
			UNION ALL
			SELECT
				'transcript_turn' AS record_type,
				turn_id AS record_id,
				occurred_at,
				turn_index AS source_sequence,
				LENGTH(payload) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM transcript_turns
			UNION ALL
			SELECT
				'cost_issue' AS record_type,
				issue_id AS record_id,
				last_seen AS occurred_at,
				0 AS source_sequence,
				LENGTH(payload) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM cost_issues
			UNION ALL
			SELECT
				'correction_candidate' AS record_type,
				candidate_id AS record_id,
				occurred_at,
				0 AS source_sequence,
				LENGTH(payload) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM correction_candidates
			UNION ALL
			SELECT
				'insight' AS record_type,
				insight_id AS record_id,
				generated_at AS occurred_at,
				0 AS source_sequence,
				LENGTH(payload) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM insights
			UNION ALL
			SELECT
				'cost_issue_fix' AS record_type,
				fix_id AS record_id,
				COALESCE(applied_at, proposed_at) AS occurred_at,
				0 AS source_sequence,
				LENGTH(payload) AS payload_bytes,
				NULL AS protection_base,
				0 AS pinned
			FROM cost_issue_fixes
		)
		ORDER BY occurred_at ASC, source_sequence ASC, record_id ASC, record_type ASC`)
	if err != nil {
		return nil, errors.New("read local retention records")
	}
	defer rows.Close()
	var items []retentionItem
	for rows.Next() {
		var item retentionItem
		var occurredAt string
		var protectionBase sql.NullString
		var pinned int
		if err := rows.Scan(
			&item.recordType,
			&item.recordID,
			&occurredAt,
			&item.sequence,
			&item.bytes,
			&protectionBase,
			&pinned,
		); err != nil {
			return nil, errors.New("read local retention record")
		}
		item.occurredAt, err = parseProjectionTime(occurredAt)
		if err != nil {
			return nil, errors.New("decode local retention timestamp")
		}
		if protectionBase.Valid {
			item.protectedUntil, err = parseProjectionTime(protectionBase.String)
			if err != nil {
				return nil, errors.New("decode local retention protection")
			}
			item.protectedUntil = item.protectedUntil.Add(time.Hour)
		}
		item.pinned = pinned == 1
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read local retention records")
	}
	return items, nil
}

func evaluateRetention(
	policy RetentionPolicy,
	now time.Time,
	items []retentionItem,
	dependencies map[string][]string,
) RetentionDiagnostics {
	diagnostics := RetentionDiagnostics{
		Policy:      policy,
		EvaluatedAt: now,
	}
	if len(items) > 0 {
		oldest := items[0].occurredAt
		newest := items[len(items)-1].occurredAt
		diagnostics.OldestPayloadAt = &oldest
		diagnostics.NewestPayloadAt = &newest
	}
	recordIndexes := make(map[string]int)
	for index, item := range items {
		diagnostics.CurrentPayloadBytes += item.bytes
		if item.recordType == "event" ||
			item.recordType == "finding" ||
			item.recordType == "issue" {
			recordIndexes[item.recordID] = index
		}
		if item.recordType == "event" {
			diagnostics.CurrentEventCount++
		} else if item.recordType == "finding" {
			diagnostics.CurrentFindingCount++
		} else if item.recordType == "transcript_turn" {
			diagnostics.CurrentTranscriptTurnCount++
			diagnostics.CurrentTranscriptPayloadBytes += item.bytes
		}
	}

	remainingEvents := diagnostics.CurrentEventCount
	remainingBytes := diagnostics.CurrentPayloadBytes
	var cutoff time.Time
	if policy.MaxAge > 0 {
		cutoff = now.Add(-policy.MaxAge)
	}
	selectItem := func(index int) {
		item := &items[index]
		if item.selected {
			return
		}
		item.selected = true
		remainingBytes -= item.bytes
		if item.recordType == "event" {
			remainingEvents--
		}
	}
	for index := range items {
		item := &items[index]
		if item.selected {
			continue
		}
		if !item.protectedUntil.IsZero() && now.Before(item.protectedUntil) {
			continue
		}
		if item.pinned {
			continue
		}
		expired := policy.MaxAge > 0 && item.occurredAt.Before(cutoff)
		overCount := policy.MaxEventCount > 0 &&
			item.recordType == "event" &&
			remainingEvents > policy.MaxEventCount
		overBytes := policy.MaxPayloadBytes > 0 &&
			remainingBytes > policy.MaxPayloadBytes
		if !expired && !overCount && !overBytes {
			continue
		}
		if item.recordType == "event" {
			protectedDependency := false
			for _, dependentID := range dependencies[item.recordID] {
				if dependentIndex, ok := recordIndexes[dependentID]; ok {
					dependent := items[dependentIndex]
					if dependent.pinned ||
						(!dependent.protectedUntil.IsZero() &&
							now.Before(dependent.protectedUntil)) {
						protectedDependency = true
						break
					}
				}
			}
			if protectedDependency {
				continue
			}
		}
		selectItem(index)
		if item.recordType == "event" {
			for _, dependentID := range dependencies[item.recordID] {
				if dependentIndex, ok := recordIndexes[dependentID]; ok {
					dependent := items[dependentIndex]
					if dependent.protectedUntil.IsZero() ||
						!now.Before(dependent.protectedUntil) {
						selectItem(dependentIndex)
					}
				}
			}
		}
	}
	for _, item := range items {
		if !item.selected {
			continue
		}
		diagnostics.EligiblePayloadBytes += item.bytes
		if item.recordType == "event" {
			diagnostics.EligibleEventCount++
		} else if item.recordType == "finding" {
			diagnostics.EligibleFindingCount++
		} else if item.recordType == "transcript_turn" {
			diagnostics.EligibleTranscriptTurnCount++
			diagnostics.EligibleTranscriptPayloadBytes += item.bytes
		}
	}
	return diagnostics
}

func affectedTranscriptRetentionSessions(
	ctx context.Context,
	tx *sql.Tx,
	items []retentionItem,
) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, item := range items {
		if !item.selected || item.recordType != "transcript_turn" {
			continue
		}
		var sessionKey string
		if err := tx.QueryRowContext(ctx, `
			SELECT session_key
			FROM transcript_turns
			WHERE turn_id = ?`,
			item.recordID,
		).Scan(&sessionKey); err != nil {
			return nil, errors.New("resolve retained transcript session")
		}
		result[sessionKey] = struct{}{}
	}
	return result, nil
}

func affectedRetentionSessions(
	ctx context.Context,
	tx *sql.Tx,
	items []retentionItem,
) (map[string]struct{}, bool, error) {
	result := make(map[string]struct{})
	issueDeleted := false
	for _, item := range items {
		if !item.selected {
			continue
		}
		var sessionKey sql.NullString
		switch item.recordType {
		case "event":
			if err := tx.QueryRowContext(ctx,
				"SELECT session_key FROM events WHERE event_id = ?",
				item.recordID,
			).Scan(&sessionKey); err != nil {
				return nil, false, errors.New("resolve retained event session")
			}
		case "finding":
			if err := tx.QueryRowContext(ctx,
				"SELECT session_key FROM findings WHERE finding_id = ?",
				item.recordID,
			).Scan(&sessionKey); err != nil {
				return nil, false, errors.New("resolve retained finding session")
			}
		case "issue":
			issueDeleted = true
			if err := tx.QueryRowContext(ctx,
				"SELECT session_key FROM issue_occurrences WHERE revision_id = ?",
				item.recordID,
			).Scan(&sessionKey); err != nil {
				return nil, false, errors.New("resolve retained issue session")
			}
		default:
			continue
		}
		if sessionKey.Valid && sessionKey.String != "" {
			result[sessionKey.String] = struct{}{}
		}
	}
	return result, issueDeleted, nil
}

func (s *Store) runPostPruneMaintenance(ctx context.Context) MaintenanceResult {
	pending := func(code string) MaintenanceResult {
		return MaintenanceResult{State: "pending", Code: code, Retryable: true}
	}
	if err := s.truncateWAL(ctx); err != nil {
		if errors.Is(err, ErrMaintenanceBusy) {
			return pending("wal_checkpoint_busy")
		}
		return pending("wal_checkpoint_failed")
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		if isSQLiteBusy(err) {
			return pending("vacuum_busy")
		}
		return pending("vacuum_failed")
	}
	if err := s.truncateWAL(ctx); err != nil {
		if errors.Is(err, ErrMaintenanceBusy) {
			return pending("post_vacuum_checkpoint_busy")
		}
		return pending("post_vacuum_checkpoint_failed")
	}
	return MaintenanceResult{State: "complete"}
}
