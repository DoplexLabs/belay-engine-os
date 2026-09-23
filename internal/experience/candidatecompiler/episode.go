package candidatecompiler

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

const evidenceEpisodeSchemaVersion = "belay.evidence-episode.v1"

var evidenceEpisodeIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// successfulProcedureRecord is the deterministic input to episode grouping.
// Semantic guidance is deliberately absent: this record only connects a
// successful verifier to the mutation evidence it checked.
type successfulProcedureRecord struct {
	SessionKey         string
	OutcomeID          string
	ContinuesOutcomeID string
	VerifierCallRef    trajectory.NodeRef
	VerifierResultRef  trajectory.NodeRef
	VerifierTurn       int64
	CommandClass       string
	RawCommand         string
	UserRequested      bool
	ProjectConfigured  bool
	MutationRefs       []trajectory.NodeRef
	MutationPaths      []string
	ContinuityRefs     []trajectory.NodeRef
	EvidenceRefs       []trajectory.NodeRef
	GeneratedAt        time.Time
}

type SuccessfulProcedureEpisode struct {
	CandidateID  string
	EpisodeID    string
	SessionKey   string
	FirstTurn    int64
	LastTurn     int64
	MutationRefs []trajectory.NodeRef
	EvidenceRefs []trajectory.NodeRef
	OutcomeIDs   []string
	Anchor       successfulProcedureRecord
	Supporting   []successfulProcedureRecord
	GeneratedAt  time.Time
}

type evidenceEpisode = SuccessfulProcedureEpisode

// buildEvidenceEpisodes joins successful verifiers when their deterministic
// evidence shares at least one mutation turn in the same session. Overlap is
// transitive so repeated checks of one mutation epoch become one episode.
func buildEvidenceEpisodes(
	input []successfulProcedureRecord,
) []evidenceEpisode {
	records := normalizeProcedureRecords(input)
	if len(records) == 0 {
		return nil
	}

	parent := make([]int, len(records))
	for index := range parent {
		parent[index] = index
	}
	var find func(int) int
	find = func(index int) int {
		if parent[index] != index {
			parent[index] = find(parent[index])
		}
		return parent[index]
	}
	union := func(left, right int) {
		leftRoot, rightRoot := find(left), find(right)
		if leftRoot != rightRoot {
			parent[rightRoot] = leftRoot
		}
	}
	for left := range records {
		for right := left + 1; right < len(records); right++ {
			if records[left].SessionKey == records[right].SessionKey &&
				(nodeRefsOverlap(
					records[left].MutationRefs,
					records[right].MutationRefs,
				) ||
					records[right].ContinuesOutcomeID ==
						records[left].OutcomeID ||
					records[left].ContinuesOutcomeID ==
						records[right].OutcomeID) {
				union(left, right)
			}
		}
	}

	grouped := make(map[int][]successfulProcedureRecord)
	for index, record := range records {
		root := find(index)
		grouped[root] = append(grouped[root], record)
	}

	episodes := make([]evidenceEpisode, 0, len(grouped))
	for _, group := range grouped {
		episode := newEvidenceEpisode(group)
		if episode.EpisodeID != "" {
			episodes = append(episodes, episode)
		}
	}
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].SessionKey != episodes[j].SessionKey {
			return episodes[i].SessionKey < episodes[j].SessionKey
		}
		if episodes[i].FirstTurn != episodes[j].FirstTurn {
			return episodes[i].FirstTurn < episodes[j].FirstTurn
		}
		if episodes[i].LastTurn != episodes[j].LastTurn {
			return episodes[i].LastTurn < episodes[j].LastTurn
		}
		return episodes[i].EpisodeID < episodes[j].EpisodeID
	})
	return episodes
}

func normalizeProcedureRecords(
	input []successfulProcedureRecord,
) []successfulProcedureRecord {
	unique := make(map[string]successfulProcedureRecord)
	for _, record := range input {
		record.SessionKey = strings.TrimSpace(record.SessionKey)
		record.OutcomeID = strings.TrimSpace(record.OutcomeID)
		record.ContinuesOutcomeID = strings.TrimSpace(
			record.ContinuesOutcomeID,
		)
		record.CommandClass = strings.TrimSpace(record.CommandClass)
		record.RawCommand = strings.TrimSpace(record.RawCommand)
		record.GeneratedAt = record.GeneratedAt.UTC().Round(0)
		if !validEpisodeTurnRef(record.VerifierCallRef, record.SessionKey) ||
			!validEpisodeTurnRef(record.VerifierResultRef, record.SessionKey) {
			continue
		}
		record.VerifierTurn = *record.VerifierCallRef.TurnIndex
		record.MutationRefs = normalizeEpisodeNodeRefs(
			record.MutationRefs,
			func(ref trajectory.NodeRef) bool {
				return validEpisodeTurnRef(ref, record.SessionKey)
			},
		)
		record.MutationPaths = normalizeStrings(record.MutationPaths)
		record.ContinuityRefs = normalizeEpisodeNodeRefs(
			record.ContinuityRefs,
			func(ref trajectory.NodeRef) bool {
				return validEpisodeTurnRef(ref, record.SessionKey)
			},
		)
		record.EvidenceRefs = normalizeEpisodeNodeRefs(
			record.EvidenceRefs,
			func(trajectory.NodeRef) bool { return true },
		)
		if record.SessionKey == "" ||
			record.OutcomeID == "" ||
			record.CommandClass == "" ||
			record.RawCommand == "" ||
			len(record.MutationRefs) == 0 {
			continue
		}
		key := procedureRecordKey(record)
		unique[key] = record
	}
	result := make([]successfulProcedureRecord, 0, len(unique))
	for _, record := range unique {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return procedureRecordLess(result[i], result[j])
	})
	return result
}

func newEvidenceEpisode(
	records []successfulProcedureRecord,
) evidenceEpisode {
	if len(records) == 0 {
		return evidenceEpisode{}
	}
	sort.Slice(records, func(i, j int) bool {
		return procedureRecordLess(records[i], records[j])
	})

	mutationRefs := make([]trajectory.NodeRef, 0)
	evidenceRefs := make([]trajectory.NodeRef, 0)
	outcomeIDs := make([]string, 0, len(records))
	firstTurn, finalMutationTurn := int64(-1), int64(-1)
	lastTurn := int64(-1)
	var generatedAt time.Time
	for _, record := range records {
		mutationRefs = append(mutationRefs, record.MutationRefs...)
		evidenceRefs = append(evidenceRefs, record.EvidenceRefs...)
		evidenceRefs = append(evidenceRefs, record.ContinuityRefs...)
		evidenceRefs = append(
			evidenceRefs,
			record.VerifierCallRef,
			record.VerifierResultRef,
		)
		outcomeIDs = append(outcomeIDs, record.OutcomeID)
		for _, ref := range record.MutationRefs {
			if firstTurn < 0 || *ref.TurnIndex < firstTurn {
				firstTurn = *ref.TurnIndex
			}
			if *ref.TurnIndex > finalMutationTurn {
				finalMutationTurn = *ref.TurnIndex
			}
		}
		if record.VerifierTurn > lastTurn {
			lastTurn = record.VerifierTurn
		}
		if *record.VerifierResultRef.TurnIndex > lastTurn {
			lastTurn = *record.VerifierResultRef.TurnIndex
		}
		if record.GeneratedAt.After(generatedAt) {
			generatedAt = record.GeneratedAt
		}
	}
	mutationRefs = normalizeEpisodeNodeRefs(
		mutationRefs,
		func(trajectory.NodeRef) bool { return true },
	)
	evidenceRefs = normalizeEpisodeNodeRefs(
		evidenceRefs,
		func(trajectory.NodeRef) bool { return true },
	)
	outcomeIDs = normalizeStrings(outcomeIDs)

	anchorIndex := selectAnchorVerifier(records, finalMutationTurn)
	anchor := records[anchorIndex]
	supporting := make([]successfulProcedureRecord, 0, len(records)-1)
	for index, record := range records {
		if index != anchorIndex {
			supporting = append(supporting, record)
		}
	}
	episode := evidenceEpisode{
		SessionKey:   records[0].SessionKey,
		FirstTurn:    firstTurn,
		LastTurn:     lastTurn,
		MutationRefs: mutationRefs,
		EvidenceRefs: evidenceRefs,
		OutcomeIDs:   outcomeIDs,
		Anchor:       anchor,
		Supporting:   supporting,
		GeneratedAt:  generatedAt,
	}
	episode.EpisodeID = deterministicEvidenceEpisodeID(episode)
	return episode
}

func selectAnchorVerifier(
	records []successfulProcedureRecord,
	finalMutationTurn int64,
) int {
	best := 0
	for index := 1; index < len(records); index++ {
		if preferredAnchor(
			records[index],
			records[best],
			finalMutationTurn,
		) {
			best = index
		}
	}
	return best
}

func preferredAnchor(
	candidate successfulProcedureRecord,
	current successfulProcedureRecord,
	finalMutationTurn int64,
) bool {
	candidateAfter := candidate.VerifierTurn > finalMutationTurn
	currentAfter := current.VerifierTurn > finalMutationTurn
	if candidateAfter != currentAfter {
		return candidateAfter
	}
	if candidate.UserRequested != current.UserRequested {
		return candidate.UserRequested
	}
	if candidate.ProjectConfigured != current.ProjectConfigured {
		return candidate.ProjectConfigured
	}
	if candidate.VerifierTurn != current.VerifierTurn {
		return candidate.VerifierTurn > current.VerifierTurn
	}
	if candidate.CommandClass != current.CommandClass {
		return candidate.CommandClass < current.CommandClass
	}
	if candidate.RawCommand != current.RawCommand {
		return candidate.RawCommand < current.RawCommand
	}
	return procedureRecordKey(candidate) < procedureRecordKey(current)
}

func procedureRecordLess(
	left successfulProcedureRecord,
	right successfulProcedureRecord,
) bool {
	if left.SessionKey != right.SessionKey {
		return left.SessionKey < right.SessionKey
	}
	if left.VerifierTurn != right.VerifierTurn {
		return left.VerifierTurn < right.VerifierTurn
	}
	if left.CommandClass != right.CommandClass {
		return left.CommandClass < right.CommandClass
	}
	if left.RawCommand != right.RawCommand {
		return left.RawCommand < right.RawCommand
	}
	return procedureRecordKey(left) < procedureRecordKey(right)
}

func procedureRecordKey(record successfulProcedureRecord) string {
	encoded, err := json.Marshal(struct {
		SessionKey         string
		OutcomeID          string
		ContinuesOutcomeID string
		VerifierCallRef    trajectory.NodeRef
		VerifierResultRef  trajectory.NodeRef
		VerifierTurn       int64
		CommandClass       string
		RawCommand         string
		UserRequested      bool
		ProjectConfigured  bool
		MutationRefs       []trajectory.NodeRef
		MutationPaths      []string
		ContinuityRefs     []trajectory.NodeRef
		EvidenceRefs       []trajectory.NodeRef
		GeneratedAt        time.Time
	}{
		SessionKey:         record.SessionKey,
		OutcomeID:          record.OutcomeID,
		ContinuesOutcomeID: record.ContinuesOutcomeID,
		VerifierCallRef:    record.VerifierCallRef,
		VerifierResultRef:  record.VerifierResultRef,
		VerifierTurn:       record.VerifierTurn,
		CommandClass:       record.CommandClass,
		RawCommand:         record.RawCommand,
		UserRequested:      record.UserRequested,
		ProjectConfigured:  record.ProjectConfigured,
		MutationRefs:       record.MutationRefs,
		MutationPaths:      record.MutationPaths,
		ContinuityRefs:     record.ContinuityRefs,
		EvidenceRefs:       record.EvidenceRefs,
		GeneratedAt:        record.GeneratedAt,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func deterministicEvidenceEpisodeID(episode evidenceEpisode) string {
	verificationRefs := make([]trajectory.NodeRef, 0, 2*(len(episode.Supporting)+1))
	verificationRefs = append(
		verificationRefs,
		episode.Anchor.VerifierCallRef,
		episode.Anchor.VerifierResultRef,
	)
	for _, record := range episode.Supporting {
		verificationRefs = append(
			verificationRefs,
			record.VerifierCallRef,
			record.VerifierResultRef,
		)
	}
	verificationRefs = normalizeEpisodeNodeRefs(
		verificationRefs,
		func(trajectory.NodeRef) bool { return true },
	)
	encoded, err := json.Marshal(struct {
		SchemaVersion    string
		SessionKey       string
		FirstTurn        int64
		LastTurn         int64
		MutationRefs     []trajectory.NodeRef
		VerificationRefs []trajectory.NodeRef
		OutcomeIDs       []string
	}{
		SchemaVersion:    evidenceEpisodeSchemaVersion,
		SessionKey:       episode.SessionKey,
		FirstTurn:        episode.FirstTurn,
		LastTurn:         episode.LastTurn,
		MutationRefs:     episode.MutationRefs,
		VerificationRefs: verificationRefs,
		OutcomeIDs:       episode.OutcomeIDs,
	})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return "epe_" + strings.ToLower(
		evidenceEpisodeIDEncoding.EncodeToString(sum[:]),
	)
}

func nodeRefsOverlap(
	left []trajectory.NodeRef,
	right []trajectory.NodeRef,
) bool {
	for _, ref := range left {
		if containsNode(right, ref) {
			return true
		}
	}
	return false
}

func validEpisodeTurnRef(
	ref trajectory.NodeRef,
	sessionKey string,
) bool {
	return ref.Kind == trajectory.NodeTranscriptTurn &&
		ref.SessionKey == sessionKey &&
		ref.TurnIndex != nil &&
		*ref.TurnIndex >= 0
}

func normalizeEpisodeNodeRefs(
	refs []trajectory.NodeRef,
	keep func(trajectory.NodeRef) bool,
) []trajectory.NodeRef {
	unique := make(map[string]trajectory.NodeRef)
	for _, ref := range refs {
		if !keep(ref) {
			continue
		}
		encoded, err := json.Marshal(ref)
		if err != nil {
			panic(err)
		}
		unique[string(encoded)] = ref
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]trajectory.NodeRef, 0, len(keys))
	for _, key := range keys {
		result = append(result, unique[key])
	}
	return result
}

func normalizeStrings(values []string) []string {
	unique := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
