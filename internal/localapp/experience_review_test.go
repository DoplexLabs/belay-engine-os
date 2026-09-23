package localapp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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

type experienceReviewListTestStore struct {
	experienceReviewTestStore
	candidates map[string]experience.Candidate
	proposals  map[string]experience.SemanticProposal
	queried    []experience.Harness
}

func (store *experienceReviewListTestStore) GetExperienceCandidate(
	_ context.Context,
	candidateID string,
) (experience.Candidate, error) {
	value, ok := store.candidates[candidateID]
	if !ok {
		return experience.Candidate{}, errors.New("missing candidate")
	}
	return value, nil
}

func (store *experienceReviewListTestStore) GetExperienceSemanticProposal(
	_ context.Context,
	proposalID string,
) (experience.SemanticProposal, error) {
	value, ok := store.proposals[proposalID]
	if !ok {
		return experience.SemanticProposal{}, errors.New("missing proposal")
	}
	return value, nil
}

func (store *experienceReviewListTestStore) QueryPendingCurrentExperienceSemanticProposals(
	_ context.Context,
	projectIdentity string,
	harness experience.Harness,
	limit int,
	_ bool,
) ([]experience.SemanticProposal, error) {
	store.queried = append(store.queried, harness)
	// Mirror the real repository: newest first, proposal_id as the
	// tie-break, then the limit. Map iteration order must not leak into
	// which proposals a bounded query returns.
	var matching []experience.SemanticProposal
	for _, proposal := range store.proposals {
		if proposal.ProjectIdentity == projectIdentity &&
			proposal.Provenance.Harness == harness {
			matching = append(matching, proposal)
		}
	}
	sort.Slice(matching, func(i, j int) bool {
		left, right := matching[i].Provenance.GeneratedAt, matching[j].Provenance.GeneratedAt
		if !left.Equal(right) {
			return left.After(right)
		}
		return matching[i].ProposalID < matching[j].ProposalID
	})
	if len(matching) > limit {
		matching = matching[:limit]
	}
	return matching, nil
}

func experienceReviewListProposal(
	t *testing.T,
	candidate experience.Candidate,
	engine SemanticHarness,
	generatedAt time.Time,
	scope []experience.Harness,
) experience.SemanticProposal {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(experienceSemanticValidOutput(candidate), &raw); err != nil {
		t.Fatal(err)
	}
	harnesses := make([]any, 0, len(scope))
	for _, harness := range scope {
		harnesses = append(harnesses, string(harness))
	}
	firstSemanticApplicability(raw)["harnesses"] = harnesses
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	output, err := decodeExperienceSemanticOutput(encoded)
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := compileExperienceSemanticResults(
		[]experience.Candidate{candidate},
		output,
		engine,
		string(engine)+"-test",
		sha256Prefixed([]byte("review input "+string(engine))),
		sha256Prefixed([]byte("review output "+string(engine))),
		generatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return *results[0].Proposal
}

func TestListExperienceCandidateReviewsForCursorMergesEngineProposals(
	t *testing.T,
) {
	shared := experienceSemanticTestCandidate("shared cursor lesson")
	outcome := experienceReviewOutcome(shared.ProjectIdentity)
	shared.OutcomeRefs = []string{outcome.OutcomeID}
	shared.CandidateID = shared.DeterministicID()
	codexOnly := experienceSemanticTestCandidate("codex only lesson")
	codexOnly.OutcomeRefs = []string{outcome.OutcomeID}
	codexOnly.CandidateID = codexOnly.DeterministicID()
	claudeAndCursor := experienceSemanticTestCandidate("claude and cursor lesson")
	claudeAndCursor.OutcomeRefs = []string{outcome.OutcomeID}
	claudeAndCursor.CandidateID = claudeAndCursor.DeterministicID()

	everyHarness := []experience.Harness{
		experience.HarnessClaude,
		experience.HarnessCodex,
		experience.HarnessCursor,
	}
	older := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	sharedByClaude := experienceReviewListProposal(
		t, shared, SemanticHarnessClaude, older, everyHarness,
	)
	sharedByCodex := experienceReviewListProposal(
		t, shared, SemanticHarnessCodex, newer, everyHarness,
	)
	codexScoped := experienceReviewListProposal(
		t, codexOnly, SemanticHarnessCodex, newer,
		[]experience.Harness{experience.HarnessCodex},
	)
	claudeAndCursorByClaude := experienceReviewListProposal(
		t, claudeAndCursor, SemanticHarnessClaude, older,
		[]experience.Harness{experience.HarnessClaude, experience.HarnessCursor},
	)
	store := &experienceReviewListTestStore{
		experienceReviewTestStore: experienceReviewTestStore{
			outcomes: map[string]trajectory.Outcome{
				outcome.OutcomeID: outcome,
			},
		},
		candidates: map[string]experience.Candidate{
			shared.CandidateID:          shared,
			codexOnly.CandidateID:       codexOnly,
			claudeAndCursor.CandidateID: claudeAndCursor,
		},
		proposals: map[string]experience.SemanticProposal{
			sharedByClaude.ProposalID:          sharedByClaude,
			sharedByCodex.ProposalID:           sharedByCodex,
			codexScoped.ProposalID:             codexScoped,
			claudeAndCursorByClaude.ProposalID: claudeAndCursorByClaude,
		},
	}

	reviews, err := ListExperienceCandidateReviews(
		context.Background(),
		store,
		shared.ProjectIdentity,
		experience.HarnessCursor,
		5,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 {
		t.Fatalf("cursor reviews = %d, want 2: %+v", len(reviews), reviews)
	}
	if reviews[0].CandidateID != shared.CandidateID ||
		reviews[0].ProposalID != sharedByCodex.ProposalID ||
		reviews[0].SemanticProvenance.Harness != experience.HarnessCodex {
		t.Fatalf("first cursor review = %+v, want the newest shared proposal", reviews[0])
	}
	if reviews[1].CandidateID != claudeAndCursor.CandidateID ||
		reviews[1].SemanticProvenance.Harness != experience.HarnessClaude {
		t.Fatalf("second cursor review = %+v, want the claude-engine proposal scoped to Cursor", reviews[1])
	}
	for _, review := range reviews {
		if review.CandidateID == codexOnly.CandidateID {
			t.Fatal("codex-only proposal reached the Cursor review queue")
		}
	}
	if len(store.queried) != 4 {
		t.Fatalf("cursor listing queried %v, want every engine", store.queried)
	}

	store.queried = nil
	claudeReviews, err := ListExperienceCandidateReviews(
		context.Background(),
		store,
		shared.ProjectIdentity,
		experience.HarnessClaude,
		5,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claudeReviews) != 2 || len(store.queried) != 1 ||
		store.queried[0] != experience.HarnessClaude {
		t.Fatalf("claude reviews = %+v queried %v, want only claude-engine proposals", claudeReviews, store.queried)
	}
	for _, review := range claudeReviews {
		if review.SemanticProvenance.Harness != experience.HarnessClaude {
			t.Fatalf("claude queue showed %s proposal", review.SemanticProvenance.Harness)
		}
	}

	limited, err := ListExperienceCandidateReviews(
		context.Background(),
		store,
		shared.ProjectIdentity,
		experience.HarnessCursor,
		1,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ProposalID != sharedByCodex.ProposalID {
		t.Fatalf("limited cursor reviews = %+v, want the newest only", limited)
	}
}

func TestReviewProvenanceHarnessesReadEveryEngineForDeliveryOnlyHarnesses(
	t *testing.T,
) {
	every := []experience.Harness{
		experience.HarnessClaude,
		experience.HarnessCodex,
		experience.HarnessCursor,
		experience.HarnessAntigravity,
	}
	for harness, want := range map[experience.Harness][]experience.Harness{
		experience.HarnessClaude:      {experience.HarnessClaude},
		experience.HarnessCodex:       {experience.HarnessCodex},
		experience.HarnessCursor:      every,
		experience.HarnessAntigravity: every,
	} {
		got := reviewProvenanceHarnesses(harness)
		if len(got) != len(want) {
			t.Fatalf("reviewProvenanceHarnesses(%q) = %v, want %v", harness, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("reviewProvenanceHarnesses(%q) = %v, want %v", harness, got, want)
			}
		}
	}
}

func TestListExperienceCandidateReviewsForAntigravityMergesEngineProposals(
	t *testing.T,
) {
	shared := experienceSemanticTestCandidate("shared antigravity lesson")
	outcome := experienceReviewOutcome(shared.ProjectIdentity)
	shared.OutcomeRefs = []string{outcome.OutcomeID}
	shared.CandidateID = shared.DeterministicID()
	cursorOnly := experienceSemanticTestCandidate("cursor only lesson")
	cursorOnly.OutcomeRefs = []string{outcome.OutcomeID}
	cursorOnly.CandidateID = cursorOnly.DeterministicID()
	codexAndAntigravity := experienceSemanticTestCandidate(
		"codex and antigravity lesson",
	)
	codexAndAntigravity.OutcomeRefs = []string{outcome.OutcomeID}
	codexAndAntigravity.CandidateID = codexAndAntigravity.DeterministicID()

	everyHarness := []experience.Harness{
		experience.HarnessClaude,
		experience.HarnessCodex,
		experience.HarnessCursor,
		experience.HarnessAntigravity,
	}
	older := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	sharedByCodex := experienceReviewListProposal(
		t, shared, SemanticHarnessCodex, older, everyHarness,
	)
	sharedByClaude := experienceReviewListProposal(
		t, shared, SemanticHarnessClaude, newer, everyHarness,
	)
	cursorScoped := experienceReviewListProposal(
		t, cursorOnly, SemanticHarnessClaude, older,
		[]experience.Harness{experience.HarnessCursor},
	)
	codexAndAntigravityByCodex := experienceReviewListProposal(
		t, codexAndAntigravity, SemanticHarnessCodex, older,
		[]experience.Harness{
			experience.HarnessCodex,
			experience.HarnessAntigravity,
		},
	)
	store := &experienceReviewListTestStore{
		experienceReviewTestStore: experienceReviewTestStore{
			outcomes: map[string]trajectory.Outcome{
				outcome.OutcomeID: outcome,
			},
		},
		candidates: map[string]experience.Candidate{
			shared.CandidateID:              shared,
			cursorOnly.CandidateID:          cursorOnly,
			codexAndAntigravity.CandidateID: codexAndAntigravity,
		},
		proposals: map[string]experience.SemanticProposal{
			sharedByCodex.ProposalID:              sharedByCodex,
			sharedByClaude.ProposalID:             sharedByClaude,
			cursorScoped.ProposalID:               cursorScoped,
			codexAndAntigravityByCodex.ProposalID: codexAndAntigravityByCodex,
		},
	}

	reviews, err := ListExperienceCandidateReviews(
		context.Background(),
		store,
		shared.ProjectIdentity,
		experience.HarnessAntigravity,
		5,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 {
		t.Fatalf("antigravity reviews = %d, want 2: %+v", len(reviews), reviews)
	}
	if reviews[0].CandidateID != shared.CandidateID ||
		reviews[0].ProposalID != sharedByClaude.ProposalID ||
		reviews[0].SemanticProvenance.Harness != experience.HarnessClaude {
		t.Fatalf("first antigravity review = %+v, want the newest shared proposal", reviews[0])
	}
	if reviews[1].CandidateID != codexAndAntigravity.CandidateID ||
		reviews[1].SemanticProvenance.Harness != experience.HarnessCodex {
		t.Fatalf("second antigravity review = %+v, want the codex-engine proposal scoped to Antigravity", reviews[1])
	}
	for _, review := range reviews {
		if review.CandidateID == cursorOnly.CandidateID {
			t.Fatal("cursor-only proposal reached the Antigravity review queue")
		}
	}
	if len(store.queried) != 4 {
		t.Fatalf("antigravity listing queried %v, want every engine", store.queried)
	}

	limited, err := ListExperienceCandidateReviews(
		context.Background(),
		store,
		shared.ProjectIdentity,
		experience.HarnessAntigravity,
		1,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ProposalID != sharedByClaude.ProposalID {
		t.Fatalf("limited antigravity reviews = %+v, want the newest only", limited)
	}
}
