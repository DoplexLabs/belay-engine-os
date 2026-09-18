package trajectory

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxIdentifierBytes = 256
	maxSourceRefs      = 128
)

func (value NodeRef) Validate() error {
	if !value.Kind.Valid() {
		return errors.New("trajectory node kind is invalid")
	}
	switch value.Kind {
	case NodeCanonicalEvent:
		if err := validateIdentifier("canonical event node ID", value.EventID); err != nil {
			return err
		}
		if value.SessionKey != "" || value.TurnIndex != nil || value.OutcomeID != "" ||
			value.ExperienceID != "" || value.ExperienceVersion != 0 || value.ApplicationID != "" || value.GitObject != "" {
			return errors.New("canonical event node contains another node kind's fields")
		}
	case NodeTranscriptTurn:
		if err := validateIdentifier("transcript node session key", value.SessionKey); err != nil {
			return err
		}
		if value.TurnIndex == nil || *value.TurnIndex < 0 {
			return errors.New("transcript node requires a non-negative turn index")
		}
		if value.EventID != "" || value.OutcomeID != "" || value.ExperienceID != "" ||
			value.ExperienceVersion != 0 || value.ApplicationID != "" || value.GitObject != "" {
			return errors.New("transcript node contains another node kind's fields")
		}
	case NodeOutcome:
		if err := validateIdentifier("outcome node ID", value.OutcomeID); err != nil {
			return err
		}
		if value.EventID != "" || value.SessionKey != "" || value.TurnIndex != nil || value.ExperienceID != "" ||
			value.ExperienceVersion != 0 || value.ApplicationID != "" || value.GitObject != "" {
			return errors.New("outcome node contains another node kind's fields")
		}
	case NodeExperience:
		if err := validateIdentifier("experience node ID", value.ExperienceID); err != nil {
			return err
		}
		if value.ExperienceVersion < 1 {
			return errors.New("experience node requires a positive version")
		}
		if value.EventID != "" || value.SessionKey != "" || value.TurnIndex != nil || value.OutcomeID != "" ||
			value.ApplicationID != "" || value.GitObject != "" {
			return errors.New("experience node contains another node kind's fields")
		}
	case NodeApplication:
		if err := validateIdentifier("application node ID", value.ApplicationID); err != nil {
			return err
		}
		if value.EventID != "" || value.SessionKey != "" || value.TurnIndex != nil || value.OutcomeID != "" ||
			value.ExperienceID != "" || value.ExperienceVersion != 0 || value.GitObject != "" {
			return errors.New("application node contains another node kind's fields")
		}
	case NodeGitObject:
		if !validGitObject(value.GitObject) {
			return errors.New("git object node requires a 40- or 64-character lowercase hex object ID")
		}
		if value.EventID != "" || value.SessionKey != "" || value.TurnIndex != nil || value.OutcomeID != "" ||
			value.ExperienceID != "" || value.ExperienceVersion != 0 || value.ApplicationID != "" {
			return errors.New("git object node contains another node kind's fields")
		}
	}
	return nil
}

func (value Edge) Validate() error {
	if value.SchemaVersion != EdgeSchemaVersion {
		return errors.New("trajectory edge schema version is invalid")
	}
	if err := validateIdentifier("edge project identity", value.ProjectIdentity); err != nil {
		return err
	}
	if err := validateIdentifier("edge session key", value.SessionKey); err != nil {
		return err
	}
	if err := value.From.Validate(); err != nil {
		return fmt.Errorf("edge from reference: %w", err)
	}
	if err := value.To.Validate(); err != nil {
		return fmt.Errorf("edge to reference: %w", err)
	}
	if !value.Relation.Valid() {
		return errors.New("trajectory edge relation is invalid")
	}
	if !value.EvidenceClass.Valid() {
		return errors.New("trajectory edge evidence class is invalid")
	}
	if !value.Confidence.Valid() {
		return errors.New("trajectory edge confidence is invalid")
	}
	if err := validateIdentifier("edge derivation version", value.DerivationVersion); err != nil {
		return err
	}
	if value.OccurredAt.IsZero() {
		return errors.New("trajectory edge occurred_at is required")
	}
	if len(value.SourceRefs) == 0 || len(value.SourceRefs) > maxSourceRefs {
		return errors.New("trajectory edge source references are required and bounded")
	}
	for _, ref := range value.SourceRefs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("edge source reference: %w", err)
		}
	}
	if value.EdgeID == "" || value.EdgeID != value.DeterministicID() {
		return errors.New("trajectory edge ID does not match deterministic content")
	}
	return nil
}

func (value Outcome) Validate() error {
	if value.SchemaVersion != OutcomeSchemaVersion {
		return errors.New("outcome schema version is invalid")
	}
	if err := validateIdentifier("outcome project identity", value.ProjectIdentity); err != nil {
		return err
	}
	if err := validateIdentifier("outcome session key", value.SessionKey); err != nil {
		return err
	}
	if value.OccurredAt.IsZero() {
		return errors.New("outcome occurred_at is required")
	}
	if !value.Kind.Valid() || !value.Result.Valid() {
		return errors.New("outcome kind or result is invalid")
	}
	if value.EvidenceClass != EvidenceObserved && value.EvidenceClass != EvidenceDeterministicInference {
		return errors.New("outcome evidence must be observed or deterministic inference")
	}
	if !value.Confidence.Valid() {
		return errors.New("outcome confidence is invalid")
	}
	if len(value.SourceRefs) == 0 || len(value.SourceRefs) > maxSourceRefs {
		return errors.New("outcome source references are required and bounded")
	}
	for _, ref := range value.SourceRefs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("outcome source reference: %w", err)
		}
	}
	if err := validateIdentifier("outcome derivation version", value.DerivationVersion); err != nil {
		return err
	}
	if value.OutcomeID == "" || value.OutcomeID != value.DeterministicID() {
		return errors.New("outcome ID does not match deterministic content")
	}
	return nil
}

func ValidateDeterministicVerifierInputs(edges []Edge, outcomes []Outcome) error {
	for _, edge := range edges {
		if err := edge.Validate(); err != nil {
			return err
		}
		if edge.EvidenceClass == EvidenceSemanticHypothesis {
			return errors.New("semantic hypothesis cannot feed a deterministic verifier")
		}
	}
	for _, outcome := range outcomes {
		if err := outcome.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateIdentifier(name, value string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) ||
		len(value) > maxIdentifierBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%s is invalid", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func validGitObject(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
