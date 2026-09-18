package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const maxCostIssueFixPayloadBytes = 2 << 20

func (s *Store) SaveCostIssueFix(
	ctx context.Context,
	record issueintel.FixRecord,
) error {
	if err := validateCostIssueFix(record); err != nil {
		return err
	}
	payload, err := encodeCostIssueFix(s, record)
	if err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin cost issue fix save")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationCostIssueFix, func() error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO cost_issue_fixes (
				fix_id, issue_id, project_identity, kind, target_file,
				state, content_sha256, applied_path, git_commit,
				proposed_at, applied_at, payload, payload_encoding,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.FixID,
			record.IssueID,
			record.Project.Identity,
			record.Kind,
			record.TargetFile,
			record.State,
			record.ContentSHA256,
			record.AppliedPath,
			record.GitCommit,
			formatProjectionTime(record.ProposedAt),
			nullableProjectionTime(record.AppliedAt),
			payload,
			payloadEncodingAESGCM,
			now,
			now,
		)
		if err != nil {
			return errors.New("persist cost issue fix")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit cost issue fix save")
	}
	return nil
}

func (s *Store) RecordCostIssueFixApplied(
	ctx context.Context,
	fixID, appliedPath, contentSHA256, gitCommit string,
	appliedAt time.Time,
) (issueintel.FixRecord, error) {
	record, err := s.GetCostIssueFix(ctx, fixID)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	if record.State == "applied" {
		if record.AppliedPath == appliedPath &&
			record.ContentSHA256 == contentSHA256 &&
			record.GitCommit == gitCommit {
			return record, nil
		}
		return issueintel.FixRecord{}, errors.New(
			"cost issue fix was already applied with different evidence",
		)
	}
	record.State = "applied"
	record.AppliedPath = strings.TrimSpace(appliedPath)
	record.ContentSHA256 = strings.ToLower(strings.TrimSpace(contentSHA256))
	record.GitCommit = strings.TrimSpace(gitCommit)
	appliedAt = appliedAt.UTC()
	record.AppliedAt = &appliedAt
	if err := validateCostIssueFix(record); err != nil {
		return issueintel.FixRecord{}, err
	}
	payload, err := encodeCostIssueFix(s, record)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issueintel.FixRecord{}, errors.New(
			"begin cost issue fix application",
		)
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationCostIssueFix, func() error {
		result, err := tx.ExecContext(ctx, `
			UPDATE cost_issue_fixes
			SET state = 'applied',
				content_sha256 = ?,
				applied_path = ?,
				git_commit = ?,
				applied_at = ?,
				payload = ?,
				updated_at = ?
			WHERE fix_id = ? AND state = 'proposed'`,
			record.ContentSHA256,
			record.AppliedPath,
			record.GitCommit,
			formatProjectionTime(appliedAt),
			payload,
			now,
			fixID,
		)
		if err != nil {
			return errors.New("record cost issue fix application")
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return errors.New("cost issue fix changed during application")
		}
		return nil
	})
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return issueintel.FixRecord{}, errors.New(
			"commit cost issue fix application",
		)
	}
	return record, nil
}

func (s *Store) GetCostIssueFix(
	ctx context.Context,
	fixID string,
) (issueintel.FixRecord, error) {
	fixID = strings.TrimSpace(fixID)
	if fixID == "" || len(fixID) > 512 {
		return issueintel.FixRecord{}, errors.New("invalid cost issue fix ID")
	}
	var indexed issueintel.FixRecord
	var proposedAtValue string
	var appliedAtValue sql.NullString
	var payload []byte
	var encoding string
	err := s.db.QueryRowContext(ctx, `
		SELECT issue_id, project_identity, kind, target_file, state,
			content_sha256, applied_path, git_commit, proposed_at,
			applied_at, payload, payload_encoding
		FROM cost_issue_fixes WHERE fix_id = ?`,
		fixID,
	).Scan(
		&indexed.IssueID,
		&indexed.Project.Identity,
		&indexed.Kind,
		&indexed.TargetFile,
		&indexed.State,
		&indexed.ContentSHA256,
		&indexed.AppliedPath,
		&indexed.GitCommit,
		&proposedAtValue,
		&appliedAtValue,
		&payload,
		&encoding,
	)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	payload, err = s.cipher.open(
		"cost_issue_fix",
		fixID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	var record issueintel.FixRecord
	proposedAt, proposedErr := parseProjectionTime(proposedAtValue)
	var indexedAppliedAt *time.Time
	if appliedAtValue.Valid {
		parsed, parseErr := parseProjectionTime(appliedAtValue.String)
		if parseErr != nil {
			return issueintel.FixRecord{}, errors.New(
				"invalid cost issue fix applied time",
			)
		}
		indexedAppliedAt = &parsed
	}
	if json.Unmarshal(payload, &record) != nil || record.FixID != fixID ||
		record.IssueID != indexed.IssueID ||
		record.Project.Identity != indexed.Project.Identity ||
		record.Kind != indexed.Kind ||
		record.TargetFile != indexed.TargetFile ||
		record.State != indexed.State ||
		record.ContentSHA256 != indexed.ContentSHA256 ||
		record.AppliedPath != indexed.AppliedPath ||
		record.GitCommit != indexed.GitCommit ||
		proposedErr != nil ||
		!record.ProposedAt.Equal(proposedAt) ||
		!equalOptionalTimes(record.AppliedAt, indexedAppliedAt) {
		return issueintel.FixRecord{}, errors.New(
			"cost issue fix index does not match payload",
		)
	}
	return record, nil
}

func equalOptionalTimes(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func encodeCostIssueFix(
	s *Store,
	record issueintel.FixRecord,
) ([]byte, error) {
	payload, err := json.Marshal(record)
	if err != nil || len(payload) > maxCostIssueFixPayloadBytes {
		return nil, errors.New("encode cost issue fix payload")
	}
	return s.cipher.seal(
		"cost_issue_fix",
		record.FixID,
		"payload",
		payload,
	)
}

func validateCostIssueFix(record issueintel.FixRecord) error {
	if record.FixID == "" || len(record.FixID) > 512 ||
		record.IssueID == "" || len(record.IssueID) > 512 ||
		record.Project.Identity == "" ||
		record.Kind == "" || len(record.Kind) > 128 ||
		record.TargetFile == "" || len(record.TargetFile) > 4096 ||
		record.RuleText == "" || len(record.RuleText) > 4096 ||
		record.UnifiedDiff == "" ||
		record.ProposedAt.IsZero() ||
		(record.State != "proposed" && record.State != "applied") {
		return errors.New("invalid cost issue fix")
	}
	if record.State == "applied" {
		if record.AppliedAt == nil ||
			record.AppliedPath == "" ||
			len(record.ContentSHA256) != 64 {
			return errors.New("invalid applied cost issue fix")
		}
	}
	return nil
}

func nullableProjectionTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatProjectionTime(value.UTC())
}
