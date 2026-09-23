package localapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	ExperiencePromptVersion               = experience.SemanticProposalPromptVersion
	maxExperienceSemanticCandidates       = 16
	maxExperienceSemanticCandidateQuery   = 500
	maxExperienceSemanticExcerpts         = 8
	maxExperienceSemanticOutcomeIDs       = 16
	maxExperienceSemanticObservationBytes = 1024
	maxExperienceSemanticFeedbackBytes    = 1536
	maxExperienceSemanticExcerptBytes     = 1536
	maxExperienceSemanticDecisionBytes    = 1024
	maxExperienceSemanticPromptAndSchema  = 512 << 10
	maxExperienceSemanticOutputBytes      = 256 << 10
	maxExperienceSemanticListItems        = 32
)

var experienceSemanticLocalPathPattern = regexp.MustCompile(
	`/(?:Users|home|private|tmp|var/folders)/[^\s"'<>\\]+`,
)

var experienceSemanticMarkupPattern = regexp.MustCompile(
	`(?i)</?[a-z][^>]*>|\[[^\]]+\]\([^)]+\)`,
)

var errExperienceSemanticEvidenceSupport = errors.New(
	"experience semantic proposal evidence support is invalid",
)

type ExperienceSemanticProposalStore interface {
	QueryExperienceCandidates(
		context.Context,
		string,
		int,
	) ([]experience.Candidate, error)
	HasExperienceSemanticResult(
		context.Context,
		string,
		experience.Harness,
		string,
	) (bool, error)
	StoreExperienceSemanticResults(
		context.Context,
		[]experience.SemanticResult,
	) (
		experience.SemanticDispositionCounts,
		experience.SemanticDispositionCounts,
		error,
	)
}

type ExperienceSemanticAnalysisReport struct {
	CandidatesConsidered          int
	CandidatesSkippedExisting     int
	CandidatesSkippedInsufficient int
	CandidateQueryCapReached      bool
	PendingCandidatesDeferred     int
	Proposed                      int
	Rejected                      int
	Deferred                      int
	ResultsInserted               int
	ResultsReplayed               int
	ProposedReplayed              int
	RejectedReplayed              int
	DeferredReplayed              int
	ProposalsInserted             int
	ProposalsReplayed             int
}

type experienceSemanticPromptPayload struct {
	Candidates []experienceSemanticPromptCandidate `json:"candidates"`
}

type experienceSemanticPromptCandidate struct {
	CandidateID          string                            `json:"candidate_id"`
	ProjectIdentity      string                            `json:"project_identity"`
	Family               experience.CandidateFamily        `json:"family"`
	EvidenceSessionCount int                               `json:"evidence_session_count"`
	ObservedBehavior     string                            `json:"observed_behavior"`
	UserFeedback         string                            `json:"user_feedback,omitempty"`
	OutcomeIDs           []string                          `json:"outcome_ids"`
	Excerpts             []experienceSemanticPromptExcerpt `json:"excerpts"`
}

type experienceSemanticPromptExcerpt struct {
	EvidenceRefID string                        `json:"evidence_ref_id"`
	Kind          experience.EvidenceSourceKind `json:"kind"`
	SessionKey    string                        `json:"session_key,omitempty"`
	TurnIndex     *int64                        `json:"turn_index,omitempty"`
	TurnRole      experience.EvidenceTurnRole   `json:"turn_role,omitempty"`
	ToolName      string                        `json:"tool_name,omitempty"`
	EventID       string                        `json:"event_id,omitempty"`
	OutcomeID     string                        `json:"outcome_id,omitempty"`
	OccurredAt    *time.Time                    `json:"occurred_at,omitempty"`
	Text          string                        `json:"text"`
}

type experienceSemanticOutput struct {
	Candidates []json.RawMessage `json:"candidates"`
}

type experienceSemanticOutputCandidate struct {
	CandidateID string
	Disposition experience.SemanticDisposition
	ReasonCode  experience.SemanticDecisionReasonCode
	Explanation string
	Confidence  float64
	Proposal    *experienceSemanticProposeOutputCandidate
}

type experienceSemanticProposeOutputCandidate struct {
	CandidateID          string                                `json:"candidate_id"`
	Disposition          experience.SemanticDisposition        `json:"disposition"`
	ExperienceType       experience.ExperienceType             `json:"experience_type"`
	Scope                experienceSemanticOutputScope         `json:"scope"`
	Guidance             string                                `json:"guidance"`
	Rationale            string                                `json:"rationale"`
	Applicability        experienceSemanticOutputApplicability `json:"applicability"`
	Exceptions           []string                              `json:"exceptions"`
	GuidanceSupportRefs  []string                              `json:"guidance_support_refs"`
	ExceptionSupportRefs [][]string                            `json:"exception_support_refs"`
	VerifierSupportRefs  []string                              `json:"verifier_support_refs"`
	InterventionStrength experience.InterventionStrength       `json:"intervention_strength"`
	Verifier             experienceSemanticOutputVerifier      `json:"verifier"`
	Confidence           *float64                              `json:"confidence"`
}

type experienceSemanticNoProposalOutputCandidate struct {
	CandidateID string                                `json:"candidate_id"`
	Disposition experience.SemanticDisposition        `json:"disposition"`
	ReasonCode  experience.SemanticDecisionReasonCode `json:"reason_code"`
	Explanation string                                `json:"explanation"`
	Confidence  *float64                              `json:"confidence"`
}

type experienceSemanticOutputScope struct {
	Kind            experience.ScopeKind `json:"kind"`
	ProjectIdentity string               `json:"project_identity"`
	SessionKey      string               `json:"session_key,omitempty"`
}

type experienceSemanticOutputApplicability struct {
	TaskFamilies        []string             `json:"task_families"`
	PathHints           []string             `json:"path_hints"`
	Harnesses           []experience.Harness `json:"harnesses"`
	Models              []string             `json:"models"`
	SemanticDescription string               `json:"semantic_description"`
}

type experienceSemanticOutputVerifier struct {
	Kind                 experience.VerifierKind          `json:"kind"`
	CoverageRequirements []experience.CoverageRequirement `json:"coverage_requirements"`
	Parameters           json.RawMessage                  `json:"parameters"`
}

func AnalyzeExperienceCandidates(
	ctx context.Context,
	store ExperienceSemanticProposalStore,
	projectIdentity string,
	harness SemanticHarness,
	runner ExperienceSemanticHarnessRunner,
) (ExperienceSemanticAnalysisReport, error) {
	return analyzeExperienceCandidates(
		ctx,
		store,
		projectIdentity,
		harness,
		runner,
		time.Now,
	)
}

func analyzeExperienceCandidates(
	ctx context.Context,
	store ExperienceSemanticProposalStore,
	projectIdentity string,
	harness SemanticHarness,
	runner ExperienceSemanticHarnessRunner,
	now func() time.Time,
) (ExperienceSemanticAnalysisReport, error) {
	if store == nil || runner == nil || now == nil {
		return ExperienceSemanticAnalysisReport{}, errors.New(
			"experience semantic analysis requires a store, runner, and clock",
		)
	}
	pending, report, err := queryPendingExperienceCandidates(
		ctx,
		store,
		projectIdentity,
		harness,
	)
	if err != nil {
		return report, err
	}
	bounded, prompt, schema, inputHash, skipped, err :=
		prepareExperienceSemanticPrompt(harness, pending)
	report.CandidatesSkippedInsufficient += skipped
	if err != nil {
		return report, err
	}
	if len(bounded) == 0 {
		return report, nil
	}
	result, err := runner(ctx, harness, prompt, schema)
	if err != nil {
		return report, err
	}
	if len(result.Output) == 0 ||
		len(result.Output) > maxExperienceSemanticOutputBytes {
		return report, errors.New(
			"experience semantic output is empty or exceeds safety limit",
		)
	}
	output, err := decodeExperienceSemanticOutput(result.Output)
	if err != nil {
		return report, err
	}
	generatedAt := now().UTC().Round(0)
	if generatedAt.IsZero() {
		return report, errors.New(
			"experience semantic analysis generated_at is required",
		)
	}
	results, counts, err := compileExperienceSemanticResults(
		bounded,
		output,
		harness,
		semanticModelName(result.Model),
		inputHash,
		sha256Prefixed(result.Output),
		generatedAt,
	)
	if err != nil {
		return report, err
	}
	inserted, replayed, err := store.StoreExperienceSemanticResults(
		ctx,
		results,
	)
	if err != nil {
		return report, err
	}
	report.Proposed = counts.Proposed
	report.Rejected = counts.Rejected
	report.Deferred = counts.Deferred
	report.ResultsInserted =
		inserted.Proposed + inserted.Rejected + inserted.Deferred
	report.ResultsReplayed =
		replayed.Proposed + replayed.Rejected + replayed.Deferred
	report.ProposedReplayed = replayed.Proposed
	report.RejectedReplayed = replayed.Rejected
	report.DeferredReplayed = replayed.Deferred
	report.ProposalsInserted = inserted.Proposed
	report.ProposalsReplayed = replayed.Proposed
	return report, nil
}

func queryPendingExperienceCandidates(
	ctx context.Context,
	store ExperienceSemanticProposalStore,
	projectIdentity string,
	harness SemanticHarness,
) (
	[]experience.Candidate,
	ExperienceSemanticAnalysisReport,
	error,
) {
	if store == nil {
		return nil, ExperienceSemanticAnalysisReport{}, errors.New(
			"experience semantic analysis requires a store",
		)
	}
	projectIdentity = strings.TrimSpace(projectIdentity)
	if projectIdentity == "" || !harness.Valid() {
		return nil, ExperienceSemanticAnalysisReport{}, errors.New(
			"experience semantic analysis requires a project and supported harness",
		)
	}
	candidates, err := store.QueryExperienceCandidates(
		ctx,
		projectIdentity,
		maxExperienceSemanticCandidateQuery,
	)
	if err != nil {
		return nil, ExperienceSemanticAnalysisReport{}, err
	}
	report := ExperienceSemanticAnalysisReport{
		CandidatesConsidered:     len(candidates),
		CandidateQueryCapReached: len(candidates) == maxExperienceSemanticCandidateQuery,
	}
	pending := make([]experience.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ProjectIdentity != projectIdentity ||
			candidate.Authority != experience.AuthorityNone ||
			candidate.LifecycleState != experience.LifecycleCandidate {
			return nil, report, errors.New(
				"experience semantic input contains a non-inactive candidate",
			)
		}
		exists, err := store.HasExperienceSemanticResult(
			ctx,
			candidate.CandidateID,
			experience.Harness(harness),
			ExperiencePromptVersion,
		)
		if err != nil {
			return nil, report, err
		}
		if exists {
			report.CandidatesSkippedExisting++
			continue
		}
		pending = append(pending, candidate)
	}
	if len(pending) > maxExperienceSemanticCandidates {
		report.PendingCandidatesDeferred =
			len(pending) - maxExperienceSemanticCandidates
		pending = pending[:maxExperienceSemanticCandidates]
	}
	return pending, report, nil
}

func prepareExperienceSemanticPrompt(
	harness SemanticHarness,
	candidates []experience.Candidate,
) (
	[]experience.Candidate,
	[]byte,
	[]byte,
	string,
	int,
	error,
) {
	if !harness.Valid() {
		return nil, nil, nil, "", 0, errors.New(
			"invalid experience semantic harness",
		)
	}
	values := append([]experience.Candidate(nil), candidates...)
	sort.Slice(values, func(i, j int) bool {
		return values[i].CandidateID < values[j].CandidateID
	})
	if len(values) > maxExperienceSemanticCandidates {
		values = values[:maxExperienceSemanticCandidates]
	}
	if len(values) == 0 {
		return nil, nil, nil, "", 0, nil
	}
	payload := experienceSemanticPromptPayload{
		Candidates: make(
			[]experienceSemanticPromptCandidate,
			0,
			len(values),
		),
	}
	bounded := make([]experience.Candidate, 0, len(values))
	skipped := 0
	projectIdentity := ""
	for _, candidate := range values {
		if err := candidate.Validate(); err != nil {
			return nil, nil, nil, "", skipped, err
		}
		switch {
		case projectIdentity == "":
			projectIdentity = candidate.ProjectIdentity
		case candidate.ProjectIdentity != projectIdentity:
			return nil, nil, nil, "", skipped, errors.New(
				"experience semantic candidates must share one project",
			)
		}
		excerpts := boundedExperienceSemanticExcerpts(candidate)
		payload.Candidates = append(
			payload.Candidates,
			experienceSemanticPromptCandidate{
				CandidateID:     candidate.CandidateID,
				ProjectIdentity: candidate.ProjectIdentity,
				Family:          candidate.Family,
				EvidenceSessionCount: evidenceSessionCount(
					candidate.Evidence.Refs,
				),
				ObservedBehavior: clipSemanticText(
					sanitizeExperienceSemanticPromptText(
						candidate.ObservedBehavior,
						candidate.Proposal.Scope.RepositoryPaths,
					),
					maxExperienceSemanticObservationBytes,
				),
				UserFeedback: clipSemanticText(
					sanitizeExperienceSemanticPromptText(
						candidate.UserFeedback,
						candidate.Proposal.Scope.RepositoryPaths,
					),
					maxExperienceSemanticFeedbackBytes,
				),
				OutcomeIDs: boundedSortedStrings(
					candidate.OutcomeRefs,
					maxExperienceSemanticOutcomeIDs,
				),
				Excerpts: sanitizeExperienceSemanticPromptExcerpts(
					excerpts,
					candidate.Proposal.Scope.RepositoryPaths,
				),
			},
		)
		bounded = append(bounded, candidate)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, nil, "", skipped, errors.New(
			"encode experience semantic candidates",
		)
	}
	schema, err := experienceSemanticOutputSchema(payload)
	if err != nil {
		return nil, nil, nil, "", skipped, err
	}
	instructions := []byte(
		"You are making one bounded semantic decision for each supplied " +
			"deterministic candidate. Do not use tools, inspect files, browse, or add " +
			"facts. Evaluate each candidate independently using only that candidate's " +
			"fields and cited excerpts; never use another candidate as corroboration. " +
			"Return exactly one decision for every supplied candidate and no others. " +
			"Propose only guidance that is materially useful in a future task, narrowly " +
			"scoped, and directly supported as a durable preference, procedure, warning, " +
			"constraint, or fact. Reject immediate task controls, one-off implementation " +
			"choices, obvious baseline behavior, and project-wide rules inferred from " +
			"session-local instructions. Examples such as stop, revert, do not run or " +
			"delegate, and answer now are temporary unless that candidate's own evidence " +
			"explicitly establishes a durable future preference. Reject unsafe, " +
			"overbroad, redundant, or otherwise non-reusable guidance. Defer when the " +
			"candidate's cited evidence is insufficient to tell. Evidence from one " +
			"session can support an explicit stable communication preference or a " +
			"directly verified reusable repair, but not an invented project-wide policy. " +
			"Do not reject a domain invariant as obvious baseline behavior when the " +
			"candidate directly implements and verifies that invariant. " +
			"For a successful_procedure candidate, proposed guidance must state the " +
			"reusable behavior established by the cited evidence; do not propose guidance " +
			"that merely repeats the verifier command. If the evidence supports only a " +
			"verification command and no reusable behavior, reject it as redundant. " +
			"When cited evidence directly supports an exact command, relative path, " +
			"filename, or explicit exception that is necessary to apply or verify the " +
			"reusable behavior, preserve that exact detail in guidance, verifier " +
			"parameters, path_hints, or exceptions as appropriate. Never replace a " +
			"necessary exact command or path with a generic phrase such as run the " +
			"repository checks. " +
			"Preserve every explicit qualification or exception that materially limits " +
			"the reusable behavior; never broaden a rule by dropping its exception. " +
			"For successful_procedure candidates, copy the reusable rule into guidance " +
			"as an exact contiguous quote from its cited evidence. Put limiting " +
			"exceptions only in exceptions, each as an exact contiguous quote from its " +
			"cited evidence. When a user excerpt explicitly labels a Rule or Exception, " +
			"that user wording is authoritative: do not merge it with an assistant " +
			"restatement, add consequences, or strengthen the exception. " +
			"Every proposed guidance clause, every exception, and the verifier must cite " +
			"evidence_ref_id values from that same candidate. Never cite another " +
			"candidate, invent an ID, or emit an unsupported clause. If the candidate " +
			"does not contain enough cited evidence, defer it. " +
			"Proposed guidance must be concise, single-line, inactive, and authority-free. " +
			"Never grant authority, activate guidance, or use deny intervention. All " +
			"guidance, rationale, semantic_description, and exception strings must be " +
			"plain text without URLs, HTML or Markdown links, backticks, or control " +
			"characters. Ordinary comparison operators are allowed. Do not copy " +
			"project_identity into those text fields. The harnesses array describes " +
			"where the learned behavior applies, not which harness produced the evidence. " +
			"For harness-neutral repository or code behavior, include claude, codex, " +
			"cursor, and antigravity; restrict harnesses only when the evidence itself " +
			"is harness-specific. " +
			"The supported harnesses are claude for Claude Code, codex for Codex, " +
			"cursor for Cursor Agent, and antigravity for Antigravity. All " +
			"path_hints and verifier " +
			"paths must be project-relative slash-separated paths or glob patterns; " +
			"never emit absolute paths or parent traversal, and use an empty path_hints " +
			"array when no relative path is supported by the evidence. Return only " +
			"schema-valid JSON.\n\n",
	)
	prompt := append(instructions, body...)
	if len(prompt)+len(schema) > maxExperienceSemanticPromptAndSchema ||
		len(schema) > maxClaudeInlineSchema {
		return nil, nil, nil, "", skipped, errors.New(
			"experience semantic prompt exceeds safety limit",
		)
	}
	return bounded, prompt, schema, experienceSemanticInputHash(
		prompt,
		schema,
		harness,
		ExperiencePromptVersion,
	), skipped, nil
}

func experienceSemanticInputHash(
	prompt []byte,
	schema []byte,
	harness SemanticHarness,
	promptVersion string,
) string {
	hasher := sha256.New()
	for _, value := range [][]byte{
		[]byte("belay.experience-model-input.v1"),
		[]byte(promptVersion),
		[]byte(harness),
		prompt,
		schema,
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hasher.Write(size[:])
		_, _ = hasher.Write(value)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func boundedExperienceSemanticExcerpts(
	candidate experience.Candidate,
) []experienceSemanticPromptExcerpt {
	refs := candidate.Evidence.Refs
	roleAware := false
	for _, ref := range refs {
		if ref.Kind == experience.EvidenceTranscriptTurn &&
			ref.TurnRole.Valid() {
			roleAware = true
			break
		}
	}
	if !roleAware {
		return lexicallyBoundedExperienceSemanticExcerpts(
			candidate.CandidateID,
			refs,
		)
	}

	values := excerptEvidenceRefsChronological(refs)
	selected := make([]experience.EvidenceRef, 0, maxExperienceSemanticExcerpts)
	seen := make(map[string]bool)
	appendRef := func(ref experience.EvidenceRef) {
		if len(selected) >= maxExperienceSemanticExcerpts ||
			strings.TrimSpace(ref.Excerpt) == "" {
			return
		}
		key := experienceSemanticEvidenceRefKey(ref)
		if !seen[key] {
			seen[key] = true
			selected = append(selected, ref)
		}
	}

	userRefs := evidenceRefsWithRole(values, experience.EvidenceTurnUser)
	if len(userRefs) > 0 {
		appendRef(userRefs[0])
	}

	mutations := make([]experience.EvidenceRef, 0, 2)
	for _, ref := range values {
		if ref.TurnRole == experience.EvidenceTurnToolCall &&
			isMutationEvidenceRef(ref) {
			mutations = append(mutations, ref)
		}
	}
	if len(mutations) > 0 {
		appendRef(mutations[0])
	}
	if len(mutations) > 1 {
		appendRef(mutations[len(mutations)-1])
	}

	if len(userRefs) > 1 {
		start := len(userRefs) - 2
		for _, ref := range userRefs[start:] {
			appendRef(ref)
		}
	}

	anchorCall, anchorFound := anchorVerifierEvidenceRef(candidate, values)
	if anchorFound {
		appendRef(anchorCall)
		if result, ok := verifierResultEvidenceRef(anchorCall, values); ok {
			appendRef(result)
		}
	}

	assistantRefs := evidenceRefsWithRole(
		values,
		experience.EvidenceTurnAssistant,
	)
	if len(assistantRefs) > 0 {
		appendRef(assistantRefs[len(assistantRefs)-1])
	}
	if explanation, ok := assistantResponseToLatestUserEvidenceRef(
		values,
	); ok {
		appendRef(explanation)
	}

	for _, ref := range values {
		appendRef(ref)
	}
	return experienceSemanticPromptExcerpts(candidate.CandidateID, selected)
}

func lexicallyBoundedExperienceSemanticExcerpts(
	candidateID string,
	refs []experience.EvidenceRef,
) []experienceSemanticPromptExcerpt {
	values := append([]experience.EvidenceRef(nil), refs...)
	sort.Slice(values, func(i, j int) bool {
		left, _ := json.Marshal(values[i])
		right, _ := json.Marshal(values[j])
		return string(left) < string(right)
	})
	selected := make([]experience.EvidenceRef, 0, maxExperienceSemanticExcerpts)
	for _, ref := range values {
		if len(selected) == maxExperienceSemanticExcerpts {
			break
		}
		if strings.TrimSpace(ref.Excerpt) != "" {
			selected = append(selected, ref)
		}
	}
	return experienceSemanticPromptExcerpts(candidateID, selected)
}

func experienceSemanticPromptExcerpts(
	candidateID string,
	refs []experience.EvidenceRef,
) []experienceSemanticPromptExcerpt {
	result := make([]experienceSemanticPromptExcerpt, 0, len(refs))
	for _, ref := range refs {
		result = append(result, experienceSemanticPromptExcerpt{
			EvidenceRefID: experienceSemanticEvidenceReferenceID(
				candidateID,
				ref,
			),
			Kind:       ref.Kind,
			SessionKey: ref.SessionKey,
			TurnIndex:  cloneInt64Pointer(ref.TurnIndex),
			TurnRole:   ref.TurnRole,
			ToolName:   ref.ToolName,
			EventID:    ref.EventID,
			OutcomeID:  ref.OutcomeID,
			OccurredAt: cloneTimePointer(ref.OccurredAt),
			Text: clipSemanticText(
				strings.TrimSpace(ref.Excerpt),
				maxExperienceSemanticExcerptBytes,
			),
		})
	}
	return result
}

func experienceSemanticEvidenceReferenceID(
	candidateID string,
	ref experience.EvidenceRef,
) string {
	encoded, _ := json.Marshal(struct {
		CandidateID string
		Ref         experience.EvidenceRef
	}{
		CandidateID: strings.TrimSpace(candidateID),
		Ref:         ref,
	})
	sum := sha256.Sum256(encoded)
	return "evr_" + hex.EncodeToString(sum[:16])
}

func excerptEvidenceRefsChronological(
	refs []experience.EvidenceRef,
) []experience.EvidenceRef {
	result := make([]experience.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Excerpt) != "" {
			result = append(result, ref)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SessionKey != result[j].SessionKey {
			return result[i].SessionKey < result[j].SessionKey
		}
		switch {
		case result[i].TurnIndex != nil && result[j].TurnIndex != nil &&
			*result[i].TurnIndex != *result[j].TurnIndex:
			return *result[i].TurnIndex < *result[j].TurnIndex
		case result[i].TurnIndex != nil && result[j].TurnIndex == nil:
			return true
		case result[i].TurnIndex == nil && result[j].TurnIndex != nil:
			return false
		}
		return experienceSemanticEvidenceRefKey(result[i]) <
			experienceSemanticEvidenceRefKey(result[j])
	})
	return result
}

func evidenceRefsWithRole(
	refs []experience.EvidenceRef,
	role experience.EvidenceTurnRole,
) []experience.EvidenceRef {
	result := make([]experience.EvidenceRef, 0)
	for _, ref := range refs {
		if ref.TurnRole == role {
			result = append(result, ref)
		}
	}
	return result
}

func isMutationEvidenceRef(ref experience.EvidenceRef) bool {
	toolName := strings.ToLower(strings.TrimSpace(ref.ToolName))
	return strings.Contains(toolName, "edit") ||
		strings.Contains(toolName, "write") ||
		strings.Contains(toolName, "patch")
}

func anchorVerifierEvidenceRef(
	candidate experience.Candidate,
	refs []experience.EvidenceRef,
) (experience.EvidenceRef, bool) {
	command := candidate.Proposal.Verifier.Command
	if command == nil {
		return experience.EvidenceRef{}, false
	}
	want := strings.TrimSpace(command.Command)
	for index := len(refs) - 1; index >= 0; index-- {
		ref := refs[index]
		if ref.TurnRole == experience.EvidenceTurnToolCall &&
			strings.TrimSpace(ref.Excerpt) == want {
			return ref, true
		}
	}
	return experience.EvidenceRef{}, false
}

func verifierResultEvidenceRef(
	call experience.EvidenceRef,
	refs []experience.EvidenceRef,
) (experience.EvidenceRef, bool) {
	if call.TurnIndex == nil {
		return experience.EvidenceRef{}, false
	}
	for _, ref := range refs {
		if ref.TurnRole == experience.EvidenceTurnToolResult &&
			ref.SessionKey == call.SessionKey &&
			ref.TurnIndex != nil &&
			*ref.TurnIndex > *call.TurnIndex {
			return ref, true
		}
	}
	return experience.EvidenceRef{}, false
}

func assistantResponseToLatestUserEvidenceRef(
	refs []experience.EvidenceRef,
) (experience.EvidenceRef, bool) {
	sessionKey := ""
	var latestUserTurn int64
	userFound := false
	for _, ref := range refs {
		if ref.TurnRole == experience.EvidenceTurnUser &&
			ref.TurnIndex != nil &&
			(!userFound || *ref.TurnIndex > latestUserTurn) {
			sessionKey = ref.SessionKey
			latestUserTurn = *ref.TurnIndex
			userFound = true
		}
	}
	if !userFound {
		return experience.EvidenceRef{}, false
	}
	for _, ref := range refs {
		if ref.SessionKey == sessionKey &&
			ref.TurnRole == experience.EvidenceTurnAssistant &&
			ref.TurnIndex != nil &&
			*ref.TurnIndex > latestUserTurn {
			return ref, true
		}
	}
	return experience.EvidenceRef{}, false
}

func experienceSemanticEvidenceRefKey(ref experience.EvidenceRef) string {
	encoded, _ := json.Marshal(ref)
	return string(encoded)
}

func sanitizeExperienceSemanticPromptExcerpts(
	values []experienceSemanticPromptExcerpt,
	repositoryPaths []string,
) []experienceSemanticPromptExcerpt {
	result := append([]experienceSemanticPromptExcerpt(nil), values...)
	for index := range result {
		result[index].Text = sanitizeExperienceSemanticPromptText(
			result[index].Text,
			repositoryPaths,
		)
	}
	return result
}

func sanitizeExperienceSemanticPromptText(
	value string,
	repositoryPaths []string,
) string {
	value = scrubTranscriptMetadata(value)
	return experienceSemanticLocalPathPattern.ReplaceAllStringFunc(
		value,
		func(raw string) string {
			trimmed := strings.TrimRight(raw, ".,;:)]}")
			trailing := raw[len(trimmed):]
			normalized := strings.ReplaceAll(trimmed, "\\", "/")
			for _, repositoryPath := range repositoryPaths {
				relative := strings.TrimPrefix(
					strings.TrimSpace(
						strings.ReplaceAll(repositoryPath, "\\", "/"),
					),
					"./",
				)
				if relative != "" &&
					strings.HasSuffix(normalized, "/"+relative) {
					return relative + trailing
				}
			}
			base := path.Base(normalized)
			if base != "" && base != "." && strings.Contains(base, ".") {
				return base + trailing
			}
			return "LOCAL_PATH" + trailing
		},
	)
}

func evidenceSessionCount(refs []experience.EvidenceRef) int {
	values := make(map[string]struct{})
	for _, ref := range refs {
		if ref.SessionKey != "" {
			values[ref.SessionKey] = struct{}{}
		}
	}
	return len(values)
}

func decodeExperienceSemanticOutput(
	body []byte,
) ([]experienceSemanticOutputCandidate, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope experienceSemanticOutput
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New(
			"decode experience semantic output",
		)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	result := make([]experienceSemanticOutputCandidate, 0, len(envelope.Candidates))
	for _, raw := range envelope.Candidates {
		var header struct {
			Disposition experience.SemanticDisposition `json:"disposition"`
		}
		if err := json.Unmarshal(raw, &header); err != nil ||
			!header.Disposition.Valid() {
			return nil, errors.New("decode experience semantic disposition")
		}
		switch header.Disposition {
		case experience.SemanticDispositionPropose:
			var proposal experienceSemanticProposeOutputCandidate
			if err := decodeStrictJSON(raw, &proposal); err != nil {
				return nil, errors.New("decode experience semantic propose branch")
			}
			if proposal.Confidence == nil {
				return nil, errors.New(
					"experience semantic proposal confidence is required",
				)
			}
			result = append(result, experienceSemanticOutputCandidate{
				CandidateID: proposal.CandidateID,
				Disposition: proposal.Disposition,
				ReasonCode:  experience.SemanticReasonReusableSupported,
				Explanation: proposal.Rationale,
				Confidence:  *proposal.Confidence,
				Proposal:    &proposal,
			})
		case experience.SemanticDispositionReject,
			experience.SemanticDispositionDefer:
			var decision experienceSemanticNoProposalOutputCandidate
			if err := decodeStrictJSON(raw, &decision); err != nil {
				return nil, errors.New(
					"decode experience semantic non-propose branch",
				)
			}
			if decision.Confidence == nil {
				return nil, errors.New(
					"experience semantic decision confidence is required",
				)
			}
			result = append(result, experienceSemanticOutputCandidate{
				CandidateID: decision.CandidateID,
				Disposition: decision.Disposition,
				ReasonCode:  decision.ReasonCode,
				Explanation: decision.Explanation,
				Confidence:  *decision.Confidence,
			})
		}
	}
	return result, nil
}

func compileExperienceSemanticResults(
	candidates []experience.Candidate,
	output []experienceSemanticOutputCandidate,
	harness SemanticHarness,
	model string,
	inputHash string,
	outputHash string,
	generatedAt time.Time,
) (
	[]experience.SemanticResult,
	experience.SemanticDispositionCounts,
	error,
) {
	if len(output) != len(candidates) {
		return nil, experience.SemanticDispositionCounts{}, errors.New(
			"experience semantic output is missing or adds candidate decisions",
		)
	}
	known := make(map[string]experience.Candidate, len(candidates))
	for _, candidate := range candidates {
		known[candidate.CandidateID] = candidate
	}
	seen := make(map[string]bool, len(output))
	results := make([]experience.SemanticResult, 0, len(output))
	var counts experience.SemanticDispositionCounts
	for _, value := range output {
		candidate, ok := known[value.CandidateID]
		if !ok || seen[value.CandidateID] {
			return nil, experience.SemanticDispositionCounts{}, errors.New(
				"experience semantic output contains unknown or duplicate candidate ID",
			)
		}
		seen[value.CandidateID] = true
		provenance := experience.SemanticProposalProvenance{
			Harness:       experience.Harness(harness),
			Model:         model,
			PromptVersion: ExperiencePromptVersion,
			InputHash:     inputHash,
			OutputHash:    outputHash,
			GeneratedAt:   generatedAt,
		}
		var proposal *experience.SemanticProposal
		if value.Disposition == experience.SemanticDispositionPropose {
			if value.Proposal == nil {
				return nil, experience.SemanticDispositionCounts{}, errors.New(
					"experience semantic propose branch is missing proposal fields",
				)
			}
			content, err := semanticProposalContent(candidate, *value.Proposal)
			if err != nil {
				if errors.Is(err, errExperienceSemanticEvidenceSupport) {
					value.Disposition = experience.SemanticDispositionDefer
					value.ReasonCode = experience.SemanticReasonInsufficientContext
					value.Explanation =
						"Proposed guidance lacked valid clause-level evidence support."
					value.Proposal = nil
				} else {
					return nil, experience.SemanticDispositionCounts{}, err
				}
			} else {
				record := experience.SemanticProposal{
					SchemaVersion:   experience.SemanticProposalSchemaVersion,
					CandidateID:     candidate.CandidateID,
					ProjectIdentity: candidate.ProjectIdentity,
					Proposal:        content,
					Provenance:      provenance,
					Authority:       experience.AuthorityNone,
				}
				record.ProposalID = record.DeterministicID()
				if err := record.Validate(); err != nil {
					return nil, experience.SemanticDispositionCounts{}, fmt.Errorf(
						"validate experience semantic proposal: %w",
						err,
					)
				}
				proposal = &record
			}
		}
		decision := experience.SemanticDecision{
			SchemaVersion:   experience.SemanticDecisionSchemaVersion,
			CandidateID:     candidate.CandidateID,
			ProjectIdentity: candidate.ProjectIdentity,
			Disposition:     value.Disposition,
			ReasonCode:      value.ReasonCode,
			Explanation:     value.Explanation,
			Confidence:      value.Confidence,
			Provenance:      provenance,
		}
		if proposal != nil {
			decision.ProposalID = proposal.ProposalID
		}
		decision.DecisionID = decision.DeterministicID()
		result := experience.SemanticResult{
			Proposal: proposal,
			Decision: decision,
		}
		if err := result.Validate(); err != nil {
			return nil, experience.SemanticDispositionCounts{}, fmt.Errorf(
				"validate experience semantic result: %w",
				err,
			)
		}
		incrementExperienceSemanticCounts(&counts, value.Disposition)
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Decision.CandidateID <
			results[j].Decision.CandidateID
	})
	return results, counts, nil
}

func incrementExperienceSemanticCounts(
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

func semanticProposalContent(
	candidate experience.Candidate,
	value experienceSemanticProposeOutputCandidate,
) (experience.ExperienceProposal, error) {
	if value.Applicability.TaskFamilies == nil ||
		value.Applicability.PathHints == nil ||
		value.Applicability.Harnesses == nil ||
		value.Applicability.Models == nil ||
		value.Exceptions == nil ||
		len(value.Applicability.TaskFamilies) > maxExperienceSemanticListItems ||
		len(value.Applicability.PathHints) > maxExperienceSemanticListItems ||
		len(value.Applicability.Harnesses) > maxExperienceSemanticListItems ||
		len(value.Applicability.Models) > maxExperienceSemanticListItems ||
		len(value.Exceptions) > maxExperienceSemanticListItems ||
		hasDuplicateStrings(value.Applicability.TaskFamilies) ||
		hasDuplicateStrings(value.Applicability.PathHints) ||
		hasDuplicateHarnesses(value.Applicability.Harnesses) ||
		hasDuplicateStrings(value.Applicability.Models) ||
		hasDuplicateStrings(value.Exceptions) {
		return experience.ExperienceProposal{}, errors.New(
			"experience semantic proposal lists are missing, duplicated, or exceed limits",
		)
	}
	if value.Scope.ProjectIdentity != candidate.ProjectIdentity {
		return experience.ExperienceProposal{}, errors.New(
			"experience semantic proposal project does not match candidate",
		)
	}
	if value.Scope.Kind == experience.ScopeSession &&
		!candidateCitesSession(candidate, value.Scope.SessionKey) {
		return experience.ExperienceProposal{}, errors.New(
			"experience semantic proposal session is not cited by candidate",
		)
	}
	if value.Confidence == nil {
		return experience.ExperienceProposal{}, errors.New(
			"experience semantic proposal confidence is required",
		)
	}
	if len(value.Rationale) > maxExperienceSemanticDecisionBytes {
		return experience.ExperienceProposal{}, errors.New(
			"experience semantic proposal rationale exceeds decision explanation limit",
		)
	}
	textFields := []struct {
		name  string
		value string
	}{
		{name: "guidance", value: value.Guidance},
		{name: "rationale", value: value.Rationale},
		{
			name:  "semantic_description",
			value: value.Applicability.SemanticDescription,
		},
	}
	for index, exception := range value.Exceptions {
		textFields = append(textFields, struct {
			name  string
			value string
		}{
			name:  fmt.Sprintf("exception[%d]", index),
			value: exception,
		})
	}
	for _, field := range textFields {
		if !semanticProposalPlainText(field.value) {
			return experience.ExperienceProposal{}, fmt.Errorf(
				"experience semantic proposal %s contains URL, markup, or control text",
				field.name,
			)
		}
	}
	verifier, err := semanticProposalVerifier(value.Verifier)
	if err != nil {
		return experience.ExperienceProposal{}, err
	}
	evidenceSupport, err := semanticProposalEvidenceSupport(candidate, value)
	if err != nil {
		return experience.ExperienceProposal{}, err
	}
	if err := semanticProposalPreservesEvidenceBoundaries(
		candidate,
		value,
		evidenceSupport,
	); err != nil {
		return experience.ExperienceProposal{}, err
	}
	conditions := make([]experience.DeterministicCondition, 0, 1)
	if len(value.Applicability.PathHints) > 0 {
		conditions = append(conditions, experience.DeterministicCondition{
			Kind:   experience.ConditionPathPattern,
			Values: append([]string(nil), value.Applicability.PathHints...),
		})
	}
	harnesses := semanticProposalHarnesses(candidate, value)
	proposal := experience.ExperienceProposal{
		Type: value.ExperienceType,
		Scope: experience.Scope{
			Kind:            value.Scope.Kind,
			ProjectIdentity: value.Scope.ProjectIdentity,
			SessionKey:      value.Scope.SessionKey,
			RepositoryPaths: append([]string(nil), value.Applicability.PathHints...),
			TaskFamilies:    append([]string(nil), value.Applicability.TaskFamilies...),
			Harnesses:       harnesses,
			Models:          append([]string(nil), value.Applicability.Models...),
		},
		Applicability: experience.Applicability{
			SemanticDescription:     value.Applicability.SemanticDescription,
			DeterministicConditions: conditions,
		},
		Guidance: experience.Guidance{
			Instruction:          value.Guidance,
			Rationale:            value.Rationale,
			Exceptions:           append([]string(nil), value.Exceptions...),
			InterventionStrength: value.InterventionStrength,
		},
		Verifier:        verifier,
		EvidenceSupport: evidenceSupport,
		Confidence:      value.Confidence,
	}
	if err := proposal.Validate(); err != nil {
		return experience.ExperienceProposal{}, fmt.Errorf(
			"invalid experience semantic proposal content: %w",
			err,
		)
	}
	return proposal, nil
}

func semanticProposalPreservesEvidenceBoundaries(
	candidate experience.Candidate,
	value experienceSemanticProposeOutputCandidate,
	support *experience.EvidenceSupport,
) error {
	if support == nil {
		return fmt.Errorf(
			"%w: support map is required",
			errExperienceSemanticEvidenceSupport,
		)
	}
	excerpts := boundedExperienceSemanticExcerpts(candidate)
	byID := make(
		map[string]experienceSemanticPromptExcerpt,
		len(excerpts),
	)
	for _, excerpt := range excerpts {
		byID[excerpt.EvidenceRefID] = excerpt
	}

	if candidate.Family == experience.CandidateSuccessfulProcedure {
		authoritative := authoritativeSemanticBoundaryRefs(
			excerpts,
			"rule:",
		)
		refs := support.GuidanceRefs
		if len(authoritative) > 0 {
			refs = intersectSemanticEvidenceRefs(refs, authoritative)
		}
		if !semanticTextQuotedByEvidence(value.Guidance, refs, byID) {
			return fmt.Errorf(
				"%w: successful-procedure guidance is not an exact cited evidence quote",
				errExperienceSemanticEvidenceSupport,
			)
		}
	}

	authoritativeExceptions := authoritativeSemanticBoundaryRefs(
		excerpts,
		"exception:",
	)
	for index, exception := range value.Exceptions {
		refs := support.ExceptionRefs[index]
		if len(authoritativeExceptions) > 0 {
			refs = intersectSemanticEvidenceRefs(
				refs,
				authoritativeExceptions,
			)
		}
		if !semanticTextQuotedByEvidence(exception, refs, byID) {
			return fmt.Errorf(
				"%w: exception %d is not an exact cited evidence quote",
				errExperienceSemanticEvidenceSupport,
				index,
			)
		}
	}
	return nil
}

func authoritativeSemanticBoundaryRefs(
	excerpts []experienceSemanticPromptExcerpt,
	marker string,
) []string {
	marker = strings.ToLower(strings.TrimSpace(marker))
	refs := make([]string, 0)
	for _, excerpt := range excerpts {
		if excerpt.TurnRole != experience.EvidenceTurnUser ||
			!strings.Contains(
				strings.ToLower(excerpt.Text),
				marker,
			) {
			continue
		}
		refs = append(refs, excerpt.EvidenceRefID)
	}
	return refs
}

func intersectSemanticEvidenceRefs(
	refs []string,
	allowed []string,
) []string {
	allow := make(map[string]bool, len(allowed))
	for _, ref := range allowed {
		allow[ref] = true
	}
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		if allow[ref] {
			result = append(result, ref)
		}
	}
	return result
}

func semanticTextQuotedByEvidence(
	value string,
	refs []string,
	excerpts map[string]experienceSemanticPromptExcerpt,
) bool {
	value = normalizeSemanticEvidenceQuote(value)
	if value == "" {
		return false
	}
	for _, ref := range refs {
		excerpt, found := excerpts[ref]
		if !found {
			continue
		}
		if strings.Contains(
			normalizeSemanticEvidenceQuote(excerpt.Text),
			value,
		) {
			return true
		}
	}
	return false
}

func normalizeSemanticEvidenceQuote(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func semanticProposalEvidenceSupport(
	candidate experience.Candidate,
	value experienceSemanticProposeOutputCandidate,
) (*experience.EvidenceSupport, error) {
	excerpts := boundedExperienceSemanticExcerpts(candidate)
	known := make(map[string]bool, len(excerpts))
	for _, excerpt := range excerpts {
		known[excerpt.EvidenceRefID] = true
	}
	if value.GuidanceSupportRefs == nil ||
		value.ExceptionSupportRefs == nil ||
		value.VerifierSupportRefs == nil ||
		len(value.ExceptionSupportRefs) != len(value.Exceptions) {
		return nil, fmt.Errorf(
			"%w: support groups are missing or misaligned",
			errExperienceSemanticEvidenceSupport,
		)
	}
	groups := make([][]string, 0, len(value.ExceptionSupportRefs)+2)
	groups = append(
		groups,
		value.GuidanceSupportRefs,
		value.VerifierSupportRefs,
	)
	groups = append(groups, value.ExceptionSupportRefs...)
	for _, refs := range groups {
		if len(refs) == 0 || len(refs) > maxExperienceSemanticListItems {
			return nil, fmt.Errorf(
				"%w: support group is empty or exceeds limit",
				errExperienceSemanticEvidenceSupport,
			)
		}
		seen := make(map[string]bool, len(refs))
		for _, ref := range refs {
			if !known[ref] || seen[ref] {
				return nil, fmt.Errorf(
					"%w: reference is unknown or duplicated",
					errExperienceSemanticEvidenceSupport,
				)
			}
			seen[ref] = true
		}
	}
	return &experience.EvidenceSupport{
		GuidanceRefs: append(
			[]string(nil),
			value.GuidanceSupportRefs...,
		),
		ExceptionRefs: cloneStringGroups(value.ExceptionSupportRefs),
		VerifierRefs: append(
			[]string(nil),
			value.VerifierSupportRefs...,
		),
	}, nil
}

func cloneStringGroups(values [][]string) [][]string {
	result := make([][]string, len(values))
	for index := range values {
		result[index] = append([]string(nil), values[index]...)
	}
	return result
}

func semanticProposalHarnesses(
	candidate experience.Candidate,
	value experienceSemanticProposeOutputCandidate,
) []experience.Harness {
	harnesses := append(
		[]experience.Harness(nil),
		value.Applicability.Harnesses...,
	)
	if candidate.Family != experience.CandidateSuccessfulProcedure ||
		len(value.Applicability.PathHints) == 0 ||
		len(value.Applicability.Models) != 0 {
		return harnesses
	}
	for _, pathHint := range value.Applicability.PathHints {
		if semanticProposalHarnessSpecificPath(pathHint) {
			return harnesses
		}
	}
	return []experience.Harness{
		experience.HarnessClaude,
		experience.HarnessCodex,
		experience.HarnessCursor,
		experience.HarnessAntigravity,
	}
}

// semanticProposalHarnessSpecificPath reports whether a path hint names a
// harness configuration surface. Antigravity reads .agents/rules/ and, for
// backward compatibility, the legacy .agent/rules/ directory.
func semanticProposalHarnessSpecificPath(value string) bool {
	value = strings.ToLower(strings.TrimPrefix(path.Clean(value), "./"))
	return value == "claude.md" ||
		value == "agents.md" ||
		value == "codex.md" ||
		value == ".cursorrules" ||
		strings.HasPrefix(value, ".claude/") ||
		strings.HasPrefix(value, ".codex/") ||
		strings.HasPrefix(value, ".cursor/") ||
		strings.HasPrefix(value, ".agents/") ||
		strings.HasPrefix(value, ".agent/")
}

func semanticProposalVerifier(
	value experienceSemanticOutputVerifier,
) (experience.Verifier, error) {
	if value.CoverageRequirements == nil ||
		len(value.CoverageRequirements) > maxExperienceSemanticListItems ||
		hasDuplicateCoverage(value.CoverageRequirements) {
		return experience.Verifier{}, errors.New(
			"experience semantic verifier coverage is missing, duplicated, or exceeds limit",
		)
	}
	result := experience.Verifier{
		Kind: value.Kind,
		CoverageRequirements: append(
			[]experience.CoverageRequirement(nil),
			value.CoverageRequirements...,
		),
	}
	switch value.Kind {
	case experience.VerifierCommandObserved,
		experience.VerifierCommandSucceeded:
		var parameters struct {
			Command      string `json:"command"`
			CommandClass string `json:"command_class,omitempty"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil {
			return experience.Verifier{}, err
		}
		result.Command = &experience.CommandVerifierSpec{
			Command:          parameters.Command,
			CommandClass:     parameters.CommandClass,
			ScrubbingVersion: "belay.redaction.v1",
		}
	case experience.VerifierFileNotModified,
		experience.VerifierFileModified:
		var parameters struct {
			Path string `json:"path"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil {
			return experience.Verifier{}, err
		}
		result.File = &experience.FileVerifierSpec{Path: parameters.Path}
	case experience.VerifierPathPatternNotModified:
		var parameters struct {
			Patterns []string `json:"patterns"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil {
			return experience.Verifier{}, err
		}
		result.PathPattern = &experience.PathPatternVerifierSpec{
			Patterns: parameters.Patterns,
		}
	case experience.VerifierVerificationAfterLastEdit:
		var parameters struct {
			CommandClasses []string `json:"command_classes"`
			RequireSuccess *bool    `json:"require_success"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil ||
			parameters.RequireSuccess == nil {
			return experience.Verifier{}, errors.New(
				"invalid verification-after-edit parameters",
			)
		}
		result.VerificationAfterLastEdit =
			&experience.VerificationAfterLastEditSpec{
				CommandClasses: parameters.CommandClasses,
				RequireSuccess: *parameters.RequireSuccess,
			}
	case experience.VerifierNoRepeatFailure:
		var parameters struct {
			CommandClass      string `json:"command_class"`
			NormalizedPattern string `json:"normalized_pattern"`
			WindowTurns       *int   `json:"window_turns"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil ||
			parameters.WindowTurns == nil {
			return experience.Verifier{}, errors.New(
				"invalid no-repeat-failure parameters",
			)
		}
		result.NoRepeatFailure = &experience.NoRepeatFailureSpec{
			CommandClass:      parameters.CommandClass,
			NormalizedPattern: parameters.NormalizedPattern,
			WindowTurns:       *parameters.WindowTurns,
		}
	case experience.VerifierUserCorrectionAbsent:
		var parameters struct {
			MarkerFamilies []string `json:"marker_families"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil {
			return experience.Verifier{}, err
		}
		result.UserCorrectionAbsent = &experience.UserCorrectionAbsentSpec{
			MarkerFamilies: parameters.MarkerFamilies,
		}
	case experience.VerifierObservationOnly:
		var parameters struct {
			Explanation string `json:"explanation"`
		}
		if err := decodeStrictJSON(value.Parameters, &parameters); err != nil {
			return experience.Verifier{}, err
		}
		result.ObservationOnly = &experience.ObservationOnlySpec{
			Explanation: parameters.Explanation,
		}
	default:
		return experience.Verifier{}, errors.New(
			"unsupported experience semantic verifier",
		)
	}
	if err := result.Validate(); err != nil {
		return experience.Verifier{}, fmt.Errorf(
			"invalid experience semantic verifier: %w",
			err,
		)
	}
	return result, nil
}

func experienceSemanticOutputSchema(
	payload experienceSemanticPromptPayload,
) ([]byte, error) {
	candidateIDs := make([]string, 0, len(payload.Candidates))
	evidenceRefIDs := make([]string, 0)
	projectIdentity := ""
	for _, candidate := range payload.Candidates {
		if strings.TrimSpace(candidate.ProjectIdentity) == "" {
			return nil, errors.New(
				"experience semantic schema requires a project identity",
			)
		}
		if projectIdentity == "" {
			projectIdentity = candidate.ProjectIdentity
		} else if candidate.ProjectIdentity != projectIdentity {
			return nil, errors.New(
				"experience semantic schema candidates span projects",
			)
		}
		candidateIDs = append(candidateIDs, candidate.CandidateID)
		for _, excerpt := range candidate.Excerpts {
			if strings.TrimSpace(excerpt.EvidenceRefID) == "" {
				return nil, errors.New(
					"experience semantic schema requires evidence reference IDs",
				)
			}
			evidenceRefIDs = append(
				evidenceRefIDs,
				excerpt.EvidenceRefID,
			)
		}
	}
	if len(candidateIDs) == 0 {
		return nil, errors.New(
			"experience semantic schema requires candidates",
		)
	}
	line := func(maximum int) map[string]any {
		return map[string]any{
			"type":      "string",
			"minLength": 1,
			"maxLength": maximum,
			"pattern":   `^[^\r\n]+$`,
		}
	}
	stringArray := func(maximum int) map[string]any {
		return map[string]any{
			"type":        "array",
			"maxItems":    maxExperienceSemanticListItems,
			"uniqueItems": true,
			"items":       line(maximum),
		}
	}
	supportRefs := func() map[string]any {
		return map[string]any{
			"type":        "array",
			"minItems":    1,
			"maxItems":    maxExperienceSemanticListItems,
			"uniqueItems": true,
			"items": map[string]any{
				"type": "string",
				"enum": evidenceRefIDs,
			},
		}
	}
	relativePath := func() map[string]any {
		return map[string]any{
			"type":      "string",
			"minLength": 1,
			"maxLength": 512,
			"pattern": `^(?:[A-Za-z0-9_@+-][A-Za-z0-9_@+.-]*|` +
				`\.[A-Za-z0-9_@+-][A-Za-z0-9_@+.-]*)` +
				`(?:/(?:[A-Za-z0-9_@+-][A-Za-z0-9_@+.-]*|` +
				`\.[A-Za-z0-9_@+-][A-Za-z0-9_@+.-]*))*$`,
		}
	}
	relativePatternArray := func(minimum int) map[string]any {
		return map[string]any{
			"type":        "array",
			"minItems":    minimum,
			"maxItems":    maxExperienceSemanticListItems,
			"uniqueItems": true,
			"items": map[string]any{
				"type":      "string",
				"minLength": 1,
				"maxLength": 512,
				"pattern": `^(?:[A-Za-z0-9_@+*?+-][A-Za-z0-9_@+.*?-]*|` +
					`\.[A-Za-z0-9_@+*?+-][A-Za-z0-9_@+.*?-]*)` +
					`(?:/(?:[A-Za-z0-9_@+*?+-][A-Za-z0-9_@+.*?-]*|` +
					`\.[A-Za-z0-9_@+*?+-][A-Za-z0-9_@+.*?-]*))*$`,
			},
		}
	}
	confidence := map[string]any{
		"type":    "number",
		"minimum": 0,
		"maximum": 1,
	}
	candidateID := map[string]any{
		"type": "string",
		"enum": candidateIDs,
	}
	scopeProjectIdentity := map[string]any{
		"type": "string",
		"enum": []string{projectIdentity},
	}
	scope := map[string]any{
		"oneOf": []any{
			map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"kind",
					"project_identity",
				},
				"properties": map[string]any{
					"kind": map[string]any{
						"type":  "string",
						"const": "project",
					},
					"project_identity": scopeProjectIdentity,
				},
			},
			map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"kind",
					"project_identity",
					"session_key",
				},
				"properties": map[string]any{
					"kind": map[string]any{
						"type":  "string",
						"const": "session",
					},
					"project_identity": scopeProjectIdentity,
					"session_key":      line(256),
				},
			},
		},
	}
	proposeBranch := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"candidate_id",
			"disposition",
			"experience_type",
			"scope",
			"guidance",
			"rationale",
			"applicability",
			"exceptions",
			"guidance_support_refs",
			"exception_support_refs",
			"verifier_support_refs",
			"intervention_strength",
			"verifier",
			"confidence",
		},
		"properties": map[string]any{
			"candidate_id": candidateID,
			"disposition": map[string]any{
				"type":  "string",
				"const": "propose",
			},
			"experience_type": map[string]any{
				"type": "string",
				"enum": []string{
					"preference",
					"procedure",
					"warning",
					"constraint",
					"fact",
				},
			},
			"scope":     scope,
			"guidance":  line(2048),
			"rationale": line(maxExperienceSemanticDecisionBytes),
			"applicability": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"task_families",
					"path_hints",
					"harnesses",
					"models",
					"semantic_description",
				},
				"properties": map[string]any{
					"task_families": stringArray(256),
					"path_hints":    relativePatternArray(0),
					"harnesses": map[string]any{
						"type":        "array",
						"maxItems":    4,
						"uniqueItems": true,
						"items": map[string]any{
							"type": "string",
							"enum": []string{
								"claude",
								"codex",
								"cursor",
								"antigravity",
							},
						},
					},
					"models":               stringArray(256),
					"semantic_description": line(8192),
				},
			},
			"exceptions":            stringArray(2048),
			"guidance_support_refs": supportRefs(),
			"exception_support_refs": map[string]any{
				"type":     "array",
				"maxItems": maxExperienceSemanticListItems,
				"items":    supportRefs(),
			},
			"verifier_support_refs": supportRefs(),
			"intervention_strength": map[string]any{
				"type": "string",
				"enum": []string{
					"observe",
					"advise",
					"clarify",
					"require_verification",
				},
			},
			"verifier": experienceSemanticVerifierSchema(
				line,
				stringArray,
				relativePath,
				relativePatternArray,
			),
			"confidence": confidence,
		},
	}
	decisionBranch := func(
		disposition experience.SemanticDisposition,
		reasons []string,
	) map[string]any {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"candidate_id",
				"disposition",
				"reason_code",
				"explanation",
				"confidence",
			},
			"properties": map[string]any{
				"candidate_id": candidateID,
				"disposition": map[string]any{
					"type":  "string",
					"const": disposition,
				},
				"reason_code": map[string]any{
					"type": "string",
					"enum": reasons,
				},
				"explanation": line(maxExperienceSemanticDecisionBytes),
				"confidence":  confidence,
			},
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"candidates"},
		"properties": map[string]any{
			"candidates": map[string]any{
				"type":     "array",
				"minItems": len(candidateIDs),
				"maxItems": len(candidateIDs),
				"items": map[string]any{
					"oneOf": []any{
						proposeBranch,
						decisionBranch(
							experience.SemanticDispositionReject,
							[]string{
								"temporary_or_task_specific",
								"unsafe_or_overbroad",
								"not_reusable",
							},
						),
						decisionBranch(
							experience.SemanticDispositionDefer,
							[]string{"insufficient_context"},
						),
					},
				},
			},
		},
	}
	body, err := json.Marshal(schema)
	if err != nil {
		return nil, errors.New(
			"encode experience semantic output schema",
		)
	}
	return body, nil
}

func experienceSemanticVerifierSchema(
	line func(int) map[string]any,
	stringArray func(int) map[string]any,
	relativePath func() map[string]any,
	relativePatternArray func(int) map[string]any,
) map[string]any {
	coverage := func(minimum int) map[string]any {
		return map[string]any{
			"type":        "array",
			"minItems":    minimum,
			"maxItems":    4,
			"uniqueItems": true,
			"items": map[string]any{
				"type": "string",
				"enum": []string{
					"transcript_complete",
					"canonical_events_complete",
					"workspace_state_captured",
					"outcome_observations_complete",
				},
			},
		}
	}
	typed := func(
		kind experience.VerifierKind,
		minimumCoverage int,
		required []string,
		properties map[string]any,
	) map[string]any {
		parameters := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           properties,
		}
		if len(required) > 0 {
			parameters["required"] = required
		}
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"kind",
				"coverage_requirements",
				"parameters",
			},
			"properties": map[string]any{
				"kind": map[string]any{
					"type":  "string",
					"const": kind,
				},
				"coverage_requirements": coverage(minimumCoverage),
				"parameters":            parameters,
			},
		}
	}
	return map[string]any{
		"oneOf": []any{
			typed(
				experience.VerifierCommandObserved,
				0,
				[]string{"command"},
				map[string]any{
					"command":       line(16 << 10),
					"command_class": line(256),
				},
			),
			typed(
				experience.VerifierCommandSucceeded,
				0,
				[]string{"command"},
				map[string]any{
					"command":       line(16 << 10),
					"command_class": line(256),
				},
			),
			typed(
				experience.VerifierFileNotModified,
				1,
				[]string{"path"},
				map[string]any{"path": relativePath()},
			),
			typed(
				experience.VerifierFileModified,
				0,
				[]string{"path"},
				map[string]any{"path": relativePath()},
			),
			typed(
				experience.VerifierPathPatternNotModified,
				1,
				[]string{"patterns"},
				map[string]any{"patterns": relativePatternArray(1)},
			),
			typed(
				experience.VerifierVerificationAfterLastEdit,
				0,
				[]string{"require_success"},
				map[string]any{
					"command_classes": stringArray(256),
					"require_success": map[string]any{"type": "boolean"},
				},
			),
			typed(
				experience.VerifierNoRepeatFailure,
				1,
				[]string{
					"command_class",
					"normalized_pattern",
					"window_turns",
				},
				map[string]any{
					"command_class":      line(256),
					"normalized_pattern": line(2048),
					"window_turns": map[string]any{
						"type":    "integer",
						"minimum": 1,
						"maximum": 1000,
					},
				},
			),
			typed(
				experience.VerifierUserCorrectionAbsent,
				1,
				[]string{},
				map[string]any{
					"marker_families": stringArray(256),
				},
			),
			typed(
				experience.VerifierObservationOnly,
				0,
				[]string{"explanation"},
				map[string]any{"explanation": line(8192)},
			),
		},
	}
}

func decodeStrictJSON(body []byte, target any) error {
	if len(body) == 0 {
		return errors.New("experience semantic verifier parameters are required")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("decode experience semantic verifier parameters")
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("experience semantic output contains trailing JSON")
	}
	return nil
}

func candidateCitesSession(
	candidate experience.Candidate,
	sessionKey string,
) bool {
	if strings.TrimSpace(sessionKey) == "" {
		return false
	}
	for _, ref := range candidate.Evidence.Refs {
		if ref.Kind == experience.EvidenceTranscriptTurn &&
			ref.SessionKey == sessionKey {
			return true
		}
	}
	return false
}

func semanticProposalPlainText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" ||
		strings.Contains(value, "`") ||
		experienceSemanticMarkupPattern.MatchString(value) ||
		strings.Contains(strings.ToLower(value), "http://") ||
		strings.Contains(strings.ToLower(value), "https://") {
		return false
	}
	for _, character := range value {
		if character < 0x20 {
			return false
		}
	}
	return true
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func hasDuplicateHarnesses(values []experience.Harness) bool {
	seen := make(map[experience.Harness]bool, len(values))
	for _, value := range values {
		if !value.Valid() || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func hasDuplicateCoverage(
	values []experience.CoverageRequirement,
) bool {
	seen := make(map[experience.CoverageRequirement]bool, len(values))
	for _, value := range values {
		if !value.Valid() || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func boundedSortedStrings(values []string, limit int) []string {
	result := make([]string, 0, min(len(values), limit))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := value.UTC().Round(0)
	return &copyValue
}

func sha256Prefixed(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
