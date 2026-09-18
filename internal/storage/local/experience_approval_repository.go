package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const experienceApprovalExtractorVersion = "belay.experience-approval.v1"

var (
	ErrExperienceApprovalStale = errors.New(
		"experience approval preview is stale",
	)
	ErrExperienceApprovalConflict = errors.New(
		"experience candidate already has a different approved version",
	)
	ErrExperienceApprovalInvalidContent = errors.New(
		"experience approved content is invalid",
	)
)

type ExperienceApprovalInput struct {
	Claims           experience.ApprovalTokenClaims
	ApprovedBy       string
	ApprovedAt       time.Time
	Mode             experience.ApprovalMode
	ApprovedProposal experience.ExperienceProposal
}

type ExperienceApprovalResult struct {
	Experience experience.Experience
	Replayed   bool
}

// ApproveExperienceProposal validates the preview claims and creates the first
// immutable, user-approved experience in one SQLite transaction. It does not
// activate the experience or compile a Mission Pack generation.
func (s *Store) ApproveExperienceProposal(
	ctx context.Context,
	input ExperienceApprovalInput,
) (ExperienceApprovalResult, error) {
	input.ApprovedBy = strings.TrimSpace(input.ApprovedBy)
	if input.Mode == "" {
		input.Mode = experience.ApprovalAsProposed
	}
	input.Claims.IssuedAt = input.Claims.IssuedAt.UTC()
	input.Claims.ExpiresAt = input.Claims.ExpiresAt.UTC()
	input.ApprovedAt = input.ApprovedAt.UTC()
	if !validExperienceApprovalClaims(input.Claims) ||
		!input.Mode.Valid() ||
		input.ApprovedBy == "" ||
		len(input.ApprovedBy) > 256 ||
		input.ApprovedAt.IsZero() ||
		input.ApprovedAt.Before(input.Claims.IssuedAt) ||
		input.ApprovedAt.After(input.Claims.ExpiresAt) {
		return ExperienceApprovalResult{},
			ErrExperienceApprovalTokenInvalid
	}
	now := s.nowUTC()
	if now.After(input.Claims.ExpiresAt) {
		return ExperienceApprovalResult{},
			ErrExperienceApprovalTokenExpired
	}
	if input.ApprovedAt.After(now) {
		return ExperienceApprovalResult{},
			ErrExperienceApprovalTokenInvalid
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExperienceApprovalResult{},
			errors.New("begin experience approval")
	}
	defer tx.Rollback()
	var result ExperienceApprovalResult
	err = withMutationTx(
		ctx,
		tx,
		mutationExperienceRegistry,
		func() error {
			proposal, err := s.scanExperienceSemanticProposal(
				tx.QueryRowContext(
					ctx,
					experienceSemanticProposalSelectSQL+
						` WHERE proposal_id = ?`,
					input.Claims.ProposalID,
				),
			)
			if err != nil {
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
				return err
			}
			if !approvalClaimsMatchCurrent(
				input.Claims,
				candidate,
				proposal,
			) {
				return ErrExperienceApprovalStale
			}
			blocked, err := s.experienceReviewActionBlocksApprovalTx(
				ctx,
				tx,
				input.Claims.ProposalID,
				input.Claims.IssuedAt,
				now,
			)
			if err != nil {
				return err
			}
			if blocked {
				return ErrExperienceApprovalStale
			}
			approvedProposal := proposal.Proposal
			if input.Mode != experience.ApprovalAsProposed {
				if err := experience.ValidateApprovedProposal(
					proposal.Proposal,
					input.ApprovedProposal,
					input.Mode,
				); err != nil {
					return fmt.Errorf(
						"%w: %v",
						ErrExperienceApprovalInvalidContent,
						err,
					)
				}
				approvedProposal = input.ApprovedProposal
			}
			approvedContentHash :=
				approvedProposal.CanonicalContentHash()

			existing, found, err := s.findApprovedCandidateExperienceTx(
				ctx,
				tx,
				candidate.ProjectIdentity,
				candidate.CandidateID,
			)
			if err != nil {
				return err
			}
			if found {
				if existing.Experience.ContentHash !=
					approvedContentHash ||
					existing.Experience.Provenance.InputHash !=
						input.Claims.SemanticInputHash ||
					existing.Experience.Governance.Approval == nil ||
					existing.Experience.Governance.Approval.Mode !=
						input.Mode ||
					existing.Experience.Governance.Approval.
						ProposedContentHash !=
						input.Claims.ProposedContentHash ||
					existing.Experience.Governance.Approval.
						ApprovedContentHash != approvedContentHash {
					return ErrExperienceApprovalConflict
				}
				result = ExperienceApprovalResult{
					Experience: existing.Experience,
					Replayed:   true,
				}
				return nil
			}

			value := approvedExperienceFromProposal(
				candidate,
				proposal,
				approvedProposal,
				input.Mode,
				input.ApprovedBy,
				input.ApprovedAt,
			)
			prepared, err := s.prepareExperiencePersistence(value)
			if err != nil {
				return err
			}
			inserted, err := s.insertPreparedExperienceTx(
				ctx,
				tx,
				prepared,
			)
			if err != nil {
				return err
			}
			if !inserted {
				return ErrExperienceApprovalConflict
			}
			result = ExperienceApprovalResult{Experience: value}
			return nil
		},
	)
	if err != nil {
		return ExperienceApprovalResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExperienceApprovalResult{},
			errors.New("commit experience approval")
	}
	return result, nil
}

func (s *Store) experienceReviewActionBlocksApprovalTx(
	ctx context.Context,
	tx *sql.Tx,
	proposalID string,
	tokenIssuedAt time.Time,
	now time.Time,
) (bool, error) {
	var disposition ExperienceReviewDisposition
	var occurredAtValue string
	var availableAfterValue sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT disposition, occurred_at, available_after
		FROM experience_review_actions
		WHERE proposal_id = ?
		ORDER BY occurred_at DESC, action_id DESC
		LIMIT 1`,
		proposalID,
	).Scan(
		&disposition,
		&occurredAtValue,
		&availableAfterValue,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New(
			"read experience approval review state",
		)
	}
	occurredAt, err := parseProjectionTime(occurredAtValue)
	if err != nil {
		return false, errors.New(
			"parse experience approval review state",
		)
	}
	if !occurredAt.Before(tokenIssuedAt) ||
		disposition == ExperienceReviewReject {
		return true, nil
	}
	if disposition != ExperienceReviewDefer ||
		!availableAfterValue.Valid {
		return false, errors.New(
			"invalid experience approval review state",
		)
	}
	// A token issued after the defer was recorded represents an explicit
	// reopened review. Tokens issued before the review action remain stale.
	if tokenIssuedAt.After(occurredAt) {
		return false, nil
	}
	availableAfter, err := parseProjectionTime(
		availableAfterValue.String,
	)
	if err != nil {
		return false, errors.New(
			"parse experience approval review availability",
		)
	}
	return availableAfter.After(now), nil
}

func approvalClaimsMatchCurrent(
	claims experience.ApprovalTokenClaims,
	candidate experience.Candidate,
	proposal experience.SemanticProposal,
) bool {
	return proposal.Provenance.PromptVersion ==
		experience.SemanticProposalPromptVersion &&
		claims.CandidateID == candidate.CandidateID &&
		claims.CandidateID == proposal.CandidateID &&
		claims.ProposalID == proposal.ProposalID &&
		claims.ProjectIdentity == candidate.ProjectIdentity &&
		claims.ProjectIdentity == proposal.ProjectIdentity &&
		claims.SemanticInputHash == proposal.Provenance.InputHash &&
		claims.ProposedContentHash ==
			proposal.Proposal.CanonicalContentHash() &&
		claims.EvidenceGeneration == candidate.Provenance.InputHash &&
		candidate.Authority == experience.AuthorityNone &&
		candidate.LifecycleState == experience.LifecycleCandidate &&
		proposal.Authority == experience.AuthorityNone
}

func approvedExperienceFromProposal(
	candidate experience.Candidate,
	proposal experience.SemanticProposal,
	approvedProposal experience.ExperienceProposal,
	mode experience.ApprovalMode,
	approvedBy string,
	approvedAt time.Time,
) experience.Experience {
	value := experience.Experience{
		SchemaVersion:     experience.ExperienceSchemaVersion,
		OriginCandidateID: candidate.CandidateID,
		Version:           1,
		Type:              approvedProposal.Type,
		Scope:             approvedProposal.Scope,
		Applicability:     approvedProposal.Applicability,
		Guidance:          approvedProposal.Guidance,
		Verifier:          approvedProposal.Verifier,
		Evidence:          candidate.Evidence,
		Provenance: experience.Provenance{
			ExtractorVersion:  experienceApprovalExtractorVersion,
			Harness:           proposal.Provenance.Harness,
			Model:             proposal.Provenance.Model,
			PromptVersion:     proposal.Provenance.PromptVersion,
			InputHash:         proposal.Provenance.InputHash,
			SourceCandidateID: candidate.CandidateID,
			GeneratedAt:       approvedAt,
		},
		Governance: experience.Governance{
			LifecycleState: experience.LifecycleApproved,
			Authority:      experience.AuthorityUserApproved,
		},
		CreatedAt: approvedAt,
	}
	value.ExperienceID = experience.DeriveExperienceID(
		value.Scope.ProjectIdentity,
		value.OriginCandidateID,
	)
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval = &experience.ApprovalProvenance{
		ApprovedBy:          approvedBy,
		ApprovedAt:          approvedAt,
		Mode:                mode,
		CandidateID:         candidate.CandidateID,
		ProposedContentHash: proposal.Proposal.CanonicalContentHash(),
		ApprovedContentHash: value.ContentHash,
	}
	return value
}

func (s *Store) findApprovedCandidateExperienceTx(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	candidateID string,
) (StoredExperience, bool, error) {
	value, err := s.scanStoredExperience(tx.QueryRowContext(
		ctx,
		experienceSelectSQL+`
			WHERE value.project_identity = ?
				AND value.origin_candidate_id = ?
				AND value.version = 1`,
		projectIdentity,
		candidateID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredExperience{}, false, nil
	}
	return value, err == nil, err
}
