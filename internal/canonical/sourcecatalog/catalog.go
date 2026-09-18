package sourcecatalog

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	CatalogVersion = "belay.attention-families.v1"

	GuardrailsConfigurationMappingKey = "attention.agent_guardrails_configuration"
	GuardrailsSourceSignalCode        = "tamper.guardrails_off"

	GuardrailsDisplayTitle         = "Fewer approval prompts enabled"
	GuardrailsObservationStatement = "Belay recorded a setting that lets actions already permitted " +
		"by the agent run without asking for approval each time."
	GuardrailsCaveat = "This setting may be intentional. The record does not show whether an action " +
		"bypassed a prompt or caused harm."
	GuardrailsNextEvidenceAction = "review_agent_permissions"

	GuardrailsEvidenceTitle  = "Fewer approval prompts enabled"
	GuardrailsEvidenceDetail = "This cited event recorded the session starting in or switching to a " +
		"permission mode that asks for fewer approvals. Belay does not retain the configuration value or body."

	UnsupportedImportedDisplayTitle = "Imported finding—not yet explained by Belay"
	UnsupportedImportedObservation  = "Belay retained this imported finding but does not yet have a reviewed explanation."
	UnsupportedImportedCaveat       = "Review the cited evidence; Belay does not infer its impact or recommend a change."
)

type Mapping struct {
	Key                  string
	Version              string
	GroupingVersion      string
	Origin               string
	TitleCode            string
	Category             string
	SourceSignalCode     string
	RawRuleVersions      []string
	FingerprintVersions  []string
	AttentionKind        string
	AttentionSeverity    string
	DefaultVisible       bool
	DisplayTitle         string
	ObservationStatement string
	Caveat               string
	NextEvidenceAction   string
}

var guardrailsConfiguration = Mapping{
	Key:                  GuardrailsConfigurationMappingKey,
	Version:              "1",
	GroupingVersion:      "1",
	Origin:               "numbat",
	TitleCode:            "issue.numbat_finding",
	Category:             "numbat_finding",
	SourceSignalCode:     GuardrailsSourceSignalCode,
	RawRuleVersions:      []string{"1.1"},
	FingerprintVersions:  []string{"1"},
	AttentionKind:        model.AttentionKindIssue,
	AttentionSeverity:    "low",
	DefaultVisible:       true,
	DisplayTitle:         GuardrailsDisplayTitle,
	ObservationStatement: GuardrailsObservationStatement,
	Caveat:               GuardrailsCaveat,
	NextEvidenceAction:   GuardrailsNextEvidenceAction,
}

func Mappings() []Mapping {
	return []Mapping{cloneMapping(guardrailsConfiguration)}
}

func MappingByKey(key, version, groupingVersion string) (Mapping, bool) {
	for _, mapping := range Mappings() {
		if mapping.Key == key &&
			mapping.Version == version &&
			mapping.GroupingVersion == groupingVersion {
			return mapping, true
		}
	}
	return Mapping{}, false
}

func MatchSummary(summary model.IssueSummary) (Mapping, bool) {
	for _, mapping := range Mappings() {
		if mapping.DefaultVisible &&
			summary.Origin == mapping.Origin &&
			summary.TitleCode == mapping.TitleCode &&
			summary.Category == mapping.Category &&
			summary.SourceSignalCode != nil &&
			*summary.SourceSignalCode == mapping.SourceSignalCode &&
			contains(mapping.FingerprintVersions, summary.FingerprintVersion) {
			return mapping, true
		}
	}
	return Mapping{}, false
}

func (mapping Mapping) AcceptsRawRuleVersion(version string) bool {
	return contains(mapping.RawRuleVersions, version)
}

func OpaqueRuleVersion(version string) string {
	sum := sha256.Sum256([]byte(version))
	return "rule-" + hex.EncodeToString(sum[:6])
}

func cloneMapping(mapping Mapping) Mapping {
	mapping.RawRuleVersions = append([]string(nil), mapping.RawRuleVersions...)
	mapping.FingerprintVersions = append([]string(nil), mapping.FingerprintVersions...)
	return mapping
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
