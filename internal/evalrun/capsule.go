package evalrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

const PrivateEvalCapsuleSchemaVersion = "belay.private-eval-capsule.v1"

type PrivateEvalCapsule struct {
	SchemaVersion      string                        `json:"schema_version"`
	CapsuleID          string                        `json:"capsule_id"`
	Readiness          string                        `json:"readiness"`
	ReadinessGaps      []string                      `json:"readiness_gaps"`
	CandidateID        string                        `json:"candidate_id"`
	ProposalID         string                        `json:"proposal_id"`
	ProjectIdentity    string                        `json:"project_identity"`
	CandidateFamily    experience.CandidateFamily    `json:"candidate_family"`
	ObservedBehavior   string                        `json:"observed_behavior"`
	UserFeedback       string                        `json:"user_feedback,omitempty"`
	TreatmentCandidate experience.ExperienceProposal `json:"treatment_candidate"`
	SourceSessions     []string                      `json:"source_sessions"`
	Evidence           []experience.EvidenceRef      `json:"evidence"`
	Outcomes           []trajectory.Outcome          `json:"outcomes"`
	Provenance         CapsuleProvenance             `json:"provenance"`
	Evaluation         CapsuleEvaluationContract     `json:"evaluation"`
	Replay             *CapsuleReplayContract        `json:"replay,omitempty"`
	CreatedAt          time.Time                     `json:"created_at"`
}

type CapsuleProvenance struct {
	SemanticHarness      experience.Harness `json:"semantic_harness"`
	SemanticModel        string             `json:"semantic_model"`
	SemanticPrompt       string             `json:"semantic_prompt_version"`
	SemanticInputHash    string             `json:"semantic_input_hash"`
	EvidenceGeneration   string             `json:"evidence_generation"`
	ProposedContentHash  string             `json:"proposed_content_hash"`
	InstructionAuthority string             `json:"instruction_authority"`
}

type CapsuleEvaluationContract struct {
	Question                 string   `json:"question"`
	TaskState                string   `json:"task_state"`
	RequiredBaselines        []string `json:"required_baselines"`
	RequiredMetrics          []string `json:"required_metrics"`
	ScoringAuthority         string   `json:"scoring_authority"`
	LLMJudgeAuthority        string   `json:"llm_judge_authority"`
	NegativeResultsPreserved bool     `json:"negative_results_preserved"`
}

// LoadPrivateEvalCapsule opens an existing disposable store copy, reads one
// current semantic proposal, and creates a non-authoritative evaluation
// artifact. Callers must never point it at a live Belay database because Store
// opening may apply additive migrations.
func LoadPrivateEvalCapsule(
	ctx context.Context,
	databasePath string,
	proposalID string,
) (PrivateEvalCapsule, error) {
	if ctx == nil {
		return PrivateEvalCapsule{}, errors.New(
			"private eval capsule requires context",
		)
	}
	databasePath = strings.TrimSpace(databasePath)
	if databasePath == "" {
		return PrivateEvalCapsule{}, errors.New(
			"private eval capsule requires a disposable database copy",
		)
	}
	info, err := os.Stat(databasePath)
	if err != nil {
		return PrivateEvalCapsule{}, fmt.Errorf(
			"inspect private eval database copy: %w",
			err,
		)
	}
	if !info.Mode().IsRegular() {
		return PrivateEvalCapsule{}, errors.New(
			"private eval database copy must be a regular file",
		)
	}
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return PrivateEvalCapsule{}, fmt.Errorf(
			"resolve private eval database copy: %w",
			err,
		)
	}
	store, err := local.Open(
		absolute,
		local.NewPlatformKeyProvider(absolute),
	)
	if err != nil {
		return PrivateEvalCapsule{}, fmt.Errorf(
			"open private eval database copy: %w",
			err,
		)
	}
	defer store.Close()
	review, err := localapp.GetExperienceCandidateReview(
		ctx,
		store,
		strings.TrimSpace(proposalID),
	)
	if err != nil {
		return PrivateEvalCapsule{}, fmt.Errorf(
			"read private eval proposal: %w",
			err,
		)
	}
	return BuildPrivateEvalCapsule(review, time.Now().UTC().Round(0))
}

func BuildPrivateEvalCapsule(
	review localapp.ExperienceCandidateReview,
	createdAt time.Time,
) (PrivateEvalCapsule, error) {
	if review.SchemaVersion != localapp.ExperienceCandidateReviewSchemaVersion ||
		strings.TrimSpace(review.CandidateID) == "" ||
		strings.TrimSpace(review.ProposalID) == "" ||
		strings.TrimSpace(review.ProjectIdentity) == "" {
		return PrivateEvalCapsule{}, errors.New(
			"private eval capsule requires a valid experience review",
		)
	}
	if review.Family != experience.CandidateCorrection &&
		review.Family != experience.CandidateFailedApproach &&
		review.Family != experience.CandidateSuccessfulProcedure {
		return PrivateEvalCapsule{}, errors.New(
			"private eval capsule requires a correction, failed approach, or successful procedure",
		)
	}
	if review.Authority != experience.AuthorityNone {
		return PrivateEvalCapsule{}, errors.New(
			"private eval treatment candidate must remain non-authoritative",
		)
	}
	if len(review.Evidence) == 0 || len(review.Outcomes) == 0 ||
		strings.TrimSpace(review.SemanticInputHash) == "" ||
		strings.TrimSpace(review.EvidenceGeneration) == "" ||
		strings.TrimSpace(review.ProposedContentHash) == "" {
		return PrivateEvalCapsule{}, errors.New(
			"private eval capsule requires cited evidence, outcomes, and provenance",
		)
	}
	if review.Proposed.Scope.ProjectIdentity != review.ProjectIdentity {
		return PrivateEvalCapsule{}, errors.New(
			"private eval treatment project does not match source evidence",
		)
	}
	if err := review.Proposed.Validate(); err != nil {
		return PrivateEvalCapsule{}, fmt.Errorf(
			"validate private eval treatment candidate: %w",
			err,
		)
	}
	sourceSessions := capsuleSourceSessions(review.Evidence)
	if len(sourceSessions) == 0 {
		return PrivateEvalCapsule{}, errors.New(
			"private eval capsule requires session-bound evidence",
		)
	}
	question := "Does the proposed procedure improve a related task over equivalent baselines?"
	switch review.Family {
	case experience.CandidateCorrection:
		question = "Does applying the cited correction reduce the corrected behavior on a related task over equivalent baselines?"
	case experience.CandidateFailedApproach:
		question = "Does avoiding the cited failed approach and reusing its repair improve a related task over equivalent baselines?"
	}
	capsule := PrivateEvalCapsule{
		SchemaVersion:      PrivateEvalCapsuleSchemaVersion,
		Readiness:          "draft",
		ReadinessGaps:      capsuleReadinessGaps(review),
		CandidateID:        review.CandidateID,
		ProposalID:         review.ProposalID,
		ProjectIdentity:    review.ProjectIdentity,
		CandidateFamily:    review.Family,
		ObservedBehavior:   review.ObservedBehavior,
		UserFeedback:       review.UserFeedback,
		TreatmentCandidate: review.Proposed,
		SourceSessions:     sourceSessions,
		Evidence:           append([]experience.EvidenceRef(nil), review.Evidence...),
		Outcomes:           append([]trajectory.Outcome(nil), review.Outcomes...),
		Provenance: CapsuleProvenance{
			SemanticHarness:      review.SemanticProvenance.Harness,
			SemanticModel:        review.SemanticProvenance.Model,
			SemanticPrompt:       review.SemanticProvenance.PromptVersion,
			SemanticInputHash:    review.SemanticInputHash,
			EvidenceGeneration:   review.EvidenceGeneration,
			ProposedContentHash:  review.ProposedContentHash,
			InstructionAuthority: string(experience.AuthorityNone),
		},
		Evaluation: CapsuleEvaluationContract{
			Question:  question,
			TaskState: "reconstruction_required",
			RequiredBaselines: []string{
				"no_memory",
				"static_project_instruction",
				"retrieved_source_experience",
				"candidate_treatment",
			},
			RequiredMetrics: []string{
				"task_success",
				"failed_attempts",
				"user_corrections",
				"latency_ms",
				"tokens",
				"cost_usd",
			},
			ScoringAuthority:         "deterministic_observation",
			LLMJudgeAuthority:        "none",
			NegativeResultsPreserved: true,
		},
		CreatedAt: createdAt.UTC().Round(0),
	}
	capsule.CapsuleID = privateEvalCapsuleID(capsule)
	return capsule, nil
}

func capsuleReadinessGaps(
	review localapp.ExperienceCandidateReview,
) []string {
	gaps := []string{
		"source_repository_revision_missing",
		"task_setup_missing",
		"expected_task_outcome_missing",
	}
	if review.EvidenceTruncated {
		gaps = append(gaps, "source_evidence_truncated")
	}
	if review.Proposed.Verifier.Kind == experience.VerifierObservationOnly {
		gaps = append(gaps, "deterministic_treatment_outcome_missing")
	}
	sort.Strings(gaps)
	return gaps
}

func capsuleSourceSessions(refs []experience.EvidenceRef) []string {
	seen := make(map[string]bool)
	for _, ref := range refs {
		if session := strings.TrimSpace(ref.SessionKey); session != "" {
			seen[session] = true
		}
	}
	result := make([]string, 0, len(seen))
	for session := range seen {
		result = append(result, session)
	}
	sort.Strings(result)
	return result
}

func privateEvalCapsuleID(value PrivateEvalCapsule) string {
	material := struct {
		SchemaVersion       string
		CandidateID         string
		ProposalID          string
		ProjectIdentity     string
		SemanticInputHash   string
		EvidenceGeneration  string
		ProposedContentHash string
		ReplayHash          string
	}{
		SchemaVersion:       value.SchemaVersion,
		CandidateID:         value.CandidateID,
		ProposalID:          value.ProposalID,
		ProjectIdentity:     value.ProjectIdentity,
		SemanticInputHash:   value.Provenance.SemanticInputHash,
		EvidenceGeneration:  value.Provenance.EvidenceGeneration,
		ProposedContentHash: value.Provenance.ProposedContentHash,
	}
	if value.Replay != nil {
		replay, _ := json.Marshal(value.Replay)
		sum := sha256.Sum256(replay)
		material.ReplayHash = hex.EncodeToString(sum[:])
	}
	encoded, _ := json.Marshal(material)
	sum := sha256.Sum256(encoded)
	return "evc_" + hex.EncodeToString(sum[:16])
}
