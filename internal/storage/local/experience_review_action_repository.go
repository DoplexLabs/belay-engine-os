package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const maxExperienceReviewActionPayloadBytes = 64 << 10

var (
	ErrExperienceReviewActionInvalid = errors.New(
		"experience review action is invalid",
	)
	ErrExperienceReviewActionStale = errors.New(
		"experience review action preview is stale",
	)
	ErrExperienceReviewActionConflict = errors.New(
		"experience review action identity conflicts with persisted record",
	)
)

type ExperienceReviewDisposition string

const (
	ExperienceReviewDefer  ExperienceReviewDisposition = "defer"
	ExperienceReviewReject ExperienceReviewDisposition = "reject"
)

func (value ExperienceReviewDisposition) Valid() bool {
	return value == ExperienceReviewDefer ||
		value == ExperienceReviewReject
}

type ExperienceReviewActionInput struct {
	Claims         experience.ApprovalTokenClaims
	Actor          string
	Disposition    ExperienceReviewDisposition
	OccurredAt     time.Time
	AvailableAfter *time.Time
}

type ExperienceReviewAction struct {
	ActionID              string                         `json:"action_id"`
	ProposalID            string                         `json:"proposal_id"`
	CandidateID           string                         `json:"candidate_id"`
	ProjectIdentity       string                         `json:"project_identity"`
	Disposition           ExperienceReviewDisposition    `json:"disposition"`
	OccurredAt            time.Time                      `json:"occurred_at"`
	AvailableAfter        *time.Time                     `json:"available_after,omitempty"`
	ApprovalTokenIssuedAt time.Time                      `json:"approval_token_issued_at"`
	Actor                 string                         `json:"actor"`
	Claims                experience.ApprovalTokenClaims `json:"approval_token_claims"`
}

type ExperienceReviewActionResult struct {
	Action   ExperienceReviewAction `json:"action"`
	Replayed bool                   `json:"replayed"`
}

// RecordExperienceReviewAction atomically validates a token-bound review
// preview and appends a durable defer or reject action. Review actions never
// mutate the semantic proposal or create instruction authority.
func (s *Store) RecordExperienceReviewAction(
	ctx context.Context,
	input ExperienceReviewActionInput,
) (ExperienceReviewActionResult, error) {
	input.Actor = strings.TrimSpace(input.Actor)
	input.Claims.IssuedAt = input.Claims.IssuedAt.UTC()
	input.Claims.ExpiresAt = input.Claims.ExpiresAt.UTC()
	input.OccurredAt = input.OccurredAt.UTC()
	if input.AvailableAfter != nil {
		value := input.AvailableAfter.UTC()
		input.AvailableAfter = &value
	}
	if !validExperienceApprovalClaims(input.Claims) ||
		!input.Disposition.Valid() ||
		input.Actor == "" ||
		len(input.Actor) > 256 ||
		input.OccurredAt.IsZero() ||
		input.OccurredAt.Before(input.Claims.IssuedAt) ||
		input.OccurredAt.After(input.Claims.ExpiresAt) ||
		!validExperienceReviewAvailability(
			input.Disposition,
			input.OccurredAt,
			input.AvailableAfter,
		) {
		return ExperienceReviewActionResult{},
			ErrExperienceReviewActionInvalid
	}
	now := s.nowUTC()
	if input.OccurredAt.After(now) {
		return ExperienceReviewActionResult{},
			ErrExperienceReviewActionInvalid
	}

	action := ExperienceReviewAction{
		ActionID: experienceReviewActionID(
			input.Claims.ProposalID,
			input.Disposition,
			input.Claims.IssuedAt,
		),
		ProposalID:            input.Claims.ProposalID,
		CandidateID:           input.Claims.CandidateID,
		ProjectIdentity:       input.Claims.ProjectIdentity,
		Disposition:           input.Disposition,
		OccurredAt:            input.OccurredAt,
		AvailableAfter:        input.AvailableAfter,
		ApprovalTokenIssuedAt: input.Claims.IssuedAt,
		Actor:                 input.Actor,
		Claims:                input.Claims,
	}
	plaintext, payload, err := s.sealDomainJSON(
		"experience_review_action",
		action.ActionID,
		"payload",
		action,
		maxExperienceReviewActionPayloadBytes,
	)
	if err != nil {
		return ExperienceReviewActionResult{}, err
	}

	tx, err := s.beginExperienceReviewActionTx(ctx)
	if err != nil {
		return ExperienceReviewActionResult{},
			errors.New("begin experience review action")
	}
	defer tx.Rollback()
	result := ExperienceReviewActionResult{Action: action}
	err = withMutationTx(
		ctx,
		tx,
		mutationExperienceReviewAction,
		func() error {
			replayed, err := s.replayExperienceReviewActionTx(
				ctx,
				tx,
				action.ActionID,
				plaintext,
			)
			if err != nil {
				return err
			}
			if replayed {
				result.Replayed = true
				return nil
			}
			if now.After(input.Claims.ExpiresAt) {
				return ErrExperienceReviewActionStale
			}
			proposal, err := s.scanExperienceSemanticProposal(
				tx.QueryRowContext(
					ctx,
					experienceSemanticProposalSelectSQL+
						` WHERE proposal_id = ?`,
					input.Claims.ProposalID,
				),
			)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrExperienceReviewActionStale
				}
				return err
			}
			candidate, err := s.scanExperienceCandidate(
				tx.QueryRowContext(
					ctx,
					experienceCandidateSelectSQL+
						` WHERE candidate_id = ?`,
					input.Claims.CandidateID,
				),
			)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrExperienceReviewActionStale
				}
				return err
			}
			if !approvalClaimsMatchCurrent(
				input.Claims,
				candidate,
				proposal,
			) {
				return ErrExperienceReviewActionStale
			}
			_, approved, err := s.findApprovedCandidateExperienceTx(
				ctx,
				tx,
				candidate.ProjectIdentity,
				candidate.CandidateID,
			)
			if err != nil {
				return err
			}
			if approved {
				return ErrExperienceReviewActionStale
			}

			insert, err := tx.ExecContext(ctx, `
				INSERT INTO experience_review_actions (
					action_id, proposal_id, candidate_id,
					project_identity, disposition, occurred_at,
					available_after, approval_token_issued_at,
					payload, payload_encoding, inserted_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(action_id) DO NOTHING`,
				action.ActionID,
				action.ProposalID,
				action.CandidateID,
				action.ProjectIdentity,
				action.Disposition,
				formatProjectionTime(action.OccurredAt),
				nullableExperienceReviewTime(action.AvailableAfter),
				formatProjectionTime(action.ApprovalTokenIssuedAt),
				payload,
				payloadEncodingAESGCM,
				formatProjectionTime(now),
			)
			if err != nil {
				return fmt.Errorf(
					"insert experience review action: %w",
					err,
				)
			}
			affected, err := insert.RowsAffected()
			if err != nil {
				return errors.New(
					"inspect experience review action persistence",
				)
			}
			if affected == 1 {
				return nil
			}

			replayed, err = s.replayExperienceReviewActionTx(
				ctx,
				tx,
				action.ActionID,
				plaintext,
			)
			if err != nil {
				return err
			}
			if !replayed {
				return errors.New(
					"duplicate experience review action was not found",
				)
			}
			result.Replayed = true
			return nil
		},
	)
	if err != nil {
		return ExperienceReviewActionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExperienceReviewActionResult{},
			errors.New("commit experience review action")
	}
	return result, nil
}

func (s *Store) beginExperienceReviewActionTx(
	ctx context.Context,
) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err == nil ||
		!isSQLiteBusy(err) ||
		ctx.Err() != nil {
		return tx, err
	}
	return s.db.BeginTx(ctx, nil)
}

func (s *Store) replayExperienceReviewActionTx(
	ctx context.Context,
	tx *sql.Tx,
	actionID string,
	plaintext []byte,
) (bool, error) {
	var payload []byte
	var encoding string
	err := tx.QueryRowContext(ctx, `
		SELECT payload, payload_encoding
		FROM experience_review_actions
		WHERE action_id = ?`,
		actionID,
	).Scan(&payload, &encoding)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New(
			"read duplicate experience review action",
		)
	}
	if err := validateSealedPayloadSize(
		"experience review action",
		payload,
		maxExperienceReviewActionPayloadBytes,
	); err != nil {
		return false, err
	}
	payload, err = s.cipher.open(
		"experience_review_action",
		actionID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(payload, plaintext) {
		return false, ErrExperienceReviewActionConflict
	}
	return true, nil
}

func validExperienceReviewAvailability(
	disposition ExperienceReviewDisposition,
	occurredAt time.Time,
	availableAfter *time.Time,
) bool {
	switch disposition {
	case ExperienceReviewDefer:
		return availableAfter != nil &&
			availableAfter.After(occurredAt)
	case ExperienceReviewReject:
		return availableAfter == nil
	default:
		return false
	}
}

func experienceReviewActionID(
	proposalID string,
	disposition ExperienceReviewDisposition,
	issuedAt time.Time,
) string {
	return stableLocalID(
		"era_",
		proposalID,
		string(disposition),
		formatProjectionTime(issuedAt.UTC()),
	)
}

func nullableExperienceReviewTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatProjectionTime(value.UTC())
}

func decodeExperienceReviewActionPayload(
	payload []byte,
) (ExperienceReviewAction, error) {
	var action ExperienceReviewAction
	if err := json.Unmarshal(payload, &action); err != nil {
		return ExperienceReviewAction{},
			errors.New("decode experience review action payload")
	}
	return action, nil
}
