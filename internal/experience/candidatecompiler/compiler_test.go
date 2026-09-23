package candidatecompiler

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const testProject = "git@example.test:doplexlabs/belay-engine.git"

func TestCompileCorrectionRequiresRealMarkerAndExactEdges(t *testing.T) {
	assistant := candidateTurn("ses_correction", 1, transcript.RoleAssistant)
	assistant.Payload.Text = "I changed the generated file."
	user := candidateTurn("ses_correction", 2, transcript.RoleUser)
	user.Payload.Text = "No, use the schema source instead."
	behaviorRef := candidateTurnRef(assistant)
	userRef := candidateTurnRef(user)
	outcome := candidateOutcome(
		testProject,
		"ses_correction",
		trajectory.OutcomeCorrection,
		trajectory.ResultObserved,
		user.OccurredAt,
		[]trajectory.NodeRef{behaviorRef, userRef},
	)
	edges := []trajectory.Edge{
		candidateEdge(
			testProject,
			"ses_correction",
			userRef,
			trajectory.RelationRespondsTo,
			behaviorRef,
			user.OccurredAt,
			[]trajectory.NodeRef{userRef, behaviorRef},
		),
		candidateEdge(
			testProject,
			"ses_correction",
			userRef,
			trajectory.RelationCorrects,
			behaviorRef,
			user.OccurredAt,
			[]trajectory.NodeRef{userRef, behaviorRef},
		),
	}

	got, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{assistant, user},
		Edges:           edges,
		Outcomes:        []trajectory.Outcome{outcome},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("correction candidates = %+v, want one", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.Family != experience.CandidateCorrection ||
		candidate.UserFeedback != user.Payload.Text ||
		candidate.Authority != experience.AuthorityNone ||
		candidate.LifecycleState != experience.LifecycleCandidate ||
		candidate.Provenance.ExtractorVersion != ExtractorVersion ||
		!candidate.Proposal.SemanticCompilationPending {
		t.Fatalf("correction candidate = %+v", candidate)
	}

	for _, falseText := range []string{
		"Please use the schema source.",
		`<task-notification>wrong result</task-notification>`,
		"MODE: planning\nRAW QUERY:\nWrong, use the schema source.",
		`{"description":"Wrong result","prompt":"No, use the schema source."}`,
	} {
		falseUser := user
		falseUser.Payload.Text = falseText
		falseResult, err := Compile(Input{
			ProjectIdentity: testProject,
			Turns:           []transcript.Turn{assistant, falseUser},
			Edges:           edges,
			Outcomes:        []trajectory.Outcome{outcome},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(falseResult.Candidates) != 0 {
			t.Fatalf("false correction %q produced %+v", falseText, falseResult.Candidates)
		}
	}

	delegatedUser := user
	delegatedUser.Payload.ParentToolUseID = "delegating-tool-call"
	delegated, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{assistant, delegatedUser},
		Edges:           edges,
		Outcomes:        []trajectory.Outcome{outcome},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegated.Candidates) != 0 {
		t.Fatalf(
			"delegated correction produced stale-evidence candidate %+v",
			delegated.Candidates,
		)
	}

	ordinaryUser := user
	ordinaryUser.Payload.Text =
		"No, mode and raw query are labels; use the schema source."
	ordinary, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{assistant, ordinaryUser},
		Edges:           edges,
		Outcomes:        []trajectory.Outcome{outcome},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary.Candidates) != 1 {
		t.Fatalf(
			"ordinary correction prose was excluded: %+v",
			ordinary.Candidates,
		)
	}

	missingEdge, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{assistant, user},
		Edges:           edges[:1],
		Outcomes:        []trajectory.Outcome{outcome},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missingEdge.Candidates) != 0 {
		t.Fatalf("correction without both edges produced %+v", missingEdge.Candidates)
	}
}

func TestCompileSuccessfulProcedureRequiresVerificationPassAndMutationSequence(t *testing.T) {
	priorUser := candidateTurn("ses_success", 0, transcript.RoleUser)
	priorUser.Payload.Text = "Make the migration retry-safe."
	edit := candidateTurn("ses_success", 1, transcript.RoleToolCall)
	edit.ToolName = "Edit"
	edit.Payload.ToolCallID = "edit_1"
	edit.Payload.ToolInput = json.RawMessage(`{"file_path":"internal/example.go"}`)
	verify := candidateTurn("ses_success", 2, transcript.RoleToolCall)
	verify.Payload.ToolCallID = "verify_1"
	verify.Payload.RawCommand = "go test ./..."
	result := candidateTurn("ses_success", 3, transcript.RoleToolResult)
	result.Payload.ToolCallID = "verify_1"
	notError := false
	result.Payload.ToolIsError = &notError
	result.Payload.ToolResult = "ok"
	followingUser := candidateTurn("ses_success", 4, transcript.RoleUser)
	followingUser.Payload.Text =
		"No partial migrations. Keep the schema change atomic."
	ruleAssistant := candidateTurn(
		"ses_success",
		5,
		transcript.RoleAssistant,
	)
	ruleAssistant.Payload.Text =
		"The reusable convention is one rollback-safe migration transaction."
	finalAssistant := candidateTurn(
		"ses_success",
		6,
		transcript.RoleAssistant,
	)
	finalAssistant.Payload.Text = "Implemented and verified."
	editRef := candidateTurnRef(edit)
	verifyRef := candidateTurnRef(verify)
	resultRef := candidateTurnRef(result)
	eventRef := trajectory.NodeRef{
		Kind:    trajectory.NodeCanonicalEvent,
		EventID: "evt_success",
	}
	pass := candidateOutcome(
		testProject,
		"ses_success",
		trajectory.OutcomeVerificationPass,
		trajectory.ResultSucceeded,
		result.OccurredAt,
		[]trajectory.NodeRef{verifyRef, resultRef},
	)
	verifies := candidateEdge(
		testProject,
		"ses_success",
		verifyRef,
		trajectory.RelationVerifies,
		eventRef,
		verify.OccurredAt,
		[]trajectory.NodeRef{verifyRef, editRef, eventRef},
	)

	got, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns: []transcript.Turn{
			priorUser,
			edit,
			verify,
			result,
			followingUser,
			ruleAssistant,
			finalAssistant,
		},
		Edges:    []trajectory.Edge{verifies},
		Outcomes: []trajectory.Outcome{pass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 ||
		got.Candidates[0].Family != experience.CandidateSuccessfulProcedure ||
		got.Candidates[0].Proposal.Verifier.Kind != experience.VerifierCommandSucceeded {
		t.Fatalf("successful procedure candidates = %+v", got.Candidates)
	}
	if len(got.SuccessfulProcedureEpisodes) != 1 ||
		got.SuccessfulProcedureEpisodes[0].CandidateID !=
			got.Candidates[0].CandidateID ||
		got.SuccessfulProcedureEpisodes[0].Anchor.RawCommand !=
			"go test ./..." {
		t.Fatalf(
			"successful procedure episode = %+v",
			got.SuccessfulProcedureEpisodes,
		)
	}
	contextIndexes := map[int64]bool{}
	for _, ref := range got.Candidates[0].Evidence.Refs {
		if ref.TurnIndex != nil {
			contextIndexes[*ref.TurnIndex] = true
		}
	}
	for _, want := range []int64{0, 4, 5, 6} {
		if !contextIndexes[want] {
			t.Fatalf(
				"successful procedure evidence lacks context turn %d: %+v",
				want,
				got.Candidates[0].Evidence.Refs,
			)
		}
	}

	withoutMutationEdge, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{edit, verify, result},
		Outcomes:        []trajectory.Outcome{pass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutMutationEdge.Candidates) != 0 {
		t.Fatalf("success without verifies edge produced %+v", withoutMutationEdge.Candidates)
	}

	ordinaryCall := verify
	ordinaryCall.Payload.RawCommand = "echo done"
	ordinary, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{edit, ordinaryCall, result},
		Edges:           []trajectory.Edge{verifies},
		Outcomes:        []trajectory.Outcome{pass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary.Candidates) != 0 {
		t.Fatalf("ordinary command success produced %+v", ordinary.Candidates)
	}

	verifyAgain := candidateTurn("ses_success", 6, transcript.RoleToolCall)
	verifyAgain.Payload.ToolCallID = "verify_2"
	verifyAgain.Payload.RawCommand = "go test ./internal/..."
	resultAgain := candidateTurn("ses_success", 7, transcript.RoleToolResult)
	resultAgain.Payload.ToolCallID = "verify_2"
	resultAgain.Payload.ToolIsError = &notError
	resultAgain.Payload.ToolResult = "ok"
	continuationUser := candidateTurn(
		"ses_success",
		4,
		transcript.RoleUser,
	)
	continuationUser.Payload.Text =
		"Tests pass. Document the rollback rule, then rerun verification."
	editAgain := candidateTurn("ses_success", 5, transcript.RoleToolCall)
	editAgain.ToolName = "Edit"
	editAgain.Payload.ToolCallID = "edit_2"
	editAgain.Payload.ToolInput =
		json.RawMessage(`{"file_path":"internal/example.go"}`)
	editAgainRef := candidateTurnRef(editAgain)
	verifyAgainRef := candidateTurnRef(verifyAgain)
	resultAgainRef := candidateTurnRef(resultAgain)
	passAgain := candidateOutcome(
		testProject,
		"ses_success",
		trajectory.OutcomeVerificationPass,
		trajectory.ResultSucceeded,
		resultAgain.OccurredAt,
		[]trajectory.NodeRef{verifyAgainRef, resultAgainRef},
	)
	verifiesAgain := candidateEdge(
		testProject,
		"ses_success",
		verifyAgainRef,
		trajectory.RelationVerifies,
		eventRef,
		verifyAgain.OccurredAt,
		[]trajectory.NodeRef{verifyAgainRef, editAgainRef, eventRef},
	)
	grouped, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns: []transcript.Turn{
			edit,
			verify,
			result,
			continuationUser,
			editAgain,
			verifyAgain,
			resultAgain,
		},
		Edges:    []trajectory.Edge{verifies, verifiesAgain},
		Outcomes: []trajectory.Outcome{pass, passAgain},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Candidates) != 1 {
		t.Fatalf("shared mutation produced %+v", grouped.Candidates)
	}
	groupedCandidate := grouped.Candidates[0]
	if len(groupedCandidate.OutcomeRefs) != 2 ||
		groupedCandidate.Proposal.Verifier.Command == nil ||
		groupedCandidate.Proposal.Verifier.Command.Command !=
			"go test ./internal/..." {
		t.Fatalf("grouped candidate = %+v", groupedCandidate)
	}

	newTaskUser := continuationUser
	newTaskUser.Payload.Text = "Implement the next unrelated change."
	separate, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns: []transcript.Turn{
			edit,
			verify,
			result,
			newTaskUser,
			editAgain,
			verifyAgain,
			resultAgain,
		},
		Edges:    []trajectory.Edge{verifies, verifiesAgain},
		Outcomes: []trajectory.Outcome{pass, passAgain},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(separate.Candidates) != 2 {
		t.Fatalf("new task was incorrectly grouped: %+v", separate.Candidates)
	}
}

func TestCompileFailedApproachRequiresExplicitFailureAndRepair(t *testing.T) {
	failureCall := candidateTurn("ses_repair", 1, transcript.RoleToolCall)
	failureCall.Payload.ToolCallID = "failure_1"
	failureCall.Payload.RawCommand = "go test ./internal/old"
	failureResult := candidateTurn("ses_repair", 2, transcript.RoleToolResult)
	failureResult.Payload.ToolCallID = "failure_1"
	failed := 1
	failureResult.Payload.ExitCode = &failed
	failureResult.Payload.ToolResult =
		"failed to initialize build cache: permission denied"
	successCall := candidateTurn("ses_repair", 3, transcript.RoleToolCall)
	successCall.Payload.ToolCallID = "success_1"
	successCall.Payload.RawCommand = "env GOCACHE=/tmp/belay-cache go test ./internal/new"
	successResult := candidateTurn("ses_repair", 4, transcript.RoleToolResult)
	successResult.Payload.ToolCallID = "success_1"
	passed := 0
	successResult.Payload.ExitCode = &passed
	successResult.Payload.ToolResult = "ok"
	refs := []trajectory.NodeRef{
		candidateTurnRef(failureCall),
		candidateTurnRef(failureResult),
		candidateTurnRef(successCall),
		candidateTurnRef(successResult),
	}
	repair := candidateOutcome(
		testProject,
		"ses_repair",
		trajectory.OutcomeRepair,
		trajectory.ResultSucceeded,
		successResult.OccurredAt,
		refs,
	)
	repairEdge := candidateEdge(
		testProject,
		"ses_repair",
		refs[2],
		trajectory.RelationRepairs,
		refs[0],
		successResult.OccurredAt,
		refs,
	)

	got, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns: []transcript.Turn{
			failureCall,
			failureResult,
			successCall,
			successResult,
		},
		Edges:    []trajectory.Edge{repairEdge},
		Outcomes: []trajectory.Outcome{repair},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 ||
		got.Candidates[0].Family != experience.CandidateFailedApproach {
		t.Fatalf("failed approach candidates = %+v", got.Candidates)
	}
	if len(got.FailedApproachRecoveries) != 1 ||
		got.FailedApproachRecoveries[0].CandidateID !=
			got.Candidates[0].CandidateID ||
		got.FailedApproachRecoveries[0].FailureResult.TurnID !=
			failureResult.TurnID ||
		got.FailedApproachRecoveries[0].SuccessResult.TurnID !=
			successResult.TurnID {
		t.Fatalf(
			"failed approach recoveries = %+v",
			got.FailedApproachRecoveries,
		)
	}

	withoutRepair, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           []transcript.Turn{failureCall, failureResult},
		Outcomes: []trajectory.Outcome{candidateOutcome(
			testProject,
			"ses_repair",
			trajectory.OutcomeVerificationFail,
			trajectory.ResultFailed,
			failureResult.OccurredAt,
			refs[:2],
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutRepair.Candidates) != 0 {
		t.Fatalf("failure without repair produced %+v", withoutRepair.Candidates)
	}

	sameCommand := successCall
	sameCommand.Payload.RawCommand = failureCall.Payload.RawCommand
	sameCommandResult, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns: []transcript.Turn{
			failureCall,
			failureResult,
			sameCommand,
			successResult,
		},
		Edges:    []trajectory.Edge{repairEdge},
		Outcomes: []trajectory.Outcome{repair},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sameCommandResult.Candidates) != 0 {
		t.Fatalf("same-command retry produced %+v", sameCommandResult.Candidates)
	}

	unrelatedSuccess := successCall
	unrelatedSuccess.Payload.RawCommand =
		"wc -l report.txt && awk '{print $1}' report.txt && rg TODO ."
	unrelatedResult, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns: []transcript.Turn{
			failureCall,
			failureResult,
			unrelatedSuccess,
			successResult,
		},
		Edges:    []trajectory.Edge{repairEdge},
		Outcomes: []trajectory.Outcome{repair},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unrelatedResult.Candidates) != 0 {
		t.Fatalf(
			"unrelated successful command produced stale-evidence candidate %+v",
			unrelatedResult.Candidates,
		)
	}
}

func TestCompileIsDeterministicProjectIsolatedAndEvidenceBounded(t *testing.T) {
	verify := candidateTurn("ses_many", 100, transcript.RoleToolCall)
	verify.Payload.ToolCallID = "verify_many"
	verify.Payload.RawCommand = "go test ./..."
	result := candidateTurn("ses_many", 101, transcript.RoleToolResult)
	result.Payload.ToolCallID = "verify_many"
	exitCode := 0
	result.Payload.ExitCode = &exitCode
	verifyRef := candidateTurnRef(verify)
	resultRef := candidateTurnRef(result)
	pass := candidateOutcome(
		testProject,
		"ses_many",
		trajectory.OutcomeVerificationPass,
		trajectory.ResultSucceeded,
		result.OccurredAt,
		[]trajectory.NodeRef{verifyRef, resultRef},
	)
	turns := []transcript.Turn{verify, result}
	edges := make([]trajectory.Edge, 0, 40)
	for index := int64(0); index < 40; index++ {
		edit := candidateTurn("ses_many", index, transcript.RoleToolCall)
		edit.ToolName = "Edit"
		edit.Payload.ToolCallID = "edit_many"
		edit.Payload.ToolInput = json.RawMessage(`{"file_path":"internal/example.go"}`)
		turns = append(turns, edit)
		eventRef := trajectory.NodeRef{
			Kind:    trajectory.NodeCanonicalEvent,
			EventID: "evt_many_" + time.Unix(index, 0).UTC().Format("150405"),
		}
		edges = append(edges, candidateEdge(
			testProject,
			"ses_many",
			verifyRef,
			trajectory.RelationVerifies,
			eventRef,
			verify.OccurredAt,
			[]trajectory.NodeRef{verifyRef, candidateTurnRef(edit), eventRef},
		))
	}
	input := Input{
		ProjectIdentity: testProject,
		Turns:           turns,
		Edges:           edges,
		Outcomes:        []trajectory.Outcome{pass},
	}
	first, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("duplicate compile differs:\nfirst  %+v\nsecond %+v", first, second)
	}
	if len(first.Candidates) != 1 ||
		len(first.Candidates[0].Evidence.Refs) != maxCandidateEvidenceRefs {
		t.Fatalf("bounded candidate evidence = %+v", first.Candidates)
	}

	foreignEdges := append([]trajectory.Edge(nil), edges...)
	for index := range foreignEdges {
		foreignEdges[index].ProjectIdentity = "git@example.test:doplexlabs/other.git"
		foreignEdges[index].EdgeID = foreignEdges[index].DeterministicID()
	}
	foreignPass := pass
	foreignPass.ProjectIdentity = "git@example.test:doplexlabs/other.git"
	foreignPass.OutcomeID = foreignPass.DeterministicID()
	isolated, err := Compile(Input{
		ProjectIdentity: testProject,
		Turns:           turns,
		Edges:           foreignEdges,
		Outcomes:        []trajectory.Outcome{foreignPass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(isolated.Candidates) != 0 {
		t.Fatalf("foreign project produced %+v", isolated.Candidates)
	}
}

func candidateTurn(
	sessionKey string,
	index int64,
	role transcript.Role,
) transcript.Turn {
	return transcript.Turn{
		TurnID:     "turn_" + sessionKey + "_" + time.Unix(index, 0).UTC().Format("150405"),
		SessionKey: sessionKey,
		TurnIndex:  index,
		OccurredAt: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC).
			Add(time.Duration(index) * time.Second),
		Role: role,
	}
}

func candidateTurnRef(turn transcript.Turn) trajectory.NodeRef {
	index := turn.TurnIndex
	return trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: turn.SessionKey,
		TurnIndex:  &index,
	}
}

func candidateEdge(
	projectIdentity string,
	sessionKey string,
	from trajectory.NodeRef,
	relation trajectory.EdgeRelation,
	to trajectory.NodeRef,
	occurredAt time.Time,
	sourceRefs []trajectory.NodeRef,
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
		DerivationVersion: "belay.trajectory-derive.v4",
		SourceRefs:        sourceRefs,
		OccurredAt:        occurredAt,
	}
	value.EdgeID = value.DeterministicID()
	return value
}

func candidateOutcome(
	projectIdentity string,
	sessionKey string,
	kind trajectory.OutcomeKind,
	result trajectory.OutcomeResult,
	occurredAt time.Time,
	sourceRefs []trajectory.NodeRef,
) trajectory.Outcome {
	value := trajectory.Outcome{
		SchemaVersion:     trajectory.OutcomeSchemaVersion,
		ProjectIdentity:   projectIdentity,
		SessionKey:        sessionKey,
		OccurredAt:        occurredAt,
		Kind:              kind,
		Result:            result,
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		SourceRefs:        sourceRefs,
		DerivationVersion: "belay.trajectory-derive.v4",
	}
	value.OutcomeID = value.DeterministicID()
	return value
}
