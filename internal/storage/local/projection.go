package local

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

var (
	ErrNoDirtySession          = errors.New("no dirty session is ready")
	ErrStaleProjection         = errors.New("session projection generation is stale")
	ErrEventEnrichmentConflict = errors.New("event enrichment conflicts with persisted metadata")
	fixedCodePattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	opaqueIdentityPattern      = regexp.MustCompile(`^(psc|cmd|ifp|iss)_[a-z2-7]{52}$`)
)

type EventEnrichmentConflictError struct {
	EventID string
	Field   string
}

func (e *EventEnrichmentConflictError) Error() string {
	return "event enrichment conflict"
}

func (e *EventEnrichmentConflictError) Unwrap() error {
	return ErrEventEnrichmentConflict
}

var allowedCommandClasses = map[string]bool{
	"test": true, "build": true, "typecheck": true, "lint": true,
	"format_check": true, "other": true, "unknown": true,
}

var allowedPermissionClasses = map[string]bool{
	"permission.shell":   true,
	"permission.file":    true,
	"permission.network": true,
	"permission.mcp":     true,
	"permission.tool":    true,
	"permission.unknown": true,
}

type SessionScope struct {
	SessionKey           string
	ProjectScopeID       string
	NormalizationVersion string
	Quality              model.ScopeQuality
}

type EnrichmentUpsertResult struct {
	Changed          bool
	TargetGeneration int64
}

type ScopeUpsertResult struct {
	Scope            SessionScope
	Changed          bool
	TargetGeneration int64
}

type DirtySession struct {
	SessionKey       string
	TargetGeneration int64
	Reason           string
	State            string
	AttemptCount     int
}

type ProjectionReplacement struct {
	SessionKey        string
	Origin            string
	ClaimedGeneration int64
	Status            model.AnalysisStatus
	ScopeQuality      model.ScopeQuality
	Occurrences       []model.IssueOccurrence
}

type SessionProjectionReplacement struct {
	SessionKey              string
	ClaimedGeneration       int64
	Status                  model.AnalysisStatus
	ScopeQuality            model.ScopeQuality
	AnalysisThroughOrderNS  *int64
	AnalyzedEventGeneration int64
	BelayOccurrences        []model.IssueOccurrence
	NumbatOccurrences       []model.IssueOccurrence
	Capabilities            []model.AnalysisCapability
}

type ProjectionCommitResult struct {
	ProjectionGeneration int64
	OccurrenceCount      int
}

func (s *Store) UpsertSessionScope(
	ctx context.Context,
	sessionKey string,
	scope ProjectScope,
) (ScopeUpsertResult, error) {
	if sessionKey == "" || !validScope(scope) {
		return ScopeUpsertResult{}, errors.New("invalid session scope")
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScopeUpsertResult{}, errors.New("begin session scope persistence")
	}
	defer tx.Rollback()

	var result ScopeUpsertResult
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var existing SessionScope
		var existingID sql.NullString
		readErr := tx.QueryRowContext(ctx, `
			SELECT session_key, project_scope_id, normalization_version, scope_quality
			FROM session_scopes
			WHERE session_key = ?`,
			sessionKey,
		).Scan(
			&existing.SessionKey,
			&existingID,
			&existing.NormalizationVersion,
			&existing.Quality,
		)
		existing.ProjectScopeID = existingID.String
		switch {
		case errors.Is(readErr, sql.ErrNoRows):
			result.Scope = SessionScope{
				SessionKey:           sessionKey,
				ProjectScopeID:       scope.ID,
				NormalizationVersion: scope.NormalizationVersion,
				Quality:              scope.Quality,
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO session_scopes (
					session_key, project_scope_id, normalization_version,
					scope_quality, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?)`,
				sessionKey,
				nullable(scope.ID),
				scope.NormalizationVersion,
				scope.Quality,
				now,
				now,
			); err != nil {
				return errors.New("insert session scope")
			}
			result.Changed = true
		case readErr != nil:
			return errors.New("read session scope")
		default:
			result.Scope = mergeSessionScope(existing, scope)
			result.Changed = result.Scope.ProjectScopeID != existing.ProjectScopeID ||
				result.Scope.Quality != existing.Quality ||
				result.Scope.NormalizationVersion != existing.NormalizationVersion
			if result.Changed {
				if _, err := tx.ExecContext(ctx, `
					UPDATE session_scopes
					SET project_scope_id = ?, normalization_version = ?,
						scope_quality = ?, updated_at = ?
					WHERE session_key = ?`,
					nullable(result.Scope.ProjectScopeID),
					result.Scope.NormalizationVersion,
					result.Scope.Quality,
					now,
					sessionKey,
				); err != nil {
					return errors.New("update session scope")
				}
				if result.Scope.Quality == model.ScopeConflict {
					if err := recordAnalysisDiagnosticTx(
						ctx,
						tx,
						sessionKey,
						"project_scope",
						"conflicting_project_scope",
						now,
					); err != nil {
						return err
					}
				}
			}
		}
		if result.Changed {
			if _, err := tx.ExecContext(ctx, `
					UPDATE issue_projection_metadata
					SET retention_generation = retention_generation + 1
					WHERE singleton = 1`); err != nil {
				return errors.New("expire monitoring snapshots after scope change")
			}
			result.TargetGeneration, err = s.markSessionDirtyTx(
				ctx,
				tx,
				sessionKey,
				"scope_changed",
				result.Scope.Quality,
				now,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ScopeUpsertResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScopeUpsertResult{}, errors.New("commit session scope persistence")
	}
	return result, nil
}

func (s *Store) GetSessionScope(ctx context.Context, sessionKey string) (SessionScope, error) {
	var result SessionScope
	var projectScopeID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT session_key, project_scope_id, normalization_version, scope_quality
		FROM session_scopes
		WHERE session_key = ?`,
		sessionKey,
	).Scan(
		&result.SessionKey,
		&projectScopeID,
		&result.NormalizationVersion,
		&result.Quality,
	)
	result.ProjectScopeID = projectScopeID.String
	return result, err
}

func (s *Store) UpsertEventEnrichment(
	ctx context.Context,
	eventID string,
	enrichment model.EventEnrichment,
) (EnrichmentUpsertResult, error) {
	if eventID == "" || enrichment.Version == "" {
		return EnrichmentUpsertResult{}, errors.New("event enrichment requires event and version")
	}
	if enrichment.CommandClass == "" {
		enrichment.CommandClass = "unknown"
	}
	if enrichment.PermissionClass == "" {
		enrichment.PermissionClass = "permission.unknown"
	}
	if enrichment.CommandSignatureID != "" &&
		(!opaqueIdentityPattern.MatchString(enrichment.CommandSignatureID) ||
			!strings.HasPrefix(enrichment.CommandSignatureID, "cmd_")) ||
		!allowedCommandClasses[enrichment.CommandClass] ||
		!allowedPermissionClasses[enrichment.PermissionClass] ||
		!fixedCodePattern.MatchString(enrichment.Version) {
		return EnrichmentUpsertResult{}, errors.New("event enrichment contains unsupported metadata")
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EnrichmentUpsertResult{}, errors.New("begin event enrichment persistence")
	}
	defer tx.Rollback()
	var result EnrichmentUpsertResult
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var sessionKey string
		if err := tx.QueryRowContext(ctx,
			"SELECT session_key FROM events WHERE event_id = ?",
			eventID,
		).Scan(&sessionKey); err != nil {
			return errors.New("resolve enriched event")
		}
		var existing model.EventEnrichment
		var existingSignature sql.NullString
		readErr := tx.QueryRowContext(ctx, `
			SELECT command_signature_id, command_class, permission_class,
				enrichment_version
			FROM event_enrichments WHERE event_id = ?`,
			eventID,
		).Scan(
			&existingSignature,
			&existing.CommandClass,
			&existing.PermissionClass,
			&existing.Version,
		)
		existing.CommandSignatureID = existingSignature.String
		persisted := enrichment
		switch {
		case errors.Is(readErr, sql.ErrNoRows):
			payload, err := s.sealEventEnrichment(eventID, persisted)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO event_enrichments (
					event_id, command_signature_id, command_class, permission_class,
					enrichment_version, enrichment_payload, enrichment_encoding,
					created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				eventID,
				nullable(enrichment.CommandSignatureID),
				enrichment.CommandClass,
				enrichment.PermissionClass,
				enrichment.Version,
				payload,
				payloadEncodingAESGCM,
				now,
				now,
			); err != nil {
				return errors.New("insert event enrichment")
			}
			result.Changed = true
		case readErr != nil:
			return errors.New("read event enrichment")
		default:
			persisted, err = mergeEventEnrichment(eventID, existing, enrichment)
			if err != nil {
				return err
			}
			result.Changed = existing != persisted
			if result.Changed {
				payload, err := s.sealEventEnrichment(eventID, persisted)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `
					UPDATE event_enrichments
					SET command_signature_id = ?, command_class = ?,
						permission_class = ?, enrichment_version = ?,
						enrichment_payload = ?, enrichment_encoding = ?, updated_at = ?
					WHERE event_id = ?`,
					nullable(persisted.CommandSignatureID),
					persisted.CommandClass,
					persisted.PermissionClass,
					persisted.Version,
					payload,
					payloadEncodingAESGCM,
					now,
					eventID,
				); err != nil {
					return errors.New("update event enrichment")
				}
			}
		}
		if result.Changed {
			scopeQuality := s.sessionScopeQualityTx(ctx, tx, sessionKey)
			result.TargetGeneration, err = s.markSessionDirtyTx(
				ctx,
				tx,
				sessionKey,
				"enrichment_changed",
				scopeQuality,
				now,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return EnrichmentUpsertResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return EnrichmentUpsertResult{}, errors.New("commit event enrichment persistence")
	}
	return result, nil
}

func (s *Store) sealEventEnrichment(
	eventID string,
	enrichment model.EventEnrichment,
) ([]byte, error) {
	payload, err := json.Marshal(enrichment)
	if err != nil {
		return nil, errors.New("encode event enrichment")
	}
	return s.cipher.seal("event_enrichment", eventID, "enrichment_payload", payload)
}

func mergeEventEnrichment(
	eventID string,
	existing model.EventEnrichment,
	incoming model.EventEnrichment,
) (model.EventEnrichment, error) {
	result := existing
	merge := func(field, empty string, current, next *string) error {
		switch {
		case *current == "" || *current == empty:
			if *next != "" && *next != empty {
				*current = *next
			}
		case *next == "" || *next == empty || *next == *current:
		default:
			return &EventEnrichmentConflictError{EventID: eventID, Field: field}
		}
		return nil
	}
	if err := merge(
		"command_signature_id",
		"",
		&result.CommandSignatureID,
		&incoming.CommandSignatureID,
	); err != nil {
		return model.EventEnrichment{}, err
	}
	if err := merge(
		"command_class",
		"unknown",
		&result.CommandClass,
		&incoming.CommandClass,
	); err != nil {
		return model.EventEnrichment{}, err
	}
	if err := merge(
		"permission_class",
		"permission.unknown",
		&result.PermissionClass,
		&incoming.PermissionClass,
	); err != nil {
		return model.EventEnrichment{}, err
	}
	if incoming.Version != "" {
		result.Version = incoming.Version
	}
	return result, nil
}

func (s *Store) EventEnrichments(
	ctx context.Context,
	sessionKey string,
) (map[string]model.EventEnrichment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ee.event_id, ee.enrichment_payload, ee.enrichment_encoding
		FROM event_enrichments ee
		JOIN events e ON e.event_id = ee.event_id
		WHERE e.session_key = ?
		ORDER BY ee.event_id`,
		sessionKey,
	)
	if err != nil {
		return nil, errors.New("read event enrichments")
	}
	defer rows.Close()
	result := make(map[string]model.EventEnrichment)
	for rows.Next() {
		var eventID, encoding string
		var payload []byte
		if err := rows.Scan(&eventID, &payload, &encoding); err != nil {
			return nil, errors.New("read event enrichment")
		}
		payload, err = s.cipher.open(
			"event_enrichment",
			eventID,
			"enrichment_payload",
			encoding,
			payload,
		)
		if err != nil {
			return nil, err
		}
		var enrichment model.EventEnrichment
		if err := json.Unmarshal(payload, &enrichment); err != nil {
			return nil, errors.New("decode event enrichment")
		}
		result[eventID] = enrichment
	}
	return result, rows.Err()
}

func (s *Store) MarkSessionDirty(
	ctx context.Context,
	sessionKey string,
	reason string,
) (int64, error) {
	if sessionKey == "" || !fixedCodePattern.MatchString(reason) {
		return 0, errors.New("invalid dirty-session request")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("begin dirty-session persistence")
	}
	defer tx.Rollback()
	var generation int64
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		generation, err = s.markSessionDirtyTx(
			ctx,
			tx,
			sessionKey,
			reason,
			s.sessionScopeQualityTx(ctx, tx, sessionKey),
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		err = tx.Commit()
	}
	return generation, err
}

func (s *Store) ClaimDirtySession(
	ctx context.Context,
	now time.Time,
) (DirtySession, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DirtySession{}, errors.New("begin dirty-session claim")
	}
	defer tx.Rollback()
	var result DirtySession
	var retryAt sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT session_key, target_generation, dirty_reason, state,
			attempt_count, retry_at
		FROM dirty_sessions
		WHERE state IN ('pending', 'failed')
			AND (retry_at IS NULL OR retry_at <= ?)
		ORDER BY updated_at, session_key
		LIMIT 1`,
		formatProjectionTime(now),
	).Scan(
		&result.SessionKey,
		&result.TargetGeneration,
		&result.Reason,
		&result.State,
		&result.AttemptCount,
		&retryAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DirtySession{}, ErrNoDirtySession
	}
	if err != nil {
		return DirtySession{}, errors.New("read dirty-session work")
	}
	updated, err := tx.ExecContext(ctx, `
		UPDATE dirty_sessions
		SET state = 'claimed', attempt_count = attempt_count + 1,
			claimed_at = ?, updated_at = ?
		WHERE session_key = ? AND target_generation = ?
			AND state IN ('pending', 'failed')`,
		formatProjectionTime(now),
		formatProjectionTime(now),
		result.SessionKey,
		result.TargetGeneration,
	)
	if err != nil {
		return DirtySession{}, errors.New("claim dirty-session work")
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return DirtySession{}, ErrStaleProjection
	}
	result.State = "claimed"
	result.AttemptCount++
	if err := tx.Commit(); err != nil {
		return DirtySession{}, errors.New("commit dirty-session claim")
	}
	return result, nil
}

func (s *Store) ReplaceIssueProjection(
	ctx context.Context,
	replacement ProjectionReplacement,
) (ProjectionCommitResult, error) {
	if replacement.Origin == "" {
		replacement.Origin = "belay"
	}
	if replacement.Origin == "belay" {
		for index := range replacement.Occurrences {
			occurrence := &replacement.Occurrences[index]
			if occurrence.FingerprintScopeID != "" ||
				(occurrence.ScopeQuality != model.ScopeResolved &&
					occurrence.ScopeQuality != model.ScopeLexical) {
				continue
			}
			scopeID, err := s.deriveLegacySessionScopeID(replacement.SessionKey)
			if err != nil {
				return ProjectionCommitResult{}, err
			}
			occurrence.FingerprintScopeID = scopeID
		}
	}
	if err := validateProjectionReplacement(replacement); err != nil {
		return ProjectionCommitResult{}, err
	}
	return s.replaceSessionProjection(
		ctx,
		replacement.SessionKey,
		replacement.ClaimedGeneration,
		replacement.Status,
		replacement.ScopeQuality,
		map[string][]model.IssueOccurrence{
			replacement.Origin: replacement.Occurrences,
		},
		nil,
		0,
		nil,
	)
}

func (s *Store) ReplaceSessionProjection(
	ctx context.Context,
	replacement SessionProjectionReplacement,
) (ProjectionCommitResult, error) {
	for origin, occurrences := range map[string][]model.IssueOccurrence{
		"belay":  replacement.BelayOccurrences,
		"numbat": replacement.NumbatOccurrences,
	} {
		if err := validateProjectionReplacement(ProjectionReplacement{
			SessionKey:        replacement.SessionKey,
			Origin:            origin,
			ClaimedGeneration: replacement.ClaimedGeneration,
			Status:            replacement.Status,
			ScopeQuality:      replacement.ScopeQuality,
			Occurrences:       occurrences,
		}); err != nil {
			return ProjectionCommitResult{}, err
		}
	}
	if err := validateAnalysisCapabilities(
		replacement.SessionKey,
		replacement.Capabilities,
	); err != nil {
		return ProjectionCommitResult{}, err
	}
	if replacement.AnalyzedEventGeneration < 0 {
		return ProjectionCommitResult{}, errors.New("projection replacement has invalid event generation")
	}
	return s.replaceSessionProjection(
		ctx,
		replacement.SessionKey,
		replacement.ClaimedGeneration,
		replacement.Status,
		replacement.ScopeQuality,
		map[string][]model.IssueOccurrence{
			"belay":  replacement.BelayOccurrences,
			"numbat": replacement.NumbatOccurrences,
		},
		replacement.AnalysisThroughOrderNS,
		replacement.AnalyzedEventGeneration,
		replacement.Capabilities,
	)
}

func (s *Store) replaceSessionProjection(
	ctx context.Context,
	sessionKey string,
	claimedGeneration int64,
	status model.AnalysisStatus,
	scopeQuality model.ScopeQuality,
	origins map[string][]model.IssueOccurrence,
	analysisThroughOrderNS *int64,
	analyzedEventGeneration int64,
	capabilities []model.AnalysisCapability,
) (ProjectionCommitResult, error) {
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectionCommitResult{}, errors.New("begin issue projection replacement")
	}
	defer tx.Rollback()
	result := ProjectionCommitResult{}
	for _, occurrences := range origins {
		result.OccurrenceCount += len(occurrences)
	}
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var target int64
		if err := tx.QueryRowContext(ctx, `
			SELECT target_generation FROM dirty_sessions WHERE session_key = ?`,
			sessionKey,
		).Scan(&target); err != nil {
			return ErrStaleProjection
		}
		if target != claimedGeneration {
			return ErrStaleProjection
		}
		affectedIssueIDs, err := activeIssueIDsForSessionTx(ctx, tx, sessionKey)
		if err != nil {
			return err
		}
		generation, err := nextProjectionGenerationTx(ctx, tx)
		if err != nil {
			return err
		}
		result.ProjectionGeneration = generation
		if err := closeActiveAnalysisRevisionTx(
			ctx,
			tx,
			sessionKey,
			generation,
			now,
		); err != nil {
			return err
		}
		revisionID, err := insertAnalysisRevisionTx(
			ctx,
			tx,
			sessionKey,
			status,
			scopeQuality,
			target,
			target,
			"",
			generation,
			now,
			analysisThroughOrderNS,
			analyzedEventGeneration,
		)
		if err != nil {
			return err
		}
		if err := insertAnalysisCapabilitiesTx(
			ctx,
			tx,
			revisionID,
			capabilities,
		); err != nil {
			return err
		}
		originNames := make([]string, 0, len(origins))
		for origin := range origins {
			originNames = append(originNames, origin)
		}
		sort.Strings(originNames)
		for _, origin := range originNames {
			if _, err := tx.ExecContext(ctx, `
				UPDATE issue_occurrences
				SET visible_until_generation = ?, updated_at = ?
				WHERE session_key = ? AND origin = ?
					AND visible_until_generation IS NULL`,
				generation, now, sessionKey, origin,
			); err != nil {
				return errors.New("close prior issue occurrences")
			}
			for index := range origins[origin] {
				occurrence := origins[origin][index]
				if occurrence.FingerprintScopeID == "" &&
					origin == "belay" &&
					(occurrence.ScopeQuality == model.ScopeResolved ||
						occurrence.ScopeQuality == model.ScopeLexical) {
					occurrence.FingerprintScopeID = s.sessionProjectScopeIDTx(
						ctx, tx, sessionKey,
					)
				}
				if (occurrence.ScopeQuality == model.ScopeResolved ||
					occurrence.ScopeQuality == model.ScopeLexical) &&
					!validProjectScopeID(occurrence.FingerprintScopeID) {
					return errors.New("projection replacement lacks exact fingerprint scope")
				}
				occurrence.AnalysisGeneration = generation
				occurrence.AnalysisStatus = status
				if err := s.insertIssueOccurrenceTx(
					ctx, tx, sessionKey, generation, now, occurrence,
				); err != nil {
					return err
				}
			}
		}
		if err := s.enqueueRecurrenceJobTx(
			ctx,
			tx,
			sessionKey,
			revisionID,
			generation,
			now,
		); err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `
			UPDATE dirty_sessions
			SET state = ?, retry_at = NULL, claimed_at = NULL, updated_at = ?
			WHERE session_key = ? AND target_generation = ?`,
			status, now, sessionKey, target,
		)
		if err != nil {
			return errors.New("acknowledge issue projection")
		}
		if count, err := updated.RowsAffected(); err != nil || count != 1 {
			return ErrStaleProjection
		}
		if status == model.AnalysisCurrent {
			if _, err := tx.ExecContext(ctx, `
				UPDATE issue_projection_metadata
				SET last_successful_analysis_at = ?
				WHERE singleton = 1`,
				now,
			); err != nil {
				return errors.New("record successful issue analysis")
			}
		}
		currentIssueIDs, err := activeIssueIDsForSessionTx(ctx, tx, sessionKey)
		if err != nil {
			return err
		}
		mergeIssueIDs(affectedIssueIDs, currentIssueIDs)
		if err := s.refreshIssueProjectionIfReadyTx(
			ctx, tx, generation, now, affectedIssueIDs,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ProjectionCommitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectionCommitResult{}, errors.New("commit issue projection replacement")
	}
	return result, nil
}

func (s *Store) insertIssueOccurrenceTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
	generation int64,
	now string,
	occurrence model.IssueOccurrence,
) error {
	occurrence.OccurrenceID = stableLocalID(
		"occ_", sessionKey, occurrence.FingerprintID, occurrence.Origin,
	)
	revisionID := stableLocalID(
		"ior_", occurrence.OccurrenceID, fmt.Sprint(generation),
	)
	evidence, err := json.Marshal(occurrence.Evidence)
	if err != nil {
		return errors.New("encode issue occurrence evidence")
	}
	evidence, err = s.cipher.seal(
		"issue_occurrence", revisionID, "evidence_payload", evidence,
	)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
					INSERT INTO issue_occurrences (
						revision_id, occurrence_id, issue_id, fingerprint_id,
						fingerprint_version, origin, origin_record_id, session_key,
						harness, detector_id, detector_version, projection_version,
						category, title_code, source_signal_code, severity,
						confidence, scope_quality,
						first_observed_at, last_observed_at, evidence_complete,
						retained_history_only, experimental, analysis_status,
						analysis_generation, evidence_payload, evidence_encoding,
						visible_from_generation, created_at, updated_at,
						fingerprint_scope_id
					) VALUES (
						?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
						?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
					)`,
		revisionID,
		occurrence.OccurrenceID,
		occurrence.IssueID,
		occurrence.FingerprintID,
		occurrence.FingerprintVersion,
		occurrence.Origin,
		nullable(occurrence.OriginRecordID),
		sessionKey,
		occurrence.Harness,
		occurrence.Provenance.DetectorID,
		occurrence.Provenance.DetectorVersion,
		occurrence.Provenance.ProjectionVersion,
		occurrence.Category,
		occurrence.TitleCode,
		nullableOptionalString(occurrence.SourceSignalCode),
		occurrence.Severity,
		occurrence.Confidence,
		occurrence.ScopeQuality,
		formatProjectionTime(occurrence.FirstObservedAt),
		formatProjectionTime(occurrence.LastObservedAt),
		boolInt(occurrence.EvidenceComplete),
		boolInt(occurrence.RetainedHistoryOnly),
		boolInt(occurrence.Experimental),
		occurrence.AnalysisStatus,
		generation,
		evidence,
		payloadEncodingAESGCM,
		generation,
		now,
		now,
		nullable(occurrence.FingerprintScopeID),
	); err != nil {
		return fmt.Errorf("insert issue occurrence revision: %w", err)
	}
	for _, eventID := range uniqueSorted(occurrence.Evidence.CitedEventIDs) {
		var matching int
		if err := tx.QueryRowContext(ctx, `
					SELECT COUNT(*) FROM events
					WHERE event_id = ? AND session_key = ?`,
			eventID, sessionKey,
		).Scan(&matching); err != nil || matching != 1 {
			return errors.New("issue occurrence cites an unknown session event")
		}
		if _, err := tx.ExecContext(ctx, `
					INSERT INTO issue_occurrence_events (revision_id, event_id)
					VALUES (?, ?)`,
			revisionID, eventID,
		); err != nil {
			return errors.New("persist issue occurrence citation")
		}
	}
	return nil
}

func (s *Store) PublishAnalysisFailure(
	ctx context.Context,
	sessionKey string,
	targetGeneration int64,
	errorCode string,
	retryAt time.Time,
) (int64, error) {
	if sessionKey == "" || targetGeneration < 1 || !fixedCodePattern.MatchString(errorCode) {
		return 0, errors.New("invalid analysis failure")
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("begin analysis failure persistence")
	}
	defer tx.Rollback()
	var generation int64
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var target int64
		if err := tx.QueryRowContext(ctx,
			"SELECT target_generation FROM dirty_sessions WHERE session_key = ?",
			sessionKey,
		).Scan(&target); err != nil || target != targetGeneration {
			return ErrStaleProjection
		}
		affectedIssueIDs, err := activeIssueIDsForSessionTx(ctx, tx, sessionKey)
		if err != nil {
			return err
		}
		generation, err = nextProjectionGenerationTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := closeActiveAnalysisRevisionTx(ctx, tx, sessionKey, generation, now); err != nil {
			return err
		}
		analyzed := activeAnalyzedGenerationTx(ctx, tx, sessionKey, generation)
		if _, err := insertAnalysisRevisionTx(
			ctx,
			tx,
			sessionKey,
			model.AnalysisFailed,
			s.sessionScopeQualityTx(ctx, tx, sessionKey),
			target,
			analyzed,
			errorCode,
			generation,
			now,
			nil,
			target,
		); err != nil {
			return err
		}
		var retry any
		if !retryAt.IsZero() {
			retry = formatProjectionTime(retryAt)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE dirty_sessions
			SET state = 'failed', retry_at = ?, claimed_at = NULL, updated_at = ?
			WHERE session_key = ? AND target_generation = ?`,
			retry,
			now,
			sessionKey,
			target,
		); err != nil {
			return errors.New("record failed dirty-session work")
		}
		if err := s.refreshIssueProjectionIfReadyTx(
			ctx, tx, generation, now, affectedIssueIDs,
		); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		if commitErr := tx.Commit(); commitErr != nil {
			err = errors.New("commit analysis failure")
		}
	}
	return generation, err
}

func (s *Store) RecordAnalysisDiagnostic(
	ctx context.Context,
	sessionKey string,
	detectorID string,
	errorCode string,
) error {
	if sessionKey == "" ||
		!fixedCodePattern.MatchString(detectorID) ||
		!fixedCodePattern.MatchString(errorCode) {
		return errors.New("invalid analysis diagnostic")
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin analysis diagnostic persistence")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		return recordAnalysisDiagnosticTx(
			ctx,
			tx,
			sessionKey,
			detectorID,
			errorCode,
			now,
		)
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit analysis diagnostic persistence")
	}
	return nil
}

type diagnosticExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func recordAnalysisDiagnosticTx(
	ctx context.Context,
	execer diagnosticExecer,
	sessionKey string,
	detectorID string,
	errorCode string,
	now string,
) error {
	_, err := execer.ExecContext(ctx, `
		INSERT INTO analysis_diagnostics (
			session_key, detector_id, error_code, occurrence_count,
			first_observed_at, last_observed_at
		) VALUES (?, ?, ?, 1, ?, ?)
		ON CONFLICT(session_key, detector_id, error_code) DO UPDATE SET
			occurrence_count = occurrence_count + 1,
			last_observed_at = excluded.last_observed_at`,
		sessionKey,
		detectorID,
		errorCode,
		now,
		now,
	)
	if err != nil {
		return errors.New("record analysis diagnostic")
	}
	return nil
}

func (s *Store) markSessionDirtyTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
	reason string,
	scopeQuality model.ScopeQuality,
	now string,
) (int64, error) {
	if sessionKey == "" || !fixedCodePattern.MatchString(reason) {
		return 0, errors.New("invalid dirty session")
	}
	var target int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO dirty_sessions (
			session_key, target_generation, dirty_reason, state, updated_at
		) VALUES (?, 1, ?, 'pending', ?)
		ON CONFLICT(session_key) DO UPDATE SET
			target_generation = dirty_sessions.target_generation + 1,
			dirty_reason = excluded.dirty_reason,
			state = 'pending',
			retry_at = NULL,
			claimed_at = NULL,
			updated_at = excluded.updated_at
		RETURNING target_generation`,
		sessionKey,
		reason,
		now,
	).Scan(&target); err != nil {
		return 0, errors.New("mark session dirty")
	}
	affectedIssueIDs, err := activeIssueIDsForSessionTx(ctx, tx, sessionKey)
	if err != nil {
		return 0, err
	}
	generation, err := nextProjectionGenerationTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	analyzed := activeAnalyzedGenerationTx(ctx, tx, sessionKey, generation)
	if err := closeActiveAnalysisRevisionTx(ctx, tx, sessionKey, generation, now); err != nil {
		return 0, err
	}
	if _, err := insertAnalysisRevisionTx(
		ctx,
		tx,
		sessionKey,
		model.AnalysisPending,
		scopeQuality,
		target,
		analyzed,
		"",
		generation,
		now,
		nil,
		analyzed,
	); err != nil {
		return 0, err
	}
	if err := s.refreshIssueProjectionIfReadyTx(
		ctx, tx, generation, now, affectedIssueIDs,
	); err != nil {
		return 0, err
	}
	return target, nil
}

func nextProjectionGenerationTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var generation int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE issue_projection_metadata
		SET current_generation = current_generation + 1
		WHERE singleton = 1
		RETURNING current_generation`,
	).Scan(&generation); err != nil {
		return 0, errors.New("advance issue projection generation")
	}
	return generation, nil
}

func closeActiveAnalysisRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
	generation int64,
	now string,
) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_analysis_revisions
		SET visible_until_generation = ?, updated_at = ?
		WHERE session_key = ? AND visible_until_generation IS NULL`,
		generation,
		now,
		sessionKey,
	); err != nil {
		return errors.New("close session analysis revision")
	}
	return nil
}

func insertAnalysisRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
	status model.AnalysisStatus,
	scopeQuality model.ScopeQuality,
	targetGeneration int64,
	analyzedGeneration int64,
	errorCode string,
	projectionGeneration int64,
	now string,
	analysisThroughOrderNS *int64,
	analyzedEventGeneration int64,
) (string, error) {
	revisionID := stableLocalID(
		"sar_",
		sessionKey,
		fmt.Sprint(projectionGeneration),
	)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_analysis_revisions (
			revision_id, session_key, status, scope_quality, target_generation,
			analyzed_generation, error_code, visible_from_generation,
			created_at, updated_at, analysis_through_order_ns,
			analyzed_event_generation
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revisionID,
		sessionKey,
		status,
		scopeQuality,
		targetGeneration,
		analyzedGeneration,
		nullable(errorCode),
		projectionGeneration,
		now,
		now,
		analysisThroughOrderNS,
		analyzedEventGeneration,
	); err != nil {
		return "", errors.New("insert session analysis revision")
	}
	return revisionID, nil
}

func insertAnalysisCapabilitiesTx(
	ctx context.Context,
	tx *sql.Tx,
	revisionID string,
	capabilities []model.AnalysisCapability,
) error {
	for _, capability := range capabilities {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_analysis_capabilities (
				revision_id, session_id, fingerprint_scope_id, origin,
				detector_id, detector_version, fingerprint_version,
				negative_comparison_mode, analysis_through_order_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			revisionID,
			capability.SessionID,
			capability.FingerprintScopeID,
			capability.Origin,
			capability.DetectorID,
			capability.DetectorVersion,
			capability.FingerprintVersion,
			capability.NegativeComparisonMode,
			capability.AnalysisThroughOrderNS,
		); err != nil {
			return errors.New("insert session analysis capability")
		}
	}
	return nil
}

func (s *Store) enqueueRecurrenceJobTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
	revisionID string,
	projectionGeneration int64,
	now string,
) error {
	jobID, err := s.deriveFixRecurrenceJobID(sessionID, projectionGeneration)
	if err != nil {
		return err
	}
	var annotationSnapshot int64
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(sequence), 0) FROM fix_annotations",
	).Scan(&annotationSnapshot); err != nil {
		return errors.New("capture recurrence annotation snapshot")
	}
	state := "pending"
	if annotationSnapshot == 0 {
		state = "complete"
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO fix_recurrence_jobs (
			job_id, session_id, revision_id, projection_generation,
			annotation_snapshot, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO NOTHING`,
		jobID,
		sessionID,
		revisionID,
		projectionGeneration,
		annotationSnapshot,
		state,
		now,
		now,
	)
	if err != nil {
		return errors.New("enqueue recurrence job")
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return errors.New("inspect recurrence job enqueue")
	}
	if inserted == 0 {
		var existingRevision string
		if err := tx.QueryRowContext(ctx,
			"SELECT revision_id FROM fix_recurrence_jobs WHERE job_id = ?",
			jobID,
		).Scan(&existingRevision); err != nil || existingRevision != revisionID {
			return errors.New("recurrence job identity collision")
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fix_recurrence_job_events (
			job_id, event_kind, attempt_number, recorded_at
		) VALUES (?, 'queued', 0, ?)`,
		jobID,
		now,
	); err != nil {
		return errors.New("record recurrence job enqueue")
	}
	if annotationSnapshot == 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fix_recurrence_job_events (
				job_id, event_kind, attempt_number, recorded_at
			) VALUES (?, 'complete', 0, ?)`,
			jobID,
			now,
		); err != nil {
			return errors.New("complete empty recurrence job")
		}
	}
	return nil
}

func (s *Store) sessionProjectScopeIDTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
) string {
	var scope sql.NullString
	_ = tx.QueryRowContext(ctx, `
		SELECT project_scope_id
		FROM session_scopes
		WHERE session_key = ?
			AND scope_quality IN ('resolved', 'lexical')`,
		sessionID,
	).Scan(&scope)
	if validProjectScopeID(scope.String) {
		return scope.String
	}
	return ""
}

func activeAnalyzedGenerationTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
	closedAtGeneration int64,
) int64 {
	var analyzed sql.NullInt64
	_ = tx.QueryRowContext(ctx, `
		SELECT analyzed_generation
		FROM session_analysis_revisions
		WHERE session_key = ? AND visible_until_generation = ?
		ORDER BY visible_from_generation DESC
		LIMIT 1`,
		sessionKey,
		closedAtGeneration,
	).Scan(&analyzed)
	return analyzed.Int64
}

func (s *Store) sessionScopeQualityTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
) model.ScopeQuality {
	var quality model.ScopeQuality
	if err := tx.QueryRowContext(ctx, `
		SELECT scope_quality FROM session_scopes WHERE session_key = ?`,
		sessionKey,
	).Scan(&quality); err != nil {
		return model.ScopeUnscoped
	}
	return quality
}

func validScope(scope ProjectScope) bool {
	if scope.NormalizationVersion != projectScopeNormalizationVersion {
		return false
	}
	switch scope.Quality {
	case model.ScopeResolved, model.ScopeLexical:
		return strings.HasPrefix(scope.ID, "psc_") &&
			opaqueIdentityPattern.MatchString(scope.ID)
	case model.ScopeUnscoped:
		return scope.ID == ""
	case model.ScopeConflict:
		return false
	default:
		return false
	}
}

func mergeSessionScope(existing SessionScope, incoming ProjectScope) SessionScope {
	result := existing
	if existing.Quality == model.ScopeConflict {
		return result
	}
	if incoming.ID == "" {
		return result
	}
	if existing.ProjectScopeID == "" {
		result.ProjectScopeID = incoming.ID
		result.Quality = incoming.Quality
		result.NormalizationVersion = incoming.NormalizationVersion
		return result
	}
	if existing.ProjectScopeID != incoming.ID {
		result.ProjectScopeID = ""
		result.Quality = model.ScopeConflict
		result.NormalizationVersion = incoming.NormalizationVersion
		return result
	}
	if existing.Quality == model.ScopeLexical && incoming.Quality == model.ScopeResolved {
		result.Quality = model.ScopeResolved
		result.NormalizationVersion = incoming.NormalizationVersion
	}
	return result
}

func validateProjectionReplacement(replacement ProjectionReplacement) error {
	if replacement.SessionKey == "" || replacement.ClaimedGeneration < 1 {
		return errors.New("projection replacement requires session and generation")
	}
	if replacement.Origin != "belay" && replacement.Origin != "numbat" {
		return errors.New("projection replacement has invalid origin")
	}
	if replacement.Status != model.AnalysisCurrent &&
		replacement.Status != model.AnalysisTruncated {
		return errors.New("projection replacement requires current or truncated status")
	}
	if len(replacement.Occurrences) > 100 {
		return errors.New("projection replacement exceeds occurrence limit")
	}
	seen := make(map[string]struct{}, len(replacement.Occurrences))
	for _, occurrence := range replacement.Occurrences {
		fingerprintDigest := strings.TrimPrefix(occurrence.FingerprintID, "ifp_")
		issueDigest := strings.TrimPrefix(occurrence.IssueID, "iss_")
		if occurrence.SessionID != replacement.SessionKey ||
			occurrence.Origin != replacement.Origin ||
			!strings.HasPrefix(occurrence.IssueID, "iss_") ||
			!opaqueIdentityPattern.MatchString(occurrence.IssueID) ||
			!strings.HasPrefix(occurrence.FingerprintID, "ifp_") ||
			!opaqueIdentityPattern.MatchString(occurrence.FingerprintID) ||
			fingerprintDigest != issueDigest ||
			!fixedCodePattern.MatchString(occurrence.FingerprintVersion) ||
			!fixedCodePattern.MatchString(occurrence.Provenance.DetectorID) ||
			!fixedCodePattern.MatchString(occurrence.Provenance.DetectorVersion) ||
			!fixedCodePattern.MatchString(occurrence.Provenance.ProjectionVersion) ||
			!fixedCodePattern.MatchString(occurrence.Category) ||
			!fixedCodePattern.MatchString(occurrence.TitleCode) ||
			!fixedCodePattern.MatchString(occurrence.Harness) ||
			!validSeverity(occurrence.Severity) ||
			!validConfidence(occurrence.Confidence) ||
			!validScopeQuality(occurrence.ScopeQuality) ||
			occurrence.FirstObservedAt.IsZero() ||
			occurrence.LastObservedAt.IsZero() ||
			occurrence.LastObservedAt.Before(occurrence.FirstObservedAt) {
			return errors.New("projection replacement contains an invalid occurrence")
		}
		if occurrence.SourceSignalCode != nil &&
			(occurrence.Origin != "numbat" ||
				!model.IsSafeSourceSignalCode(*occurrence.SourceSignalCode)) {
			return errors.New("projection replacement contains an invalid source signal")
		}
		if occurrence.ScopeQuality == model.ScopeResolved ||
			occurrence.ScopeQuality == model.ScopeLexical {
			if occurrence.FingerprintScopeID == "" && occurrence.Origin == "belay" {
				// Belay's exact scope can be filled transactionally from
				// session_scopes. Numbat must always carry its finding scope.
			} else if !validProjectScopeID(occurrence.FingerprintScopeID) {
				return errors.New("projection replacement lacks exact fingerprint scope")
			}
		} else if occurrence.FingerprintScopeID != "" {
			return errors.New("projection replacement has invalid fingerprint scope")
		}
		if len(occurrence.Evidence.CitedEventIDs) > 50 {
			return errors.New("issue occurrence exceeds citation limit")
		}
		for _, eventID := range occurrence.Evidence.CitedEventIDs {
			if eventID == "" {
				return errors.New("issue occurrence contains an empty citation")
			}
		}
		key := occurrence.FingerprintID + "\x00" + occurrence.Origin
		if _, ok := seen[key]; ok {
			return errors.New("projection replacement contains duplicate fingerprints")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func nullableOptionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func validateAnalysisCapabilities(
	sessionID string,
	capabilities []model.AnalysisCapability,
) error {
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability.SessionID != sessionID ||
			!validProjectScopeID(capability.FingerprintScopeID) ||
			(capability.Origin != "belay" && capability.Origin != "numbat") ||
			!fixedCodePattern.MatchString(capability.DetectorID) ||
			!fixedCodePattern.MatchString(capability.DetectorVersion) ||
			!fixedCodePattern.MatchString(capability.FingerprintVersion) ||
			(capability.NegativeComparisonMode != model.FixNegativeComparisonSupported &&
				capability.NegativeComparisonMode != model.FixNegativeComparisonPositiveOnly) {
			return errors.New("projection replacement contains an invalid capability")
		}
		key := strings.Join([]string{
			capability.FingerprintScopeID,
			capability.Origin,
			capability.DetectorID,
			capability.FingerprintVersion,
		}, "\x00")
		if _, exists := seen[key]; exists {
			return errors.New("projection replacement contains duplicate capabilities")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validSeverity(value string) bool {
	switch value {
	case "info", "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func validConfidence(value string) bool {
	switch value {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func validScopeQuality(value model.ScopeQuality) bool {
	switch value {
	case model.ScopeResolved, model.ScopeLexical, model.ScopeUnscoped, model.ScopeConflict:
		return true
	default:
		return false
	}
}

func stableLocalID(prefix string, values ...string) string {
	sum := sha256.Sum256(lengthPrefixed(values))
	return prefix + strings.ToLower(opaqueBase32.EncodeToString(sum[:]))
}

func uniqueSorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if value == "" || write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}
