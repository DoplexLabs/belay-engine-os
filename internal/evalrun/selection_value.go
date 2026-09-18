package evalrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
)

const SelectionValuePlanSchemaVersion = "belay.selection-value-plan.v1"

var selectionValueBaselines = []string{
	"no_memory",
	"static_all_experiences",
	"retrieved_source_experience",
	"selected_candidate_treatment",
}

var selectionValueMetrics = []string{
	"task_success",
	"failed_attempts",
	"selection_precision",
	"selection_recall",
	"latency_ms",
	"tokens",
	"cost_usd",
}

type SelectionValuePlan struct {
	SchemaVersion            string                     `json:"schema_version"`
	PlanID                   string                     `json:"plan_id"`
	ProjectIdentity          string                     `json:"project_identity"`
	SourceCapsuleID          string                     `json:"source_capsule_id"`
	SourceCandidateID        string                     `json:"source_candidate_id"`
	SourceCandidateFamily    experience.CandidateFamily `json:"source_candidate_family"`
	SourceProposalHash       string                     `json:"source_proposal_hash"`
	SourceReplayHash         string                     `json:"source_replay_hash"`
	ExperiencePool           []SelectionValueCandidate  `json:"experience_pool"`
	Targets                  []SelectionValueTarget     `json:"targets"`
	ForbiddenPromptPhrases   []string                   `json:"forbidden_prompt_phrases"`
	RequiredBaselines        []string                   `json:"required_baselines"`
	RequiredMetrics          []string                   `json:"required_metrics"`
	ScoringAuthority         string                     `json:"scoring_authority"`
	LLMJudgeAuthority        string                     `json:"llm_judge_authority"`
	NegativeResultsPreserved bool                       `json:"negative_results_preserved"`
	CreatedAt                time.Time                  `json:"created_at"`
}

type SelectionValueCandidate struct {
	CandidateID     string                        `json:"candidate_id"`
	Family          experience.CandidateFamily    `json:"family"`
	SourceCapsuleID string                        `json:"source_capsule_id,omitempty"`
	Proposal        experience.ExperienceProposal `json:"proposal"`
}

type SelectionValueTarget struct {
	TargetID             string                              `json:"target_id"`
	Replay               CapsuleReplayContract               `json:"replay"`
	SelectionRequest     localapp.ExperienceSelectionRequest `json:"selection_request"`
	RelevantCandidateIDs []string                            `json:"relevant_candidate_ids"`
}

func BuildSelectionValuePlan(
	source PrivateEvalCapsule,
	pool []SelectionValueCandidate,
	targets []SelectionValueTarget,
	forbiddenPromptPhrases []string,
	createdAt time.Time,
) (SelectionValuePlan, error) {
	if err := validateSelectionValueSource(source); err != nil {
		return SelectionValuePlan{}, err
	}
	replayHash, err := stableSelectionValueHash(source.Replay)
	if err != nil {
		return SelectionValuePlan{}, err
	}
	plan := SelectionValuePlan{
		SchemaVersion:            SelectionValuePlanSchemaVersion,
		ProjectIdentity:          source.ProjectIdentity,
		SourceCapsuleID:          source.CapsuleID,
		SourceCandidateID:        source.CandidateID,
		SourceCandidateFamily:    source.CandidateFamily,
		SourceProposalHash:       source.TreatmentCandidate.CanonicalContentHash(),
		SourceReplayHash:         replayHash,
		ExperiencePool:           append([]SelectionValueCandidate(nil), pool...),
		Targets:                  append([]SelectionValueTarget(nil), targets...),
		ForbiddenPromptPhrases:   append([]string(nil), forbiddenPromptPhrases...),
		RequiredBaselines:        append([]string(nil), selectionValueBaselines...),
		RequiredMetrics:          append([]string(nil), selectionValueMetrics...),
		ScoringAuthority:         "deterministic_observation",
		LLMJudgeAuthority:        "none",
		NegativeResultsPreserved: true,
		CreatedAt:                createdAt.UTC().Round(0),
	}
	normalizeSelectionValuePlan(&plan)
	if err := plan.Validate(); err != nil {
		return SelectionValuePlan{}, err
	}
	plan.PlanID = selectionValuePlanID(plan)
	return plan, nil
}

func (value SelectionValuePlan) Validate() error {
	if value.SchemaVersion != SelectionValuePlanSchemaVersion {
		return errors.New("selection value plan schema version is invalid")
	}
	if strings.TrimSpace(value.ProjectIdentity) == "" ||
		strings.TrimSpace(value.SourceCapsuleID) == "" ||
		strings.TrimSpace(value.SourceCandidateID) == "" ||
		(value.SourceCandidateFamily != experience.CandidateSuccessfulProcedure &&
			value.SourceCandidateFamily != experience.CandidateFailedApproach) ||
		!validSelectionValueSHA256(value.SourceProposalHash) ||
		!validSelectionValueSHA256(value.SourceReplayHash) {
		return errors.New(
			"selection value plan requires a verified procedure or repair source",
		)
	}
	if value.CreatedAt.IsZero() {
		return errors.New("selection value plan creation time is required")
	}
	if value.ScoringAuthority != "deterministic_observation" ||
		value.LLMJudgeAuthority != "none" ||
		!value.NegativeResultsPreserved {
		return errors.New(
			"selection value plan requires deterministic scoring and preserved negative results",
		)
	}
	if !sameSelectionValueStrings(
		value.RequiredBaselines,
		selectionValueBaselines,
	) || !sameSelectionValueStrings(
		value.RequiredMetrics,
		selectionValueMetrics,
	) {
		return errors.New(
			"selection value plan baselines or metrics are incomplete",
		)
	}
	if err := value.validatePool(); err != nil {
		return err
	}
	if err := value.validateForbiddenPromptPhrases(); err != nil {
		return err
	}
	if err := value.validateTargets(); err != nil {
		return err
	}
	if value.PlanID != "" && value.PlanID != selectionValuePlanID(value) {
		return errors.New("selection value plan ID does not match content")
	}
	return nil
}

func validateSelectionValueSource(source PrivateEvalCapsule) error {
	if source.SchemaVersion != PrivateEvalCapsuleSchemaVersion ||
		source.Readiness != "executable" ||
		len(source.ReadinessGaps) != 0 ||
		source.Evaluation.TaskState != "reconstructed" ||
		source.Replay == nil ||
		source.Provenance.InstructionAuthority !=
			string(experience.AuthorityNone) ||
		(source.CandidateFamily != experience.CandidateSuccessfulProcedure &&
			source.CandidateFamily != experience.CandidateFailedApproach) {
		return errors.New(
			"selection value source must be an executable non-authoritative procedure or repair capsule",
		)
	}
	if strings.TrimSpace(source.CapsuleID) == "" ||
		strings.TrimSpace(source.CandidateID) == "" ||
		strings.TrimSpace(source.ProjectIdentity) == "" ||
		len(source.SourceSessions) == 0 ||
		len(source.Evidence) == 0 ||
		len(source.Outcomes) == 0 {
		return errors.New("selection value source identity is incomplete")
	}
	sessionSet := make(map[string]bool, len(source.SourceSessions))
	for _, sessionKey := range source.SourceSessions {
		sessionKey = strings.TrimSpace(sessionKey)
		if sessionKey == "" || sessionSet[sessionKey] {
			return errors.New(
				"selection value source sessions are invalid or duplicated",
			)
		}
		sessionSet[sessionKey] = true
	}
	evidenceSession := false
	for _, ref := range source.Evidence {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf(
				"validate selection value source evidence: %w",
				err,
			)
		}
		if sessionSet[ref.SessionKey] {
			evidenceSession = true
		}
	}
	if !evidenceSession {
		return errors.New(
			"selection value source evidence is not bound to a source session",
		)
	}
	verifiedOutcome := false
	for _, outcome := range source.Outcomes {
		if outcome.ProjectIdentity != source.ProjectIdentity ||
			!sessionSet[outcome.SessionKey] ||
			outcome.OccurredAt.IsZero() ||
			len(outcome.SourceRefs) == 0 {
			return errors.New(
				"selection value source outcome does not match its project and sessions",
			)
		}
		if outcome.Result == "succeeded" &&
			(outcome.Kind == "verification_pass" ||
				outcome.Kind == "commit") {
			verifiedOutcome = true
		}
	}
	if !verifiedOutcome {
		return errors.New(
			"selection value source requires a successful verification or commit outcome",
		)
	}
	if source.TreatmentCandidate.Scope.ProjectIdentity !=
		source.ProjectIdentity {
		return errors.New(
			"selection value source treatment project does not match evidence",
		)
	}
	if err := source.TreatmentCandidate.Validate(); err != nil {
		return fmt.Errorf("validate selection value source treatment: %w", err)
	}
	if err := source.Replay.Validate(); err != nil {
		return fmt.Errorf("validate selection value source replay: %w", err)
	}
	if source.Replay.Source.Repository != source.ProjectIdentity {
		return errors.New(
			"selection value source replay repository does not match its project",
		)
	}
	return nil
}

func (value SelectionValuePlan) validatePool() error {
	if len(value.ExperiencePool) < 4 || len(value.ExperiencePool) > 12 {
		return errors.New(
			"selection value plan requires one source candidate and at least three bounded distractors",
		)
	}
	seen := make(map[string]bool, len(value.ExperiencePool))
	sourceMatches := 0
	for _, candidate := range value.ExperiencePool {
		candidateID := strings.TrimSpace(candidate.CandidateID)
		if candidateID == "" || seen[candidateID] ||
			!candidate.Family.Valid() {
			return errors.New(
				"selection value experience pool contains an invalid or duplicate candidate",
			)
		}
		seen[candidateID] = true
		if err := candidate.Proposal.Validate(); err != nil {
			return fmt.Errorf(
				"validate selection value candidate %s: %w",
				candidateID,
				err,
			)
		}
		if candidate.Proposal.Scope.ProjectIdentity !=
			value.ProjectIdentity {
			return errors.New(
				"selection value candidate project does not match plan",
			)
		}
		if candidateID == value.SourceCandidateID {
			sourceMatches++
			if candidate.SourceCapsuleID != value.SourceCapsuleID ||
				candidate.Family != value.SourceCandidateFamily ||
				candidate.Proposal.CanonicalContentHash() !=
					value.SourceProposalHash {
				return errors.New(
					"selection value source candidate does not match its capsule",
				)
			}
		} else if strings.TrimSpace(candidate.SourceCapsuleID) != "" {
			return errors.New(
				"selection value distractor cannot claim the source capsule",
			)
		}
	}
	if sourceMatches != 1 {
		return errors.New(
			"selection value pool must contain the source candidate exactly once",
		)
	}
	return nil
}

func (value SelectionValuePlan) validateForbiddenPromptPhrases() error {
	if len(value.ForbiddenPromptPhrases) == 0 ||
		len(value.ForbiddenPromptPhrases) > 8 {
		return errors.New(
			"selection value plan requires bounded source-answer leakage guards",
		)
	}
	sourceText := ""
	for _, candidate := range value.ExperiencePool {
		if candidate.CandidateID == value.SourceCandidateID {
			sourceText = candidate.Proposal.Guidance.Instruction + "\n" +
				candidate.Proposal.Guidance.Rationale
			break
		}
	}
	sourceText = strings.ToLower(sourceText)
	seen := make(map[string]bool, len(value.ForbiddenPromptPhrases))
	for _, phrase := range value.ForbiddenPromptPhrases {
		phrase = strings.Join(strings.Fields(strings.TrimSpace(phrase)), " ")
		normalized := strings.ToLower(phrase)
		if len([]byte(phrase)) < 8 || len([]byte(phrase)) > 256 ||
			seen[normalized] ||
			!strings.Contains(sourceText, normalized) {
			return errors.New(
				"selection value leakage guard must be unique, bounded, and sourced from the procedure",
			)
		}
		seen[normalized] = true
	}
	return nil
}

func (value SelectionValuePlan) validateTargets() error {
	if len(value.Targets) < 2 || len(value.Targets) > 4 {
		return errors.New(
			"selection value plan requires two to four transfer targets",
		)
	}
	pool := make(map[string]bool, len(value.ExperiencePool))
	for _, candidate := range value.ExperiencePool {
		pool[candidate.CandidateID] = true
	}
	seenTargets := make(map[string]bool, len(value.Targets))
	seenReplays := make(map[string]bool, len(value.Targets))
	for _, target := range value.Targets {
		targetID := strings.TrimSpace(target.TargetID)
		if targetID == "" || seenTargets[targetID] {
			return errors.New(
				"selection value target identity is missing or duplicated",
			)
		}
		seenTargets[targetID] = true
		if err := target.Replay.Validate(); err != nil {
			return fmt.Errorf(
				"validate selection value target %s replay: %w",
				targetID,
				err,
			)
		}
		if target.Replay.Source.Repository != value.ProjectIdentity {
			return errors.New(
				"selection value target replay repository does not match the plan project",
			)
		}
		replayHash, err := stableSelectionValueHash(target.Replay)
		if err != nil {
			return err
		}
		if replayHash == value.SourceReplayHash || seenReplays[replayHash] {
			return errors.New(
				"selection value targets must be distinct from the source and each other",
			)
		}
		seenReplays[replayHash] = true
		if err := validateSelectionValueRequest(
			target.SelectionRequest,
			value.ProjectIdentity,
		); err != nil {
			return fmt.Errorf(
				"validate selection value target %s request: %w",
				targetID,
				err,
			)
		}
		if strings.Join(
			strings.Fields(target.SelectionRequest.TaskHint),
			" ",
		) != strings.Join(
			strings.Fields(target.Replay.Task.Prompt),
			" ",
		) || !sameSelectionValueStrings(
			target.SelectionRequest.RepositoryPaths,
			target.Replay.Task.AllowedMutationPaths,
		) {
			return errors.New(
				"selection value target must give the production selector the exact task and mutation paths",
			)
		}
		if len(target.RelevantCandidateIDs) == 0 ||
			len(target.RelevantCandidateIDs) > 3 {
			return errors.New(
				"selection value target requires bounded relevant candidates",
			)
		}
		relevantSeen := make(map[string]bool, len(target.RelevantCandidateIDs))
		sourceRelevant := false
		for _, candidateID := range target.RelevantCandidateIDs {
			candidateID = strings.TrimSpace(candidateID)
			if !pool[candidateID] || relevantSeen[candidateID] {
				return errors.New(
					"selection value target references an invalid relevant candidate",
				)
			}
			relevantSeen[candidateID] = true
			sourceRelevant = sourceRelevant ||
				candidateID == value.SourceCandidateID
		}
		if !sourceRelevant {
			return errors.New(
				"selection value target must make the source procedure relevant",
			)
		}
		prompt := strings.ToLower(target.Replay.Task.Prompt)
		for _, phrase := range value.ForbiddenPromptPhrases {
			normalized := strings.ToLower(
				strings.Join(strings.Fields(strings.TrimSpace(phrase)), " "),
			)
			if strings.Contains(prompt, normalized) {
				return errors.New(
					"selection value target prompt leaks the source procedure",
				)
			}
		}
	}
	return nil
}

func validateSelectionValueRequest(
	request localapp.ExperienceSelectionRequest,
	projectIdentity string,
) error {
	if strings.TrimSpace(request.ProjectIdentity) != projectIdentity ||
		!request.Harness.Valid() ||
		strings.TrimSpace(request.TaskFamily) == "" ||
		strings.TrimSpace(request.TaskHint) == "" ||
		len(request.RepositoryPaths) == 0 ||
		len(request.RepositoryPaths) > 128 ||
		len([]byte(request.TaskHint)) > 1120 ||
		utf8.RuneCountInString(request.TaskHint) > 280 {
		return errors.New(
			"selection request requires exact project, harness, task, hint, and paths",
		)
	}
	scope := experience.Scope{
		Kind:            experience.ScopeProject,
		ProjectIdentity: projectIdentity,
		TaskFamilies:    []string{strings.TrimSpace(request.TaskFamily)},
		Harnesses:       []experience.Harness{request.Harness},
	}
	if strings.TrimSpace(request.Model) != "" {
		scope.Models = []string{strings.TrimSpace(request.Model)}
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	for _, repositoryPath := range request.RepositoryPaths {
		if err := (experience.FileVerifierSpec{
			Path: strings.TrimSpace(repositoryPath),
		}).Validate(); err != nil {
			return fmt.Errorf("selection request repository path: %w", err)
		}
	}
	return nil
}

func selectionValuePlanID(value SelectionValuePlan) string {
	copyValue := value
	copyValue.PlanID = ""
	copyValue.CreatedAt = time.Time{}
	encoded, err := json.Marshal(copyValue)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return "svp_" + hex.EncodeToString(sum[:])
}

func normalizeSelectionValuePlan(value *SelectionValuePlan) {
	sort.Slice(value.ExperiencePool, func(first, second int) bool {
		return value.ExperiencePool[first].CandidateID <
			value.ExperiencePool[second].CandidateID
	})
	for index := range value.Targets {
		target := &value.Targets[index]
		target.RelevantCandidateIDs = append(
			[]string(nil),
			target.RelevantCandidateIDs...,
		)
		sort.Strings(target.RelevantCandidateIDs)
		target.SelectionRequest.RepositoryPaths = append(
			[]string(nil),
			target.SelectionRequest.RepositoryPaths...,
		)
		sort.Strings(target.SelectionRequest.RepositoryPaths)
	}
	sort.Slice(value.Targets, func(first, second int) bool {
		return value.Targets[first].TargetID < value.Targets[second].TargetID
	})
	for index, phrase := range value.ForbiddenPromptPhrases {
		value.ForbiddenPromptPhrases[index] = strings.Join(
			strings.Fields(strings.TrimSpace(phrase)),
			" ",
		)
	}
	sort.Slice(value.ForbiddenPromptPhrases, func(first, second int) bool {
		return strings.ToLower(value.ForbiddenPromptPhrases[first]) <
			strings.ToLower(value.ForbiddenPromptPhrases[second])
	})
}

func stableSelectionValueHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("encode selection value content")
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validSelectionValueSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) ||
		len(value) != len(prefix)+64 ||
		value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}

func sameSelectionValueStrings(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
