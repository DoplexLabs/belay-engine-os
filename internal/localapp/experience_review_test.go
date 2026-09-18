package localapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

type experienceReviewTestStore struct {
	candidate experience.Candidate
	proposal  experience.SemanticProposal
	outcomes  map[string]trajectory.Outcome
	active    []local.StoredExperience
}

func (store *experienceReviewTestStore) GetExperienceCandidate(
	_ context.Context,
	_ string,
) (experience.Candidate, error) {
	return store.candidate, nil
}

func (store *experienceReviewTestStore) GetExperienceSemanticProposal(
	_ context.Context,
	_ string,
) (experience.SemanticProposal, error) {
	return store.proposal, nil
}

func (store *experienceReviewTestStore) GetOutcome(
	_ context.Context,
	outcomeID string,
) (trajectory.Outcome, error) {
	value, ok := store.outcomes[outcomeID]
	if !ok {
		return trajectory.Outcome{}, errors.New("missing outcome")
	}
	return value, nil
}

func (store *experienceReviewTestStore) QueryActiveExperiences(
	_ context.Context,
	_ string,
	_ int,
) ([]local.StoredExperience, error) {
	return append([]local.StoredExperience(nil), store.active...), nil
}

func TestGetExperienceCandidateReviewBindsCurrentProposalEvidenceAndOutcome(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("candidate review")
	outcome := experienceReviewOutcome(candidate.ProjectIdentity)
	candidate.OutcomeRefs = []string{outcome.OutcomeID}
	candidate.CandidateID = candidate.DeterministicID()
	output, err := decodeExperienceSemanticOutput(
		experienceSemanticValidOutput(candidate),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := compileExperienceSemanticResults(
		[]experience.Candidate{candidate},
		output,
		SemanticHarnessClaude,
		"claude-test",
		sha256Prefixed([]byte("review input")),
		sha256Prefixed([]byte("review output")),
		time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &experienceReviewTestStore{
		candidate: candidate,
		proposal:  *results[0].Proposal,
		outcomes: map[string]trajectory.Outcome{
			outcome.OutcomeID: outcome,
		},
	}
	got, err := GetExperienceCandidateReview(
		context.Background(),
		store,
		store.proposal.ProposalID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != ExperienceCandidateReviewSchemaVersion ||
		got.CandidateID != candidate.CandidateID ||
		got.ProposalID != store.proposal.ProposalID ||
		got.ProjectIdentity != candidate.ProjectIdentity ||
		got.Authority != experience.AuthorityNone ||
		got.ProposedContentHash != store.proposal.Proposal.CanonicalContentHash() ||
		got.SemanticInputHash != store.proposal.Provenance.InputHash ||
		got.EvidenceGeneration != candidate.Provenance.InputHash ||
		len(got.Evidence) == 0 ||
		len(got.Evidence) > maxExperienceReviewEvidence ||
		len(got.Outcomes) != 1 ||
		got.Outcomes[0].OutcomeID != outcome.OutcomeID ||
		got.Conflict.State != experience.ConflictNoConflict {
		t.Fatalf("candidate review = %+v", got)
	}
}

func TestGetExperienceCandidateReviewRejectsStaleSemanticPrompt(t *testing.T) {
	candidate := experienceSemanticTestCandidate("stale review")
	proposal := experienceReviewProposal(t, candidate)
	proposal.Provenance.PromptVersion =
		experience.SemanticProposalPromptVersionV2
	proposal.ProposalID = proposal.DeterministicID()
	store := &experienceReviewTestStore{
		candidate: candidate,
		proposal:  proposal,
		outcomes:  map[string]trajectory.Outcome{},
	}
	if _, err := GetExperienceCandidateReview(
		context.Background(),
		store,
		proposal.ProposalID,
	); !errors.Is(err, ErrExperienceReviewStaleProposal) {
		t.Fatalf("stale semantic proposal error = %v", err)
	}
}

func experienceReviewProposal(
	t *testing.T,
	candidate experience.Candidate,
) experience.SemanticProposal {
	t.Helper()
	output, err := decodeExperienceSemanticOutput(
		experienceSemanticValidOutput(candidate),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := compileExperienceSemanticResults(
		[]experience.Candidate{candidate},
		output,
		SemanticHarnessClaude,
		"claude-test",
		sha256Prefixed([]byte("review input")),
		sha256Prefixed([]byte("review output")),
		time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return *results[0].Proposal
}

func experienceReviewOutcome(projectIdentity string) trajectory.Outcome {
	turnIndex := int64(4)
	value := trajectory.Outcome{
		SchemaVersion:   trajectory.OutcomeSchemaVersion,
		ProjectIdentity: projectIdentity,
		SessionKey:      "ses_review",
		OccurredAt: time.Date(
			2026,
			9,
			10,
			16,
			50,
			0,
			0,
			time.UTC,
		),
		Kind:          trajectory.OutcomeCorrection,
		Result:        trajectory.ResultObserved,
		EvidenceClass: trajectory.EvidenceObserved,
		Confidence:    trajectory.ConfidenceHigh,
		SourceRefs: []trajectory.NodeRef{{
			Kind:       trajectory.NodeTranscriptTurn,
			SessionKey: "ses_review",
			TurnIndex:  &turnIndex,
		}},
		DerivationVersion: "review-test.v1",
	}
	value.OutcomeID = value.DeterministicID()
	return value
}
