// Package local owns Belay Local's SQLite persistence and repository queries.
// It accepts only canonical events and payload-free operational records.
package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/commandsafe"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/limits"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db      *sql.DB
	cipher  *payloadCipher
	storeID string
	clock   func() time.Time
	random  io.Reader
}

type OpenOptions struct {
	KeyProvider KeyProvider
	Random      io.Reader
	Clock       func() time.Time
}

type Finding struct {
	FindingID        string
	SourceRunID      string
	SessionKey       string
	ProjectScopeHint string `json:"-"`
	DetectedAt       time.Time
	RuleID           string
	RuleVersion      string
	Severity         string
	SourceAgent      string
	Confidence       string
	CitedEventIDs    []string
}

type ImportSummary struct {
	SourceRunID       string
	Status            string
	Complete          bool
	ArtifactsScanned  int
	EventsEmitted     int
	FindingsEmitted   int
	IndicatorsEmitted int
	Diagnostics       int
}

type Quarantine struct {
	SourceRunID  string
	LineNumber   int64
	Category     string
	Reason       string
	RecordSHA256 string
}

var ErrUnresolvedCitation = errors.New("finding citation could not be resolved")

type AppendEventResult struct {
	Inserted       bool
	EventID        string
	ReadGeneration int64
}

type RecordFindingResult struct {
	Inserted        bool
	SessionKey      string
	ScopeBackfilled bool
}

var (
	ErrFindingCitationLimit    = errors.New("finding citation count exceeds limit")
	ErrFindingIdentityConflict = errors.New("finding identity conflicts with persisted record")
)

func Open(path string, keyProvider KeyProvider) (*Store, error) {
	return OpenWithOptions(path, OpenOptions{KeyProvider: keyProvider})
}

func OpenWithOptions(path string, options OpenOptions) (*Store, error) {
	if options.KeyProvider == nil {
		return nil, errors.New("local key provider is required")
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := openGuardedSQLite(dsn)
	if err != nil {
		return nil, fmt.Errorf("open local database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect local database: %w", err)
	}
	if _, err := db.Exec("PRAGMA secure_delete = ON"); err != nil {
		_ = db.Close()
		return nil, errors.New("enable secure local deletion")
	}
	store := &Store{db: db, clock: options.Clock, random: options.Random}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.installMutationGuards(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.initializeEncryptedPayloads(
		context.Background(),
		options.KeyProvider,
		options.Random,
	); err != nil {
		if store.cipher != nil {
			store.cipher.close()
		}
		_ = db.Close()
		return nil, err
	}
	if err := store.repriceTranscriptCosts(context.Background()); err != nil {
		store.cipher.close()
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) nowUTC() time.Time {
	return s.clock().UTC()
}

func sqliteDSN(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("local database path is required")
	}

	var databaseURL url.URL
	if path == ":memory:" {
		databaseURL = url.URL{Scheme: "file", Opaque: ":memory:"}
	} else {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve local database path: %w", err)
		}
		databaseURL = url.URL{Scheme: "file", Path: sqliteURIPath(filepath.ToSlash(absolute))}
	}

	query := databaseURL.Query()
	query.Set("_busy_timeout", "5000")
	query.Set("_foreign_keys", "on")
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "FULL")
	query.Set("_txlock", "exclusive")
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String(), nil
}

// sqliteURIPath roots a slash-separated absolute path for a file: URI. A
// Windows drive path such as C:/profile/belay.sqlite must become
// /C:/profile/belay.sqlite; otherwise the URI renders as file://C:/... and
// SQLite rejects "C:" as the URI authority.
func sqliteURIPath(slashed string) string {
	if strings.HasPrefix(slashed, "/") {
		return slashed
	}
	return "/" + slashed
}

func (s *Store) Close() error {
	if s.cipher != nil {
		s.cipher.close()
	}
	return s.db.Close()
}

func (s *Store) AppendEvent(ctx context.Context, event model.Event) (bool, error) {
	result, err := s.AppendEventResolved(ctx, event)
	return result.Inserted, err
}

func (s *Store) AppendEventResolved(
	ctx context.Context,
	event model.Event,
) (AppendEventResult, error) {
	occurredAtOrder, validOrder := projectionOrderNS(event.OccurredAt)
	if !validOrder {
		return AppendEventResult{}, errors.New("canonical event time is outside supported range")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return AppendEventResult{}, fmt.Errorf("encode canonical event: %w", err)
	}
	body, err = s.cipher.seal("event", event.EventID, "canonical_json", body)
	if err != nil {
		return AppendEventResult{}, err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AppendEventResult{}, errors.New("begin canonical event persistence")
	}
	defer tx.Rollback()
	var appendResult AppendEventResult
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO events (
				event_id, source_deduplication_key, schema_version, installation_id,
				session_key, occurred_at, observed_at, source_sequence, event_type,
				actor, action, outcome, source_agent, source_kind, source_record_id,
				source_run_id, historical, canonical_json, canonical_encoding, created_at,
				occurred_at_order_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_deduplication_key) DO NOTHING`,
			event.EventID,
			event.Source.DeduplicationKey,
			event.SchemaVersion,
			event.InstallationID,
			event.Session.Key,
			event.OccurredAt.Format(time.RFC3339Nano),
			event.ObservedAt.Format(time.RFC3339Nano),
			event.Source.Sequence,
			event.Observation.Type,
			event.Observation.Actor,
			event.Observation.Action,
			event.Observation.Outcome,
			event.Source.Agent,
			event.Source.Kind,
			event.Source.RecordID,
			event.Source.RunID,
			boolInt(event.Historical.IsHistorical),
			body,
			payloadEncodingAESGCM,
			now,
			occurredAtOrder,
		)
		if err != nil {
			return fmt.Errorf("append canonical event: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect canonical event persistence")
		}
		appendResult.Inserted = inserted == 1
		if appendResult.Inserted {
			readOrder, err := tx.ExecContext(ctx,
				"INSERT INTO event_read_order (event_id) VALUES (?)",
				event.EventID,
			)
			if err != nil {
				return errors.New("record canonical event read order")
			}
			appendResult.ReadGeneration, err = readOrder.LastInsertId()
			if err != nil {
				return errors.New("inspect canonical event read order")
			}
			appendResult.EventID = event.EventID
			if _, err := s.markSessionDirtyTx(
				ctx,
				tx,
				event.Session.Key,
				"event_appended",
				s.sessionScopeQualityTx(ctx, tx, event.Session.Key),
				now,
			); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRowContext(ctx, `
				SELECT e.event_id, read_order.sequence
				FROM events e
				JOIN event_read_order read_order ON read_order.event_id = e.event_id
				WHERE e.source_deduplication_key = ?`,
				event.Source.DeduplicationKey,
			).Scan(&appendResult.EventID, &appendResult.ReadGeneration); err != nil {
				return errors.New("resolve duplicate canonical event")
			}
		}
		return nil
	}); err != nil {
		return AppendEventResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppendEventResult{}, errors.New("commit canonical event persistence")
	}
	return appendResult, nil
}

func (s *Store) TouchImportRun(ctx context.Context, runID string) error {
	if runID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO import_runs (source_run_id, first_seen_at, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(source_run_id) DO UPDATE SET updated_at = excluded.updated_at`,
		runID, now, now,
	)
	return err
}

func (s *Store) RecordImportSummary(ctx context.Context, summary ImportSummary) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO import_runs (
			source_run_id, status, complete, artifacts_scanned, events_emitted,
			findings_emitted, indicators_emitted, diagnostics, first_seen_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_run_id) DO UPDATE SET
			status = excluded.status,
			complete = excluded.complete,
			artifacts_scanned = excluded.artifacts_scanned,
			events_emitted = excluded.events_emitted,
			findings_emitted = excluded.findings_emitted,
			indicators_emitted = excluded.indicators_emitted,
			diagnostics = excluded.diagnostics,
			updated_at = excluded.updated_at`,
		summary.SourceRunID,
		summary.Status,
		boolInt(summary.Complete),
		summary.ArtifactsScanned,
		summary.EventsEmitted,
		summary.FindingsEmitted,
		summary.IndicatorsEmitted,
		summary.Diagnostics,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("record import summary: %w", err)
	}
	return nil
}

func (s *Store) RecordFinding(ctx context.Context, finding Finding) (bool, error) {
	result, err := s.RecordFindingResolved(ctx, finding)
	return result.Inserted, err
}

func (s *Store) RecordFindingResolved(
	ctx context.Context,
	finding Finding,
) (RecordFindingResult, error) {
	if finding.FindingID == "" || finding.SourceRunID == "" ||
		finding.SessionKey == "" || len(finding.CitedEventIDs) == 0 {
		return RecordFindingResult{}, ErrUnresolvedCitation
	}
	if len(finding.CitedEventIDs) > limits.MaxFindingCitedEventIDs {
		return RecordFindingResult{}, ErrFindingCitationLimit
	}
	if finding.ProjectScopeHint != "" && !validProjectScopeHint(finding.ProjectScopeHint) {
		return RecordFindingResult{}, errors.New("finding project scope hint is invalid")
	}
	cited, err := json.Marshal(finding.CitedEventIDs)
	if err != nil {
		return RecordFindingResult{}, err
	}
	cited, err = s.cipher.seal("finding", finding.FindingID, "cited_event_ids_json", cited)
	if err != nil {
		return RecordFindingResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecordFindingResult{}, errors.New("begin finding persistence")
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO findings (
			finding_id, source_run_id, session_key, detected_at, rule_id,
			rule_version, severity, source_agent, confidence,
			project_scope_hint, cited_event_ids_json, cited_event_ids_encoding, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(finding_id) DO NOTHING`,
		finding.FindingID,
		finding.SourceRunID,
		nullable(finding.SessionKey),
		finding.DetectedAt.UTC().Format(time.RFC3339Nano),
		finding.RuleID,
		finding.RuleVersion,
		finding.Severity,
		finding.SourceAgent,
		finding.Confidence,
		nullable(finding.ProjectScopeHint),
		cited,
		payloadEncodingAESGCM,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return RecordFindingResult{}, fmt.Errorf("record finding: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return RecordFindingResult{}, errors.New("inspect finding persistence")
	}
	if inserted == 0 {
		persisted, err := s.findingIdentityTx(ctx, tx, finding.FindingID)
		if err != nil {
			return RecordFindingResult{}, err
		}
		if !sameFindingIdentity(persisted, finding) {
			return RecordFindingResult{}, ErrFindingIdentityConflict
		}
		recordResult := RecordFindingResult{SessionKey: persisted.SessionKey}
		if finding.ProjectScopeHint != "" {
			if err := withMutationTx(ctx, tx, mutationPayloadUpgrade, func() error {
				update, err := tx.ExecContext(ctx, `
					UPDATE findings
					SET project_scope_hint = ?
					WHERE finding_id = ?
						AND (project_scope_hint IS NULL OR project_scope_hint = '')`,
					finding.ProjectScopeHint,
					finding.FindingID,
				)
				if err == nil {
					updated, rowsErr := update.RowsAffected()
					if rowsErr != nil {
						return rowsErr
					}
					recordResult.ScopeBackfilled = updated == 1
				}
				return err
			}); err != nil {
				return RecordFindingResult{}, errors.New("backfill duplicate finding project scope")
			}
		}
		if err := tx.Commit(); err != nil {
			return RecordFindingResult{}, errors.New("complete duplicate finding persistence")
		}
		return recordResult, nil
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO finding_read_order (finding_id) VALUES (?)",
		finding.FindingID,
	); err != nil {
		return RecordFindingResult{}, errors.New("record finding read order")
	}
	for _, eventID := range finding.CitedEventIDs {
		var matching int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM events
			WHERE event_id = ? AND source_run_id = ? AND session_key = ?`,
			eventID,
			finding.SourceRunID,
			finding.SessionKey,
		).Scan(&matching); err != nil {
			return RecordFindingResult{}, errors.New("validate canonical finding citation")
		}
		if matching != 1 {
			return RecordFindingResult{}, ErrUnresolvedCitation
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO finding_event_citations (finding_id, event_id)
			VALUES (?, ?)`,
			finding.FindingID,
			eventID,
		); err != nil {
			return RecordFindingResult{}, errors.New("persist canonical finding citation")
		}
	}
	if err := tx.Commit(); err != nil {
		return RecordFindingResult{}, errors.New("commit finding persistence")
	}
	return RecordFindingResult{
		Inserted:   true,
		SessionKey: finding.SessionKey,
	}, nil
}

func (s *Store) findingIdentityTx(
	ctx context.Context,
	tx *sql.Tx,
	findingID string,
) (Finding, error) {
	var result Finding
	var sessionKey sql.NullString
	var detectedAt, encoding string
	var cited []byte
	err := tx.QueryRowContext(ctx, `
		SELECT finding_id, source_run_id, session_key, detected_at, rule_id,
			rule_version, severity, source_agent, confidence,
			cited_event_ids_json, cited_event_ids_encoding
		FROM findings
		WHERE finding_id = ?`,
		findingID,
	).Scan(
		&result.FindingID,
		&result.SourceRunID,
		&sessionKey,
		&detectedAt,
		&result.RuleID,
		&result.RuleVersion,
		&result.Severity,
		&result.SourceAgent,
		&result.Confidence,
		&cited,
		&encoding,
	)
	if err != nil {
		return Finding{}, errors.New("read duplicate finding identity")
	}
	result.SessionKey = sessionKey.String
	result.DetectedAt, err = time.Parse(time.RFC3339Nano, detectedAt)
	if err != nil {
		return Finding{}, errors.New("decode duplicate finding timestamp")
	}
	cited, err = s.cipher.open(
		"finding",
		findingID,
		"cited_event_ids_json",
		encoding,
		cited,
	)
	if err != nil {
		return Finding{}, err
	}
	if err := json.Unmarshal(cited, &result.CitedEventIDs); err != nil {
		return Finding{}, errors.New("decode duplicate finding citations")
	}
	return result, nil
}

func sameFindingIdentity(persisted, incoming Finding) bool {
	return persisted.FindingID == incoming.FindingID &&
		persisted.SourceRunID == incoming.SourceRunID &&
		persisted.SessionKey == incoming.SessionKey &&
		persisted.DetectedAt.UTC().Equal(incoming.DetectedAt.UTC()) &&
		persisted.RuleID == incoming.RuleID &&
		persisted.RuleVersion == incoming.RuleVersion &&
		persisted.Severity == incoming.Severity &&
		persisted.SourceAgent == incoming.SourceAgent &&
		persisted.Confidence == incoming.Confidence &&
		slices.Equal(persisted.CitedEventIDs, incoming.CitedEventIDs)
}

func (s *Store) ResolveCanonicalEventIDs(
	ctx context.Context,
	sourceRunID string,
	sessionKey string,
	sourceRecordIDs []string,
) ([]string, error) {
	if sourceRunID == "" || sessionKey == "" || len(sourceRecordIDs) == 0 {
		return nil, ErrUnresolvedCitation
	}
	resolved := make([]string, 0, len(sourceRecordIDs))
	for _, sourceRecordID := range sourceRecordIDs {
		rows, err := s.db.QueryContext(ctx, `
			SELECT event_id
			FROM events
			WHERE source_run_id = ? AND session_key = ? AND source_record_id = ?
			ORDER BY event_id
			LIMIT 2`,
			sourceRunID,
			sessionKey,
			sourceRecordID,
		)
		if err != nil {
			return nil, errors.New("resolve canonical finding citation")
		}
		var matches []string
		for rows.Next() {
			var eventID string
			if err := rows.Scan(&eventID); err != nil {
				rows.Close()
				return nil, errors.New("resolve canonical finding citation")
			}
			matches = append(matches, eventID)
		}
		if err := rows.Close(); err != nil {
			return nil, errors.New("resolve canonical finding citation")
		}
		if len(matches) != 1 {
			return nil, ErrUnresolvedCitation
		}
		resolved = append(resolved, matches[0])
	}
	return resolved, nil
}

func (s *Store) RecordQuarantine(ctx context.Context, quarantine Quarantine) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO quarantine (
			source_run_id, line_number, category, reason, record_sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		nullable(quarantine.SourceRunID),
		quarantine.LineNumber,
		quarantine.Category,
		quarantine.Reason,
		quarantine.RecordSHA256,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) RecordDiagnostic(ctx context.Context, runID string, line int64, level, code string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO diagnostics (source_run_id, line_number, level, code, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		nullable(runID),
		line,
		level,
		code,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListSessions(ctx context.Context, limit int) ([]model.SessionSummary, time.Time, error) {
	page, err := s.QuerySessions(ctx, model.SessionQuery{Limit: limit})
	return page.Data, page.DataThrough, err
}

func (s *Store) QuerySessions(ctx context.Context, query model.SessionQuery) (model.SessionPage, error) {
	query.Limit = boundedReadLimit(query.Limit, 20, 101)
	snapshot, err := s.eventSnapshot(ctx, query.Snapshot)
	if err != nil {
		return model.SessionPage{}, err
	}
	args := []any{snapshot}
	clauses := []string{"1 = 1"}
	if query.CursorEndedAt != nil {
		clauses = append(clauses, "(ended_at < ? OR (ended_at = ? AND session_key > ?))")
		value := query.CursorEndedAt.UTC().Format(time.RFC3339Nano)
		args = append(args, value, value, query.CursorSessionID)
	}
	if query.Harness != "" {
		clauses = append(clauses, "LOWER(harness) = LOWER(?)")
		args = append(args, query.Harness)
	}
	if query.Outcome != "" {
		clauses = append(clauses, "outcome = ?")
		args = append(args, query.Outcome)
	}
	if query.History != "" {
		clauses = append(clauses, "history = ?")
		args = append(args, query.History)
	}
	if query.OccurredAfter != nil {
		clauses = append(clauses, "ended_at >= ?")
		args = append(args, query.OccurredAfter.UTC().Format(time.RFC3339Nano))
	}
	if query.OccurredBefore != nil {
		clauses = append(clauses, "started_at <= ?")
		args = append(args, query.OccurredBefore.UTC().Format(time.RFC3339Nano))
	}
	if query.Search != "" {
		search := "%" + escapeLike(strings.ToLower(query.Search)) + "%"
		clauses = append(clauses,
			"(LOWER(session_key) LIKE ? ESCAPE '\\' OR LOWER(harness) LIKE ? ESCAPE '\\')")
		args = append(args, search, search)
	}
	args = append(args, query.Limit)

	rows, err := s.db.QueryContext(ctx, `
		WITH session_rows AS (
			SELECT
				e.session_key,
				MIN(e.source_agent) AS harness,
				MIN(e.occurred_at) AS started_at,
				MAX(e.occurred_at) AS ended_at,
				COUNT(*) AS event_count,
				CASE
					WHEN SUM(CASE WHEN e.event_type = 'session.end' THEN 1 ELSE 0 END) = 0
						THEN 'incomplete'
					WHEN SUM(CASE WHEN e.event_type = 'session.end' AND e.outcome = 'failed' THEN 1 ELSE 0 END) > 0
						THEN 'failed'
					WHEN SUM(CASE WHEN e.event_type = 'session.end' AND e.outcome = 'interrupted' THEN 1 ELSE 0 END) > 0
						THEN 'interrupted'
					WHEN SUM(CASE WHEN e.event_type = 'session.end' AND e.outcome = 'succeeded' THEN 1 ELSE 0 END) > 0
						THEN 'succeeded'
					ELSE 'unknown'
				END AS outcome,
				MAX(e.historical) AS historical,
				CASE
					WHEN MIN(e.historical) = 1 THEN 'historical'
					WHEN MAX(e.historical) = 0 THEN 'live'
					ELSE 'mixed'
				END AS history
			FROM events e
			JOIN event_read_order read_order ON read_order.event_id = e.event_id
			WHERE read_order.sequence <= ?
			GROUP BY e.session_key
		)
		SELECT session_key, harness, started_at, ended_at, event_count,
			outcome, historical, history
		FROM session_rows
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY ended_at DESC, session_key ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.SessionPage{}, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []model.SessionSummary
	for rows.Next() {
		summary, err := scanSessionSummary(rows)
		if err != nil {
			return model.SessionPage{}, err
		}
		sessions = append(sessions, summary)
	}
	if err := rows.Err(); err != nil {
		return model.SessionPage{}, err
	}
	dataThrough, err := s.dataThroughAtSnapshot(ctx, snapshot)
	return model.SessionPage{
		Data:        sessions,
		Snapshot:    snapshot,
		DataThrough: dataThrough,
	}, err
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (model.SessionSummary, time.Time, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.SessionSummary{}, time.Time{}, errors.New("begin session read")
	}
	defer tx.Rollback()

	summary, err := querySessionSummary(ctx, tx, sessionID)
	if err != nil {
		return model.SessionSummary{}, time.Time{}, err
	}
	overview, err := s.querySessionOverview(ctx, tx, summary)
	if err != nil {
		return model.SessionSummary{}, time.Time{}, err
	}
	summary.Overview = &overview
	dataThrough, err := dataThroughQuery(ctx, tx, "")
	if err != nil {
		return model.SessionSummary{}, time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.SessionSummary{}, time.Time{}, errors.New("complete session read")
	}
	return summary, dataThrough, nil
}

func (s *Store) GetSessionTimeline(ctx context.Context, sessionID string, limit int) ([]model.Event, time.Time, error) {
	page, err := s.QuerySessionTimeline(ctx, model.TimelineQuery{
		SessionID: sessionID,
		Limit:     limit,
	})
	return page.Data, page.DataThrough, err
}

func (s *Store) QuerySessionTimeline(ctx context.Context, query model.TimelineQuery) (model.EventPage, error) {
	query.Limit = boundedReadLimit(query.Limit, 100, 501)
	snapshot, err := s.eventSnapshot(ctx, query.Snapshot)
	if err != nil {
		return model.EventPage{}, err
	}
	clauses := []string{"e.session_key = ?", "read_order.sequence <= ?"}
	args := []any{query.SessionID, snapshot}
	if query.Cursor != nil {
		clauses = append(clauses, `(e.source_sequence > ? OR
			(e.source_sequence = ? AND e.occurred_at > ?) OR
			(e.source_sequence = ? AND e.occurred_at = ? AND e.event_id > ?))`)
		occurredAt := query.Cursor.OccurredAt.UTC().Format(time.RFC3339Nano)
		args = append(args,
			query.Cursor.SourceSequence,
			query.Cursor.SourceSequence, occurredAt,
			query.Cursor.SourceSequence, occurredAt, query.Cursor.EventID,
		)
	}
	args = append(args, query.Limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.event_id, e.canonical_json, e.canonical_encoding
		FROM events e
		JOIN event_read_order read_order ON read_order.event_id = e.event_id
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY e.source_sequence ASC, e.occurred_at ASC, e.event_id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.EventPage{}, fmt.Errorf("get session timeline: %w", err)
	}
	events, err := s.decodeEventRows(rows)
	if err != nil {
		return model.EventPage{}, err
	}
	dataThrough, err := s.dataThroughAtSnapshot(ctx, snapshot)
	return model.EventPage{
		Data:        events,
		Snapshot:    snapshot,
		DataThrough: dataThrough,
	}, err
}

func (s *Store) QueryActivity(ctx context.Context, filter model.ActivityFilter) ([]model.Event, time.Time, error) {
	page, err := s.QueryActivityPage(ctx, model.ActivityQuery{Filter: filter})
	return page.Data, page.DataThrough, err
}

func (s *Store) QueryActivityPage(ctx context.Context, query model.ActivityQuery) (model.EventPage, error) {
	query.Filter.Limit = boundedReadLimit(query.Filter.Limit, 50, 201)
	snapshot, err := s.eventSnapshot(ctx, query.Snapshot)
	if err != nil {
		return model.EventPage{}, err
	}
	position := query.Cursor
	events := make([]model.Event, 0, query.Filter.Limit)
	const scanChunk = 256

	for len(events) < query.Filter.Limit {
		clauses := []string{"read_order.sequence <= ?"}
		args := []any{snapshot}
		if query.Filter.OccurredAfter != nil {
			clauses = append(clauses, "e.occurred_at >= ?")
			args = append(args, query.Filter.OccurredAfter.UTC().Format(time.RFC3339Nano))
		}
		if query.Filter.OccurredBefore != nil {
			clauses = append(clauses, "e.occurred_at <= ?")
			args = append(args, query.Filter.OccurredBefore.UTC().Format(time.RFC3339Nano))
		}
		if query.Filter.Harness != "" {
			clauses = append(clauses, "LOWER(e.source_agent) = LOWER(?)")
			args = append(args, query.Filter.Harness)
		}
		if query.Filter.Outcome != "" {
			clauses = append(clauses, "e.outcome = ?")
			args = append(args, query.Filter.Outcome)
		}
		if position != nil {
			clauses = append(clauses, `(e.occurred_at < ? OR
				(e.occurred_at = ? AND e.source_sequence < ?) OR
				(e.occurred_at = ? AND e.source_sequence = ? AND e.event_id < ?))`)
			occurredAt := position.OccurredAt.UTC().Format(time.RFC3339Nano)
			args = append(args,
				occurredAt,
				occurredAt, position.SourceSequence,
				occurredAt, position.SourceSequence, position.EventID,
			)
		}
		args = append(args, scanChunk)
		rows, err := s.db.QueryContext(ctx, `
			SELECT e.event_id, e.canonical_json, e.canonical_encoding,
				e.occurred_at, e.source_sequence
			FROM events e
			JOIN event_read_order read_order ON read_order.event_id = e.event_id
			WHERE `+strings.Join(clauses, " AND ")+`
			ORDER BY e.occurred_at DESC, e.source_sequence DESC, e.event_id DESC
			LIMIT ?`,
			args...,
		)
		if err != nil {
			return model.EventPage{}, fmt.Errorf("query local activity: %w", err)
		}
		scanned := 0
		for rows.Next() {
			scanned++
			var eventID, encoding, occurredAt string
			var sequence int64
			var body []byte
			if err := rows.Scan(&eventID, &body, &encoding, &occurredAt, &sequence); err != nil {
				rows.Close()
				return model.EventPage{}, err
			}
			parsedAt, err := time.Parse(time.RFC3339Nano, occurredAt)
			if err != nil {
				rows.Close()
				return model.EventPage{}, errors.New("decode stored event timestamp")
			}
			position = &model.EventPosition{
				OccurredAt:     parsedAt,
				SourceSequence: sequence,
				EventID:        eventID,
			}
			event, err := s.decodeEvent(eventID, encoding, body)
			if err != nil {
				rows.Close()
				return model.EventPage{}, err
			}
			if query.Filter.ResourceKind != "" &&
				(event.Observation.Resource == nil ||
					!strings.EqualFold(event.Observation.Resource.Kind, query.Filter.ResourceKind)) {
				continue
			}
			events = append(events, event)
			if len(events) == query.Filter.Limit {
				break
			}
		}
		if err := rows.Close(); err != nil {
			return model.EventPage{}, err
		}
		if len(events) == query.Filter.Limit || scanned < scanChunk {
			break
		}
	}
	dataThrough, err := s.dataThroughAtSnapshot(ctx, snapshot)
	return model.EventPage{
		Data:        events,
		Snapshot:    snapshot,
		DataThrough: dataThrough,
	}, err
}

func (s *Store) ListFindings(ctx context.Context, limit int) ([]model.FindingSummary, time.Time, error) {
	page, err := s.QueryFindings(ctx, model.FindingQuery{
		Filter: model.FindingFilter{Limit: limit},
	})
	return page.Data, page.DataThrough, err
}

func (s *Store) QueryFindings(ctx context.Context, query model.FindingQuery) (model.FindingPage, error) {
	page, _, err := s.queryFindings(ctx, query, 0)
	return page, err
}

// QueryFindingsForAnalysis reads only a bounded prefix of citations from each
// legacy finding payload. The boolean result reports whether any payload in
// the page exceeded the canonical per-finding limit.
func (s *Store) QueryFindingsForAnalysis(
	ctx context.Context,
	query model.FindingQuery,
) (model.FindingPage, bool, error) {
	return s.queryFindings(ctx, query, limits.MaxFindingCitedEventIDs)
}

func (s *Store) queryFindings(
	ctx context.Context,
	query model.FindingQuery,
	citationLimit int,
) (model.FindingPage, bool, error) {
	query.Filter.Limit = boundedReadLimit(query.Filter.Limit, 20, 101)
	snapshot, err := s.findingSnapshot(ctx, query.Snapshot)
	if err != nil {
		return model.FindingPage{}, false, err
	}
	clauses := []string{"read_order.sequence <= ?"}
	args := []any{snapshot}
	if query.Filter.DetectedAfter != nil {
		clauses = append(clauses, "f.detected_at >= ?")
		args = append(args, query.Filter.DetectedAfter.UTC().Format(time.RFC3339Nano))
	}
	if query.Filter.Severity != "" {
		clauses = append(clauses, "LOWER(f.severity) = LOWER(?)")
		args = append(args, query.Filter.Severity)
	}
	if query.Filter.SessionID != "" {
		clauses = append(clauses, "f.session_key = ?")
		args = append(args, query.Filter.SessionID)
	}
	if query.Cursor != nil {
		clauses = append(clauses,
			"(f.detected_at < ? OR (f.detected_at = ? AND f.finding_id < ?))")
		detectedAt := query.Cursor.DetectedAt.UTC().Format(time.RFC3339Nano)
		args = append(args, detectedAt, detectedAt, query.Cursor.FindingID)
	}
	args = append(args, query.Filter.Limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.finding_id, COALESCE(f.session_key, ''), f.detected_at, f.rule_id,
			f.rule_version, f.severity, f.source_agent, f.confidence,
			COALESCE(f.project_scope_hint, ''),
			f.cited_event_ids_json, f.cited_event_ids_encoding
		FROM findings f
		JOIN finding_read_order read_order ON read_order.finding_id = f.finding_id
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY f.detected_at DESC, f.finding_id DESC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.FindingPage{}, false, fmt.Errorf("list local findings: %w", err)
	}
	defer rows.Close()
	findings := make([]model.FindingSummary, 0, query.Filter.Limit)
	citationsTruncated := false
	for rows.Next() {
		finding, truncated, err := s.scanFinding(rows, citationLimit)
		if err != nil {
			return model.FindingPage{}, false, err
		}
		citationsTruncated = citationsTruncated || truncated
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return model.FindingPage{}, false, err
	}
	dataThrough, err := s.dataThrough(ctx)
	return model.FindingPage{
		Data:        findings,
		Snapshot:    snapshot,
		DataThrough: dataThrough,
	}, citationsTruncated, err
}

type rowScanner interface {
	Scan(...any) error
}

func scanSessionSummary(row rowScanner) (model.SessionSummary, error) {
	var summary model.SessionSummary
	var startedAt, endedAt string
	var historical int
	if err := row.Scan(
		&summary.SessionID,
		&summary.Harness,
		&startedAt,
		&endedAt,
		&summary.EventCount,
		&summary.Outcome,
		&historical,
		&summary.History,
	); err != nil {
		return model.SessionSummary{}, err
	}
	var err error
	summary.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return model.SessionSummary{}, errors.New("decode stored session start")
	}
	summary.EndedAt, err = time.Parse(time.RFC3339Nano, endedAt)
	if err != nil {
		return model.SessionSummary{}, errors.New("decode stored session end")
	}
	summary.Historical = historical == 1
	return summary, nil
}

func querySessionSummary(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
) (model.SessionSummary, error) {
	return scanSessionSummary(tx.QueryRowContext(ctx, `
		SELECT
			session_key,
			MIN(source_agent),
			MIN(occurred_at),
			MAX(occurred_at),
			COUNT(*),
			CASE
				WHEN SUM(CASE WHEN event_type = 'session.end' THEN 1 ELSE 0 END) = 0
					THEN 'incomplete'
				WHEN SUM(CASE WHEN event_type = 'session.end' AND outcome = 'failed' THEN 1 ELSE 0 END) > 0
					THEN 'failed'
				WHEN SUM(CASE WHEN event_type = 'session.end' AND outcome = 'interrupted' THEN 1 ELSE 0 END) > 0
					THEN 'interrupted'
				WHEN SUM(CASE WHEN event_type = 'session.end' AND outcome = 'succeeded' THEN 1 ELSE 0 END) > 0
					THEN 'succeeded'
				ELSE 'unknown'
			END,
			MAX(historical),
			CASE
				WHEN MIN(historical) = 1 THEN 'historical'
				WHEN MAX(historical) = 0 THEN 'live'
				ELSE 'mixed'
			END
		FROM events
		WHERE session_key = ?
		GROUP BY session_key`,
		sessionID,
	))
}

func (s *Store) querySessionOverview(
	ctx context.Context,
	tx *sql.Tx,
	summary model.SessionSummary,
) (model.SessionOverview, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT event_id, event_type, outcome, canonical_json, canonical_encoding
		FROM events
		WHERE session_key = ?
		ORDER BY source_sequence ASC, occurred_at ASC, event_id ASC`,
		summary.SessionID,
	)
	if err != nil {
		return model.SessionOverview{}, errors.New("read session overview")
	}
	defer rows.Close()

	type resourceKey struct {
		kind string
		name string
	}
	resources := make(map[resourceKey]int)
	depths := make(map[string]struct{})
	confidences := make(map[string]struct{})
	var counts model.SessionInsightCounts
	for rows.Next() {
		var eventID, eventType, outcome, encoding string
		var body []byte
		if err := rows.Scan(&eventID, &eventType, &outcome, &body, &encoding); err != nil {
			return model.SessionOverview{}, err
		}
		switch eventType {
		case "command.exec":
			counts.Commands++
		case "tool.call":
			counts.ToolCalls++
		case "file.read":
			counts.FileReads++
		case "file.write":
			counts.FileWrites++
		case "file.delete":
			counts.FileDeletes++
		case "network.indicator":
			counts.NetworkIndicators++
		case "permission.requested", "permission.approved", "permission.denied":
			counts.PermissionEvents++
		}
		if outcome == "failed" {
			counts.ExplicitFailedEvents++
		}
		if outcome == "unknown" {
			counts.SourceUnreportedOutcomes++
		}
		event, err := s.decodeEvent(eventID, encoding, body)
		if err != nil {
			return model.SessionOverview{}, err
		}
		if event.Observation.Resource != nil &&
			event.Observation.Resource.Kind != "" &&
			event.Observation.Resource.Name != "" {
			resources[resourceKey{
				kind: event.Observation.Resource.Kind,
				name: event.Observation.Resource.Name,
			}]++
		}
		if event.Coverage.Depth != "" {
			depths[event.Coverage.Depth] = struct{}{}
		}
		if event.Coverage.Confidence != "" {
			confidences[event.Coverage.Confidence] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return model.SessionOverview{}, err
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM findings WHERE session_key = ?",
		summary.SessionID,
	).Scan(&counts.Findings); err != nil {
		return model.SessionOverview{}, errors.New("count session findings")
	}

	salient := make([]model.SalientResource, 0, len(resources))
	for resource, count := range resources {
		salient = append(salient, model.SalientResource{
			Kind:       resource.kind,
			Name:       resource.name,
			EventCount: count,
		})
	}
	sort.Slice(salient, func(i, j int) bool {
		if salient[i].EventCount != salient[j].EventCount {
			return salient[i].EventCount > salient[j].EventCount
		}
		if salient[i].Kind != salient[j].Kind {
			return salient[i].Kind < salient[j].Kind
		}
		return salient[i].Name < salient[j].Name
	})
	const maxSalientResources = 20
	truncated := len(salient) > maxSalientResources
	if truncated {
		salient = salient[:maxSalientResources]
	}

	return model.SessionOverview{
		Counts:                    counts,
		SalientResources:          salient,
		SalientResourcesTruncated: truncated,
		ObservedCoverage: model.ObservedCoverage{
			Depths:      sortedSet(depths),
			Confidences: sortedSet(confidences),
		},
		Outcome: outcomeExplanation(summary.Outcome),
	}, nil
}

func outcomeExplanation(outcome string) model.OutcomeExplanation {
	result := model.OutcomeExplanation{Value: outcome}
	switch outcome {
	case "incomplete":
		result.Source = "absence_of_session_end"
		result.Explanation = "The agent reported that the session ended but did not report an outcome."
	case "succeeded":
		result.Source = "session.end"
		result.Explanation = "The agent reported that this session completed successfully."
	case "failed":
		result.Source = "session.end"
		result.Explanation = "The agent reported that this session ended with a failure."
	case "interrupted":
		result.Source = "session.end"
		result.Explanation = "The agent reported that this session was interrupted."
	case "unknown":
		result.Source = "session.end"
		result.Explanation = "The agent did not report how this session ended."
	default:
		result.Value = "unknown"
		result.Source = "session.end"
		result.Explanation = "The agent reported that the session ended but did not report an outcome."
	}
	return result
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s *Store) decodeEventRows(rows *sql.Rows) ([]model.Event, error) {
	defer rows.Close()
	var events []model.Event
	for rows.Next() {
		var eventID, encoding string
		var body []byte
		if err := rows.Scan(&eventID, &body, &encoding); err != nil {
			return nil, err
		}
		event, err := s.decodeEvent(eventID, encoding, body)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) decodeEvent(eventID, encoding string, body []byte) (model.Event, error) {
	body, err := s.cipher.open("event", eventID, "canonical_json", encoding, body)
	if err != nil {
		return model.Event{}, err
	}
	var event model.Event
	if err := json.Unmarshal(body, &event); err != nil {
		return model.Event{}, fmt.Errorf("decode stored canonical event: %w", err)
	}
	normalizeDecodedCommand(&event)
	return event, nil
}

func normalizeDecodedCommand(event *model.Event) {
	if event == nil ||
		(event.Observation.Type != "command.exec" &&
			event.Observation.Type != "command.result") {
		return
	}
	candidate := event.Observation.Summary
	if candidate == "" &&
		event.Observation.Resource != nil &&
		event.Observation.Resource.Kind == "command" {
		candidate = event.Observation.Resource.Name
	}
	display := commandsafe.Normalize(candidate)
	if display.Executable == "" {
		event.Observation.Summary = ""
		event.Observation.Resource = nil
		return
	}
	event.Observation.Summary = display.Summary
	event.Observation.Resource = &model.Resource{
		Kind: "command",
		Name: display.Executable,
	}
}

func (s *Store) scanFinding(
	row rowScanner,
	citationLimit int,
) (model.FindingSummary, bool, error) {
	var finding model.FindingSummary
	var detectedAt, encoding string
	var cited []byte
	if err := row.Scan(
		&finding.FindingID,
		&finding.SessionID,
		&detectedAt,
		&finding.RuleID,
		&finding.RuleVersion,
		&finding.Severity,
		&finding.Harness,
		&finding.Confidence,
		&finding.ProjectScopeHint,
		&cited,
		&encoding,
	); err != nil {
		return model.FindingSummary{}, false, err
	}
	var err error
	finding.DetectedAt, err = time.Parse(time.RFC3339Nano, detectedAt)
	if err != nil {
		return model.FindingSummary{}, false, errors.New("decode stored finding timestamp")
	}
	cited, err = s.cipher.open(
		"finding",
		finding.FindingID,
		"cited_event_ids_json",
		encoding,
		cited,
	)
	if err != nil {
		return model.FindingSummary{}, false, err
	}
	var truncated bool
	finding.CitedEventIDs, truncated, err = decodeFindingCitations(cited, citationLimit)
	if err != nil {
		return model.FindingSummary{}, false, errors.New("decode stored finding payload")
	}
	return finding, truncated, nil
}

func decodeFindingCitations(body []byte, limit int) ([]string, bool, error) {
	if limit <= 0 {
		var result []string
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, false, err
		}
		return result, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, false, errors.New("finding citations are not an array")
	}
	result := make([]string, 0, limit)
	for decoder.More() {
		if len(result) == limit {
			return result, true, nil
		}
		var eventID string
		if err := decoder.Decode(&eventID); err != nil {
			return nil, false, err
		}
		result = append(result, eventID)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return nil, false, errors.New("finding citations have an invalid terminator")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("finding citations contain trailing data")
	}
	return result, false, nil
}

func (s *Store) eventSnapshot(ctx context.Context, requested int64) (int64, error) {
	if requested > 0 {
		return requested, nil
	}
	var snapshot int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(sequence), 0) FROM event_read_order",
	).Scan(&snapshot)
	return snapshot, err
}

func (s *Store) findingSnapshot(ctx context.Context, requested int64) (int64, error) {
	if requested > 0 {
		return requested, nil
	}
	var snapshot int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(sequence), 0) FROM finding_read_order",
	).Scan(&snapshot)
	return snapshot, err
}

func (s *Store) dataThroughAtSnapshot(ctx context.Context, snapshot int64) (time.Time, error) {
	return dataThroughQuery(ctx, s.db, `
		JOIN event_read_order read_order ON read_order.event_id = events.event_id
		WHERE read_order.sequence <= ?`,
		snapshot,
	)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func dataThroughQuery(
	ctx context.Context,
	queryer queryRower,
	suffix string,
	args ...any,
) (time.Time, error) {
	var value sql.NullString
	if err := queryer.QueryRowContext(
		ctx,
		"SELECT MAX(events.observed_at) FROM events "+suffix,
		args...,
	).Scan(&value); err != nil {
		return time.Time{}, err
	}
	if !value.Valid {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value.String)
}

func boundedReadLimit(value, fallback, maximum int) int {
	if value <= 0 || value > maximum {
		return fallback
	}
	return value
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func (s *Store) GetStats(ctx context.Context) (model.LocalStats, time.Time, error) {
	stats := model.LocalStats{
		HarnessCounts: make(map[string]int),
		OutcomeCounts: make(map[string]int),
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(DISTINCT session_key),
			(SELECT COUNT(*) FROM findings),
			COUNT(DISTINCT CASE WHEN historical = 1 THEN session_key END)
		FROM events`,
	).Scan(
		&stats.EventCount,
		&stats.SessionCount,
		&stats.FindingCount,
		&stats.HistoricalRuns,
	); err != nil {
		return model.LocalStats{}, time.Time{}, fmt.Errorf("read local stats: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT source_agent, COUNT(*)
		FROM events
		GROUP BY source_agent
		ORDER BY source_agent`)
	if err != nil {
		return model.LocalStats{}, time.Time{}, fmt.Errorf("read local harness stats: %w", err)
	}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			rows.Close()
			return model.LocalStats{}, time.Time{}, err
		}
		stats.HarnessCounts[key] = count
	}
	if err := rows.Close(); err != nil {
		return model.LocalStats{}, time.Time{}, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT outcome, COUNT(*)
		FROM events
		GROUP BY outcome
		ORDER BY outcome`)
	if err != nil {
		return model.LocalStats{}, time.Time{}, fmt.Errorf("read local outcome stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return model.LocalStats{}, time.Time{}, err
		}
		stats.OutcomeCounts[key] = count
	}
	if err := rows.Err(); err != nil {
		return model.LocalStats{}, time.Time{}, err
	}
	dataThrough, err := s.dataThrough(ctx)
	return stats, dataThrough, err
}

func (s *Store) GetFinding(ctx context.Context, findingID string) (Finding, error) {
	var result Finding
	var sessionKey sql.NullString
	var detectedAt, encoding string
	var cited []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT finding_id, source_run_id, session_key, detected_at, rule_id,
			rule_version, severity, source_agent, confidence,
			COALESCE(project_scope_hint, ''),
			cited_event_ids_json, cited_event_ids_encoding
		FROM findings
		WHERE finding_id = ?`,
		findingID,
	).Scan(
		&result.FindingID,
		&result.SourceRunID,
		&sessionKey,
		&detectedAt,
		&result.RuleID,
		&result.RuleVersion,
		&result.Severity,
		&result.SourceAgent,
		&result.Confidence,
		&result.ProjectScopeHint,
		&cited,
		&encoding,
	)
	if err != nil {
		return Finding{}, err
	}
	result.SessionKey = sessionKey.String
	result.DetectedAt, err = time.Parse(time.RFC3339Nano, detectedAt)
	if err != nil {
		return Finding{}, errors.New("decode stored finding timestamp")
	}
	cited, err = s.cipher.open(
		"finding",
		findingID,
		"cited_event_ids_json",
		encoding,
		cited,
	)
	if err != nil {
		return Finding{}, err
	}
	if err := json.Unmarshal(cited, &result.CitedEventIDs); err != nil {
		return Finding{}, errors.New("decode stored finding payload")
	}
	return result, nil
}

func (s *Store) ImportRun(ctx context.Context, runID string) (ImportSummary, error) {
	var result ImportSummary
	var complete int
	err := s.db.QueryRowContext(ctx, `
		SELECT source_run_id, status, complete, artifacts_scanned, events_emitted,
			findings_emitted, indicators_emitted, diagnostics
		FROM import_runs WHERE source_run_id = ?`,
		runID,
	).Scan(
		&result.SourceRunID,
		&result.Status,
		&complete,
		&result.ArtifactsScanned,
		&result.EventsEmitted,
		&result.FindingsEmitted,
		&result.IndicatorsEmitted,
		&result.Diagnostics,
	)
	result.Complete = complete == 1
	return result, err
}

func (s *Store) Quarantines(ctx context.Context) ([]Quarantine, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(source_run_id, ''), line_number, category, reason, record_sha256
		FROM quarantine
		ORDER BY id`)
	if err != nil {
		return nil, errors.New("list quarantine diagnostics")
	}
	defer rows.Close()
	var result []Quarantine
	for rows.Next() {
		var quarantine Quarantine
		if err := rows.Scan(
			&quarantine.SourceRunID,
			&quarantine.LineNumber,
			&quarantine.Category,
			&quarantine.Reason,
			&quarantine.RecordSHA256,
		); err != nil {
			return nil, errors.New("read quarantine diagnostic")
		}
		result = append(result, quarantine)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list quarantine diagnostics")
	}
	return result, nil
}

func (s *Store) DiagnosticCodes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT code FROM diagnostics ORDER BY id")
	if err != nil {
		return nil, errors.New("list diagnostic codes")
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, errors.New("read diagnostic code")
		}
		result = append(result, code)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list diagnostic codes")
	}
	return result, nil
}

func (s *Store) Count(ctx context.Context, table string) (int, error) {
	allowed := map[string]bool{
		"events": true, "findings": true, "import_runs": true,
		"quarantine": true, "diagnostics": true, "local_store_metadata": true,
		"finding_event_citations": true,
		"session_scopes":          true, "event_enrichments": true,
		"dirty_sessions": true, "session_analysis_revisions": true,
		"issue_occurrences": true, "issue_occurrence_events": true,
		"issue_projection_metadata": true, "analysis_diagnostics": true,
		"fix_annotations": true, "fix_annotation_events": true,
		"fix_annotation_retractions": true,
	}
	if !allowed[table] {
		return 0, errors.New("unsupported count table")
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count)
	return count, err
}

func (s *Store) dataThrough(ctx context.Context) (time.Time, error) {
	var value sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT MAX(observed_at) FROM events").Scan(&value); err != nil {
		return time.Time{}, err
	}
	if !value.Valid {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value.String)
}

func (s *Store) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for index, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := index + 1
		var applied int
		err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
		).Scan(&applied)
		if err != nil && !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied > 0 {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		apply := func() error {
			_, err := tx.ExecContext(ctx, string(body))
			return err
		}
		if version == 9 {
			err = withMutationTx(ctx, tx, mutationProjectionRebuild, apply)
		} else if version == 25 || version == 27 {
			err = withMutationTx(ctx, tx, mutationTranscriptIngestion, apply)
		} else if version == 28 || version == 30 {
			err = withMutationTx(ctx, tx, mutationCostIssueAnalysis, apply)
		} else {
			err = apply()
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if version == issueSummaryMigrationVersion {
			epoch, err := newIssueCursorEpoch(s.random)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE issue_summary_metadata
				SET cursor_epoch = ?, updated_at = ?
				WHERE singleton = 1 AND cursor_epoch IS NULL`,
				epoch,
				formatProjectionTime(s.nowUTC()),
			); err != nil {
				_ = tx.Rollback()
				return errors.New("initialize issue cursor epoch")
			}
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			version, time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if err := s.resumeFixRecurrenceMigration(ctx); err != nil {
		return err
	}
	return s.resumeValueFirstMigration(ctx)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
