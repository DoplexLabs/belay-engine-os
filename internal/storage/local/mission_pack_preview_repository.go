package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	maxMissionPackPreviewPayloadBytes = 256 << 10
	missionPackReceiptBindingWindow   = 5 * time.Minute
)

var (
	ErrMissionPackPreviewNotFound = errors.New("mission pack preview was not found")
	ErrMissionPackPreviewExpired  = errors.New("mission pack preview has expired")
	ErrMissionPackPreviewConflict = errors.New("mission pack preview identity conflicts with persisted record")
)

type MissionPackPreview struct {
	PackID          string                     `json:"pack_id"`
	ProjectIdentity string                     `json:"project_identity"`
	Harness         experience.Harness         `json:"harness"`
	Generation      int64                      `json:"generation"`
	ExperienceRefs  []experience.ExperienceRef `json:"experience_refs"`
	GeneratedAt     time.Time                  `json:"generated_at"`
	ExpiresAt       time.Time                  `json:"expires_at"`
	TaskHintHash    string                     `json:"task_hint_hash,omitempty"`
}

func (s *Store) RegisterMissionPackPreview(
	ctx context.Context,
	packID string,
	projectIdentity string,
	harness experience.Harness,
	generation int64,
	refs []experience.ExperienceRef,
	taskHintHash string,
	generatedAt time.Time,
	expiresAt time.Time,
) error {
	_, err := s.InsertMissionPackPreview(ctx, MissionPackPreview{
		PackID:          packID,
		ProjectIdentity: projectIdentity,
		Harness:         harness,
		Generation:      generation,
		ExperienceRefs:  append([]experience.ExperienceRef(nil), refs...),
		GeneratedAt:     generatedAt,
		ExpiresAt:       expiresAt,
		TaskHintHash:    taskHintHash,
	})
	return err
}

func (s *Store) InsertMissionPackPreview(
	ctx context.Context,
	preview MissionPackPreview,
) (bool, error) {
	preview.GeneratedAt = preview.GeneratedAt.UTC()
	preview.ExpiresAt = preview.ExpiresAt.UTC()
	if err := validateMissionPackPreview(preview); err != nil {
		return false, err
	}
	plaintext, payload, err := s.sealDomainJSON(
		"mission_pack_preview",
		preview.PackID,
		"payload",
		preview,
		maxMissionPackPreviewPayloadBytes,
	)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin mission pack preview persistence")
	}
	defer tx.Rollback()

	inserted := false
	err = withMutationTx(ctx, tx, mutationMissionPackPreview, func() error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO mission_pack_previews (
				pack_id, expires_at, payload, payload_encoding, created_at
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(pack_id) DO NOTHING`,
			preview.PackID,
			formatProjectionTime(preview.ExpiresAt),
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		)
		if err != nil {
			return fmt.Errorf("insert mission pack preview: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect mission pack preview persistence")
		}
		inserted = affected == 1
		if inserted {
			return nil
		}

		var existingPayload []byte
		var encoding string
		if err := tx.QueryRowContext(ctx, `
			SELECT payload, payload_encoding
			FROM mission_pack_previews
			WHERE pack_id = ?`,
			preview.PackID,
		).Scan(&existingPayload, &encoding); err != nil {
			return errors.New("read duplicate mission pack preview")
		}
		if err := validateSealedPayloadSize(
			"mission pack preview",
			existingPayload,
			maxMissionPackPreviewPayloadBytes,
		); err != nil {
			return err
		}
		existingPayload, err = s.cipher.open(
			"mission_pack_preview",
			preview.PackID,
			"payload",
			encoding,
			existingPayload,
		)
		if err != nil {
			return err
		}
		if bytes.Equal(existingPayload, plaintext) {
			return nil
		}
		var existing MissionPackPreview
		if err := json.Unmarshal(existingPayload, &existing); err != nil ||
			validateMissionPackPreview(existing) != nil {
			return errors.New("decode duplicate mission pack preview")
		}
		if !sameMissionPackPreviewIdentity(existing, preview) {
			return ErrMissionPackPreviewConflict
		}
		if preview.GeneratedAt.Before(existing.GeneratedAt) {
			return nil
		}
		if preview.GeneratedAt.Equal(existing.GeneratedAt) {
			return ErrMissionPackPreviewConflict
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mission_pack_previews
			WHERE pack_id = ?`,
			preview.PackID,
		); err != nil {
			return errors.New("replace refreshed mission pack preview")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mission_pack_previews (
				pack_id, expires_at, payload, payload_encoding, created_at
			) VALUES (?, ?, ?, ?, ?)`,
			preview.PackID,
			formatProjectionTime(preview.ExpiresAt),
			payload,
			payloadEncodingAESGCM,
			formatProjectionTime(s.nowUTC()),
		); err != nil {
			return errors.New("persist refreshed mission pack preview")
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit mission pack preview persistence")
	}
	return inserted, nil
}

func sameMissionPackPreviewIdentity(
	first MissionPackPreview,
	second MissionPackPreview,
) bool {
	if first.PackID != second.PackID ||
		first.ProjectIdentity != second.ProjectIdentity ||
		first.Harness != second.Harness ||
		first.Generation != second.Generation ||
		first.TaskHintHash != second.TaskHintHash ||
		len(first.ExperienceRefs) != len(second.ExperienceRefs) {
		return false
	}
	for index := range first.ExperienceRefs {
		if first.ExperienceRefs[index] != second.ExperienceRefs[index] {
			return false
		}
	}
	return true
}

func (s *Store) GetMissionPackPreview(
	ctx context.Context,
	packID string,
) (MissionPackPreview, error) {
	if err := validateStorageIdentifier("mission pack preview ID", packID); err != nil {
		return MissionPackPreview{}, err
	}
	return s.scanMissionPackPreview(s.db.QueryRowContext(
		ctx,
		missionPackPreviewSelectSQL+` WHERE pack_id = ?`,
		packID,
	))
}

func (s *Store) AcceptMissionPackPreview(
	ctx context.Context,
	packID string,
	acceptedAt time.Time,
) (MissionPackReceipt, error) {
	if err := validateStorageIdentifier("mission pack preview ID", packID); err != nil {
		return MissionPackReceipt{}, err
	}
	if acceptedAt.IsZero() {
		return MissionPackReceipt{}, errors.New("mission pack acceptance time is required")
	}
	acceptedAt = acceptedAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MissionPackReceipt{}, errors.New("begin mission pack preview acceptance")
	}
	defer tx.Rollback()

	receiptID := missionPackReceiptID(packID)
	var accepted MissionPackReceipt
	err = withMutationTx(ctx, tx, mutationMissionPackReceipt, func() error {
		existing, err := s.scanMissionPackReceipt(tx.QueryRowContext(
			ctx,
			receiptSelectSQL+` WHERE receipt_id = ?`,
			receiptID,
		))
		switch {
		case err == nil:
			if existing.PackID != packID {
				return ErrMissionPackPreviewConflict
			}
			accepted = existing
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return errors.New("read accepted mission pack preview")
		}

		preview, err := s.scanMissionPackPreview(tx.QueryRowContext(
			ctx,
			missionPackPreviewSelectSQL+` WHERE pack_id = ?`,
			packID,
		))
		if err != nil {
			return err
		}
		if acceptedAt.Before(preview.GeneratedAt) ||
			!acceptedAt.Before(preview.ExpiresAt) {
			return ErrMissionPackPreviewExpired
		}
		if len(preview.ExperienceRefs) == 0 {
			return errors.New("mission pack preview has no experience references")
		}

		receipt := MissionPackReceipt{
			ReceiptID:       receiptID,
			PackID:          preview.PackID,
			ProjectIdentity: preview.ProjectIdentity,
			Harness:         preview.Harness,
			ExperienceRefs:  append([]experience.ExperienceRef(nil), preview.ExperienceRefs...),
			Generation:      preview.Generation,
			AcceptedAt:      acceptedAt,
			ExpiresAt:       acceptedAt.Add(missionPackReceiptBindingWindow),
			TaskHintHash:    preview.TaskHintHash,
			BindingState:    ReceiptPending,
		}
		if err := validateMissionPackReceipt(receipt); err != nil {
			return err
		}
		if err := s.validateMissionPackReceiptReferencesTx(ctx, tx, receipt); err != nil {
			return err
		}
		if err := s.insertAcceptedMissionPackReceiptTx(ctx, tx, receipt); err != nil {
			return err
		}
		accepted = receipt
		return nil
	})
	if err != nil {
		return MissionPackReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return MissionPackReceipt{}, errors.New("commit mission pack preview acceptance")
	}
	return accepted, nil
}

func (s *Store) PruneExpiredMissionPackPreviews(
	ctx context.Context,
	expiredBefore time.Time,
	limit int,
) (int, error) {
	if expiredBefore.IsZero() {
		return 0, errors.New("mission pack preview cleanup time is required")
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("begin mission pack preview cleanup")
	}
	defer tx.Rollback()

	deleted := 0
	err = withMutationTx(ctx, tx, mutationMissionPackPreview, func() error {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM mission_pack_previews
			WHERE pack_id IN (
				SELECT pack_id
				FROM mission_pack_previews
				WHERE expires_at <= ?
				ORDER BY expires_at, pack_id
				LIMIT ?
			)`,
			formatProjectionTime(expiredBefore.UTC()),
			limit,
		)
		if err != nil {
			return errors.New("delete expired mission pack previews")
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return errors.New("inspect mission pack preview cleanup")
		}
		deleted = int(affected)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, errors.New("commit mission pack preview cleanup")
	}
	return deleted, nil
}

func (s *Store) QueryUnresolvedMissionPackReceipts(
	ctx context.Context,
	limit int,
) ([]MissionPackReceipt, error) {
	if limit < 1 {
		return nil, errors.New("mission pack receipt query limit is required")
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, receiptSelectSQL+`
		WHERE binding_state = 'pending'
		ORDER BY accepted_at, receipt_id
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, errors.New("query unresolved mission pack receipts")
	}
	defer rows.Close()

	result := make([]MissionPackReceipt, 0)
	for rows.Next() {
		receipt, err := s.scanMissionPackReceipt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query unresolved mission pack receipts")
	}
	return result, nil
}

func (s *Store) QueryCompatibleTranscriptSessionKeys(
	ctx context.Context,
	projectIdentity string,
	harness experience.Harness,
	acceptedAt time.Time,
	expiresAt time.Time,
	limit int,
) ([]string, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	if !harness.Valid() {
		return nil, errors.New("invalid mission pack transcript harness")
	}
	if acceptedAt.IsZero() || expiresAt.IsZero() ||
		!expiresAt.After(acceptedAt) {
		return nil, errors.New("invalid mission pack transcript activity window")
	}
	if limit < 1 {
		return nil, errors.New("mission pack transcript query limit is required")
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sessions.session_key
		FROM transcript_sessions AS sessions
		JOIN transcript_turns AS turns
			ON turns.session_key = sessions.session_key
		WHERE sessions.project_identity = ?
			AND sessions.agent = ?
			AND turns.occurred_at >= ?
			AND turns.occurred_at < ?
		GROUP BY sessions.session_key
		ORDER BY MIN(turns.occurred_at), sessions.session_key
		LIMIT ?`,
		projectIdentity,
		harness,
		formatProjectionTime(acceptedAt.UTC()),
		formatProjectionTime(expiresAt.UTC()),
		limit,
	)
	if err != nil {
		return nil, errors.New("query compatible transcript sessions")
	}
	defer rows.Close()

	result := make([]string, 0)
	for rows.Next() {
		var sessionKey string
		if err := rows.Scan(&sessionKey); err != nil {
			return nil, errors.New("scan compatible transcript session")
		}
		if err := validateStorageIdentifier(
			"compatible transcript session key",
			sessionKey,
		); err != nil {
			return nil, errors.New("invalid compatible transcript session key")
		}
		result = append(result, sessionKey)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query compatible transcript sessions")
	}
	return result, nil
}

const missionPackPreviewSelectSQL = `
	SELECT pack_id, expires_at, payload, payload_encoding
	FROM mission_pack_previews`

func (s *Store) scanMissionPackPreview(
	scanner rowScanner,
) (MissionPackPreview, error) {
	var packID, expiresAtValue, encoding string
	var payload []byte
	if err := scanner.Scan(
		&packID,
		&expiresAtValue,
		&payload,
		&encoding,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MissionPackPreview{}, ErrMissionPackPreviewNotFound
		}
		return MissionPackPreview{}, err
	}
	if err := validateSealedPayloadSize(
		"mission pack preview",
		payload,
		maxMissionPackPreviewPayloadBytes,
	); err != nil {
		return MissionPackPreview{}, err
	}
	payload, err := s.cipher.open(
		"mission_pack_preview",
		packID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return MissionPackPreview{}, err
	}
	var preview MissionPackPreview
	if err := json.Unmarshal(payload, &preview); err != nil {
		return MissionPackPreview{}, errors.New("decode mission pack preview payload")
	}
	expiresAt, err := parseProjectionTime(expiresAtValue)
	if err != nil ||
		preview.PackID != packID ||
		!preview.ExpiresAt.Equal(expiresAt) {
		return MissionPackPreview{}, errors.New("mission pack preview index does not match payload")
	}
	if err := validateMissionPackPreview(preview); err != nil {
		return MissionPackPreview{}, errors.New("invalid persisted mission pack preview")
	}
	return preview, nil
}

func (s *Store) insertAcceptedMissionPackReceiptTx(
	ctx context.Context,
	tx *sql.Tx,
	receipt MissionPackReceipt,
) error {
	_, payload, err := s.sealDomainJSON(
		"mission_pack_receipt",
		receipt.ReceiptID,
		"payload",
		receipt,
		maxReceiptPayloadBytes,
	)
	if err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	result, err := tx.ExecContext(ctx, `
		INSERT INTO mission_pack_receipts (
			receipt_id, pack_id, project_identity, harness, generation,
			accepted_at, expires_at, task_hint_hash, binding_state,
			bound_session_key, payload, payload_encoding, created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO NOTHING`,
		receipt.ReceiptID,
		receipt.PackID,
		receipt.ProjectIdentity,
		receipt.Harness,
		receipt.Generation,
		formatProjectionTime(receipt.AcceptedAt),
		formatProjectionTime(receipt.ExpiresAt),
		receipt.TaskHintHash,
		receipt.BindingState,
		payload,
		payloadEncodingAESGCM,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert accepted mission pack receipt: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return errors.New("inspect accepted mission pack receipt persistence")
	}
	if affected != 1 {
		return ErrMissionPackPreviewConflict
	}
	return s.ensureMissionPackReceiptApplicationProgressTx(
		ctx,
		tx,
		receipt,
	)
}

func validateMissionPackPreview(preview MissionPackPreview) error {
	if err := validateStorageIdentifier("mission pack preview ID", preview.PackID); err != nil {
		return err
	}
	if err := validateProjectIdentity(preview.ProjectIdentity); err != nil {
		return err
	}
	if !preview.Harness.Valid() ||
		preview.Generation < 1 ||
		preview.GeneratedAt.IsZero() ||
		preview.ExpiresAt.IsZero() ||
		!preview.ExpiresAt.After(preview.GeneratedAt) ||
		len(preview.ExperienceRefs) > 3 {
		return errors.New("invalid mission pack preview")
	}
	if preview.TaskHintHash != "" && !validStorageSHA256(preview.TaskHintHash) {
		return errors.New("invalid mission pack preview task hint hash")
	}
	seen := make(map[experience.ExperienceRef]struct{}, len(preview.ExperienceRefs))
	for _, ref := range preview.ExperienceRefs {
		if err := ref.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[ref]; duplicate {
			return errors.New("mission pack preview contains duplicate experience reference")
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func missionPackReceiptID(packID string) string {
	sum := sha256.Sum256([]byte("mission-pack-receipt.v1\x00" + packID))
	return "mpr_" + hex.EncodeToString(sum[:])
}
