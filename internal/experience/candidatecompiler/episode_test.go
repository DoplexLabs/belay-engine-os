package candidatecompiler

import (
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

func TestBuildEvidenceEpisodesGroupsRepeatedVerifiersAndSelectsAnchor(t *testing.T) {
	edit := episodeTurnRef("ses_episode", 2)
	first := episodeProcedureRecord(
		"ses_episode",
		5,
		"go test",
		"go test ./internal/...",
		[]trajectory.NodeRef{edit},
	)
	second := episodeProcedureRecord(
		"ses_episode",
		8,
		"go test",
		"go test ./...",
		[]trajectory.NodeRef{edit},
	)

	got := buildEvidenceEpisodes([]successfulProcedureRecord{second, first})
	if len(got) != 1 {
		t.Fatalf("episodes = %d, want 1: %+v", len(got), got)
	}
	if got[0].FirstTurn != 2 || got[0].LastTurn != 9 {
		t.Fatalf(
			"episode bounds = %d..%d, want 2..9",
			got[0].FirstTurn,
			got[0].LastTurn,
		)
	}
	if got[0].Anchor.RawCommand != "go test ./..." {
		t.Fatalf("anchor = %q, want latest verifier", got[0].Anchor.RawCommand)
	}
	if len(got[0].Supporting) != 1 ||
		got[0].Supporting[0].RawCommand != "go test ./internal/..." {
		t.Fatalf("supporting verifiers = %+v", got[0].Supporting)
	}

	replayed := buildEvidenceEpisodes([]successfulProcedureRecord{first, second})
	if len(replayed) != 1 || replayed[0].EpisodeID != got[0].EpisodeID {
		t.Fatalf(
			"episode ID changed with input order: %q != %q",
			replayed[0].EpisodeID,
			got[0].EpisodeID,
		)
	}
}

func TestBuildEvidenceEpisodesKeepsMutationEpochsAndSessionsSeparate(t *testing.T) {
	first := episodeProcedureRecord(
		"ses_first",
		4,
		"go test",
		"go test ./...",
		[]trajectory.NodeRef{episodeTurnRef("ses_first", 1)},
	)
	secondEpoch := episodeProcedureRecord(
		"ses_first",
		9,
		"go test",
		"go test ./...",
		[]trajectory.NodeRef{episodeTurnRef("ses_first", 7)},
	)
	otherSession := episodeProcedureRecord(
		"ses_second",
		4,
		"go test",
		"go test ./...",
		[]trajectory.NodeRef{episodeTurnRef("ses_second", 1)},
	)

	got := buildEvidenceEpisodes(
		[]successfulProcedureRecord{otherSession, secondEpoch, first},
	)
	if len(got) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(got), got)
	}
	if got[0].SessionKey != "ses_first" || got[0].FirstTurn != 1 ||
		got[1].SessionKey != "ses_first" || got[1].FirstTurn != 7 ||
		got[2].SessionKey != "ses_second" {
		t.Fatalf("episode ordering or boundaries = %+v", got)
	}
}

func episodeProcedureRecord(
	sessionKey string,
	verifierTurn int64,
	commandClass string,
	rawCommand string,
	mutationRefs []trajectory.NodeRef,
) successfulProcedureRecord {
	callRef := episodeTurnRef(sessionKey, verifierTurn)
	resultRef := episodeTurnRef(sessionKey, verifierTurn+1)
	return successfulProcedureRecord{
		SessionKey:        sessionKey,
		OutcomeID:         "out_" + sessionKey + "_" + rawCommand,
		VerifierCallRef:   callRef,
		VerifierResultRef: resultRef,
		VerifierTurn:      verifierTurn,
		CommandClass:      commandClass,
		RawCommand:        rawCommand,
		MutationRefs:      mutationRefs,
		EvidenceRefs: append(
			append([]trajectory.NodeRef(nil), mutationRefs...),
			callRef,
			resultRef,
		),
		GeneratedAt: time.Date(
			2026,
			time.September,
			13,
			12,
			0,
			int(verifierTurn),
			0,
			time.UTC,
		),
	}
}

func episodeTurnRef(sessionKey string, turnIndex int64) trajectory.NodeRef {
	index := turnIndex
	return trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: sessionKey,
		TurnIndex:  &index,
	}
}
