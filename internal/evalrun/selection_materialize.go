package evalrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const SelectionMaterializationSchemaVersion = "belay.selection-materialization.v1"

type SelectionMaterializationResult struct {
	SchemaVersion string                           `json:"schema_version"`
	PlanID        string                           `json:"plan_id"`
	StoreKind     string                           `json:"store_kind"`
	Generation    local.ExperienceGeneration       `json:"generation"`
	Targets       []SelectionMaterializationTarget `json:"targets"`
	Summary       SelectionMaterializationSummary  `json:"summary"`
	CompletedAt   time.Time                        `json:"completed_at"`
	DatabasePath  string                           `json:"-"`
}

type SelectionMaterializationTarget struct {
	TargetID             string                              `json:"target_id"`
	SelectionRequest     localapp.ExperienceSelectionRequest `json:"selection_request"`
	RelevantCandidateIDs []string                            `json:"relevant_candidate_ids"`
	Selected             []SelectionMaterializedExperience   `json:"selected"`
	EstimatedTokens      int                                 `json:"estimated_tokens"`
	RelevantSelected     int                                 `json:"relevant_selected"`
	Precision            float64                             `json:"precision"`
	Recall               float64                             `json:"recall"`
	Abstained            bool                                `json:"abstained"`
}

type SelectionMaterializedExperience struct {
	CandidateID     string                   `json:"candidate_id"`
	Experience      experience.ExperienceRef `json:"experience"`
	Score           int                      `json:"score"`
	EstimatedTokens int                      `json:"estimated_tokens"`
}

type SelectionMaterializationSummary struct {
	Targets                      int     `json:"targets"`
	TargetsWithRelevantSelection int     `json:"targets_with_relevant_selection"`
	TargetsAbstained             int     `json:"targets_abstained"`
	RelevantSelections           int     `json:"relevant_selections"`
	TotalSelections              int     `json:"total_selections"`
	TotalRelevantCandidates      int     `json:"total_relevant_candidates"`
	MicroPrecision               float64 `json:"micro_precision"`
	MicroRecall                  float64 `json:"micro_recall"`
	GateDecision                 string  `json:"gate_decision"`
}

func MaterializeSelectionValuePlan(
	ctx context.Context,
	root string,
	plan SelectionValuePlan,
) (SelectionMaterializationResult, error) {
	if ctx == nil {
		return SelectionMaterializationResult{}, errors.New(
			"selection materialization requires context",
		)
	}
	if err := plan.Validate(); err != nil {
		return SelectionMaterializationResult{}, fmt.Errorf(
			"validate selection value plan: %w",
			err,
		)
	}
	if strings.TrimSpace(plan.PlanID) == "" {
		return SelectionMaterializationResult{}, errors.New(
			"selection materialization requires a bound plan identity",
		)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return SelectionMaterializationResult{}, errors.New(
			"selection materialization requires a disposable root",
		)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return SelectionMaterializationResult{}, fmt.Errorf(
			"resolve selection materialization root: %w",
			err,
		)
	}
	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return SelectionMaterializationResult{}, fmt.Errorf(
			"create selection materialization root: %w",
			err,
		)
	}
	databasePath := filepath.Join(absoluteRoot, "selection-value.sqlite")
	if _, err := os.Stat(databasePath); err == nil {
		return SelectionMaterializationResult{}, errors.New(
			"selection materialization refuses an existing database",
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return SelectionMaterializationResult{}, fmt.Errorf(
			"inspect selection materialization database: %w",
			err,
		)
	}
	now := time.Now().UTC().Round(0)
	store, err := local.OpenWithOptions(
		databasePath,
		local.OpenOptions{
			KeyProvider: &selectionMemoryKeyProvider{},
			Clock:       func() time.Time { return now },
		},
	)
	if err != nil {
		return SelectionMaterializationResult{}, fmt.Errorf(
			"open encrypted selection materialization store: %w",
			err,
		)
	}
	defer store.Close()

	refCandidates := make(
		map[experience.ExperienceRef]string,
		len(plan.ExperiencePool),
	)
	for _, planned := range plan.ExperiencePool {
		candidate, value, err := selectionMaterializedExperience(
			plan,
			planned,
			now,
		)
		if err != nil {
			return SelectionMaterializationResult{}, err
		}
		if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
			return SelectionMaterializationResult{}, fmt.Errorf(
				"persist selection candidate %s: %w",
				planned.CandidateID,
				err,
			)
		}
		if _, err := store.InsertExperience(ctx, value); err != nil {
			return SelectionMaterializationResult{}, fmt.Errorf(
				"persist selection experience %s: %w",
				planned.CandidateID,
				err,
			)
		}
		ref := experience.ExperienceRef{
			ExperienceID: value.ExperienceID,
			Version:      value.Version,
		}
		refCandidates[ref] = planned.CandidateID
	}

	compiler, err := localapp.NewExperienceCompilerService(
		store,
		localapp.WithExperienceCompilerClock(func() time.Time { return now }),
	)
	if err != nil {
		return SelectionMaterializationResult{}, err
	}
	compiled, err := compiler.Compile(ctx, plan.ProjectIdentity)
	if err != nil {
		return SelectionMaterializationResult{}, fmt.Errorf(
			"compile selection experience generation: %w",
			err,
		)
	}
	if len(compiled.Generation.ExperienceRefs) != len(plan.ExperiencePool) {
		return SelectionMaterializationResult{}, errors.New(
			"compiled selection generation omitted planned candidates",
		)
	}

	result := SelectionMaterializationResult{
		SchemaVersion: SelectionMaterializationSchemaVersion,
		PlanID:        plan.PlanID,
		StoreKind:     "disposable_encrypted_sqlite",
		Generation:    compiled.Generation,
		Targets: make(
			[]SelectionMaterializationTarget,
			0,
			len(plan.Targets),
		),
		DatabasePath: databasePath,
	}
	for _, target := range plan.Targets {
		selection, err := compiler.Select(ctx, target.SelectionRequest)
		if err != nil {
			return SelectionMaterializationResult{}, fmt.Errorf(
				"select experiences for target %s: %w",
				target.TargetID,
				err,
			)
		}
		targetResult := SelectionMaterializationTarget{
			TargetID:             target.TargetID,
			SelectionRequest:     target.SelectionRequest,
			RelevantCandidateIDs: append([]string(nil), target.RelevantCandidateIDs...),
			Selected: make(
				[]SelectionMaterializedExperience,
				0,
				len(selection.Selected),
			),
			EstimatedTokens: selection.EstimatedTokens,
			Abstained:       len(selection.Selected) == 0,
		}
		relevant := make(map[string]bool, len(target.RelevantCandidateIDs))
		for _, candidateID := range target.RelevantCandidateIDs {
			relevant[candidateID] = true
		}
		for _, selected := range selection.Selected {
			candidateID, found := refCandidates[selected.Ref]
			if !found {
				return SelectionMaterializationResult{}, errors.New(
					"production selector returned an experience outside the materialized pool",
				)
			}
			targetResult.Selected = append(
				targetResult.Selected,
				SelectionMaterializedExperience{
					CandidateID:     candidateID,
					Experience:      selected.Ref,
					Score:           selected.Score,
					EstimatedTokens: selected.EstimatedTokens,
				},
			)
			if relevant[candidateID] {
				targetResult.RelevantSelected++
			}
		}
		if len(targetResult.Selected) > 0 {
			targetResult.Precision = float64(
				targetResult.RelevantSelected,
			) / float64(len(targetResult.Selected))
		}
		targetResult.Recall = float64(
			targetResult.RelevantSelected,
		) / float64(len(target.RelevantCandidateIDs))
		result.Targets = append(result.Targets, targetResult)
	}
	result.Summary = summarizeSelectionMaterialization(result.Targets)
	result.CompletedAt = now
	return result, nil
}

func selectionMaterializedExperience(
	plan SelectionValuePlan,
	planned SelectionValueCandidate,
	now time.Time,
) (experience.Candidate, experience.Experience, error) {
	turn := int64(0)
	evidence := experience.EvidenceSet{
		Availability: experience.EvidenceRetainedSnapshot,
		Refs: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "ses_selection_value_fixture",
			TurnIndex:  &turn,
			Excerpt: "Evaluation fixture for plan " + plan.PlanID +
				" candidate " + planned.CandidateID,
			OccurredAt: &now,
		}},
	}
	evidence.EvidenceSetID = evidence.DeterministicID()
	inputHash, err := stableSelectionValueHash(planned)
	if err != nil {
		return experience.Candidate{}, experience.Experience{}, err
	}
	candidate := experience.Candidate{
		SchemaVersion:    experience.CandidateSchemaVersion,
		Family:           planned.Family,
		ProjectIdentity:  plan.ProjectIdentity,
		ObservedBehavior: "Selection-value evaluation candidate materialized from a bound plan.",
		Evidence:         evidence,
		Proposal:         planned.Proposal,
		Provenance: experience.Provenance{
			ExtractorVersion: "belay.selection-value.materializer.v1",
			Harness:          experience.HarnessCodex,
			Model:            "selection-eval",
			PromptVersion:    SelectionValuePlanSchemaVersion,
			InputHash:        inputHash,
			GeneratedAt:      now,
		},
		Authority:      experience.AuthorityNone,
		LifecycleState: experience.LifecycleCandidate,
		CreatedAt:      now,
	}
	candidate.CandidateID = candidate.DeterministicID()
	value := experience.Experience{
		SchemaVersion:     experience.ExperienceSchemaVersion,
		OriginCandidateID: candidate.CandidateID,
		Version:           1,
		Type:              candidate.Proposal.Type,
		Scope:             candidate.Proposal.Scope,
		Applicability:     candidate.Proposal.Applicability,
		Guidance:          candidate.Proposal.Guidance,
		Verifier:          candidate.Proposal.Verifier,
		Evidence:          candidate.Evidence,
		Provenance: experience.Provenance{
			ExtractorVersion:  "belay.selection-value.materializer.v1",
			InputHash:         inputHash,
			SourceCandidateID: candidate.CandidateID,
			GeneratedAt:       now,
		},
		Governance: experience.Governance{
			LifecycleState: experience.LifecycleActive,
			Authority:      experience.AuthorityUserApproved,
		},
		CreatedAt: now,
	}
	value.ExperienceID = experience.DeriveExperienceID(
		value.Scope.ProjectIdentity,
		value.OriginCandidateID,
	)
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval = &experience.ApprovalProvenance{
		ApprovedBy:          "belay_selection_evaluation",
		ApprovedAt:          now,
		Mode:                experience.ApprovalAsProposed,
		CandidateID:         candidate.CandidateID,
		ProposedContentHash: value.ContentHash,
		ApprovedContentHash: value.ContentHash,
	}
	if err := candidate.Validate(); err != nil {
		return experience.Candidate{}, experience.Experience{}, fmt.Errorf(
			"validate materialized candidate %s: %w",
			planned.CandidateID,
			err,
		)
	}
	if err := value.Validate(); err != nil {
		return experience.Candidate{}, experience.Experience{}, fmt.Errorf(
			"validate materialized experience %s: %w",
			planned.CandidateID,
			err,
		)
	}
	return candidate, value, nil
}

func summarizeSelectionMaterialization(
	targets []SelectionMaterializationTarget,
) SelectionMaterializationSummary {
	result := SelectionMaterializationSummary{
		Targets: len(targets),
	}
	for _, target := range targets {
		if target.RelevantSelected > 0 {
			result.TargetsWithRelevantSelection++
		}
		if target.Abstained {
			result.TargetsAbstained++
		}
		result.RelevantSelections += target.RelevantSelected
		result.TotalSelections += len(target.Selected)
		result.TotalRelevantCandidates += len(target.RelevantCandidateIDs)
	}
	if result.TotalSelections > 0 {
		result.MicroPrecision = float64(
			result.RelevantSelections,
		) / float64(result.TotalSelections)
	}
	if result.TotalRelevantCandidates > 0 {
		result.MicroRecall = float64(
			result.RelevantSelections,
		) / float64(result.TotalRelevantCandidates)
	}
	result.GateDecision = "selection_not_proven"
	if result.Targets > 0 &&
		result.TargetsWithRelevantSelection == result.Targets &&
		result.MicroPrecision == 1 &&
		result.MicroRecall == 1 {
		result.GateDecision = "ready_for_harness_baselines"
	}
	return result
}

type selectionMemoryKeyProvider struct {
	key     []byte
	storeID string
}

func (provider *selectionMemoryKeyProvider) Load(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	if len(provider.key) != 32 || provider.storeID != storeID {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *selectionMemoryKeyProvider) Create(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	if len(provider.key) != 0 {
		return nil, local.ErrKeyAlreadyExists
	}
	provider.key = bytes.Repeat([]byte{0x53}, 32)
	provider.storeID = storeID
	return append([]byte(nil), provider.key...), nil
}
