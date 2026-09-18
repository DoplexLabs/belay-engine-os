package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	maxExperienceSemanticProposalBatch = 32
	maxExperienceSemanticPayloadBytes  = 2 << 20
)

var ErrExperienceSemanticProposalConflict = errors.New(
	"experience semantic proposal identity conflicts with persisted record",
)

var ErrExperienceSemanticResultConflict = errors.New(
	"experience semantic result identity conflicts with persisted record",
)

type sealedSemanticProposal struct {
	value     experience.SemanticProposal
	plaintext []byte
	payload   []byte
}

type sealedSemanticResult struct {
	value             experience.SemanticResult
	proposalPlaintext []byte
	proposalPayload   []byte
	decisionPlaintext []byte
	decisionPayload   []byte
}

type semanticProposalSource struct {
	candidateID   string
	harness       experience.Harness
	promptVersion string
}

func (s *Store) StoreExperienceSemanticResults(
	ctx context.Context,
	results []experience.SemanticResult,
) (
	experience.SemanticDispositionCounts,
	experience.SemanticDispositionCounts,
	error,
) {
	if len(results) == 0 || len(results) > maxExperienceSemanticProposalBatch {
		return experience.SemanticDispositionCounts{},
			experience.SemanticDispositionCounts{},
			errors.New("experience semantic result batch is required and bounded")
	}
	sealed := make([]sealedSemanticResult, 0, len(results))
	seenDecisions := make(map[string]bool, len(results))
	seenProposals := make(map[string]bool, len(results))
	seenSources := make(map[semanticProposalSource]bool, len(results))
	for _, result := range results {
		if err := result.Validate(); err != nil {
			return experience.SemanticDispositionCounts{},
				experience.SemanticDispositionCounts{},
				err
		}
		source := semanticProposalSource{
			candidateID:   result.Decision.CandidateID,
			harness:       result.Decision.Provenance.Harness,
			promptVersion: result.Decision.Provenance.PromptVersion,
		}
		if seenDecisions[result.Decision.DecisionID] || seenSources[source] {
			return experience.SemanticDispositionCounts{},
				experience.SemanticDispositionCounts{},
				errors.New("experience semantic result batch contains duplicate identity")
		}
		seenDecisions[result.Decision.DecisionID] = true
		seenSources[source] = true
		item := sealedSemanticResult{value: result}
		if result.Proposal != nil {
			if seenProposals[result.Proposal.ProposalID] {
				return experience.SemanticDispositionCounts{},
					experience.SemanticDispositionCounts{},
					errors.New("experience semantic result batch contains duplicate proposal")
			}
			seenProposals[result.Proposal.ProposalID] = true
			var err error
			item.proposalPlaintext, item.proposalPayload, err = s.sealDomainJSON(
				"experience_semantic_proposal",
				result.Proposal.ProposalID,
				"payload",
				*result.Proposal,
				maxExperienceSemanticPayloadBytes,
			)
			if err != nil {
				return experience.SemanticDispositionCounts{},
					experience.SemanticDispositionCounts{},
					err
			}
		}
		var err error
		item.decisionPlaintext, item.decisionPayload, err = s.sealDomainJSON(
			"experience_semantic_decision",
			result.Decision.DecisionID,
			"payload",
			result.Decision,
			maxExperienceSemanticPayloadBytes,
		)
		if err != nil {
			return experience.SemanticDispositionCounts{},
				experience.SemanticDispositionCounts{},
				err
		}
		sealed = append(sealed, item)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return experience.SemanticDispositionCounts{},
			experience.SemanticDispositionCounts{},
			errors.New("begin experience semantic result persistence")
	}
	defer tx.Rollback()
	var inserted, replayed experience.SemanticDispositionCounts
	err = withMutationTx(ctx, tx, mutationExperienceSemantic, func() error {
		for _, item := range sealed {
			decision := item.value.Decision
			var candidateProject string
			if err := tx.QueryRowContext(ctx, `
				SELECT project_identity
				FROM experience_candidates
				WHERE candidate_id = ?`,
				decision.CandidateID,
			).Scan(&candidateProject); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errors.New(
						"experience semantic result candidate does not exist",
					)
				}
				return errors.New("read experience semantic result candidate")
			}
			if candidateProject != decision.ProjectIdentity {
				return errors.New(
					"experience semantic result candidate project mismatch",
				)
			}
			if item.value.Proposal == nil {
				var proposalExists int
				if err := tx.QueryRowContext(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM experience_semantic_proposals
						WHERE candidate_id = ?
							AND harness = ?
							AND prompt_version = ?
					)`,
					decision.CandidateID,
					decision.Provenance.Harness,
					decision.Provenance.PromptVersion,
				).Scan(&proposalExists); err != nil {
					return errors.New("inspect prior experience semantic proposal")
				}
				if proposalExists == 1 {
					return ErrExperienceSemanticResultConflict
				}
			} else {
				if candidateProject != item.value.Proposal.ProjectIdentity {
					return errors.New(
						"experience semantic result candidate project mismatch",
					)
				}
				if err := s.storeExperienceSemanticProposalTx(
					ctx,
					tx,
					*item.value.Proposal,
					item.proposalPlaintext,
					item.proposalPayload,
				); err != nil {
					return err
				}
			}

			proposalID := any(nil)
			if decision.ProposalID != "" {
				proposalID = decision.ProposalID
			}
			result, err := tx.ExecContext(ctx, `
				INSERT INTO experience_semantic_decisions (
					decision_id, candidate_id, project_identity, disposition,
					reason_code, proposal_id, harness, prompt_version,
					input_hash, output_hash, generated_at, payload,
					payload_encoding, inserted_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(candidate_id, harness, prompt_version) DO NOTHING`,
				decision.DecisionID,
				decision.CandidateID,
				decision.ProjectIdentity,
				decision.Disposition,
				decision.ReasonCode,
				proposalID,
				decision.Provenance.Harness,
				decision.Provenance.PromptVersion,
				decision.Provenance.InputHash,
				decision.Provenance.OutputHash,
				formatProjectionTime(decision.Provenance.GeneratedAt),
				item.decisionPayload,
				payloadEncodingAESGCM,
				formatProjectionTime(s.nowUTC()),
			)
			if err != nil {
				return fmt.Errorf("insert experience semantic decision: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return errors.New("inspect experience semantic decision persistence")
			}
			if affected == 1 {
				incrementSemanticDispositionCount(&inserted, decision.Disposition)
				continue
			}
			var existingDecisionID string
			var existingPayload []byte
			var encoding string
			if err := tx.QueryRowContext(ctx, `
				SELECT decision_id, payload, payload_encoding
				FROM experience_semantic_decisions
				WHERE candidate_id = ?
					AND harness = ?
					AND prompt_version = ?`,
				decision.CandidateID,
				decision.Provenance.Harness,
				decision.Provenance.PromptVersion,
			).Scan(
				&existingDecisionID,
				&existingPayload,
				&encoding,
			); err != nil {
				return errors.New("read duplicate experience semantic decision")
			}
			if err := validateSealedPayloadSize(
				"experience semantic decision",
				existingPayload,
				maxExperienceSemanticPayloadBytes,
			); err != nil {
				return err
			}
			existingPayload, err = s.cipher.open(
				"experience_semantic_decision",
				existingDecisionID,
				"payload",
				encoding,
				existingPayload,
			)
			if err != nil {
				return err
			}
			if !bytes.Equal(existingPayload, item.decisionPlaintext) {
				return ErrExperienceSemanticResultConflict
			}
			incrementSemanticDispositionCount(&replayed, decision.Disposition)
		}
		return nil
	})
	if err != nil {
		return experience.SemanticDispositionCounts{},
			experience.SemanticDispositionCounts{},
			err
	}
	if err := tx.Commit(); err != nil {
		return experience.SemanticDispositionCounts{},
			experience.SemanticDispositionCounts{},
			errors.New("commit experience semantic result persistence")
	}
	return inserted, replayed, nil
}

func (s *Store) storeExperienceSemanticProposalTx(
	ctx context.Context,
	tx *sql.Tx,
	proposal experience.SemanticProposal,
	plaintext []byte,
	payload []byte,
) error {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO experience_semantic_proposals (
			proposal_id, candidate_id, project_identity,
			experience_type, scope_kind, harness, prompt_version,
			input_hash, output_hash, generated_at, payload,
			payload_encoding, inserted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(candidate_id, harness, prompt_version) DO NOTHING`,
		proposal.ProposalID,
		proposal.CandidateID,
		proposal.ProjectIdentity,
		proposal.Proposal.Type,
		proposal.Proposal.Scope.Kind,
		proposal.Provenance.Harness,
		proposal.Provenance.PromptVersion,
		proposal.Provenance.InputHash,
		proposal.Provenance.OutputHash,
		formatProjectionTime(proposal.Provenance.GeneratedAt),
		payload,
		payloadEncodingAESGCM,
		formatProjectionTime(s.nowUTC()),
	)
	if err != nil {
		return fmt.Errorf("insert experience semantic proposal: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return errors.New("inspect experience semantic proposal persistence")
	}
	if affected == 1 {
		return nil
	}
	var existingProposalID string
	var existingPayload []byte
	var encoding string
	if err := tx.QueryRowContext(ctx, `
		SELECT proposal_id, payload, payload_encoding
		FROM experience_semantic_proposals
		WHERE candidate_id = ?
			AND harness = ?
			AND prompt_version = ?`,
		proposal.CandidateID,
		proposal.Provenance.Harness,
		proposal.Provenance.PromptVersion,
	).Scan(
		&existingProposalID,
		&existingPayload,
		&encoding,
	); err != nil {
		return errors.New("read duplicate experience semantic proposal")
	}
	if err := validateSealedPayloadSize(
		"experience semantic proposal",
		existingPayload,
		maxExperienceSemanticPayloadBytes,
	); err != nil {
		return err
	}
	existingPayload, err = s.cipher.open(
		"experience_semantic_proposal",
		existingProposalID,
		"payload",
		encoding,
		existingPayload,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(existingPayload, plaintext) {
		return ErrExperienceSemanticProposalConflict
	}
	return nil
}

func incrementSemanticDispositionCount(
	counts *experience.SemanticDispositionCounts,
	disposition experience.SemanticDisposition,
) {
	switch disposition {
	case experience.SemanticDispositionPropose:
		counts.Proposed++
	case experience.SemanticDispositionReject:
		counts.Rejected++
	case experience.SemanticDispositionDefer:
		counts.Deferred++
	}
}

func (s *Store) InsertExperienceSemanticProposals(
	ctx context.Context,
	proposals []experience.SemanticProposal,
) (inserted int, replayed int, err error) {
	if len(proposals) == 0 ||
		len(proposals) > maxExperienceSemanticProposalBatch {
		return 0, 0, errors.New(
			"experience semantic proposal batch is required and bounded",
		)
	}
	sealed := make([]sealedSemanticProposal, 0, len(proposals))
	seenProposals := make(map[string]bool, len(proposals))
	seenSources := make(map[semanticProposalSource]bool, len(proposals))
	for _, proposal := range proposals {
		if err := proposal.Validate(); err != nil {
			return 0, 0, err
		}
		source := semanticProposalSource{
			candidateID:   proposal.CandidateID,
			harness:       proposal.Provenance.Harness,
			promptVersion: proposal.Provenance.PromptVersion,
		}
		if seenProposals[proposal.ProposalID] ||
			seenSources[source] {
			return 0, 0, errors.New(
				"experience semantic proposal batch contains duplicate identity",
			)
		}
		seenProposals[proposal.ProposalID] = true
		seenSources[source] = true
		plaintext, payload, err := s.sealDomainJSON(
			"experience_semantic_proposal",
			proposal.ProposalID,
			"payload",
			proposal,
			maxExperienceSemanticPayloadBytes,
		)
		if err != nil {
			return 0, 0, err
		}
		sealed = append(sealed, sealedSemanticProposal{
			value:     proposal,
			plaintext: plaintext,
			payload:   payload,
		})
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, errors.New(
			"begin experience semantic proposal persistence",
		)
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationExperienceSemantic, func() error {
		for _, item := range sealed {
			var candidateProject string
			if err := tx.QueryRowContext(ctx, `
				SELECT project_identity
				FROM experience_candidates
				WHERE candidate_id = ?`,
				item.value.CandidateID,
			).Scan(&candidateProject); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errors.New(
						"experience semantic proposal candidate does not exist",
					)
				}
				return errors.New(
					"read experience semantic proposal candidate",
				)
			}
			if candidateProject != item.value.ProjectIdentity {
				return errors.New(
					"experience semantic proposal candidate project mismatch",
				)
			}
			result, err := tx.ExecContext(ctx, `
				INSERT INTO experience_semantic_proposals (
					proposal_id, candidate_id, project_identity,
					experience_type, scope_kind, harness, prompt_version,
					input_hash, output_hash, generated_at, payload,
					payload_encoding, inserted_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(candidate_id, harness, prompt_version) DO NOTHING`,
				item.value.ProposalID,
				item.value.CandidateID,
				item.value.ProjectIdentity,
				item.value.Proposal.Type,
				item.value.Proposal.Scope.Kind,
				item.value.Provenance.Harness,
				item.value.Provenance.PromptVersion,
				item.value.Provenance.InputHash,
				item.value.Provenance.OutputHash,
				formatProjectionTime(item.value.Provenance.GeneratedAt),
				item.payload,
				payloadEncodingAESGCM,
				formatProjectionTime(s.nowUTC()),
			)
			if err != nil {
				return fmt.Errorf(
					"insert experience semantic proposal: %w",
					err,
				)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return errors.New(
					"inspect experience semantic proposal persistence",
				)
			}
			if affected == 1 {
				inserted++
				continue
			}
			var existingProposalID string
			var existingPayload []byte
			var encoding string
			if err := tx.QueryRowContext(ctx, `
				SELECT proposal_id, payload, payload_encoding
				FROM experience_semantic_proposals
				WHERE candidate_id = ?
					AND harness = ?
					AND prompt_version = ?`,
				item.value.CandidateID,
				item.value.Provenance.Harness,
				item.value.Provenance.PromptVersion,
			).Scan(
				&existingProposalID,
				&existingPayload,
				&encoding,
			); err != nil {
				return errors.New(
					"read duplicate experience semantic proposal",
				)
			}
			if err := validateSealedPayloadSize(
				"experience semantic proposal",
				existingPayload,
				maxExperienceSemanticPayloadBytes,
			); err != nil {
				return err
			}
			existingPayload, err = s.cipher.open(
				"experience_semantic_proposal",
				existingProposalID,
				"payload",
				encoding,
				existingPayload,
			)
			if err != nil {
				return err
			}
			if !bytes.Equal(existingPayload, item.plaintext) {
				return ErrExperienceSemanticProposalConflict
			}
			replayed++
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, errors.New(
			"commit experience semantic proposal persistence",
		)
	}
	return inserted, replayed, nil
}

func (s *Store) HasExperienceSemanticResult(
	ctx context.Context,
	candidateID string,
	harness experience.Harness,
	promptVersion string,
) (bool, error) {
	if err := validateStorageIdentifier(
		"experience semantic result candidate ID",
		candidateID,
	); err != nil {
		return false, err
	}
	if !harness.Valid() {
		return false, errors.New("experience semantic result harness is invalid")
	}
	if err := validateStorageIdentifier(
		"experience semantic result prompt version",
		promptVersion,
	); err != nil {
		return false, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM experience_semantic_decisions
			WHERE candidate_id = ?
				AND harness = ?
				AND prompt_version = ?
			UNION ALL
			SELECT 1
			FROM experience_semantic_proposals
			WHERE candidate_id = ?
				AND harness = ?
				AND prompt_version = ?
		)`,
		candidateID,
		harness,
		promptVersion,
		candidateID,
		harness,
		promptVersion,
	).Scan(&exists); err != nil {
		return false, errors.New("query experience semantic result")
	}
	return exists == 1, nil
}

func (s *Store) HasExperienceSemanticProposal(
	ctx context.Context,
	candidateID string,
	harness experience.Harness,
	promptVersion string,
) (bool, error) {
	if err := validateStorageIdentifier(
		"experience semantic proposal candidate ID",
		candidateID,
	); err != nil {
		return false, err
	}
	if !harness.Valid() {
		return false, errors.New(
			"experience semantic proposal harness is invalid",
		)
	}
	if err := validateStorageIdentifier(
		"experience semantic proposal prompt version",
		promptVersion,
	); err != nil {
		return false, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM experience_semantic_proposals
			WHERE candidate_id = ?
				AND harness = ?
				AND prompt_version = ?
		)`,
		candidateID,
		harness,
		promptVersion,
	).Scan(&exists); err != nil {
		return false, errors.New("query experience semantic proposal")
	}
	return exists == 1, nil
}

func (s *Store) GetExperienceSemanticDecision(
	ctx context.Context,
	decisionID string,
) (experience.SemanticDecision, error) {
	if err := validateStorageIdentifier(
		"experience semantic decision ID",
		decisionID,
	); err != nil {
		return experience.SemanticDecision{}, err
	}
	return s.scanExperienceSemanticDecision(s.db.QueryRowContext(
		ctx,
		experienceSemanticDecisionSelectSQL+`
			WHERE decision_id = ?`,
		decisionID,
	))
}

func (s *Store) GetExperienceSemanticProposal(
	ctx context.Context,
	proposalID string,
) (experience.SemanticProposal, error) {
	if err := validateStorageIdentifier(
		"experience semantic proposal ID",
		proposalID,
	); err != nil {
		return experience.SemanticProposal{}, err
	}
	return s.scanExperienceSemanticProposal(s.db.QueryRowContext(
		ctx,
		experienceSemanticProposalSelectSQL+`
			WHERE proposal_id = ?`,
		proposalID,
	))
}

func (s *Store) QueryExperienceSemanticProposals(
	ctx context.Context,
	projectIdentity string,
	limit int,
) ([]experience.SemanticProposal, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(
		ctx,
		experienceSemanticProposalSelectSQL+`
			WHERE project_identity = ?
			ORDER BY generated_at DESC, proposal_id
			LIMIT ?`,
		projectIdentity,
		limit,
	)
	if err != nil {
		return nil, errors.New("query experience semantic proposals")
	}
	defer rows.Close()
	result := make([]experience.SemanticProposal, 0)
	for rows.Next() {
		proposal, err := s.scanExperienceSemanticProposal(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query experience semantic proposals")
	}
	return result, nil
}

// QueryPendingCurrentExperienceSemanticProposals excludes historical prompt
// versions, candidates that already produced an immutable experience, and
// proposals hidden by their latest user review action.
func (s *Store) QueryPendingCurrentExperienceSemanticProposals(
	ctx context.Context,
	projectIdentity string,
	harness experience.Harness,
	limit int,
	includeDeferred bool,
) ([]experience.SemanticProposal, error) {
	if err := validateProjectIdentity(projectIdentity); err != nil {
		return nil, err
	}
	if !harness.Valid() {
		return nil, errors.New(
			"experience semantic proposal harness is invalid",
		)
	}
	limit, err := boundedRepositoryLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(
		ctx,
		experienceSemanticProposalSelectSQL+`
			WHERE project_identity = ?
				AND harness = ?
				AND prompt_version = ?
				AND NOT EXISTS (
					SELECT 1
					FROM experiences approved
					WHERE approved.project_identity =
							experience_semantic_proposals.project_identity
						AND approved.origin_candidate_id =
							experience_semantic_proposals.candidate_id
				)
				AND NOT EXISTS (
					SELECT 1
					FROM experience_review_actions latest
					WHERE latest.proposal_id =
							experience_semantic_proposals.proposal_id
						AND NOT EXISTS (
							SELECT 1
							FROM experience_review_actions newer
							WHERE newer.proposal_id = latest.proposal_id
								AND (
									newer.occurred_at > latest.occurred_at
									OR (
										newer.occurred_at = latest.occurred_at
										AND newer.action_id > latest.action_id
									)
								)
						)
						AND (
							latest.disposition = 'reject'
							OR (
								? = 0
								AND
								latest.disposition = 'defer'
								AND latest.available_after > ?
							)
						)
				)
			ORDER BY generated_at DESC, proposal_id
			LIMIT ?`,
		projectIdentity,
		harness,
		experience.SemanticProposalPromptVersion,
		includeDeferred,
		formatProjectionTime(s.nowUTC()),
		limit,
	)
	if err != nil {
		return nil, errors.New(
			"query pending current experience semantic proposals",
		)
	}
	defer rows.Close()
	result := make([]experience.SemanticProposal, 0)
	for rows.Next() {
		proposal, err := s.scanExperienceSemanticProposal(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New(
			"query pending current experience semantic proposals",
		)
	}
	return result, nil
}

const experienceSemanticProposalSelectSQL = `
	SELECT proposal_id, candidate_id, project_identity, experience_type,
		scope_kind, harness, prompt_version, input_hash, output_hash,
		generated_at, payload, payload_encoding
	FROM experience_semantic_proposals`

func (s *Store) scanExperienceSemanticProposal(
	scanner rowScanner,
) (experience.SemanticProposal, error) {
	var proposalID, candidateID, projectIdentity, experienceType string
	var scopeKind, harness, promptVersion, inputHash, outputHash string
	var generatedAtValue, encoding string
	var payload []byte
	if err := scanner.Scan(
		&proposalID,
		&candidateID,
		&projectIdentity,
		&experienceType,
		&scopeKind,
		&harness,
		&promptVersion,
		&inputHash,
		&outputHash,
		&generatedAtValue,
		&payload,
		&encoding,
	); err != nil {
		return experience.SemanticProposal{}, err
	}
	if err := validateSealedPayloadSize(
		"experience semantic proposal",
		payload,
		maxExperienceSemanticPayloadBytes,
	); err != nil {
		return experience.SemanticProposal{}, err
	}
	payload, err := s.cipher.open(
		"experience_semantic_proposal",
		proposalID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return experience.SemanticProposal{}, err
	}
	var proposal experience.SemanticProposal
	if err := json.Unmarshal(payload, &proposal); err != nil {
		return experience.SemanticProposal{}, errors.New(
			"decode experience semantic proposal payload",
		)
	}
	generatedAt, timeErr := parseProjectionTime(generatedAtValue)
	if timeErr != nil ||
		proposal.ProposalID != proposalID ||
		proposal.CandidateID != candidateID ||
		proposal.ProjectIdentity != projectIdentity ||
		string(proposal.Proposal.Type) != experienceType ||
		string(proposal.Proposal.Scope.Kind) != scopeKind ||
		string(proposal.Provenance.Harness) != harness ||
		proposal.Provenance.PromptVersion != promptVersion ||
		proposal.Provenance.InputHash != inputHash ||
		proposal.Provenance.OutputHash != outputHash ||
		!proposal.Provenance.GeneratedAt.Equal(generatedAt) {
		return experience.SemanticProposal{}, errors.New(
			"experience semantic proposal index does not match payload",
		)
	}
	if err := proposal.Validate(); err != nil {
		return experience.SemanticProposal{}, errors.New(
			"invalid persisted experience semantic proposal",
		)
	}
	return proposal, nil
}

const experienceSemanticDecisionSelectSQL = `
	SELECT decision_id, candidate_id, project_identity, disposition,
		reason_code, proposal_id, harness, prompt_version, input_hash,
		output_hash, generated_at, payload, payload_encoding
	FROM experience_semantic_decisions`

func (s *Store) scanExperienceSemanticDecision(
	scanner rowScanner,
) (experience.SemanticDecision, error) {
	var decisionID, candidateID, projectIdentity, disposition, reasonCode string
	var proposalID sql.NullString
	var harness, promptVersion, inputHash, outputHash string
	var generatedAtValue, encoding string
	var payload []byte
	if err := scanner.Scan(
		&decisionID,
		&candidateID,
		&projectIdentity,
		&disposition,
		&reasonCode,
		&proposalID,
		&harness,
		&promptVersion,
		&inputHash,
		&outputHash,
		&generatedAtValue,
		&payload,
		&encoding,
	); err != nil {
		return experience.SemanticDecision{}, err
	}
	if err := validateSealedPayloadSize(
		"experience semantic decision",
		payload,
		maxExperienceSemanticPayloadBytes,
	); err != nil {
		return experience.SemanticDecision{}, err
	}
	payload, err := s.cipher.open(
		"experience_semantic_decision",
		decisionID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return experience.SemanticDecision{}, err
	}
	var decision experience.SemanticDecision
	if err := json.Unmarshal(payload, &decision); err != nil {
		return experience.SemanticDecision{}, errors.New(
			"decode experience semantic decision payload",
		)
	}
	generatedAt, timeErr := parseProjectionTime(generatedAtValue)
	if timeErr != nil ||
		decision.DecisionID != decisionID ||
		decision.CandidateID != candidateID ||
		decision.ProjectIdentity != projectIdentity ||
		string(decision.Disposition) != disposition ||
		string(decision.ReasonCode) != reasonCode ||
		decision.ProposalID != proposalID.String ||
		string(decision.Provenance.Harness) != harness ||
		decision.Provenance.PromptVersion != promptVersion ||
		decision.Provenance.InputHash != inputHash ||
		decision.Provenance.OutputHash != outputHash ||
		!decision.Provenance.GeneratedAt.Equal(generatedAt) {
		return experience.SemanticDecision{}, errors.New(
			"experience semantic decision index does not match payload",
		)
	}
	if err := decision.Validate(); err != nil {
		return experience.SemanticDecision{}, errors.New(
			"invalid persisted experience semantic decision",
		)
	}
	return decision, nil
}
