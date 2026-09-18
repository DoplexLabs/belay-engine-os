package local

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const keyCheckPlaintext = "belay-local-key-check-v1"

var ErrMaintenanceBusy = errors.New(
	"local database is busy; wait for other Belay work to finish, then retry",
)

type storedPayload struct {
	recordType  string
	recordID    string
	column      string
	body        []byte
	sourceRunID string
	sessionKey  string
	citations   []string
}

func (s *Store) initializeEncryptedPayloads(
	ctx context.Context,
	keyProvider KeyProvider,
	random io.Reader,
) error {
	storeID, err := s.ensureStoreID(ctx, random)
	if err != nil {
		return err
	}
	s.storeID = storeID

	plaintextCount, encryptedCount, err := s.payloadEncodingCounts(ctx)
	if err != nil {
		return err
	}
	var keyCheck []byte
	var keyCheckEncoding sql.NullString
	var cleanupRequired int
	if err := s.db.QueryRowContext(ctx, `
		SELECT key_check, key_check_encoding, plaintext_cleanup_required
		FROM local_store_metadata
		WHERE singleton = 1`,
	).Scan(&keyCheck, &keyCheckEncoding, &cleanupRequired); err != nil {
		return errors.New("read local encryption metadata")
	}

	key, err := keyProvider.Load(ctx, storeID)
	if errors.Is(err, ErrKeyNotFound) {
		if encryptedCount > 0 || len(keyCheck) > 0 {
			return ErrKeyNotFound
		}
		key, err = keyProvider.Create(ctx, storeID)
		if errors.Is(err, ErrKeyAlreadyExists) {
			key, err = keyProvider.Load(ctx, storeID)
		}
	}
	if err != nil {
		return fmt.Errorf("initialize local data key: %w", err)
	}
	defer zeroBytes(key)

	payloadCipher, err := newPayloadCipher(key, storeID, random)
	if err != nil {
		return err
	}
	s.cipher = payloadCipher

	if len(keyCheck) > 0 {
		if !keyCheckEncoding.Valid {
			return errors.New("local encryption metadata is incomplete")
		}
		plaintext, err := s.cipher.open(
			"metadata",
			"1",
			"key_check",
			keyCheckEncoding.String,
			keyCheck,
		)
		if err != nil || string(plaintext) != keyCheckPlaintext {
			return errors.New("local data key does not unlock this store")
		}
	} else if encryptedCount > 0 {
		return errors.New("local encryption metadata is incomplete")
	}

	if plaintextCount > 0 {
		if err := s.backfillPlaintextPayloads(ctx); err != nil {
			return err
		}
		cleanupRequired = 1
	} else if len(keyCheck) == 0 {
		if err := s.installKeyCheck(ctx); err != nil {
			return err
		}
	}

	if cleanupRequired == 1 {
		if err := s.removePlaintextArtifacts(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureStoreID(ctx context.Context, random io.Reader) (string, error) {
	var storeID string
	err := s.db.QueryRowContext(ctx, `
		SELECT store_id FROM local_store_metadata WHERE singleton = 1`,
	).Scan(&storeID)
	if err == nil {
		return storeID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("read local store ID")
	}
	randomBytes := make([]byte, 16)
	if _, err := io.ReadFull(random, randomBytes); err != nil {
		return "", errors.New("generate local store ID")
	}
	storeID = "store_" + hex.EncodeToString(randomBytes)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO local_store_metadata (
			singleton, store_id, created_at
		) VALUES (1, ?, ?)`,
		storeID,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", errors.New("persist local store ID")
	}
	return storeID, nil
}

func (s *Store) payloadEncodingCounts(ctx context.Context) (int, int, error) {
	var plaintext, encrypted, unsupported int
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN encoding = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN encoding = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN encoding NOT IN (?, ?) THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT canonical_encoding AS encoding FROM events
			UNION ALL
			SELECT cited_event_ids_encoding AS encoding FROM findings
			UNION ALL
			SELECT enrichment_encoding AS encoding FROM event_enrichments
			UNION ALL
			SELECT evidence_encoding AS encoding FROM issue_occurrences
			UNION ALL
			SELECT payload_encoding AS encoding FROM transcript_turns
			UNION ALL
			SELECT payload_encoding AS encoding FROM cost_issues
			UNION ALL
			SELECT payload_encoding AS encoding FROM correction_candidates
			UNION ALL
			SELECT payload_encoding AS encoding FROM insights
			UNION ALL
			SELECT payload_encoding AS encoding FROM cost_issue_fixes
			UNION ALL
			SELECT payload_encoding AS encoding FROM mission_pack_previews
			UNION ALL
			SELECT payload_encoding AS encoding FROM experience_review_actions
			)`,
		payloadEncodingPlaintext,
		payloadEncodingAESGCM,
		payloadEncodingPlaintext,
		payloadEncodingAESGCM,
	).Scan(&plaintext, &encrypted, &unsupported); err != nil {
		return 0, 0, errors.New("inspect local payload encodings")
	}
	if unsupported > 0 {
		return 0, 0, errors.New("local store contains an unsupported payload encoding")
	}
	return plaintext, encrypted, nil
}

func (s *Store) installKeyCheck(ctx context.Context) error {
	keyCheck, err := s.cipher.seal("metadata", "1", "key_check", []byte(keyCheckPlaintext))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE local_store_metadata
		SET key_check = ?, key_check_encoding = ?
		WHERE singleton = 1`,
		keyCheck,
		payloadEncodingAESGCM,
	)
	if err != nil {
		return errors.New("persist local encryption metadata")
	}
	return nil
}

func (s *Store) backfillPlaintextPayloads(ctx context.Context) error {
	payloads, err := s.readPlaintextPayloads(ctx)
	if err != nil {
		return err
	}
	defer func() {
		for index := range payloads {
			zeroBytes(payloads[index].body)
		}
	}()

	type encryptedPayload struct {
		storedPayload
		envelope []byte
	}
	encrypted := make([]encryptedPayload, 0, len(payloads))
	for index := range payloads {
		payload := &payloads[index]
		if payload.recordType == "finding" {
			var sourceRecordIDs []string
			if err := json.Unmarshal(payload.body, &sourceRecordIDs); err != nil {
				return errors.New("decode legacy finding citations")
			}
			canonicalIDs, err := s.ResolveCanonicalEventIDs(
				ctx,
				payload.sourceRunID,
				payload.sessionKey,
				sourceRecordIDs,
			)
			if err != nil {
				return errors.New("legacy finding citations could not be resolved")
			}
			payload.body, err = json.Marshal(canonicalIDs)
			if err != nil {
				return errors.New("encode canonical legacy finding citations")
			}
			payload.citations = canonicalIDs
		}
		envelope, err := s.cipher.seal(
			payload.recordType,
			payload.recordID,
			payload.column,
			payload.body,
		)
		if err != nil {
			return err
		}
		encrypted = append(encrypted, encryptedPayload{
			storedPayload: *payload,
			envelope:      envelope,
		})
	}
	keyCheck, err := s.cipher.seal("metadata", "1", "key_check", []byte(keyCheckPlaintext))
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin local payload upgrade")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationPayloadUpgrade, func() error {
		for _, payload := range encrypted {
			var result sql.Result
			switch payload.recordType {
			case "event":
				result, err = tx.ExecContext(ctx, `
					UPDATE events
					SET canonical_json = ?, canonical_encoding = ?
					WHERE event_id = ? AND canonical_encoding = ?`,
					payload.envelope,
					payloadEncodingAESGCM,
					payload.recordID,
					payloadEncodingPlaintext,
				)
			case "finding":
				result, err = tx.ExecContext(ctx, `
					UPDATE findings
					SET cited_event_ids_json = ?, cited_event_ids_encoding = ?
					WHERE finding_id = ? AND cited_event_ids_encoding = ?`,
					payload.envelope,
					payloadEncodingAESGCM,
					payload.recordID,
					payloadEncodingPlaintext,
				)
			case "event_enrichment":
				result, err = tx.ExecContext(ctx, `
					UPDATE event_enrichments
					SET enrichment_payload = ?, enrichment_encoding = ?
					WHERE event_id = ? AND enrichment_encoding = ?`,
					payload.envelope,
					payloadEncodingAESGCM,
					payload.recordID,
					payloadEncodingPlaintext,
				)
			case "issue_occurrence":
				result, err = tx.ExecContext(ctx, `
					UPDATE issue_occurrences
					SET evidence_payload = ?, evidence_encoding = ?
					WHERE revision_id = ? AND evidence_encoding = ?`,
					payload.envelope,
					payloadEncodingAESGCM,
					payload.recordID,
					payloadEncodingPlaintext,
				)
			default:
				return errors.New("unsupported local payload type")
			}
			if err != nil {
				return errors.New("encrypt existing local payload")
			}
			updated, err := result.RowsAffected()
			if err != nil || updated != 1 {
				return errors.New("existing local payload changed during upgrade")
			}
			for _, eventID := range payload.citations {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO finding_event_citations (finding_id, event_id)
					VALUES (?, ?)`,
					payload.recordID,
					eventID,
				); err != nil {
					return errors.New("persist legacy canonical finding citation")
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE local_store_metadata
			SET key_check = ?,
				key_check_encoding = ?,
				plaintext_cleanup_required = 1
			WHERE singleton = 1`,
			keyCheck,
			payloadEncodingAESGCM,
		); err != nil {
			return errors.New("mark local payload upgrade")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit local payload upgrade")
	}
	return nil
}

func (s *Store) readPlaintextPayloads(ctx context.Context) ([]storedPayload, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'event', event_id, 'canonical_json', canonical_json, '', ''
		FROM events
		WHERE canonical_encoding = ?
		UNION ALL
		SELECT
			'finding',
			finding_id,
			'cited_event_ids_json',
			cited_event_ids_json,
			source_run_id,
			COALESCE(session_key, '')
		FROM findings
		WHERE cited_event_ids_encoding = ?
		UNION ALL
		SELECT
			'event_enrichment',
			event_id,
			'enrichment_payload',
			enrichment_payload,
			'',
			''
		FROM event_enrichments
		WHERE enrichment_encoding = ?
		UNION ALL
		SELECT
			'issue_occurrence',
			revision_id,
			'evidence_payload',
			evidence_payload,
			'',
			''
		FROM issue_occurrences
		WHERE evidence_encoding = ?
		ORDER BY 1, 2`,
		payloadEncodingPlaintext,
		payloadEncodingPlaintext,
		payloadEncodingPlaintext,
		payloadEncodingPlaintext,
	)
	if err != nil {
		return nil, errors.New("read existing local payloads")
	}
	defer rows.Close()
	var payloads []storedPayload
	for rows.Next() {
		var payload storedPayload
		if err := rows.Scan(
			&payload.recordType,
			&payload.recordID,
			&payload.column,
			&payload.body,
			&payload.sourceRunID,
			&payload.sessionKey,
		); err != nil {
			return nil, errors.New("read existing local payload")
		}
		payload.body = append([]byte(nil), payload.body...)
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read existing local payloads")
	}
	return payloads, nil
}

func (s *Store) removePlaintextArtifacts(ctx context.Context) error {
	if err := s.truncateWAL(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		if isSQLiteBusy(err) {
			return ErrMaintenanceBusy
		}
		return errors.New("compact local payload upgrade")
	}
	if err := s.truncateWAL(ctx); err != nil {
		return err
	}
	mode, err := s.setJournalMode(ctx, "DELETE")
	if err != nil {
		return err
	}
	if mode != "delete" {
		return ErrMaintenanceBusy
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE local_store_metadata
		SET plaintext_cleanup_required = 0
		WHERE singleton = 1`,
	); err != nil {
		return errors.New("complete local payload upgrade")
	}
	mode, err = s.setJournalMode(ctx, "WAL")
	if err != nil {
		return errors.New("restore local WAL mode after plaintext cleanup")
	}
	if mode != "wal" {
		return errors.New("restore local WAL mode after plaintext cleanup")
	}
	return nil
}

func (s *Store) truncateWAL(ctx context.Context) error {
	var busy, logFrames, checkpointedFrames int
	err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(
		&busy,
		&logFrames,
		&checkpointedFrames,
	)
	if err != nil {
		if isSQLiteBusy(err) {
			return ErrMaintenanceBusy
		}
		return errors.New("checkpoint local WAL")
	}
	if busy != 0 || logFrames != 0 || checkpointedFrames != 0 {
		return ErrMaintenanceBusy
	}
	return nil
}

func (s *Store) setJournalMode(ctx context.Context, mode string) (string, error) {
	var applied string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode="+mode).Scan(&applied); err != nil {
		if isSQLiteBusy(err) {
			return "", ErrMaintenanceBusy
		}
		return "", errors.New("change local journal mode")
	}
	return strings.ToLower(applied), nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "busy") || strings.Contains(message, "locked")
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
