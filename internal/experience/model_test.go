package experience

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCandidateHasNoAuthorityAndRejectsLearnedDeny(t *testing.T) {
	candidate := validCandidate(t)
	if err := candidate.Validate(); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}

	withAuthority := candidate
	withAuthority.Authority = AuthorityUserApproved
	if err := withAuthority.Validate(); err == nil || !strings.Contains(err.Error(), "authority must be none") {
		t.Fatalf("candidate authority error = %v, want no-authority rejection", err)
	}

	withDeny := candidate
	withDeny.Proposal.Guidance.InterventionStrength = InterventionDeny
	if err := withDeny.Validate(); err == nil || !strings.Contains(err.Error(), "cannot use deny") {
		t.Fatalf("learned deny error = %v, want deny rejection", err)
	}

	withoutSemanticProvenance := candidate
	withoutSemanticProvenance.Provenance.Harness = ""
	withoutSemanticProvenance.Provenance.Model = ""
	withoutSemanticProvenance.Provenance.PromptVersion = ""
	if err := withoutSemanticProvenance.Validate(); err == nil || !strings.Contains(err.Error(), "requires harness and prompt") {
		t.Fatalf("semantic provenance error = %v, want harness/prompt rejection", err)
	}

	pending := withoutSemanticProvenance
	pending.Proposal.SemanticCompilationPending = true
	pending.Proposal.Confidence = nil
	pending.Proposal.Guidance.InterventionStrength = InterventionObserve
	pending.CandidateID = pending.DeterministicID()
	if err := pending.Validate(); err != nil {
		t.Fatalf("semantic-pending candidate rejected: %v", err)
	}

	pendingWithConfidence := pending
	confidence := 0.8
	pendingWithConfidence.Proposal.Confidence = &confidence
	if err := pendingWithConfidence.Validate(); err == nil || !strings.Contains(err.Error(), "cannot claim proposal confidence") {
		t.Fatalf("pending confidence error = %v, want confidence rejection", err)
	}

	pendingWithGuidance := pending
	pendingWithGuidance.Proposal.Guidance.InterventionStrength = InterventionAdvise
	if err := pendingWithGuidance.Validate(); err == nil || !strings.Contains(err.Error(), "observe-only") {
		t.Fatalf("pending guidance error = %v, want observe-only rejection", err)
	}

	crossProject := candidate
	crossProject.Proposal.Scope.ProjectIdentity = "git@example.test:doplexlabs/other.git"
	if err := crossProject.Validate(); err == nil || !strings.Contains(err.Error(), "scope must match") {
		t.Fatalf("cross-project proposal error = %v, want project-scope rejection", err)
	}
}

func TestSemanticProposalIsSeparateInactiveAndDeterministic(t *testing.T) {
	candidate := validCandidate(t)
	value := SemanticProposal{
		SchemaVersion:   SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        candidate.Proposal,
		Provenance: SemanticProposalProvenance{
			Harness:       HarnessClaude,
			Model:         "claude-test",
			PromptVersion: "belay.experience-prompt.v1",
			InputHash:     sha256Value("semantic input"),
			OutputHash:    sha256Value("semantic output"),
			GeneratedAt:   time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		},
		Authority: AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("valid semantic proposal rejected: %v", err)
	}

	withAuthority := value
	withAuthority.Authority = AuthorityUserApproved
	if err := withAuthority.Validate(); err == nil ||
		!strings.Contains(err.Error(), "authority must be none") {
		t.Fatalf("semantic proposal authority error = %v", err)
	}

	pending := value
	pending.Proposal.SemanticCompilationPending = true
	if err := pending.Validate(); err == nil ||
		!strings.Contains(err.Error(), "cannot remain pending") {
		t.Fatalf("semantic proposal pending error = %v", err)
	}

	mismatchedProject := value
	mismatchedProject.Proposal.Scope.ProjectIdentity = "other-project"
	mismatchedProject.ProposalID = mismatchedProject.DeterministicID()
	if err := mismatchedProject.Validate(); err == nil ||
		!strings.Contains(err.Error(), "scope must match") {
		t.Fatalf("semantic proposal project error = %v", err)
	}
}

func TestSemanticProposalV3ProvenanceRemainsValid(t *testing.T) {
	candidate := validCandidate(t)
	provenance := SemanticProposalProvenance{
		Harness:       HarnessClaude,
		Model:         "claude-test",
		PromptVersion: SemanticProposalPromptVersionV3,
		InputHash:     sha256Value("v3 semantic input"),
		OutputHash:    sha256Value("v3 semantic output"),
		GeneratedAt:   time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
	}
	if err := provenance.Validate(); err != nil {
		t.Fatalf("valid v3 semantic provenance rejected: %v", err)
	}
	value := SemanticProposal{
		SchemaVersion:   SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        candidate.Proposal,
		Provenance:      provenance,
		Authority:       AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("valid v3 semantic proposal rejected: %v", err)
	}
}

func TestSemanticProposalRejectsNonFiniteConfidence(t *testing.T) {
	candidate := validCandidate(t)
	value := SemanticProposal{
		SchemaVersion:   SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        candidate.Proposal,
		Provenance: SemanticProposalProvenance{
			Harness:       HarnessClaude,
			Model:         "claude-test",
			PromptVersion: SemanticProposalPromptVersion,
			InputHash:     sha256Value("semantic input"),
			OutputHash:    sha256Value("semantic output"),
			GeneratedAt:   time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		},
		Authority: AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("valid semantic proposal rejected: %v", err)
	}

	for name, confidence := range map[string]float64{
		"nan":               math.NaN(),
		"positive infinity": math.Inf(1),
		"negative infinity": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			invalid := value
			invalid.Proposal.Confidence = &confidence
			if err := invalid.Validate(); err == nil ||
				!strings.Contains(err.Error(), "confidence must be between zero and one") {
				t.Fatalf("semantic proposal confidence error = %v", err)
			}
		})
	}
}

func TestSemanticDecisionBranchesAreClosedAndDeterministic(t *testing.T) {
	candidate := validCandidate(t)
	proposal := SemanticProposal{
		SchemaVersion:   SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        candidate.Proposal,
		Provenance: SemanticProposalProvenance{
			Harness:       HarnessClaude,
			Model:         "claude-test",
			PromptVersion: SemanticProposalPromptVersion,
			InputHash:     sha256Value("decision input"),
			OutputHash:    sha256Value("decision output"),
			GeneratedAt:   time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		},
		Authority: AuthorityNone,
	}
	proposal.ProposalID = proposal.DeterministicID()
	decision := SemanticDecision{
		SchemaVersion:   SemanticDecisionSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Disposition:     SemanticDispositionPropose,
		ReasonCode:      SemanticReasonReusableSupported,
		Explanation:     "The cited correction supports reusable guidance.",
		Confidence:      0.9,
		ProposalID:      proposal.ProposalID,
		Provenance:      proposal.Provenance,
	}
	decision.DecisionID = decision.DeterministicID()
	result := SemanticResult{Proposal: &proposal, Decision: decision}
	if err := result.Validate(); err != nil {
		t.Fatalf("valid propose decision rejected: %v", err)
	}

	reject := decision
	reject.Disposition = SemanticDispositionReject
	reject.ReasonCode = SemanticReasonTemporaryOrTaskSpecific
	reject.Explanation = "The behavior is specific to the cited task."
	reject.ProposalID = ""
	reject.DecisionID = reject.DeterministicID()
	if err := (SemanticResult{Decision: reject}).Validate(); err != nil {
		t.Fatalf("valid reject decision rejected: %v", err)
	}

	deferDecision := reject
	deferDecision.Disposition = SemanticDispositionDefer
	deferDecision.ReasonCode = SemanticReasonInsufficientContext
	deferDecision.Explanation = "The cited evidence is insufficient."
	deferDecision.DecisionID = deferDecision.DeterministicID()
	if err := (SemanticResult{Decision: deferDecision}).Validate(); err != nil {
		t.Fatalf("valid defer decision rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*SemanticResult)
	}{
		{
			name: "propose without proposal",
			mutate: func(value *SemanticResult) {
				value.Proposal = nil
			},
		},
		{
			name: "reject references proposal",
			mutate: func(value *SemanticResult) {
				value.Decision = reject
				value.Decision.ProposalID = proposal.ProposalID
				value.Decision.DecisionID = value.Decision.DeterministicID()
			},
		},
		{
			name: "defer uses reject reason",
			mutate: func(value *SemanticResult) {
				value.Proposal = nil
				value.Decision = deferDecision
				value.Decision.ReasonCode = SemanticReasonNotReusable
				value.Decision.DecisionID = value.Decision.DeterministicID()
			},
		},
		{
			name: "multiline explanation",
			mutate: func(value *SemanticResult) {
				value.Decision.Explanation = "first\nsecond"
				value.Decision.DecisionID = value.Decision.DeterministicID()
			},
		},
		{
			name: "confidence out of range",
			mutate: func(value *SemanticResult) {
				value.Decision.Confidence = 1.1
				value.Decision.DecisionID = value.Decision.DeterministicID()
			},
		},
		{
			name: "missing project identity",
			mutate: func(value *SemanticResult) {
				value.Decision.ProjectIdentity = ""
				value.Decision.DecisionID = value.Decision.DeterministicID()
			},
		},
		{
			name: "proposal project mismatch",
			mutate: func(value *SemanticResult) {
				value.Decision.ProjectIdentity = "other-project"
				value.Decision.DecisionID = value.Decision.DeterministicID()
			},
		},
		{
			name: "proposal mismatch",
			mutate: func(value *SemanticResult) {
				changed := *value.Proposal
				changed.Proposal.Guidance.Instruction = "Different guidance."
				changed.ProposalID = changed.DeterministicID()
				value.Proposal = &changed
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := result
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("malformed semantic decision was accepted")
			}
		})
	}

	otherProject := reject
	otherProject.ProjectIdentity = "other-project"
	otherProject.DecisionID = otherProject.DeterministicID()
	if otherProject.DecisionID == reject.DecisionID {
		t.Fatal("semantic decision project identity did not affect deterministic identity")
	}
}

func TestSemanticDecisionRejectsNonFiniteConfidence(t *testing.T) {
	candidate := validCandidate(t)
	value := SemanticDecision{
		SchemaVersion:   SemanticDecisionSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Disposition:     SemanticDispositionReject,
		ReasonCode:      SemanticReasonNotReusable,
		Explanation:     "The cited behavior is not reusable.",
		Confidence:      0.9,
		Provenance: SemanticProposalProvenance{
			Harness:       HarnessClaude,
			Model:         "claude-test",
			PromptVersion: SemanticProposalPromptVersion,
			InputHash:     sha256Value("decision input"),
			OutputHash:    sha256Value("decision output"),
			GeneratedAt:   time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		},
	}
	value.DecisionID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("valid semantic decision rejected: %v", err)
	}

	for name, confidence := range map[string]float64{
		"nan":               math.NaN(),
		"positive infinity": math.Inf(1),
		"negative infinity": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			invalid := value
			invalid.Confidence = confidence
			if err := invalid.Validate(); err == nil ||
				!strings.Contains(err.Error(), "confidence must be between zero and one") {
				t.Fatalf("semantic decision confidence error = %v", err)
			}
		})
	}
}

func TestExperienceApprovalAuthorityRules(t *testing.T) {
	experience := validExperience(t, LifecycleActive)
	if err := experience.Validate(); err != nil {
		t.Fatalf("valid active experience rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Experience)
		want   string
	}{
		{
			name: "approved without provenance",
			mutate: func(value *Experience) {
				value.Governance.LifecycleState = LifecycleApproved
				value.Governance.Approval = nil
			},
			want: "requires approval provenance",
		},
		{
			name: "active without approved authority",
			mutate: func(value *Experience) {
				value.Governance.Authority = AuthorityNone
			},
			want: "requires user-approved authority",
		},
		{
			name: "candidate lifecycle rejected for immutable experience",
			mutate: func(value *Experience) {
				value.Governance.LifecycleState = LifecycleCandidate
				value.Governance.Authority = AuthorityUserApproved
				value.Governance.Approval = nil
			},
			want: "lifecycle state is invalid",
		},
		{
			name: "rejected lifecycle rejected for immutable experience",
			mutate: func(value *Experience) {
				value.Governance.LifecycleState = LifecycleRejected
			},
			want: "lifecycle state is invalid",
		},
		{
			name: "approval bound to different content",
			mutate: func(value *Experience) {
				value.Governance.Approval.ApprovedContentHash = sha256Value("different")
			},
			want: "approval hash does not match",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := experience
			approval := *experience.Governance.Approval
			value.Governance.Approval = &approval
			test.mutate(&value)
			if err := value.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLifecycleTransitionTable(t *testing.T) {
	tests := []struct {
		from LifecycleState
		to   LifecycleState
		want bool
	}{
		{LifecycleApproved, LifecycleActive, true},
		{LifecycleActive, LifecyclePaused, true},
		{LifecyclePaused, LifecycleActive, true},
		{LifecycleActive, LifecycleContradicted, true},
		{LifecycleContradicted, LifecycleSuperseded, true},
		{LifecycleContradicted, LifecycleRejected, false},
		{LifecycleCandidate, LifecycleApproved, false},
		{LifecycleCandidate, LifecycleRejected, false},
		{LifecycleCandidate, LifecycleActive, false},
		{LifecycleActive, LifecycleApproved, false},
		{LifecycleSuperseded, LifecycleActive, false},
		{LifecycleExpired, LifecycleActive, false},
	}

	for _, test := range tests {
		if got := ValidLifecycleTransition(test.from, test.to); got != test.want {
			t.Errorf("ValidLifecycleTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}

	transition := LifecycleTransition{
		Experience: ExperienceRef{ExperienceID: "exp_test", Version: 1},
		FromState:  LifecycleActive,
		ToState:    LifecyclePaused,
		ReasonCode: "user_paused",
		ActorKind:  ActorUser,
		ActorID:    "local_user",
		OccurredAt: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
	}
	transition.TransitionID = transition.DeterministicID()
	if err := transition.Validate(); err != nil {
		t.Fatalf("valid lifecycle transition rejected: %v", err)
	}
}

func TestExperienceRevisionChainIdentityAndOrigin(t *testing.T) {
	first := validExperience(t, LifecycleActive)
	if first.PreviousVersion != nil {
		t.Fatalf("version one previous ref = %+v, want nil", first.PreviousVersion)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("valid version one rejected: %v", err)
	}

	newerCandidateID := "exc_newer_evidence"
	second := nextExperienceVersion(first, newerCandidateID)
	if second.ExperienceID != first.ExperienceID {
		t.Fatalf("version identity changed: first=%s second=%s", first.ExperienceID, second.ExperienceID)
	}
	if second.OriginCandidateID != first.OriginCandidateID {
		t.Fatalf("origin candidate changed: first=%s second=%s", first.OriginCandidateID, second.OriginCandidateID)
	}
	if second.Provenance.SourceCandidateID != newerCandidateID {
		t.Fatalf("version source candidate = %s, want %s", second.Provenance.SourceCandidateID, newerCandidateID)
	}
	if err := second.Validate(); err != nil {
		t.Fatalf("valid second version rejected: %v", err)
	}
}

func TestExperienceRevisionChainValidation(t *testing.T) {
	first := validExperience(t, LifecycleActive)
	previous := ExperienceRef{ExperienceID: first.ExperienceID, Version: 1}

	versionOneWithPrevious := first
	versionOneWithPrevious.PreviousVersion = &previous
	if err := versionOneWithPrevious.Validate(); err == nil || !strings.Contains(err.Error(), "version one cannot") {
		t.Fatalf("version-one previous error = %v, want nil-reference rejection", err)
	}

	second := nextExperienceVersion(first, "exc_newer_evidence")
	second.PreviousVersion = nil
	if err := second.Validate(); err == nil || !strings.Contains(err.Error(), "requires a previous") {
		t.Fatalf("missing previous error = %v, want required-reference rejection", err)
	}

	second = nextExperienceVersion(first, "exc_newer_evidence")
	wrongID := *second.PreviousVersion
	wrongID.ExperienceID = "exp_other"
	second.PreviousVersion = &wrongID
	if err := second.Validate(); err == nil || !strings.Contains(err.Error(), "same experience ID") {
		t.Fatalf("wrong previous ID error = %v, want same-ID rejection", err)
	}

	second = nextExperienceVersion(first, "exc_newer_evidence")
	wrongVersion := *second.PreviousVersion
	wrongVersion.Version = 2
	second.PreviousVersion = &wrongVersion
	if err := second.Validate(); err == nil || !strings.Contains(err.Error(), "exactly version-1") {
		t.Fatalf("wrong previous version error = %v, want exact-prior rejection", err)
	}

	missingOrigin := first
	missingOrigin.OriginCandidateID = ""
	if err := missingOrigin.Validate(); err == nil || !strings.Contains(err.Error(), "origin candidate") {
		t.Fatalf("missing origin error = %v, want origin rejection", err)
	}

	wrongVersionOneSource := first
	wrongVersionOneSource.Provenance.SourceCandidateID = "exc_different"
	wrongVersionOneSource.Governance.Approval.CandidateID = "exc_different"
	if err := wrongVersionOneSource.Validate(); err == nil || !strings.Contains(err.Error(), "must match origin") {
		t.Fatalf("version-one source error = %v, want origin-match rejection", err)
	}
}

func TestCanonicalContentHashNormalizesSetOrderingAndTimezones(t *testing.T) {
	first := validExperience(t, LifecycleActive)
	expiration := time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("offset", -7*60*60))
	first.Applicability.ExpiresAt = &expiration
	first.Scope.RepositoryPaths = []string{"internal/trajectory/**", "internal/experience/**"}
	first.Scope.Harnesses = []Harness{HarnessCodex, HarnessClaude}
	first.Guidance.Exceptions = []string{"When explicitly requested.", "For generated fixtures."}
	first.Applicability.DeterministicConditions = []DeterministicCondition{
		{Kind: ConditionToolName, Values: []string{"Write", "Edit"}},
		{Kind: ConditionPathPattern, Values: []string{"internal/trajectory/**", "internal/experience/**"}},
	}
	first.Verifier.CoverageRequirements = []CoverageRequirement{CoverageWorkspaceCaptured, CoverageTranscriptComplete}

	second := first
	utcExpiration := expiration.UTC()
	second.Applicability.ExpiresAt = &utcExpiration
	second.Scope.RepositoryPaths = []string{"internal/experience/**", "internal/trajectory/**"}
	second.Scope.Harnesses = []Harness{HarnessClaude, HarnessCodex}
	second.Guidance.Exceptions = []string{"For generated fixtures.", "When explicitly requested."}
	second.Applicability.DeterministicConditions = []DeterministicCondition{
		{Kind: ConditionPathPattern, Values: []string{"internal/experience/**", "internal/trajectory/**"}},
		{Kind: ConditionToolName, Values: []string{"Edit", "Write"}},
	}
	second.Verifier.CoverageRequirements = []CoverageRequirement{CoverageTranscriptComplete, CoverageWorkspaceCaptured}

	if got, want := first.CanonicalContentHash(), second.CanonicalContentHash(); got != want {
		t.Fatalf("canonical hashes differ:\nfirst  %s\nsecond %s", got, want)
	}
}

func TestVerifierCatalogAndCoverageRules(t *testing.T) {
	valid := []Verifier{
		{
			Kind:    VerifierCommandObserved,
			Command: &CommandVerifierSpec{Command: "go test ./...", ScrubbingVersion: "belay.redaction.v1"},
		},
		{
			Kind:    VerifierCommandSucceeded,
			Command: &CommandVerifierSpec{Command: "make verify", CommandClass: "build", ScrubbingVersion: "belay.redaction.v1"},
		},
		{
			Kind:                 VerifierFileNotModified,
			CoverageRequirements: []CoverageRequirement{CoverageWorkspaceCaptured},
			File:                 &FileVerifierSpec{Path: "generated/client.go"},
		},
		{
			Kind: VerifierFileModified,
			File: &FileVerifierSpec{Path: "internal/experience/model.go"},
		},
		{
			Kind:                 VerifierPathPatternNotModified,
			CoverageRequirements: []CoverageRequirement{CoverageWorkspaceCaptured},
			PathPattern:          &PathPatternVerifierSpec{Patterns: []string{"generated/**"}},
		},
		{
			Kind:                      VerifierVerificationAfterLastEdit,
			VerificationAfterLastEdit: &VerificationAfterLastEditSpec{CommandClasses: []string{"test"}, RequireSuccess: true},
		},
		{
			Kind:                 VerifierNoRepeatFailure,
			CoverageRequirements: []CoverageRequirement{CoverageTranscriptComplete},
			NoRepeatFailure:      &NoRepeatFailureSpec{CommandClass: "test", NormalizedPattern: "exit <n>", WindowTurns: 30},
		},
		{
			Kind:                 VerifierUserCorrectionAbsent,
			CoverageRequirements: []CoverageRequirement{CoverageTranscriptComplete},
			UserCorrectionAbsent: &UserCorrectionAbsentSpec{MarkerFamilies: []string{"explicit_correction"}},
		},
		{
			Kind:            VerifierObservationOnly,
			ObservationOnly: &ObservationOnlySpec{Explanation: "No deterministic verifier is available."},
		},
	}
	for _, verifier := range valid {
		if err := verifier.Validate(); err != nil {
			t.Errorf("%s verifier rejected: %v", verifier.Kind, err)
		}
	}

	missingCoverage := valid[2]
	missingCoverage.CoverageRequirements = nil
	if err := missingCoverage.Validate(); err == nil || !strings.Contains(err.Error(), "requires explicit coverage") {
		t.Fatalf("absence coverage error = %v, want explicit coverage rejection", err)
	}

	wrongSpec := Verifier{
		Kind: VerifierCommandObserved,
		File: &FileVerifierSpec{Path: "go.mod"},
	}
	if err := wrongSpec.Validate(); err == nil || !strings.Contains(err.Error(), "requires command") {
		t.Fatalf("wrong typed spec error = %v, want command-spec rejection", err)
	}

	unscrubbed := Verifier{
		Kind:    VerifierCommandObserved,
		Command: &CommandVerifierSpec{Command: "go test ./..."},
	}
	if err := unscrubbed.Validate(); err == nil || !strings.Contains(err.Error(), "scrubbing version") {
		t.Fatalf("unscrubbed command error = %v, want scrub provenance rejection", err)
	}
}

func TestEvaluationAllowsEmptyCoverageOnlyForUnknownVerifier(t *testing.T) {
	turnIndex := int64(4)
	occurredAt := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	value := Evaluation{
		SchemaVersion:      EvaluationSchemaVersion,
		ApplicationID:      "exa_test",
		Experience:         ExperienceRef{ExperienceID: "exp_test", Version: 1},
		EvaluatedAt:        occurredAt,
		OpportunityState:   OpportunityUnknown,
		ApplicabilityState: ApplicabilityUnknown,
		VerifierState:      VerifierUnknown,
		TaskOutcomeState:   TaskOutcomeUnknown,
		SourceEvidence: []EvidenceRef{{
			Kind:       EvidenceTranscriptTurn,
			SessionKey: "ses_test",
			TurnIndex:  &turnIndex,
			OccurredAt: &occurredAt,
		}},
		DerivationVersion: "belay.experience-evaluate.v1",
	}
	value.EvaluationID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("unknown evaluation with truthful empty coverage rejected: %v", err)
	}

	value.VerifierState = VerifierSatisfied
	value.EvaluationID = value.DeterministicID()
	if err := value.Validate(); err == nil ||
		!strings.Contains(err.Error(), "coverage is required") {
		t.Fatalf("satisfied evaluation empty coverage error = %v", err)
	}
}

func TestVerifierJSONRejectsUnknownFields(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"observation_only","observation_only":{"explanation":"inspect only"},"unknown":true}`,
		`{"kind":"observation_only","observation_only":{"explanation":"inspect only","unknown":true}}`,
		`{"kind":"observation_only","observation_only":{"explanation":"inspect only"}} {}`,
	} {
		var verifier Verifier
		if err := json.Unmarshal([]byte(raw), &verifier); err == nil {
			t.Fatalf("json.Unmarshal(%s) unexpectedly accepted unknown or trailing fields", raw)
		}
	}
}

func TestPathValidationRejectsUnsafeOrUnfrozenSyntax(t *testing.T) {
	for _, pattern := range []string{
		"/absolute/**",
		"../parent/**",
		"safe/../escape",
		`windows\path\**`,
		"generated/[a-z]/**",
		"generated//**",
	} {
		verifier := Verifier{
			Kind:                 VerifierPathPatternNotModified,
			CoverageRequirements: []CoverageRequirement{CoverageWorkspaceCaptured},
			PathPattern:          &PathPatternVerifierSpec{Patterns: []string{pattern}},
		}
		if err := verifier.Validate(); err == nil {
			t.Errorf("unsafe path pattern %q was accepted", pattern)
		}
	}
}

func validCandidate(t *testing.T) Candidate {
	t.Helper()
	evidence := validEvidenceSet()
	confidence := 0.84
	value := Candidate{
		SchemaVersion:    CandidateSchemaVersion,
		Family:           CandidateCorrection,
		ProjectIdentity:  "git@example.test:doplexlabs/belay-engine.git",
		ObservedBehavior: "The assistant edited a generated file directly.",
		UserFeedback:     "Do not edit generated files directly.",
		Evidence:         evidence,
		Proposal: ExperienceProposal{
			Type: ExperienceConstraint,
			Scope: Scope{
				Kind:            ScopeProject,
				ProjectIdentity: "git@example.test:doplexlabs/belay-engine.git",
				RepositoryPaths: []string{"generated/**"},
				Harnesses:       []Harness{HarnessClaude, HarnessCodex},
			},
			Applicability: Applicability{
				SemanticDescription: "Tasks that would modify generated files.",
				DeterministicConditions: []DeterministicCondition{
					{Kind: ConditionPathPattern, Values: []string{"generated/**"}},
				},
			},
			Guidance: Guidance{
				Instruction:          "Edit the schema source instead of generated files.",
				Rationale:            "The cited correction redirected the change to its source.",
				InterventionStrength: InterventionAdvise,
			},
			Verifier: Verifier{
				Kind:                 VerifierPathPatternNotModified,
				CoverageRequirements: []CoverageRequirement{CoverageWorkspaceCaptured},
				PathPattern:          &PathPatternVerifierSpec{Patterns: []string{"generated/**"}},
			},
			Confidence: &confidence,
		},
		Provenance: Provenance{
			ExtractorVersion: "experience-candidate.det.v1",
			Harness:          HarnessClaude,
			Model:            "claude-test",
			PromptVersion:    "belay.experience-prompt.v1",
			InputHash:        sha256Value("candidate-input"),
			GeneratedAt:      time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
		},
		Authority:      AuthorityNone,
		LifecycleState: LifecycleCandidate,
		CreatedAt:      time.Date(2026, 9, 10, 10, 0, 1, 0, time.UTC),
	}
	value.CandidateID = value.DeterministicID()
	return value
}

func validExperience(t *testing.T, state LifecycleState) Experience {
	t.Helper()
	candidate := validCandidate(t)
	value := Experience{
		SchemaVersion:     ExperienceSchemaVersion,
		OriginCandidateID: candidate.CandidateID,
		Version:           1,
		Type:              candidate.Proposal.Type,
		Scope:             candidate.Proposal.Scope,
		Applicability:     candidate.Proposal.Applicability,
		Guidance:          candidate.Proposal.Guidance,
		Verifier:          candidate.Proposal.Verifier,
		Evidence:          candidate.Evidence,
		Provenance: Provenance{
			ExtractorVersion:  "experience-approval.v1",
			InputHash:         sha256Value("approved-input"),
			SourceCandidateID: candidate.CandidateID,
			GeneratedAt:       time.Date(2026, 9, 10, 10, 5, 0, 0, time.UTC),
		},
		Governance: Governance{
			LifecycleState: state,
			Authority:      AuthorityUserApproved,
		},
		CreatedAt: time.Date(2026, 9, 10, 10, 5, 0, 0, time.UTC),
	}
	value.ExperienceID = DeriveExperienceID(value.Scope.ProjectIdentity, value.OriginCandidateID)
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval = &ApprovalProvenance{
		ApprovedBy:          "local_user",
		ApprovedAt:          time.Date(2026, 9, 10, 10, 5, 0, 0, time.UTC),
		Mode:                ApprovalAsProposed,
		CandidateID:         candidate.CandidateID,
		ProposedContentHash: value.ContentHash,
		ApprovedContentHash: value.ContentHash,
	}
	return value
}

func nextExperienceVersion(previous Experience, sourceCandidateID string) Experience {
	value := previous
	value.Version = previous.Version + 1
	value.PreviousVersion = &ExperienceRef{
		ExperienceID: previous.ExperienceID,
		Version:      previous.Version,
	}
	value.Guidance.Instruction = "Use the schema source and regenerate affected files."
	value.Provenance.SourceCandidateID = sourceCandidateID
	value.Provenance.InputHash = sha256Value("revision-" + sourceCandidateID)
	value.Provenance.GeneratedAt = previous.CreatedAt.Add(time.Minute)
	value.CreatedAt = previous.CreatedAt.Add(time.Minute)
	approval := *previous.Governance.Approval
	value.Governance.Approval = &approval
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval.CandidateID = sourceCandidateID
	value.Governance.Approval.ProposedContentHash = value.ContentHash
	value.Governance.Approval.ApprovedContentHash = value.ContentHash
	value.Governance.Approval.ApprovedAt = value.CreatedAt
	return value
}

func validEvidenceSet() EvidenceSet {
	turn := int64(12)
	value := EvidenceSet{
		Availability: EvidenceAvailable,
		Refs: []EvidenceRef{
			{
				Kind:       EvidenceTranscriptTurn,
				SessionKey: "ses_test",
				TurnIndex:  &turn,
				Excerpt:    "Do not edit generated files directly.",
			},
		},
	}
	value.EvidenceSetID = value.DeterministicID()
	return value
}

func sha256Value(value string) string {
	return contentHash(value)
}
