package localapp

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type candidateCompilationTestStore struct {
	sessions   []transcript.Session
	turns      map[string][]transcript.Turn
	edges      map[string][]trajectory.Edge
	outcomes   map[string][]trajectory.Outcome
	candidates map[string]experience.Candidate
}

func (store *candidateCompilationTestStore) QueryTranscriptSessions(
	_ context.Context,
	query transcript.SessionQuery,
) ([]transcript.Session, error) {
	result := make([]transcript.Session, 0)
	for _, session := range store.sessions {
		if session.ProjectIdentity == query.ProjectIdentity {
			result = append(result, session)
		}
	}
	return result, nil
}

func (store *candidateCompilationTestStore) QueryTranscriptTurns(
	_ context.Context,
	sessionKey string,
	_ int,
) ([]transcript.Turn, error) {
	return append([]transcript.Turn(nil), store.turns[sessionKey]...), nil
}

func (store *candidateCompilationTestStore) QueryTrajectoryEdges(
	_ context.Context,
	query local.TrajectoryEdgeQuery,
) ([]trajectory.Edge, error) {
	return append([]trajectory.Edge(nil), store.edges[query.SessionKey]...), nil
}

func (store *candidateCompilationTestStore) QueryOutcomes(
	_ context.Context,
	query local.OutcomeQuery,
) ([]trajectory.Outcome, error) {
	return append([]trajectory.Outcome(nil), store.outcomes[query.SessionKey]...), nil
}

func (store *candidateCompilationTestStore) InsertExperienceCandidate(
	_ context.Context,
	candidate experience.Candidate,
) (bool, error) {
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	if previous, exists := store.candidates[candidate.CandidateID]; exists {
		if !reflect.DeepEqual(previous, candidate) {
			return false, local.ErrExperienceCandidateConflict
		}
		return false, nil
	}
	store.candidates[candidate.CandidateID] = candidate
	return true, nil
}

func TestCompileProjectExperienceCandidatesOncePersistsIdempotently(t *testing.T) {
	const (
		project    = "git@example.test:doplexlabs/belay-engine.git"
		sessionKey = "ses_candidate_adapter"
	)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	assistant := transcript.Turn{
		TurnID:     "turn_assistant",
		SessionKey: sessionKey,
		TurnIndex:  1,
		OccurredAt: base,
		Role:       transcript.RoleAssistant,
		Payload: transcript.Payload{
			Text: "I edited the generated file.",
		},
	}
	user := transcript.Turn{
		TurnID:     "turn_user",
		SessionKey: sessionKey,
		TurnIndex:  2,
		OccurredAt: base.Add(time.Second),
		Role:       transcript.RoleUser,
		Payload: transcript.Payload{
			Text: "No, edit the schema source instead.",
		},
	}
	assistantIndex := assistant.TurnIndex
	userIndex := user.TurnIndex
	assistantRef := trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: sessionKey,
		TurnIndex:  &assistantIndex,
	}
	userRef := trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: sessionKey,
		TurnIndex:  &userIndex,
	}
	edges := []trajectory.Edge{
		localappCandidateEdge(
			project,
			sessionKey,
			userRef,
			trajectory.RelationRespondsTo,
			assistantRef,
			user.OccurredAt,
		),
		localappCandidateEdge(
			project,
			sessionKey,
			userRef,
			trajectory.RelationCorrects,
			assistantRef,
			user.OccurredAt,
		),
	}
	outcome := trajectory.Outcome{
		SchemaVersion:     trajectory.OutcomeSchemaVersion,
		ProjectIdentity:   project,
		SessionKey:        sessionKey,
		OccurredAt:        user.OccurredAt,
		Kind:              trajectory.OutcomeCorrection,
		Result:            trajectory.ResultObserved,
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		SourceRefs:        []trajectory.NodeRef{assistantRef, userRef},
		DerivationVersion: "belay.trajectory-derive.v3",
	}
	outcome.OutcomeID = outcome.DeterministicID()
	store := &candidateCompilationTestStore{
		sessions: []transcript.Session{{
			SessionKey:      sessionKey,
			ProjectIdentity: project,
			TurnCount:       2,
			Coverage:        transcript.CoverageComplete,
		}},
		turns: map[string][]transcript.Turn{
			sessionKey: {assistant, user},
		},
		edges: map[string][]trajectory.Edge{
			sessionKey: edges,
		},
		outcomes: map[string][]trajectory.Outcome{
			sessionKey: {outcome},
		},
		candidates: make(map[string]experience.Candidate),
	}

	first, err := CompileProjectExperienceCandidatesOnce(
		context.Background(),
		store,
		project,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.CandidatesInserted != 1 ||
		first.CandidatesReplayed != 0 ||
		first.SessionsConsidered != 1 ||
		first.SessionsCompiled != 1 ||
		first.SessionsSkippedIncomplete != 0 ||
		len(store.candidates) != 1 {
		t.Fatalf("first compilation = %+v, candidates=%d", first, len(store.candidates))
	}

	second, err := CompileProjectExperienceCandidatesOnce(
		context.Background(),
		store,
		project,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.CandidatesInserted != 0 ||
		second.CandidatesReplayed != 1 ||
		len(store.candidates) != 1 {
		t.Fatalf("second compilation = %+v, candidates=%d", second, len(store.candidates))
	}
}

func TestCompileProjectExperienceCandidatesOnceCompilesFullPartialProjection(
	t *testing.T,
) {
	const project = "git@example.test:doplexlabs/belay-engine.git"
	session, turns, edges, outcome := localappCorrectionFixture(
		project,
		"ses_partial_projection",
	)
	session.Coverage = transcript.CoveragePartial
	store := &candidateCompilationTestStore{
		sessions: []transcript.Session{session},
		turns: map[string][]transcript.Turn{
			session.SessionKey: turns,
		},
		edges: map[string][]trajectory.Edge{
			session.SessionKey: edges,
		},
		outcomes: map[string][]trajectory.Outcome{
			session.SessionKey: {outcome},
		},
		candidates: make(map[string]experience.Candidate),
	}

	report, err := CompileProjectExperienceCandidatesOnce(
		context.Background(),
		store,
		project,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsCompiled != 1 ||
		report.PartialSessionsCompiled != 1 ||
		report.LiveSessionsCompiled != 0 ||
		report.SessionsSkippedIncomplete != 0 ||
		report.CandidatesInserted != 1 ||
		len(store.candidates) != 1 {
		t.Fatalf("partial compilation = %+v, candidates=%d", report, len(store.candidates))
	}
}

func TestCompileProjectExperienceCandidatesOnceSkipsInconsistentProjection(
	t *testing.T,
) {
	const project = "git@example.test:doplexlabs/belay-engine.git"
	session, turns, edges, outcome := localappCorrectionFixture(
		project,
		"ses_inconsistent_projection",
	)
	session.Coverage = transcript.CoveragePartial
	session.TurnCount++
	store := &candidateCompilationTestStore{
		sessions: []transcript.Session{session},
		turns: map[string][]transcript.Turn{
			session.SessionKey: turns,
		},
		edges: map[string][]trajectory.Edge{
			session.SessionKey: edges,
		},
		outcomes: map[string][]trajectory.Outcome{
			session.SessionKey: {outcome},
		},
		candidates: make(map[string]experience.Candidate),
	}

	report, err := CompileProjectExperienceCandidatesOnce(
		context.Background(),
		store,
		project,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsCompiled != 0 ||
		report.PartialSessionsCompiled != 0 ||
		report.SessionsSkippedIncomplete != 1 ||
		report.TranscriptIncompleteSessions != 1 ||
		report.CandidatesInserted != 0 ||
		len(store.candidates) != 0 {
		t.Fatalf("inconsistent projection = %+v, candidates=%d", report, len(store.candidates))
	}
}

func TestCompileProjectExperienceCandidatesOnceSkipsIncompleteAndContinues(
	t *testing.T,
) {
	const project = "git@example.test:doplexlabs/belay-engine.git"
	transcriptSession, transcriptTurns, transcriptEdges, transcriptOutcome :=
		localappCorrectionFixture(project, "ses_incomplete_transcript")
	transcriptSession.TurnCount++
	partialSession, partialTurns, partialEdges, partialOutcome :=
		localappCorrectionFixture(project, "ses_partial_transcript")
	partialSession.Coverage = transcript.CoveragePartial
	liveSession, liveTurns, liveEdges, liveOutcome :=
		localappCorrectionFixture(project, "ses_live_transcript")
	liveSession.Coverage = transcript.CoverageLive
	edgeSession, edgeTurns, edgeEdges, edgeOutcome :=
		localappCorrectionFixture(project, "ses_edge_cap")
	edgeEdges = repeatCandidateEdges(edgeEdges, maxCandidateSessionRecords)
	outcomeSession, outcomeTurns, outcomeEdges, outcomeOutcome :=
		localappCorrectionFixture(project, "ses_outcome_cap")
	outcomeValues := make([]trajectory.Outcome, maxCandidateSessionRecords)
	for index := range outcomeValues {
		outcomeValues[index] = outcomeOutcome
	}
	completeSession, completeTurns, completeEdges, completeOutcome :=
		localappCorrectionFixture(project, "ses_complete")
	store := &candidateCompilationTestStore{
		sessions: []transcript.Session{
			transcriptSession,
			partialSession,
			liveSession,
			edgeSession,
			outcomeSession,
			completeSession,
		},
		turns: map[string][]transcript.Turn{
			transcriptSession.SessionKey: transcriptTurns,
			partialSession.SessionKey:    partialTurns,
			liveSession.SessionKey:       liveTurns,
			edgeSession.SessionKey:       edgeTurns,
			outcomeSession.SessionKey:    outcomeTurns,
			completeSession.SessionKey:   completeTurns,
		},
		edges: map[string][]trajectory.Edge{
			transcriptSession.SessionKey: transcriptEdges,
			partialSession.SessionKey:    partialEdges,
			liveSession.SessionKey:       liveEdges,
			edgeSession.SessionKey:       edgeEdges,
			outcomeSession.SessionKey:    outcomeEdges,
			completeSession.SessionKey:   completeEdges,
		},
		outcomes: map[string][]trajectory.Outcome{
			transcriptSession.SessionKey: {transcriptOutcome},
			partialSession.SessionKey:    {partialOutcome},
			liveSession.SessionKey:       {liveOutcome},
			edgeSession.SessionKey:       {edgeOutcome},
			outcomeSession.SessionKey:    outcomeValues,
			completeSession.SessionKey:   {completeOutcome},
		},
		candidates: make(map[string]experience.Candidate),
	}

	report, err := CompileProjectExperienceCandidatesOnce(
		context.Background(),
		store,
		project,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsConsidered != 6 ||
		report.SessionsCompiled != 3 ||
		report.PartialSessionsCompiled != 1 ||
		report.LiveSessionsCompiled != 1 ||
		report.SessionsSkippedIncomplete != 3 ||
		report.TranscriptIncompleteSessions != 1 ||
		report.EdgeCapSessions != 1 ||
		report.OutcomeCapSessions != 1 ||
		report.CandidatesInserted != 3 ||
		len(store.candidates) != 3 {
		t.Fatalf("coverage report = %+v, candidates=%d", report, len(store.candidates))
	}
}

func TestCompileProjectExperienceCandidatesOnceReportsProjectSessionCap(
	t *testing.T,
) {
	const project = "git@example.test:doplexlabs/belay-engine.git"
	sessions := make([]transcript.Session, maxCandidateProjectSessions)
	for index := range sessions {
		sessions[index] = transcript.Session{
			SessionKey:      "ses_project_cap_" + time.Unix(int64(index), 0).UTC().Format("150405"),
			ProjectIdentity: project,
			Coverage:        transcript.CoverageComplete,
		}
	}
	store := &candidateCompilationTestStore{
		sessions:   sessions,
		turns:      make(map[string][]transcript.Turn),
		edges:      make(map[string][]trajectory.Edge),
		outcomes:   make(map[string][]trajectory.Outcome),
		candidates: make(map[string]experience.Candidate),
	}

	report, err := CompileProjectExperienceCandidatesOnce(
		context.Background(),
		store,
		project,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ProjectSessionCapReached ||
		report.SessionsConsidered != maxCandidateProjectSessions ||
		report.SessionsCompiled != maxCandidateProjectSessions ||
		report.SessionsSkippedIncomplete != 0 {
		t.Fatalf("project-cap report = %+v", report)
	}
}

func localappCorrectionFixture(
	projectIdentity string,
	sessionKey string,
) (
	transcript.Session,
	[]transcript.Turn,
	[]trajectory.Edge,
	trajectory.Outcome,
) {
	base := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	assistant := transcript.Turn{
		TurnID:     "turn_" + sessionKey + "_assistant",
		SessionKey: sessionKey,
		TurnIndex:  1,
		OccurredAt: base,
		Role:       transcript.RoleAssistant,
		Payload: transcript.Payload{
			Text: "I edited the generated file.",
		},
	}
	user := transcript.Turn{
		TurnID:     "turn_" + sessionKey + "_user",
		SessionKey: sessionKey,
		TurnIndex:  2,
		OccurredAt: base.Add(time.Second),
		Role:       transcript.RoleUser,
		Payload: transcript.Payload{
			Text: "No, edit the schema source instead.",
		},
	}
	assistantIndex := assistant.TurnIndex
	userIndex := user.TurnIndex
	assistantRef := trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: sessionKey,
		TurnIndex:  &assistantIndex,
	}
	userRef := trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: sessionKey,
		TurnIndex:  &userIndex,
	}
	edges := []trajectory.Edge{
		localappCandidateEdge(
			projectIdentity,
			sessionKey,
			userRef,
			trajectory.RelationRespondsTo,
			assistantRef,
			user.OccurredAt,
		),
		localappCandidateEdge(
			projectIdentity,
			sessionKey,
			userRef,
			trajectory.RelationCorrects,
			assistantRef,
			user.OccurredAt,
		),
	}
	outcome := trajectory.Outcome{
		SchemaVersion:     trajectory.OutcomeSchemaVersion,
		ProjectIdentity:   projectIdentity,
		SessionKey:        sessionKey,
		OccurredAt:        user.OccurredAt,
		Kind:              trajectory.OutcomeCorrection,
		Result:            trajectory.ResultObserved,
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		SourceRefs:        []trajectory.NodeRef{assistantRef, userRef},
		DerivationVersion: "belay.trajectory-derive.v3",
	}
	outcome.OutcomeID = outcome.DeterministicID()
	return transcript.Session{
			SessionKey:      sessionKey,
			ProjectIdentity: projectIdentity,
			TurnCount:       2,
			Coverage:        transcript.CoverageComplete,
		},
		[]transcript.Turn{assistant, user},
		edges,
		outcome
}

func repeatCandidateEdges(
	values []trajectory.Edge,
	count int,
) []trajectory.Edge {
	result := make([]trajectory.Edge, count)
	for index := range result {
		result[index] = values[index%len(values)]
	}
	return result
}

func localappCandidateEdge(
	projectIdentity string,
	sessionKey string,
	from trajectory.NodeRef,
	relation trajectory.EdgeRelation,
	to trajectory.NodeRef,
	occurredAt time.Time,
) trajectory.Edge {
	value := trajectory.Edge{
		SchemaVersion:     trajectory.EdgeSchemaVersion,
		ProjectIdentity:   projectIdentity,
		SessionKey:        sessionKey,
		From:              from,
		Relation:          relation,
		To:                to,
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		DerivationVersion: "belay.trajectory-derive.v3",
		SourceRefs:        []trajectory.NodeRef{from, to},
		OccurredAt:        occurredAt,
	}
	value.EdgeID = value.DeterministicID()
	return value
}
