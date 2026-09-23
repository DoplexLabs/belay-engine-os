package localapp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

const (
	ExperienceCandidateReviewSchemaVersion = "belay.experience-review.v1"
	maxExperienceReviewEvidence            = 5
	maxExperienceCandidateReviews          = 100
)

var (
	ErrExperienceReviewMismatch = errors.New(
		"experience review proposal does not match its deterministic candidate",
	)
	ErrExperienceReviewStaleProposal = errors.New(
		"experience review requires the current semantic prompt version",
	)
)

type ExperienceCandidateReviewStore interface {
	ExperienceConflictPreflightStore
	GetExperienceCandidate(
		context.Context,
		string,
	) (experience.Candidate, error)
	GetExperienceSemanticProposal(
		context.Context,
		string,
	) (experience.SemanticProposal, error)
	GetOutcome(context.Context, string) (trajectory.Outcome, error)
}

type ExperienceCandidateReviewListStore interface {
	ExperienceCandidateReviewStore
	QueryPendingCurrentExperienceSemanticProposals(
		context.Context,
		string,
		experience.Harness,
		int,
		bool,
	) ([]experience.SemanticProposal, error)
}

// ExperienceCandidateReview is a read-only approval preview. Evidence remains
// untrusted input, and the projection carries no instruction authority.
type ExperienceCandidateReview struct {
	SchemaVersion       string                                `json:"schema_version"`
	CandidateID         string                                `json:"candidate_id"`
	ProposalID          string                                `json:"proposal_id"`
	ProjectIdentity     string                                `json:"project_identity"`
	Family              experience.CandidateFamily            `json:"family"`
	ObservedBehavior    string                                `json:"observed_behavior"`
	UserFeedback        string                                `json:"user_feedback,omitempty"`
	Proposed            experience.ExperienceProposal         `json:"proposed"`
	Evidence            []experience.EvidenceRef              `json:"evidence"`
	EvidenceTruncated   bool                                  `json:"evidence_truncated"`
	Outcomes            []trajectory.Outcome                  `json:"outcomes"`
	SemanticProvenance  experience.SemanticProposalProvenance `json:"semantic_provenance"`
	Conflict            experience.ConflictAdvisory           `json:"conflict"`
	ProposedContentHash string                                `json:"proposed_content_hash"`
	SemanticInputHash   string                                `json:"semantic_input_hash"`
	EvidenceGeneration  string                                `json:"evidence_generation"`
	Authority           experience.InstructionAuthority       `json:"instruction_authority"`
}

func GetExperienceCandidateReview(
	ctx context.Context,
	store ExperienceCandidateReviewStore,
	proposalID string,
) (ExperienceCandidateReview, error) {
	if store == nil || strings.TrimSpace(proposalID) == "" {
		return ExperienceCandidateReview{}, errors.New(
			"experience candidate review requires a store and proposal ID",
		)
	}
	proposal, err := store.GetExperienceSemanticProposal(ctx, proposalID)
	if err != nil {
		return ExperienceCandidateReview{}, err
	}
	if err := proposal.Validate(); err != nil {
		return ExperienceCandidateReview{}, err
	}
	if proposal.Provenance.PromptVersion != ExperiencePromptVersion {
		return ExperienceCandidateReview{}, ErrExperienceReviewStaleProposal
	}
	candidate, err := store.GetExperienceCandidate(ctx, proposal.CandidateID)
	if err != nil {
		return ExperienceCandidateReview{}, err
	}
	if err := candidate.Validate(); err != nil {
		return ExperienceCandidateReview{}, err
	}
	if candidate.CandidateID != proposal.CandidateID ||
		candidate.ProjectIdentity != proposal.ProjectIdentity ||
		candidate.Authority != experience.AuthorityNone ||
		candidate.LifecycleState != experience.LifecycleCandidate {
		return ExperienceCandidateReview{}, ErrExperienceReviewMismatch
	}

	outcomes, err := reviewOutcomes(ctx, store, candidate)
	if err != nil {
		return ExperienceCandidateReview{}, err
	}
	conflict, err := PreflightExperienceConflict(ctx, store, proposal)
	if err != nil {
		return ExperienceCandidateReview{}, err
	}
	evidence, truncated := boundedExperienceReviewEvidence(
		candidate.Evidence.Refs,
	)
	result := ExperienceCandidateReview{
		SchemaVersion:       ExperienceCandidateReviewSchemaVersion,
		CandidateID:         candidate.CandidateID,
		ProposalID:          proposal.ProposalID,
		ProjectIdentity:     candidate.ProjectIdentity,
		Family:              candidate.Family,
		ObservedBehavior:    candidate.ObservedBehavior,
		UserFeedback:        candidate.UserFeedback,
		Proposed:            proposal.Proposal,
		Evidence:            evidence,
		EvidenceTruncated:   truncated,
		Outcomes:            outcomes,
		SemanticProvenance:  proposal.Provenance,
		Conflict:            conflict,
		ProposedContentHash: proposal.Proposal.CanonicalContentHash(),
		SemanticInputHash:   proposal.Provenance.InputHash,
		EvidenceGeneration:  candidate.Provenance.InputHash,
		Authority:           experience.AuthorityNone,
	}
	if result.Proposed.Scope.ProjectIdentity != result.ProjectIdentity ||
		result.Conflict.ProposalID != result.ProposalID ||
		result.ProposedContentHash == "" ||
		result.SemanticInputHash == "" ||
		result.EvidenceGeneration == "" {
		return ExperienceCandidateReview{}, ErrExperienceReviewMismatch
	}
	return result, nil
}

func ListExperienceCandidateReviews(
	ctx context.Context,
	store ExperienceCandidateReviewListStore,
	projectIdentity string,
	harness experience.Harness,
	limit int,
	includeDeferred bool,
) ([]ExperienceCandidateReview, error) {
	if store == nil ||
		strings.TrimSpace(projectIdentity) == "" ||
		!harness.Valid() ||
		limit < 1 ||
		limit > maxExperienceCandidateReviews {
		return nil, errors.New(
			"experience candidate review list request is invalid",
		)
	}
	engines := reviewProvenanceHarnesses(harness)
	proposals, err := queryPendingReviewProposals(
		ctx,
		store,
		projectIdentity,
		harness,
		engines,
		limit,
		includeDeferred,
	)
	if err != nil {
		return nil, err
	}
	result := make([]ExperienceCandidateReview, 0, len(proposals))
	for _, proposal := range proposals {
		review, err := GetExperienceCandidateReview(
			ctx,
			store,
			proposal.ProposalID,
		)
		if err != nil {
			return nil, err
		}
		if review.ProjectIdentity != projectIdentity ||
			!selectionContains(engines, review.SemanticProvenance.Harness) {
			return nil, ErrExperienceReviewMismatch
		}
		result = append(result, review)
	}
	return result, nil
}

// reviewProvenanceHarnesses lists the analysis engines whose pending proposals
// the review queue for a harness may show. A semantic proposal is stamped with
// the harness that ran Belay's semantic pass. Claude Code and Codex review
// their own engine's proposals. Cursor and Antigravity queues read proposals
// from every engine and keep the ones whose scope applies to that harness:
// their CLIs can run the pass, but a Cursor or Antigravity user is most often
// analyzed by whichever engine is installed, and would otherwise never see a
// lesson to approve.
func reviewProvenanceHarnesses(
	harness experience.Harness,
) []experience.Harness {
	switch harness {
	case experience.HarnessCursor, experience.HarnessAntigravity:
		return []experience.Harness{
			experience.HarnessClaude,
			experience.HarnessCodex,
			experience.HarnessCursor,
			experience.HarnessAntigravity,
		}
	default:
		return []experience.Harness{harness}
	}
}

// queryPendingReviewProposals keeps the single-engine query untouched for a
// harness that reviews its own proposals. For a delivery-only harness it merges
// every engine's pending proposals, keeps those scoped to the harness, and
// shows one proposal per candidate: the most recently generated. An empty
// scope is harness-neutral, matching the Mission Pack compiler.
func queryPendingReviewProposals(
	ctx context.Context,
	store ExperienceCandidateReviewListStore,
	projectIdentity string,
	harness experience.Harness,
	engines []experience.Harness,
	limit int,
	includeDeferred bool,
) ([]experience.SemanticProposal, error) {
	if len(engines) == 1 {
		return store.QueryPendingCurrentExperienceSemanticProposals(
			ctx,
			projectIdentity,
			engines[0],
			limit,
			includeDeferred,
		)
	}
	latest := make(map[string]experience.SemanticProposal)
	for _, engine := range engines {
		proposals, err := store.QueryPendingCurrentExperienceSemanticProposals(
			ctx,
			projectIdentity,
			engine,
			limit,
			includeDeferred,
		)
		if err != nil {
			return nil, err
		}
		for _, proposal := range proposals {
			if !proposalScopeAppliesTo(proposal, harness) {
				continue
			}
			current, seen := latest[proposal.CandidateID]
			if !seen || proposalGeneratedAfter(proposal, current) {
				latest[proposal.CandidateID] = proposal
			}
		}
	}
	merged := make([]experience.SemanticProposal, 0, len(latest))
	for _, proposal := range latest {
		merged = append(merged, proposal)
	}
	sort.Slice(merged, func(i, j int) bool {
		if !merged[i].Provenance.GeneratedAt.Equal(merged[j].Provenance.GeneratedAt) {
			return merged[i].Provenance.GeneratedAt.After(merged[j].Provenance.GeneratedAt)
		}
		return merged[i].ProposalID < merged[j].ProposalID
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

func proposalScopeAppliesTo(
	proposal experience.SemanticProposal,
	harness experience.Harness,
) bool {
	scoped := proposal.Proposal.Scope.Harnesses
	return len(scoped) == 0 || selectionContains(scoped, harness)
}

func proposalGeneratedAfter(
	candidate experience.SemanticProposal,
	current experience.SemanticProposal,
) bool {
	if candidate.Provenance.GeneratedAt.Equal(current.Provenance.GeneratedAt) {
		return candidate.ProposalID < current.ProposalID
	}
	return candidate.Provenance.GeneratedAt.After(current.Provenance.GeneratedAt)
}

func reviewOutcomes(
	ctx context.Context,
	store ExperienceCandidateReviewStore,
	candidate experience.Candidate,
) ([]trajectory.Outcome, error) {
	ids := append([]string(nil), candidate.OutcomeRefs...)
	sort.Strings(ids)
	result := make([]trajectory.Outcome, 0, len(ids))
	for _, outcomeID := range ids {
		outcome, err := store.GetOutcome(ctx, outcomeID)
		if err != nil {
			return nil, err
		}
		if outcome.OutcomeID != outcomeID ||
			outcome.ProjectIdentity != candidate.ProjectIdentity {
			return nil, ErrExperienceReviewMismatch
		}
		result = append(result, outcome)
	}
	return result, nil
}

func boundedExperienceReviewEvidence(
	refs []experience.EvidenceRef,
) ([]experience.EvidenceRef, bool) {
	values := append([]experience.EvidenceRef(nil), refs...)
	sort.Slice(values, func(i, j int) bool {
		left, _ := json.Marshal(values[i])
		right, _ := json.Marshal(values[j])
		return string(left) < string(right)
	})
	result := make(
		[]experience.EvidenceRef,
		0,
		min(len(values), maxExperienceReviewEvidence),
	)
	available := 0
	for _, ref := range values {
		if strings.TrimSpace(ref.Excerpt) == "" {
			continue
		}
		available++
		if len(result) < maxExperienceReviewEvidence {
			result = append(result, ref)
		}
	}
	return result, available > len(result)
}

var _ ExperienceCandidateReviewStore = (*local.Store)(nil)
var _ ExperienceCandidateReviewListStore = (*local.Store)(nil)
