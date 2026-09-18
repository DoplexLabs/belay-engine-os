package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const (
	defaultInsightProjectLimit = 100
	maxInsightProjectLimit     = 500
	maxInsightPayloadBytes     = 2 << 20
)

func (s *Store) ListInsightProjects(
	ctx context.Context,
	limit int,
) ([]issueintel.Project, error) {
	if limit <= 0 {
		limit = defaultInsightProjectLimit
	}
	if limit > maxInsightProjectLimit {
		return nil, errors.New("invalid insight project limit")
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH projects AS (
			SELECT project_identity FROM cost_issues
			UNION
			SELECT project_identity FROM correction_candidates
		)
		SELECT projects.project_identity,
			COALESCE((
				SELECT project_path
				FROM transcript_sessions session
				WHERE session.project_identity = projects.project_identity
				ORDER BY COALESCE(
					session.ended_at,
					session.started_at,
					session.updated_at
				) DESC, session.session_key ASC
				LIMIT 1
			), '')
		FROM projects
		ORDER BY projects.project_identity ASC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, errors.New("list insight projects")
	}
	defer rows.Close()
	var result []issueintel.Project
	for rows.Next() {
		var project issueintel.Project
		if err := rows.Scan(&project.Identity, &project.Path); err != nil {
			return nil, errors.New("read insight project")
		}
		result = append(result, project)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list insight projects")
	}
	return result, nil
}

func (s *Store) LoadSemanticInput(
	ctx context.Context,
	project issueintel.Project,
) (issueintel.SemanticInput, error) {
	if err := validateIssueProject(project); err != nil {
		return issueintel.SemanticInput{}, err
	}
	issues, err := s.QueryCostIssues(ctx, issueintel.Query{
		Limit:           maxCostIssueLimit,
		ProjectIdentity: project.Identity,
	})
	if err != nil {
		return issueintel.SemanticInput{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT candidate_id, payload, payload_encoding
		FROM correction_candidates
		WHERE project_identity = ?
		ORDER BY occurred_at ASC, candidate_id ASC`,
		project.Identity,
	)
	if err != nil {
		return issueintel.SemanticInput{}, errors.New(
			"load correction candidates for insight",
		)
	}
	defer rows.Close()
	var candidates []issueintel.CorrectionCandidate
	for rows.Next() {
		var candidateID, encoding string
		var payload []byte
		if err := rows.Scan(&candidateID, &payload, &encoding); err != nil {
			return issueintel.SemanticInput{}, errors.New(
				"read correction candidate for insight",
			)
		}
		payload, err = s.cipher.open(
			"correction_candidate",
			candidateID,
			"payload",
			encoding,
			payload,
		)
		if err != nil {
			return issueintel.SemanticInput{}, err
		}
		var candidate issueintel.CorrectionCandidate
		if json.Unmarshal(payload, &candidate) != nil ||
			candidate.CandidateID != candidateID ||
			candidate.Project.Identity != project.Identity {
			return issueintel.SemanticInput{}, errors.New(
				"decode correction candidate for insight",
			)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return issueintel.SemanticInput{}, errors.New(
			"load correction candidates for insight",
		)
	}
	return issueintel.SemanticInput{
		Project:              project,
		Issues:               issues,
		CorrectionCandidates: candidates,
	}, nil
}

func (s *Store) ReplaceProjectInsight(
	ctx context.Context,
	record issueintel.InsightRecord,
) error {
	if err := validateInsightRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return errors.New("encode insight payload")
	}
	if len(payload) > maxInsightPayloadBytes {
		return errors.New("insight payload exceeds safety limit")
	}
	payload, err = s.cipher.seal(
		"insight",
		record.InsightID,
		"payload",
		payload,
	)
	if err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin insight replacement")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationSemanticInsight, func() error {
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM insights WHERE project_identity = ?",
			record.Project.Identity,
		); err != nil {
			return errors.New("replace project insight")
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO insights (
				insight_id, project_identity, harness, model,
				prompt_version, input_hash, generated_at, payload,
				payload_encoding, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.InsightID,
			record.Project.Identity,
			record.Harness,
			record.Model,
			record.PromptVersion,
			record.InputHash,
			formatProjectionTime(record.GeneratedAt),
			payload,
			payloadEncodingAESGCM,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("persist project insight: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit insight replacement")
	}
	return nil
}

func (s *Store) GetProjectInsight(
	ctx context.Context,
	projectIdentity string,
) (issueintel.InsightRecord, error) {
	projectIdentity = strings.TrimSpace(projectIdentity)
	if projectIdentity == "" || len(projectIdentity) > maxCostIssueProjectBytes {
		return issueintel.InsightRecord{}, errors.New(
			"invalid insight project identity",
		)
	}
	var insightID, indexedProject, harness, model, promptVersion string
	var inputHash, generatedAtValue, encoding string
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT insight_id, project_identity, harness, model,
			prompt_version, input_hash, generated_at, payload,
			payload_encoding
		FROM insights
		WHERE project_identity = ?`,
		projectIdentity,
	).Scan(
		&insightID,
		&indexedProject,
		&harness,
		&model,
		&promptVersion,
		&inputHash,
		&generatedAtValue,
		&payload,
		&encoding,
	)
	if err != nil {
		return issueintel.InsightRecord{}, err
	}
	payload, err = s.cipher.open(
		"insight",
		insightID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return issueintel.InsightRecord{}, err
	}
	var record issueintel.InsightRecord
	if json.Unmarshal(payload, &record) != nil {
		return issueintel.InsightRecord{}, errors.New("decode insight payload")
	}
	generatedAt, err := parseProjectionTime(generatedAtValue)
	if err != nil ||
		record.InsightID != insightID ||
		record.Project.Identity != indexedProject ||
		record.Harness != harness ||
		record.Model != model ||
		record.PromptVersion != promptVersion ||
		record.InputHash != inputHash ||
		!record.GeneratedAt.Equal(generatedAt) {
		return issueintel.InsightRecord{}, errors.New(
			"insight index does not match payload",
		)
	}
	return record, nil
}

func validateInsightRecord(record issueintel.InsightRecord) error {
	if record.InsightID == "" || len(record.InsightID) > 512 ||
		record.Project.Identity == "" ||
		len(record.Project.Identity) > maxCostIssueProjectBytes ||
		record.Harness == "" || len(record.Harness) > 64 ||
		len(record.Model) > 256 ||
		record.PromptVersion == "" || len(record.PromptVersion) > 128 ||
		record.InputHash == "" || len(record.InputHash) > 128 ||
		record.GeneratedAt.IsZero() {
		return errors.New("invalid insight record")
	}
	return nil
}
