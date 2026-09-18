package experience

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type VerifierKind string

const (
	VerifierCommandObserved           VerifierKind = "command_observed"
	VerifierCommandSucceeded          VerifierKind = "command_succeeded"
	VerifierFileNotModified           VerifierKind = "file_not_modified"
	VerifierFileModified              VerifierKind = "file_modified"
	VerifierPathPatternNotModified    VerifierKind = "path_pattern_not_modified"
	VerifierVerificationAfterLastEdit VerifierKind = "verification_after_last_edit"
	VerifierNoRepeatFailure           VerifierKind = "no_repeat_failure"
	VerifierUserCorrectionAbsent      VerifierKind = "user_correction_absent"
	VerifierObservationOnly           VerifierKind = "observation_only"
)

func (value VerifierKind) Valid() bool {
	switch value {
	case VerifierCommandObserved,
		VerifierCommandSucceeded,
		VerifierFileNotModified,
		VerifierFileModified,
		VerifierPathPatternNotModified,
		VerifierVerificationAfterLastEdit,
		VerifierNoRepeatFailure,
		VerifierUserCorrectionAbsent,
		VerifierObservationOnly:
		return true
	default:
		return false
	}
}

type CoverageRequirement string

const (
	CoverageTranscriptComplete CoverageRequirement = "transcript_complete"
	CoverageCanonicalComplete  CoverageRequirement = "canonical_events_complete"
	CoverageWorkspaceCaptured  CoverageRequirement = "workspace_state_captured"
	CoverageOutcomesComplete   CoverageRequirement = "outcome_observations_complete"
)

func (value CoverageRequirement) Valid() bool {
	switch value {
	case CoverageTranscriptComplete, CoverageCanonicalComplete, CoverageWorkspaceCaptured, CoverageOutcomesComplete:
		return true
	default:
		return false
	}
}

type Verifier struct {
	Kind                      VerifierKind                   `json:"kind"`
	CoverageRequirements      []CoverageRequirement          `json:"coverage_requirements,omitempty"`
	Command                   *CommandVerifierSpec           `json:"command,omitempty"`
	File                      *FileVerifierSpec              `json:"file,omitempty"`
	PathPattern               *PathPatternVerifierSpec       `json:"path_pattern,omitempty"`
	VerificationAfterLastEdit *VerificationAfterLastEditSpec `json:"verification_after_last_edit,omitempty"`
	NoRepeatFailure           *NoRepeatFailureSpec           `json:"no_repeat_failure,omitempty"`
	UserCorrectionAbsent      *UserCorrectionAbsentSpec      `json:"user_correction_absent,omitempty"`
	ObservationOnly           *ObservationOnlySpec           `json:"observation_only,omitempty"`
}

type CommandVerifierSpec struct {
	Command          string `json:"command"`
	CommandClass     string `json:"command_class,omitempty"`
	ScrubbingVersion string `json:"scrubbing_version"`
}

type FileVerifierSpec struct {
	Path string `json:"path"`
}

type PathPatternVerifierSpec struct {
	Patterns []string `json:"patterns"`
}

type VerificationAfterLastEditSpec struct {
	CommandClasses []string `json:"command_classes,omitempty"`
	RequireSuccess bool     `json:"require_success"`
}

type NoRepeatFailureSpec struct {
	CommandClass      string `json:"command_class"`
	NormalizedPattern string `json:"normalized_pattern"`
	WindowTurns       int    `json:"window_turns"`
}

type UserCorrectionAbsentSpec struct {
	MarkerFamilies []string `json:"marker_families,omitempty"`
}

type ObservationOnlySpec struct {
	Explanation string `json:"explanation"`
}

func (value *Verifier) UnmarshalJSON(data []byte) error {
	type verifierAlias Verifier
	var decoded verifierAlias
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode verifier: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode verifier: trailing JSON value")
		}
		return fmt.Errorf("decode verifier trailing content: %w", err)
	}
	*value = Verifier(decoded)
	return nil
}

func (value Verifier) absenceBased() bool {
	switch value.Kind {
	case VerifierFileNotModified,
		VerifierPathPatternNotModified,
		VerifierNoRepeatFailure,
		VerifierUserCorrectionAbsent:
		return true
	default:
		return false
	}
}
