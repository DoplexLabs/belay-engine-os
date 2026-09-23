// Package sessionidentity defines Belay's deterministic source-session
// inventory. Observations are evidence; they do not merge sessions by
// themselves.
package sessionidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion     = "belay.session-identity.v1"
	DerivationVersion = "belay.session-identity.det.v1"

	SourceTranscript     = "transcript"
	SourceNumbatArtifact = "numbat_artifact"
	SourceNumbatHook     = "numbat_hook"
	SourceOTLP           = "otlp"

	CoverageComplete = "complete"
	CoveragePartial  = "partial"
	CoverageLive     = "live"
	CoverageRotated  = "rotated"
	CoverageObserved = "observed"
)

type Observation struct {
	SchemaVersion         string    `json:"schema_version"`
	ObservationID         string    `json:"observation_id"`
	SourceKind            string    `json:"source_kind"`
	SourceAgent           string    `json:"source_agent"`
	SourceSessionKey      string    `json:"source_session_key"`
	NativeNamespace       string    `json:"native_namespace"`
	NativeIDHash          string    `json:"native_id_hash"`
	ProjectIdentity       string    `json:"project_identity,omitempty"`
	ArtifactType          string    `json:"artifact_type,omitempty"`
	SourceRunID           string    `json:"source_run_id,omitempty"`
	StartedAt             time.Time `json:"started_at,omitempty"`
	EndedAt               time.Time `json:"ended_at,omitempty"`
	Coverage              string    `json:"coverage"`
	DerivationVersion     string    `json:"derivation_version"`
	ObservedAt            time.Time `json:"observed_at"`
	NativeSessionID       string    `json:"native_session_id"`
	ParentNativeSessionID string    `json:"parent_native_session_id,omitempty"`
	ArtifactSHA256        string    `json:"artifact_sha256,omitempty"`
}

func NewObservation(
	storeID string,
	value Observation,
) (Observation, error) {
	value.SchemaVersion = SchemaVersion
	value.DerivationVersion = DerivationVersion
	value.SourceKind = strings.TrimSpace(value.SourceKind)
	value.SourceAgent = strings.TrimSpace(value.SourceAgent)
	value.SourceSessionKey = strings.TrimSpace(value.SourceSessionKey)
	value.NativeNamespace = strings.TrimSpace(value.NativeNamespace)
	value.NativeSessionID = strings.TrimSpace(value.NativeSessionID)
	value.ProjectIdentity = strings.TrimSpace(value.ProjectIdentity)
	value.ArtifactType = strings.TrimSpace(value.ArtifactType)
	value.SourceRunID = strings.TrimSpace(value.SourceRunID)
	value.Coverage = strings.TrimSpace(value.Coverage)
	if value.ObservedAt.IsZero() {
		value.ObservedAt = time.Now().UTC()
	}
	if storeID == "" || value.SourceKind == "" || value.SourceAgent == "" ||
		value.SourceSessionKey == "" || value.NativeNamespace == "" ||
		value.NativeSessionID == "" || value.Coverage == "" {
		return Observation{}, errors.New("incomplete session identity observation")
	}
	hash := sha256.Sum256([]byte(strings.Join([]string{
		storeID,
		value.SourceAgent,
		value.NativeSessionID,
	}, "\x00")))
	value.NativeIDHash = hex.EncodeToString(hash[:])
	value.ObservationID = StableObservationID(
		value.SourceKind,
		value.SourceAgent,
		value.SourceSessionKey,
		value.NativeNamespace,
	)
	return value, nil
}

func StableObservationID(
	sourceKind string,
	sourceAgent string,
	sourceSessionKey string,
	nativeNamespace string,
) string {
	id := sha256.Sum256([]byte(strings.Join([]string{
		SchemaVersion,
		strings.TrimSpace(sourceKind),
		strings.TrimSpace(sourceAgent),
		strings.TrimSpace(sourceSessionKey),
		strings.TrimSpace(nativeNamespace),
	}, "\x00")))
	return "sio_" + hex.EncodeToString(id[:16])
}

const (
	ReasonHookNativeIDNotArtifactID = "hook_native_id_not_artifact_id"
	ReasonArtifactNotDiscovered     = "artifact_not_discovered"
	ReasonTranscriptPartial         = "transcript_partial"
	ReasonTranscriptRotated         = "transcript_rotated"
	ReasonAgentMismatch             = "agent_mismatch"
	ReasonProjectIdentityMismatch   = "project_identity_mismatch"
	ReasonParentSubagentUnresolved  = "parent_subagent_unresolved"
	ReasonEventOnlyExpected         = "event_only_expected"
	ReasonTranscriptOnlyExpected    = "transcript_only_expected"
	ReasonUnsupportedSourceLineage  = "unsupported_source_lineage"
	ReasonAmbiguous                 = "ambiguous"
	ReasonUnknown                   = "unknown"

	RelationSameSession  = "same_session"
	BasisExactNativeID   = "exact_native_id"
	BasisSharedToolCalls = "shared_tool_call_ids"
	BasisSourceLineage   = "numbat_source_lineage"
	ConfidenceHigh       = "high"
	StateActive          = "active"
	StateSuperseded      = "superseded"
)

type Audit struct {
	Observations         int            `json:"observations"`
	ExactNativeMatches   int            `json:"exact_native_matches"`
	ActiveLinks          int            `json:"active_links"`
	TranscriptOnly       int            `json:"transcript_only"`
	EventOnly            int            `json:"event_only"`
	ClassifiedMismatches int            `json:"classified_mismatches"`
	Reasons              map[string]int `json:"reasons"`
}

// AuditObservations classifies source coverage without using timestamps,
// project paths, command text, or any other fuzzy join.
func AuditObservations(values []Observation) Audit {
	result := Audit{
		Observations: len(values),
		Reasons:      make(map[string]int),
	}
	type identityKey struct {
		agent string
		hash  string
	}
	byIdentity := make(map[identityKey][]Observation)
	agentsByNativeID := make(map[string]map[string]bool)
	for _, value := range values {
		key := identityKey{agent: value.SourceAgent, hash: value.NativeIDHash}
		byIdentity[key] = append(byIdentity[key], value)
		nativeID := strings.TrimSpace(value.NativeSessionID)
		if nativeID != "" {
			if agentsByNativeID[nativeID] == nil {
				agentsByNativeID[nativeID] = make(map[string]bool)
			}
			agentsByNativeID[nativeID][value.SourceAgent] = true
		}
	}
	for _, group := range byIdentity {
		transcripts := make(map[string]Observation)
		sources := make(map[string]Observation)
		for _, value := range group {
			if value.SourceKind == SourceTranscript {
				transcripts[value.SourceSessionKey] = value
			} else {
				sources[value.SourceSessionKey] = value
			}
		}
		if len(transcripts) > 0 && len(sources) > 0 {
			if len(transcripts) == 1 &&
				projectIdentityCompatible(transcripts, sources) {
				result.ExactNativeMatches++
				continue
			}
			reason := ReasonAmbiguous
			if !projectIdentityCompatible(transcripts, sources) {
				reason = ReasonProjectIdentityMismatch
			}
			for range transcripts {
				result.TranscriptOnly++
				addAuditReason(&result, reason)
			}
			for range sources {
				result.EventOnly++
				addAuditReason(&result, reason)
			}
			continue
		}
		for _, value := range transcripts {
			result.TranscriptOnly++
			addAuditReason(
				&result,
				transcriptMismatchReason(value, agentsByNativeID),
			)
		}
		for _, value := range sources {
			result.EventOnly++
			addAuditReason(
				&result,
				eventMismatchReason(value, agentsByNativeID),
			)
		}
	}
	return result
}

func projectIdentityCompatible(
	transcripts map[string]Observation,
	sources map[string]Observation,
) bool {
	for _, transcript := range transcripts {
		for _, source := range sources {
			if transcript.ProjectIdentity == "" ||
				source.ProjectIdentity == "" ||
				transcript.ProjectIdentity == source.ProjectIdentity {
				return true
			}
		}
	}
	return false
}

func transcriptMismatchReason(
	value Observation,
	agentsByNativeID map[string]map[string]bool,
) string {
	switch value.Coverage {
	case CoveragePartial, CoverageLive:
		return ReasonTranscriptPartial
	case CoverageRotated:
		return ReasonTranscriptRotated
	}
	if strings.TrimSpace(value.ParentNativeSessionID) != "" {
		return ReasonParentSubagentUnresolved
	}
	if len(agentsByNativeID[value.NativeSessionID]) > 1 {
		return ReasonAgentMismatch
	}
	if value.SourceKind != SourceTranscript {
		return ReasonUnsupportedSourceLineage
	}
	return ReasonArtifactNotDiscovered
}

func eventMismatchReason(
	value Observation,
	agentsByNativeID map[string]map[string]bool,
) string {
	if strings.TrimSpace(value.ParentNativeSessionID) != "" {
		return ReasonParentSubagentUnresolved
	}
	if len(agentsByNativeID[value.NativeSessionID]) > 1 {
		return ReasonAgentMismatch
	}
	switch value.SourceKind {
	case SourceNumbatHook:
		return ReasonHookNativeIDNotArtifactID
	case SourceNumbatArtifact, SourceOTLP:
		return ReasonEventOnlyExpected
	default:
		return ReasonUnsupportedSourceLineage
	}
}

func addAuditReason(result *Audit, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = ReasonUnknown
	}
	result.Reasons[reason]++
	result.ClassifiedMismatches++
}

type SessionEvidence struct {
	SourceKind      string
	SourceAgent     string
	SessionKey      string
	ProjectIdentity string
	ToolCallIDs     []string
}

type Link struct {
	SchemaVersion     string    `json:"schema_version"`
	LinkID            string    `json:"link_id"`
	LeftSessionKey    string    `json:"left_session_key"`
	RightSessionKey   string    `json:"right_session_key"`
	Relation          string    `json:"relation"`
	Basis             string    `json:"basis"`
	Confidence        string    `json:"confidence"`
	State             string    `json:"state"`
	SourceRefs        []string  `json:"source_refs"`
	DerivationVersion string    `json:"derivation_version"`
	CreatedAt         time.Time `json:"created_at"`
}

type ActiveAlias struct {
	SessionKey       string   `json:"session_key"`
	LinkedSessionKey string   `json:"linked_session_key"`
	Bases            []string `json:"bases"`
}

// ResolveExactNativeLinks emits only one-to-one transcript/source links with
// the same agent-scoped native identity. Multiple source session keys remain
// ambiguous even when their raw native ID is equal.
func ResolveExactNativeLinks(values []Observation, now time.Time) []Link {
	covered := ExactNativeObservationIDs(values)
	type identityKey struct {
		agent string
		hash  string
	}
	grouped := make(map[identityKey][]Observation)
	for _, value := range values {
		if value.SourceAgent == "" || value.NativeIDHash == "" {
			continue
		}
		key := identityKey{agent: value.SourceAgent, hash: value.NativeIDHash}
		grouped[key] = append(grouped[key], value)
	}
	var result []Link
	for _, group := range grouped {
		transcripts := make(map[string]Observation)
		sources := make(map[string]Observation)
		var refs []string
		for _, value := range group {
			if !covered[value.ObservationID] {
				continue
			}
			refs = append(refs, value.ObservationID)
			if value.SourceKind == SourceTranscript {
				transcripts[value.SourceSessionKey] = value
			} else {
				sources[value.SourceSessionKey] = value
			}
		}
		if len(transcripts) != 1 || len(sources) != 1 {
			continue
		}
		var left, right string
		for key := range transcripts {
			left = key
		}
		for key := range sources {
			right = key
		}
		if left == "" || right == "" || left == right {
			continue
		}
		sort.Strings(refs)
		result = append(
			result,
			newLink(left, right, BasisExactNativeID, refs, now),
		)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LinkID < result[j].LinkID
	})
	return result
}

// ExactNativeObservationIDs returns observations already resolved by one
// unique transcript session and one unique non-transcript source session with
// the same agent-scoped native identity. Callers can avoid weaker or more
// expensive correlation for these observations, including when both sources
// already use the same canonical session key.
func ExactNativeObservationIDs(values []Observation) map[string]bool {
	type identityKey struct {
		agent string
		hash  string
	}
	grouped := make(map[identityKey][]Observation)
	for _, value := range values {
		if value.SourceAgent == "" || value.NativeIDHash == "" {
			continue
		}
		key := identityKey{agent: value.SourceAgent, hash: value.NativeIDHash}
		grouped[key] = append(grouped[key], value)
	}
	result := make(map[string]bool)
	for _, group := range grouped {
		transcripts := make(map[string]Observation)
		sources := make(map[string]Observation)
		for _, value := range group {
			if value.SourceKind == SourceTranscript {
				transcripts[value.SourceSessionKey] = value
			} else {
				sources[value.SourceSessionKey] = value
			}
		}
		if len(transcripts) != 1 || len(sources) != 1 ||
			!projectIdentityCompatible(transcripts, sources) {
			continue
		}
		for _, value := range group {
			result[value.ObservationID] = true
		}
	}
	return result
}

// ResolveToolCallLinks emits only unique one-to-one links supported by at
// least two exact shared tool-call IDs. It never uses timestamps.
func ResolveToolCallLinks(values []SessionEvidence, now time.Time) []Link {
	var transcripts, sources []SessionEvidence
	for _, value := range values {
		if value.SourceKind == SourceTranscript {
			transcripts = append(transcripts, value)
		} else {
			sources = append(sources, value)
		}
	}
	type pair struct{ left, right string }
	candidates := make(map[pair][]string)
	leftCandidates := make(map[string]int)
	rightCandidates := make(map[string]int)
	for _, left := range transcripts {
		for _, right := range sources {
			if left.SourceAgent != right.SourceAgent ||
				left.SessionKey == right.SessionKey ||
				left.ProjectIdentity != "" &&
					right.ProjectIdentity != "" &&
					left.ProjectIdentity != right.ProjectIdentity {
				continue
			}
			shared := sharedToolCallHashes(left.ToolCallIDs, right.ToolCallIDs)
			if len(shared) < 2 {
				continue
			}
			key := pair{left: left.SessionKey, right: right.SessionKey}
			candidates[key] = shared
			leftCandidates[left.SessionKey]++
			rightCandidates[right.SessionKey]++
		}
	}
	var result []Link
	for key, refs := range candidates {
		if leftCandidates[key.left] != 1 || rightCandidates[key.right] != 1 {
			continue
		}
		result = append(
			result,
			newLink(key.left, key.right, BasisSharedToolCalls, refs, now),
		)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LinkID < result[j].LinkID
	})
	return result
}

func newLink(
	left string,
	right string,
	basis string,
	sourceRefs []string,
	now time.Time,
) Link {
	id := sha256.Sum256([]byte(strings.Join([]string{
		SchemaVersion,
		left,
		right,
		RelationSameSession,
		basis,
		DerivationVersion,
	}, "\x00")))
	return Link{
		SchemaVersion:     SchemaVersion,
		LinkID:            "sil_" + hex.EncodeToString(id[:16]),
		LeftSessionKey:    left,
		RightSessionKey:   right,
		Relation:          RelationSameSession,
		Basis:             basis,
		Confidence:        ConfidenceHigh,
		State:             StateActive,
		SourceRefs:        append([]string(nil), sourceRefs...),
		DerivationVersion: DerivationVersion,
		CreatedAt:         now.UTC(),
	}
}

// NewSourceLineageLink creates a high-confidence same-session alias from an
// explicit upstream source-lineage record.
func NewSourceLineageLink(
	left string,
	right string,
	sourceRefs []string,
	now time.Time,
) Link {
	return newLink(left, right, BasisSourceLineage, sourceRefs, now)
}

func sharedToolCallHashes(left, right []string) []string {
	rightSet := make(map[string]bool, len(right))
	for _, value := range right {
		value = strings.TrimSpace(value)
		if value != "" {
			rightSet[value] = true
		}
	}
	seen := make(map[string]bool)
	var result []string
	for _, value := range left {
		value = strings.TrimSpace(value)
		if value == "" || !rightSet[value] || seen[value] {
			continue
		}
		seen[value] = true
		sum := sha256.Sum256([]byte(value))
		result = append(result, "tool_call_sha256:"+hex.EncodeToString(sum[:]))
	}
	sort.Strings(result)
	return result
}
