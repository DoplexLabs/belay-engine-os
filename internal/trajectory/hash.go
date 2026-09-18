package trajectory

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func (value Edge) DeterministicID() string {
	return deterministicID("edg_", struct {
		SchemaVersion     string
		ProjectIdentity   string
		SessionKey        string
		From              NodeRef
		Relation          EdgeRelation
		To                NodeRef
		EvidenceClass     EvidenceClass
		Confidence        Confidence
		DerivationVersion string
		SourceRefs        []NodeRef
		OccurredAt        time.Time
	}{
		SchemaVersion:     EdgeSchemaVersion,
		ProjectIdentity:   strings.TrimSpace(value.ProjectIdentity),
		SessionKey:        strings.TrimSpace(value.SessionKey),
		From:              value.From,
		Relation:          value.Relation,
		To:                value.To,
		EvidenceClass:     value.EvidenceClass,
		Confidence:        value.Confidence,
		DerivationVersion: strings.TrimSpace(value.DerivationVersion),
		SourceRefs:        normalizedNodeRefs(value.SourceRefs),
		OccurredAt:        value.OccurredAt.UTC().Round(0),
	})
}

func (value Outcome) DeterministicID() string {
	return deterministicID("out_", struct {
		SchemaVersion     string
		ProjectIdentity   string
		SessionKey        string
		OccurredAt        time.Time
		Kind              OutcomeKind
		Result            OutcomeResult
		EvidenceClass     EvidenceClass
		Confidence        Confidence
		SourceRefs        []NodeRef
		DerivationVersion string
	}{
		SchemaVersion:     OutcomeSchemaVersion,
		ProjectIdentity:   strings.TrimSpace(value.ProjectIdentity),
		SessionKey:        strings.TrimSpace(value.SessionKey),
		OccurredAt:        value.OccurredAt.UTC().Round(0),
		Kind:              value.Kind,
		Result:            value.Result,
		EvidenceClass:     value.EvidenceClass,
		Confidence:        value.Confidence,
		SourceRefs:        normalizedNodeRefs(value.SourceRefs),
		DerivationVersion: strings.TrimSpace(value.DerivationVersion),
	})
}

func deterministicID(prefix string, value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return prefix + strings.ToLower(idEncoding.EncodeToString(sum[:]))
}

func normalizedNodeRefs(values []NodeRef) []NodeRef {
	result := append([]NodeRef(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return string(left) < string(right)
	})
	return result
}
