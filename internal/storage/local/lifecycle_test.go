package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestEncryptedEventAndFindingRoundTripAndDatabaseCanaryAbsence(t *testing.T) {
	const (
		eventCanary = "ENCRYPTED_EVENT_PAYLOAD_CANARY_89a1d4"
	)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000001",
		"session-encryption",
		1,
		time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC),
	)
	event.Observation.Summary = eventCanary
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent() = (%t, %v), want inserted", inserted, err)
	}
	finding := Finding{
		FindingID:     "finding-encryption",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt.Add(time.Second),
		RuleID:        "rule.encryption",
		RuleVersion:   "1",
		Severity:      "medium",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}
	if inserted, err := store.RecordFinding(ctx, finding); err != nil || !inserted {
		t.Fatalf("RecordFinding() = (%t, %v), want inserted", inserted, err)
	}

	timeline, _, err := store.GetSessionTimeline(ctx, event.Session.Key, 10)
	if err != nil {
		t.Fatalf("GetSessionTimeline() error = %v", err)
	}
	if len(timeline) != 1 || timeline[0].Observation.Summary != eventCanary {
		t.Fatalf("event round trip = %+v", timeline)
	}
	roundTripFinding, err := store.GetFinding(ctx, finding.FindingID)
	if err != nil {
		t.Fatalf("GetFinding() error = %v", err)
	}
	if len(roundTripFinding.CitedEventIDs) != 1 ||
		roundTripFinding.CitedEventIDs[0] != event.EventID {
		t.Fatalf("finding round trip = %+v", roundTripFinding)
	}

	var eventBody, findingBody []byte
	var eventEncoding, findingEncoding string
	if err := store.db.QueryRowContext(ctx, `
		SELECT canonical_json, canonical_encoding
		FROM events WHERE event_id = ?`,
		event.EventID,
	).Scan(&eventBody, &eventEncoding); err != nil {
		t.Fatalf("query event ciphertext: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT cited_event_ids_json, cited_event_ids_encoding
		FROM findings WHERE finding_id = ?`,
		finding.FindingID,
	).Scan(&findingBody, &findingEncoding); err != nil {
		t.Fatalf("query finding ciphertext: %v", err)
	}
	if eventEncoding != payloadEncodingAESGCM || findingEncoding != payloadEncodingAESGCM {
		t.Fatalf("encodings = (%q, %q)", eventEncoding, findingEncoding)
	}
	if bytes.Contains(eventBody, []byte(eventCanary)) ||
		bytes.Contains(findingBody, []byte(event.EventID)) {
		t.Fatal("encrypted database columns contain plaintext canaries")
	}
	if provider.createCalls != 1 {
		t.Fatalf("key provider create calls = %d, want 1", provider.createCalls)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertSQLiteFilesExclude(t, path, eventCanary)

	movedPath := filepath.Join(filepath.Dir(path), "moved-belay.sqlite")
	if err := os.Rename(path, movedPath); err != nil {
		t.Fatalf("move encrypted database: %v", err)
	}
	reopened, err := Open(movedPath, provider)
	if err != nil {
		t.Fatalf("reopen moved database error = %v", err)
	}
	defer reopened.Close()
	if provider.createCalls != 1 {
		t.Fatalf("reopen created a replacement key; calls = %d", provider.createCalls)
	}
}

func TestEncryptedStoreFailsClosedWithMissingOrWrongKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000002",
		"session-key-failure",
		1,
		time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	missing := &memoryKeyProvider{forceMissing: true}
	if _, err := Open(path, missing); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Open() missing-key error = %v, want ErrKeyNotFound", err)
	}
	if missing.createCalls != 0 {
		t.Fatalf("encrypted store attempted replacement key creation %d times", missing.createCalls)
	}

	wrong := &memoryKeyProvider{key: bytes.Repeat([]byte{0x99}, 32)}
	if _, err := Open(path, wrong); err == nil {
		t.Fatal("Open() with wrong key unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "does not unlock this store") {
		t.Fatalf("wrong-key error = %v", err)
	}
}

func TestTranscriptPayloadEncodingParticipatesInStartupValidation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "transcript-encoding.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	session := transcriptTestSession(
		"ses_transcript_encoding_validation",
		transcript.CoverageComplete,
	)
	turn := transcriptTestTurn(
		"turn-transcript-encoding-validation",
		"source-transcript-encoding-validation",
		session.SessionKey,
		0,
		time.Date(2026, 9, 9, 11, 30, 0, 0, time.UTC),
		transcript.RoleSystem,
		transcript.Payload{JSONLByteOffset: 0},
	)
	if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationTranscriptIngestion, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE transcript_turns
			SET payload_encoding = 'unsupported.test'
			WHERE turn_id = ?`,
			turn.TurnID,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path, provider); err == nil {
		t.Fatal("Open() accepted unsupported transcript payload encoding")
	} else if !strings.Contains(err.Error(), "unsupported payload encoding") {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestExistingPlaintextPayloadsAreAtomicallyBackfilledAndScrubbed(t *testing.T) {
	const (
		eventCanary = "LEGACY_EVENT_PLAINTEXT_CANARY_1b5af8"
	)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000003",
		"session-legacy",
		3,
		time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
	)
	event.Observation.Summary = eventCanary
	finding := Finding{
		FindingID:     "finding-legacy",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt.Add(time.Second),
		RuleID:        "rule.legacy",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "medium",
		CitedEventIDs: []string{event.Source.RecordID},
	}
	createLegacyPlaintextDatabase(t, path, event, finding)

	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatalf("Open() legacy database error = %v", err)
	}
	var plaintextRows, cleanupRequired int
	if err := store.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM events WHERE canonical_encoding = ?)
			+
			(SELECT COUNT(*) FROM findings WHERE cited_event_ids_encoding = ?)`,
		payloadEncodingPlaintext,
		payloadEncodingPlaintext,
	).Scan(&plaintextRows); err != nil {
		t.Fatalf("query plaintext rows: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT plaintext_cleanup_required
		FROM local_store_metadata WHERE singleton = 1`,
	).Scan(&cleanupRequired); err != nil {
		t.Fatalf("query cleanup state: %v", err)
	}
	var activeAuthorization int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM temp.belay_mutation_authorization",
	).Scan(&activeAuthorization); err != nil {
		t.Fatalf("query mutation authorization: %v", err)
	}
	if plaintextRows != 0 || cleanupRequired != 0 || activeAuthorization != 0 {
		t.Fatalf(
			"upgrade state plaintext=%d cleanup=%d active_authorization=%d",
			plaintextRows,
			cleanupRequired,
			activeAuthorization,
		)
	}
	if citations, err := store.Count(ctx, "finding_event_citations"); err != nil {
		t.Fatalf("Count(finding_event_citations) error = %v", err)
	} else if citations != 1 {
		t.Fatalf("canonical citation rows = %d, want 1", citations)
	}
	timeline, _, err := store.GetSessionTimeline(ctx, event.Session.Key, 10)
	if err != nil {
		t.Fatalf("GetSessionTimeline() error = %v", err)
	}
	if len(timeline) != 1 || timeline[0].Observation.Summary != eventCanary {
		t.Fatalf("legacy event round trip = %+v", timeline)
	}
	roundTripFinding, err := store.GetFinding(ctx, finding.FindingID)
	if err != nil {
		t.Fatalf("GetFinding() error = %v", err)
	}
	if len(roundTripFinding.CitedEventIDs) != 1 ||
		roundTripFinding.CitedEventIDs[0] != event.EventID {
		t.Fatalf("legacy finding round trip = %+v", roundTripFinding)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertSQLiteFilesExclude(t, path, eventCanary)
}

func TestPlaintextCleanupBusyReaderPreservesRetryFlag(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cleanup-busy.sqlite")
	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	if _, err := store.db.ExecContext(ctx, "PRAGMA busy_timeout = 50"); err != nil {
		t.Fatalf("set short busy timeout: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE local_store_metadata
		SET plaintext_cleanup_required = 1
		WHERE singleton = 1`); err != nil {
		t.Fatalf("mark cleanup required: %v", err)
	}

	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	readerDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open concurrent reader: %v", err)
	}
	readerTx, err := readerDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		_ = readerDB.Close()
		t.Fatalf("begin concurrent reader: %v", err)
	}
	var storeID string
	if err := readerTx.QueryRowContext(ctx, `
		SELECT store_id FROM local_store_metadata WHERE singleton = 1`,
	).Scan(&storeID); err != nil {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("establish concurrent reader snapshot: %v", err)
	}

	err = store.removePlaintextArtifacts(ctx)
	if !errors.Is(err, ErrMaintenanceBusy) {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("removePlaintextArtifacts() error = %v, want retryable busy", err)
	}
	var cleanupRequired int
	if err := store.db.QueryRowContext(ctx, `
		SELECT plaintext_cleanup_required
		FROM local_store_metadata WHERE singleton = 1`,
	).Scan(&cleanupRequired); err != nil {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("read cleanup flag: %v", err)
	}
	if cleanupRequired != 1 {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("cleanup flag = %d, want preserved", cleanupRequired)
	}
	if err := readerTx.Rollback(); err != nil {
		_ = readerDB.Close()
		t.Fatalf("release concurrent reader: %v", err)
	}
	if err := readerDB.Close(); err != nil {
		t.Fatalf("close concurrent reader: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("restore busy timeout: %v", err)
	}
	if err := store.removePlaintextArtifacts(ctx); err != nil {
		t.Fatalf("retry plaintext cleanup: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT plaintext_cleanup_required
		FROM local_store_metadata WHERE singleton = 1`,
	).Scan(&cleanupRequired); err != nil {
		t.Fatalf("read completed cleanup flag: %v", err)
	}
	if cleanupRequired != 0 {
		t.Fatalf("cleanup flag after retry = %d, want cleared", cleanupRequired)
	}
}

func TestPlaintextBackfillFailureDoesNotLeaveMixedEncodings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-interrupted.sqlite")
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000004",
		"session-interrupted",
		1,
		time.Date(2026, 9, 7, 13, 0, 0, 0, time.UTC),
	)
	finding := Finding{
		FindingID:     "finding-interrupted",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt,
		RuleID:        "rule.interrupted",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.Source.RecordID},
	}
	createLegacyPlaintextDatabase(t, path, event, finding)
	provider := newMemoryKeyProvider()
	_, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: provider,
		// Sixteen bytes create the store ID and twelve encrypt the first
		// payload. The next payload nonce generation then fails before the
		// guarded transaction begins.
		Random: &limitedFailureReader{remaining: 28},
	})
	if err == nil {
		t.Fatal("interrupted plaintext upgrade unexpectedly succeeded")
	}

	dsn, dsnErr := sqliteDSN(path)
	if dsnErr != nil {
		t.Fatalf("sqliteDSN() error = %v", dsnErr)
	}
	db, openErr := sql.Open("sqlite", dsn)
	if openErr != nil {
		t.Fatalf("sql.Open() error = %v", openErr)
	}
	var plaintext, encrypted int
	if queryErr := db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM events WHERE canonical_encoding = 'plaintext.v0')
				+
			(SELECT COUNT(*) FROM findings WHERE cited_event_ids_encoding = 'plaintext.v0'),
			(SELECT COUNT(*) FROM events WHERE canonical_encoding = 'aes256gcm.v1')
				+
			(SELECT COUNT(*) FROM findings WHERE cited_event_ids_encoding = 'aes256gcm.v1')`,
	).Scan(&plaintext, &encrypted); queryErr != nil {
		_ = db.Close()
		t.Fatalf("query interrupted encodings: %v", queryErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close interrupted database: %v", closeErr)
	}
	if plaintext != 2 || encrypted != 0 {
		t.Fatalf("interrupted upgrade plaintext=%d encrypted=%d, want 2/0", plaintext, encrypted)
	}

	store, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: provider,
		Random:      rand.Reader,
	})
	if err != nil {
		t.Fatalf("retry plaintext upgrade error = %v", err)
	}
	defer store.Close()
}

func createLegacyPlaintextDatabase(
	t *testing.T,
	path string,
	event model.Event,
	finding Finding,
) {
	t.Helper()
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	body, err := migrationFiles.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatalf("read legacy migration: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("apply legacy migration: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?)",
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("record legacy migration: %v", err)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal legacy event: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO events (
			event_id, source_deduplication_key, schema_version, installation_id,
			session_key, occurred_at, observed_at, source_sequence, event_type,
			actor, action, outcome, source_agent, source_kind, source_record_id,
			source_run_id, historical, canonical_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		eventJSON,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	findingJSON, err := json.Marshal(finding.CitedEventIDs)
	if err != nil {
		t.Fatalf("marshal legacy finding: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO findings (
			finding_id, source_run_id, session_key, detected_at, rule_id,
			rule_version, severity, source_agent, confidence,
			cited_event_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		finding.FindingID,
		finding.SourceRunID,
		finding.SessionKey,
		finding.DetectedAt.Format(time.RFC3339Nano),
		finding.RuleID,
		finding.RuleVersion,
		finding.Severity,
		finding.SourceAgent,
		finding.Confidence,
		findingJSON,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert legacy finding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
}

func assertSQLiteFilesExclude(t *testing.T, path string, canaries ...string) {
	t.Helper()
	inspected := 0
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		body, err := os.ReadFile(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read SQLite file %s: %v", candidate, err)
		}
		inspected++
		for _, canary := range canaries {
			if bytes.Contains(body, []byte(canary)) {
				t.Errorf("%s contains plaintext canary %q", filepath.Base(candidate), canary)
			}
		}
	}
	if inspected == 0 {
		t.Fatal("no SQLite files were available for inspection")
	}
}

type memoryKeyProvider struct {
	key          []byte
	storeID      string
	forceMissing bool
	loadCalls    int
	createCalls  int
}

type limitedFailureReader struct {
	remaining int
}

func (reader *limitedFailureReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, errors.New("injected random source failure")
	}
	count := len(buffer)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		buffer[index] = byte(index + 1)
	}
	reader.remaining -= count
	return count, nil
}

func newMemoryKeyProvider() *memoryKeyProvider {
	return &memoryKeyProvider{}
}

func (provider *memoryKeyProvider) Load(_ context.Context, storeID string) ([]byte, error) {
	provider.loadCalls++
	if provider.forceMissing ||
		len(provider.key) == 0 ||
		(provider.storeID != "" && provider.storeID != storeID) {
		return nil, ErrKeyNotFound
	}
	if provider.storeID == "" {
		provider.storeID = storeID
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *memoryKeyProvider) Create(_ context.Context, storeID string) ([]byte, error) {
	provider.createCalls++
	if provider.forceMissing {
		return nil, errors.New("test key creation disabled")
	}
	if len(provider.key) != 0 {
		return nil, ErrKeyAlreadyExists
	}
	provider.key = bytes.Repeat([]byte{0x42}, 32)
	provider.storeID = storeID
	return append([]byte(nil), provider.key...), nil
}

func storageTestEvent(
	eventID string,
	sessionKey string,
	sequence int64,
	occurredAt time.Time,
) model.Event {
	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: "inst_storage_test",
		OccurredAt:     occurredAt,
		ObservedAt:     occurredAt.Add(time.Second),
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "0.3.0-test",
			SchemaVersion:    "0.3.0",
			RecordType:       "event",
			RunID:            "run-storage",
			RecordID:         "source-" + eventID,
			Kind:             "artifact",
			Agent:            "codex",
			AdapterVersion:   "numbat-0.3.0/v1",
			DeduplicationKey: fmt.Sprintf("sha256:%064x", eventID),
			Sequence:         sequence,
		},
		Session: model.SessionRef{Key: sessionKey},
		Observation: model.Observation{
			Type:    "session.start",
			Actor:   "system",
			Action:  "session",
			Outcome: "unknown",
		},
		Coverage: model.Coverage{
			Depth:      "artifact",
			Confidence: "high",
		},
		Redaction: model.Redaction{PolicyVersion: model.RedactionVersion},
		Historical: model.Historical{
			IsHistorical:         true,
			ReconstructionSource: "test",
		},
	}
}
