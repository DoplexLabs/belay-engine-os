package experience

import (
	"errors"
	"fmt"
	"sort"
)

const (
	maxConflictExperienceRefs = 64
	maxConflictReasonCodes    = 16
)

type ConflictState string

const (
	ConflictNoConflict            ConflictState = "no_conflict"
	ConflictPossibleOverlap       ConflictState = "possible_overlap"
	ConflictPossibleContradiction ConflictState = "possible_contradiction"
)

func (value ConflictState) Valid() bool {
	switch value {
	case ConflictNoConflict,
		ConflictPossibleOverlap,
		ConflictPossibleContradiction:
		return true
	default:
		return false
	}
}

type ConflictReasonCode string

const (
	ConflictReasonNoActiveExperiences       ConflictReasonCode = "no_active_experiences"
	ConflictReasonDifferentProject          ConflictReasonCode = "different_project"
	ConflictReasonDisjointSession           ConflictReasonCode = "disjoint_session"
	ConflictReasonDisjointHarnesses         ConflictReasonCode = "disjoint_harnesses"
	ConflictReasonDisjointModels            ConflictReasonCode = "disjoint_models"
	ConflictReasonDisjointTaskFamilies      ConflictReasonCode = "disjoint_task_families"
	ConflictReasonOverlappingScope          ConflictReasonCode = "overlapping_scope"
	ConflictReasonFileVerifierOpposition    ConflictReasonCode = "file_verifier_opposition"
	ConflictReasonPatternVerifierOpposition ConflictReasonCode = "path_pattern_verifier_opposition"
	ConflictReasonExperienceRefsTruncated   ConflictReasonCode = "experience_refs_truncated"
	ConflictReasonActiveQueryCapReached     ConflictReasonCode = "active_experience_query_cap_reached"
)

func (value ConflictReasonCode) Valid() bool {
	switch value {
	case ConflictReasonNoActiveExperiences,
		ConflictReasonDifferentProject,
		ConflictReasonDisjointSession,
		ConflictReasonDisjointHarnesses,
		ConflictReasonDisjointModels,
		ConflictReasonDisjointTaskFamilies,
		ConflictReasonOverlappingScope,
		ConflictReasonFileVerifierOpposition,
		ConflictReasonPatternVerifierOpposition,
		ConflictReasonExperienceRefsTruncated,
		ConflictReasonActiveQueryCapReached:
		return true
	default:
		return false
	}
}

// ConflictAdvisory is a bounded, read-only preflight result. It carries no
// authority and cannot change candidate or experience lifecycle state.
type ConflictAdvisory struct {
	ProposalID                string               `json:"proposal_id"`
	State                     ConflictState        `json:"state"`
	ConflictingExperienceRefs []ExperienceRef      `json:"conflicting_experience_refs"`
	ReasonCodes               []ConflictReasonCode `json:"reason_codes"`
}

func (value ConflictAdvisory) Validate() error {
	if err := validateIdentifier("conflict proposal ID", value.ProposalID); err != nil {
		return err
	}
	if !value.State.Valid() {
		return errors.New("conflict state is invalid")
	}
	if len(value.ConflictingExperienceRefs) > maxConflictExperienceRefs {
		return errors.New("conflicting experience references exceed item limit")
	}
	if len(value.ReasonCodes) == 0 ||
		len(value.ReasonCodes) > maxConflictReasonCodes {
		return errors.New("conflict reason codes are required and bounded")
	}
	if value.State == ConflictNoConflict &&
		len(value.ConflictingExperienceRefs) != 0 {
		return errors.New("no-conflict advisory cannot cite conflicting experiences")
	}
	hasQueryCapReason := false
	seenRefs := make(map[ExperienceRef]bool, len(value.ConflictingExperienceRefs))
	for _, ref := range value.ConflictingExperienceRefs {
		if err := ref.Validate(); err != nil {
			return err
		}
		if seenRefs[ref] {
			return errors.New("conflicting experience references must be unique")
		}
		seenRefs[ref] = true
	}
	seenReasons := make(map[ConflictReasonCode]bool, len(value.ReasonCodes))
	for _, reason := range value.ReasonCodes {
		if !reason.Valid() {
			return errors.New("conflict reason code is invalid")
		}
		if seenReasons[reason] {
			return errors.New("conflict reason codes must be unique")
		}
		seenReasons[reason] = true
		if reason == ConflictReasonActiveQueryCapReached {
			hasQueryCapReason = true
		}
	}
	if value.State == ConflictNoConflict && hasQueryCapReason {
		return errors.New("query-capped advisory cannot claim no conflict")
	}
	if value.State == ConflictPossibleOverlap &&
		len(value.ConflictingExperienceRefs) == 0 &&
		!hasQueryCapReason {
		return errors.New(
			"reference-free possible overlap requires query-cap uncertainty",
		)
	}
	if value.State == ConflictPossibleContradiction &&
		len(value.ConflictingExperienceRefs) == 0 {
		return errors.New(
			"possible contradiction requires a conflicting experience reference",
		)
	}
	return nil
}

// PreflightSemanticProposalConflict compares an immutable, inactive semantic
// proposal with experiences that the caller has already established are
// currently active. It does not infer from guidance text, experience type, or
// confidence.
func PreflightSemanticProposalConflict(
	proposal SemanticProposal,
	activeExperiences []Experience,
) (ConflictAdvisory, error) {
	if err := proposal.Validate(); err != nil {
		return ConflictAdvisory{}, fmt.Errorf(
			"validate semantic proposal for conflict preflight: %w",
			err,
		)
	}

	result := ConflictAdvisory{
		ProposalID:                proposal.ProposalID,
		State:                     ConflictNoConflict,
		ConflictingExperienceRefs: make([]ExperienceRef, 0),
		ReasonCodes:               make([]ConflictReasonCode, 0),
	}
	if len(activeExperiences) == 0 {
		result.ReasonCodes = append(
			result.ReasonCodes,
			ConflictReasonNoActiveExperiences,
		)
		return result, result.Validate()
	}

	disjointReasons := make(map[ConflictReasonCode]bool)
	conflictReasons := make(map[ConflictReasonCode]bool)
	refs := make(map[ExperienceRef]bool)
	contradiction := false
	for _, active := range activeExperiences {
		if err := active.Validate(); err != nil {
			return ConflictAdvisory{}, fmt.Errorf(
				"validate active experience for conflict preflight: %w",
				err,
			)
		}
		disjoint, reason := scopesProvenDisjoint(
			proposal.Proposal.Scope,
			active.Scope,
		)
		if disjoint {
			disjointReasons[reason] = true
			continue
		}

		ref := ExperienceRef{
			ExperienceID: active.ExperienceID,
			Version:      active.Version,
		}
		refs[ref] = true
		reason, opposed := verifierOpposition(
			proposal.Proposal.Verifier,
			active.Verifier,
		)
		if opposed {
			contradiction = true
			conflictReasons[reason] = true
			continue
		}
		conflictReasons[ConflictReasonOverlappingScope] = true
	}

	if len(refs) == 0 {
		result.ReasonCodes = sortedConflictReasons(disjointReasons)
		return result, result.Validate()
	}
	if contradiction {
		result.State = ConflictPossibleContradiction
	} else {
		result.State = ConflictPossibleOverlap
	}
	result.ConflictingExperienceRefs = sortedConflictRefs(refs)
	if len(result.ConflictingExperienceRefs) > maxConflictExperienceRefs {
		result.ConflictingExperienceRefs =
			result.ConflictingExperienceRefs[:maxConflictExperienceRefs]
		conflictReasons[ConflictReasonExperienceRefsTruncated] = true
	}
	result.ReasonCodes = sortedConflictReasons(conflictReasons)
	return result, result.Validate()
}

// MarkConflictActiveQueryCapReached preserves known conflicts while making an
// incomplete active set explicit. A no-conflict result becomes an uncertainty
// overlap and cannot be mistaken for a complete preflight.
func MarkConflictActiveQueryCapReached(
	value ConflictAdvisory,
) (ConflictAdvisory, error) {
	if err := value.Validate(); err != nil {
		return ConflictAdvisory{}, err
	}
	if value.State == ConflictNoConflict {
		value.State = ConflictPossibleOverlap
	}
	found := false
	for _, reason := range value.ReasonCodes {
		if reason == ConflictReasonActiveQueryCapReached {
			found = true
			break
		}
	}
	if !found {
		value.ReasonCodes = append(
			value.ReasonCodes,
			ConflictReasonActiveQueryCapReached,
		)
		sort.Slice(value.ReasonCodes, func(i, j int) bool {
			return value.ReasonCodes[i] < value.ReasonCodes[j]
		})
	}
	return value, value.Validate()
}

func scopesProvenDisjoint(
	left Scope,
	right Scope,
) (bool, ConflictReasonCode) {
	if left.ProjectIdentity != right.ProjectIdentity {
		return true, ConflictReasonDifferentProject
	}
	if left.Kind == ScopeSession &&
		right.Kind == ScopeSession &&
		left.SessionKey != right.SessionKey {
		return true, ConflictReasonDisjointSession
	}
	if exactSetsDisjoint(left.Harnesses, right.Harnesses) {
		return true, ConflictReasonDisjointHarnesses
	}
	if exactSetsDisjoint(left.Models, right.Models) {
		return true, ConflictReasonDisjointModels
	}
	if exactSetsDisjoint(left.TaskFamilies, right.TaskFamilies) {
		return true, ConflictReasonDisjointTaskFamilies
	}
	return false, ""
}

func exactSetsDisjoint[T comparable](left []T, right []T) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	values := make(map[T]bool, len(left))
	for _, value := range left {
		values[value] = true
	}
	for _, value := range right {
		if values[value] {
			return false
		}
	}
	return true
}

func verifierOpposition(
	left Verifier,
	right Verifier,
) (ConflictReasonCode, bool) {
	if left.Kind == VerifierFileModified &&
		right.Kind == VerifierFileNotModified &&
		left.File.Path == right.File.Path {
		return ConflictReasonFileVerifierOpposition, true
	}
	if right.Kind == VerifierFileModified &&
		left.Kind == VerifierFileNotModified &&
		right.File.Path == left.File.Path {
		return ConflictReasonFileVerifierOpposition, true
	}
	if left.Kind == VerifierFileModified &&
		right.Kind == VerifierPathPatternNotModified &&
		fileMatchesAnyRelativePathPattern(
			left.File.Path,
			right.PathPattern.Patterns,
		) {
		return ConflictReasonPatternVerifierOpposition, true
	}
	if right.Kind == VerifierFileModified &&
		left.Kind == VerifierPathPatternNotModified &&
		fileMatchesAnyRelativePathPattern(
			right.File.Path,
			left.PathPattern.Patterns,
		) {
		return ConflictReasonPatternVerifierOpposition, true
	}
	return "", false
}

func sortedConflictRefs(values map[ExperienceRef]bool) []ExperienceRef {
	result := make([]ExperienceRef, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ExperienceID != result[j].ExperienceID {
			return result[i].ExperienceID < result[j].ExperienceID
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func sortedConflictReasons(
	values map[ConflictReasonCode]bool,
) []ConflictReasonCode {
	result := make([]ConflictReasonCode, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	return result
}
