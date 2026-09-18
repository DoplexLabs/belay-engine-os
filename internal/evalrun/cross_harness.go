// Package evalrun contains isolated, non-product evaluation scenarios for
// proving Belay's experience-learning contracts end to end.
package evalrun

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	crossHarnessProjectIdentity = "https://github.com/DoplexLabs/belay-cross-harness-eval.git"
	commandScrubbingVersion     = "belay.redaction.v1"
)

// CrossHarnessResult is the durable artifact for the controlled C5 proof.
// ExecutionMode is explicit so a controlled transcript can never be mistaken
// for an invocation of an installed harness.
type CrossHarnessResult struct {
	SchemaVersion            string                               `json:"schema_version"`
	ExecutionMode            string                               `json:"execution_mode"`
	SemanticMode             string                               `json:"semantic_mode"`
	SemanticModel            string                               `json:"semantic_model"`
	SemanticProposalPath     string                               `json:"semantic_proposal_path,omitempty"`
	ApprovalMode             experience.ApprovalMode              `json:"approval_mode"`
	ProposedInstruction      string                               `json:"proposed_instruction"`
	ApprovedInstruction      string                               `json:"approved_instruction"`
	HarnessEventsPath        string                               `json:"harness_events_path,omitempty"`
	ProjectPath              string                               `json:"project_path"`
	ProjectIdentity          string                               `json:"project_identity"`
	SourceHarness            experience.Harness                   `json:"source_harness"`
	DestinationHarness       experience.Harness                   `json:"destination_harness"`
	SourceSession            string                               `json:"source_session"`
	DestinationSession       string                               `json:"destination_session"`
	CandidateID              string                               `json:"candidate_id"`
	ProposalID               string                               `json:"proposal_id"`
	Experience               experience.ExperienceRef             `json:"experience"`
	Instruction              string                               `json:"instruction"`
	PackID                   string                               `json:"pack_id"`
	ReceiptID                string                               `json:"receipt_id"`
	ReceiptState             local.ReceiptBindingState            `json:"receipt_state"`
	Status                   string                               `json:"status"`
	OpportunityState         experience.OpportunityState          `json:"opportunity_state"`
	ApplicabilityState       experience.ApplicabilityState        `json:"applicability_state"`
	VerifierState            experience.VerifierState             `json:"verifier_state"`
	TaskOutcomeState         experience.TaskOutcomeState          `json:"task_outcome_state"`
	Evidence                 []localapp.MissionPackStatusEvidence `json:"evidence"`
	PostPauseExperienceCount int                                  `json:"post_pause_experience_count"`
	CompletedAt              time.Time                            `json:"completed_at"`
}

type CrossHarnessOptions struct {
	RealClaude      bool
	RealCodex       bool
	CodexExecutable string
}

// RunControlledCrossHarnessProof exercises the production storage and service
// contracts in a disposable directory. It does not invoke Claude or Codex and
// does not read or mutate the user's Belay store.
func RunControlledCrossHarnessProof(
	ctx context.Context,
	root string,
) (CrossHarnessResult, error) {
	return RunCrossHarnessProof(ctx, root, CrossHarnessOptions{})
}

// RunCrossHarnessProof optionally invokes an installed Codex in ephemeral
// mode for the destination session. The source correction and semantic
// proposal remain controlled fixtures until the separate Claude-origin leg is
// enabled.
func RunCrossHarnessProof(
	ctx context.Context,
	root string,
	options CrossHarnessOptions,
) (CrossHarnessResult, error) {
	if ctx == nil {
		return CrossHarnessResult{}, errors.New("cross-harness proof requires context")
	}
	if root == "" {
		return CrossHarnessResult{}, errors.New("cross-harness proof requires a root")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("resolve proof root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("create proof root: %w", err)
	}
	projectPath := filepath.Join(root, "project")
	if err := initializeProject(ctx, projectPath); err != nil {
		return CrossHarnessResult{}, err
	}

	store, err := local.Open(
		filepath.Join(root, "belay.sqlite"),
		&ephemeralKeyProvider{},
	)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("open isolated Belay store: %w", err)
	}
	defer store.Close()

	now := time.Now().UTC().Add(-10 * time.Second).Round(0)
	sourceSession, sourceTurns := correctionTranscript(projectPath, now)
	if _, err := store.AppendTranscriptBatch(ctx, sourceSession, sourceTurns); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("retain Claude correction transcript: %w", err)
	}
	if _, err := localapp.AnalyzeTrajectorySessionsOnce(ctx, store, 10); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("derive correction trajectory: %w", err)
	}
	compilation, err := localapp.CompileProjectExperienceCandidatesOnce(
		ctx,
		store,
		crossHarnessProjectIdentity,
	)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("compile correction candidate: %w", err)
	}
	if compilation.CandidatesInserted != 1 {
		return CrossHarnessResult{}, fmt.Errorf(
			"compile correction candidate: inserted %d, want 1",
			compilation.CandidatesInserted,
		)
	}
	candidates, err := store.QueryExperienceCandidates(
		ctx,
		crossHarnessProjectIdentity,
		10,
	)
	if err != nil || len(candidates) != 1 {
		return CrossHarnessResult{}, fmt.Errorf(
			"load correction candidate: count %d: %w",
			len(candidates),
			err,
		)
	}
	candidate := candidates[0]
	semanticMode := "controlled_fixture"
	var proposal experience.SemanticProposal
	if options.RealClaude {
		semanticMode = "real_claude_headless"
		report, err := localapp.AnalyzeExperienceCandidates(
			ctx,
			store,
			crossHarnessProjectIdentity,
			localapp.SemanticHarnessClaude,
			localapp.RunInstalledExperienceSemanticHarness,
		)
		if err != nil {
			return CrossHarnessResult{}, fmt.Errorf(
				"analyze correction with installed Claude: %w",
				err,
			)
		}
		if report.ProposalsInserted != 1 {
			return CrossHarnessResult{}, fmt.Errorf(
				"installed Claude did not produce one proposal: %+v",
				report,
			)
		}
		proposals, err := store.QueryExperienceSemanticProposals(
			ctx,
			crossHarnessProjectIdentity,
			10,
		)
		if err != nil {
			return CrossHarnessResult{}, fmt.Errorf(
				"load Claude semantic proposal: %w",
				err,
			)
		}
		for _, value := range proposals {
			if value.CandidateID == candidate.CandidateID &&
				value.Provenance.Harness == experience.HarnessClaude &&
				value.Provenance.PromptVersion ==
					experience.SemanticProposalPromptVersion {
				proposal = value
				break
			}
		}
		if proposal.ProposalID == "" {
			return CrossHarnessResult{}, errors.New(
				"installed Claude proposal was not persisted",
			)
		}
	} else {
		proposal, err = controlledSemanticProposal(candidate, now.Add(time.Second))
		if err != nil {
			return CrossHarnessResult{}, err
		}
		inserted, replayed, err := store.InsertExperienceSemanticProposals(
			ctx,
			[]experience.SemanticProposal{proposal},
		)
		if err != nil || inserted != 1 || replayed != 0 {
			return CrossHarnessResult{}, fmt.Errorf(
				"persist semantic proposal: inserted=%d replayed=%d: %w",
				inserted,
				replayed,
				err,
			)
		}
	}
	proposalPath := filepath.Join(root, "semantic-proposal.json")
	proposalJSON, err := json.MarshalIndent(proposal, "", "  ")
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf(
			"encode semantic proposal artifact: %w",
			err,
		)
	}
	if err := os.WriteFile(
		proposalPath,
		append(proposalJSON, '\n'),
		0o600,
	); err != nil {
		return CrossHarnessResult{}, fmt.Errorf(
			"write semantic proposal artifact: %w",
			err,
		)
	}

	approval, err := localapp.NewExperienceApprovalService(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	approvalPreview, err := approval.Prepare(ctx, proposal.ProposalID)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("prepare explicit approval: %w", err)
	}
	approvalMode := experience.ApprovalAsProposed
	var approved local.ExperienceApprovalResult
	if options.RealClaude {
		approvalMode = experience.ApprovalUserEdited
		approved, err = approval.ApproveWithContent(
			ctx,
			localapp.ExperienceApprovalWithContentRequest{
				ProposalID:  proposal.ProposalID,
				ActionToken: approvalPreview.ActionToken,
				Actor:       "c5_evaluation_user",
				Mode:        approvalMode,
				ApprovedContent: reviewedCrossHarnessProposal(
					proposal.Proposal,
				),
			},
		)
	} else {
		approved, err = approval.ApproveAsProposed(
			ctx,
			proposal.ProposalID,
			approvalPreview.ActionToken,
			"c5_evaluation_user",
		)
	}
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("approve semantic proposal: %w", err)
	}
	ref := experience.ExperienceRef{
		ExperienceID: approved.Experience.ExperienceID,
		Version:      approved.Experience.Version,
	}

	lifecycle, err := localapp.NewExperienceLifecycleService(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	activation, err := lifecycle.Prepare(
		ctx,
		ref,
		experience.LifecycleActionActivate,
	)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("prepare activation: %w", err)
	}
	if _, err := lifecycle.Activate(
		ctx,
		ref,
		activation.ActionToken,
		"c5_evaluation_user",
	); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("activate experience: %w", err)
	}

	compiler, err := localapp.NewExperienceCompilerService(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	if _, err := compiler.Compile(ctx, crossHarnessProjectIdentity); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("compile active generation: %w", err)
	}
	packs, err := localapp.NewMissionPackService(
		store,
		localapp.WithMissionPackExperienceSelector(compiler),
		localapp.WithMissionPackPreviewRepository(store),
	)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	pack, err := packs.Generate(ctx, missionpack.Request{
		CWD:         projectPath,
		Intent:      missionpack.IntentImplement,
		Harness:     missionpack.HarnessCodex,
		TaskHint:    "Run the project verification command before reporting completion.",
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("generate Codex Mission Pack: %w", err)
	}
	if len(pack.Experiences) != 1 {
		return CrossHarnessResult{}, fmt.Errorf(
			"generate Codex Mission Pack: experience count %d, want 1",
			len(pack.Experiences),
		)
	}
	acceptance, err := localapp.NewMissionPackAcceptanceService(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	accepted, err := acceptance.Accept(ctx, pack.PackID)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("accept Codex Mission Pack: %w", err)
	}
	receipt, err := store.GetMissionPackReceipt(ctx, accepted.ReceiptID)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("load accepted Codex receipt: %w", err)
	}
	executionMode := "controlled_transcripts"
	harnessEventsPath := ""
	var destinationSession transcript.Session
	var destinationTurns []transcript.Turn
	if options.RealCodex {
		executionMode = "real_codex_ephemeral"
		destinationSession, destinationTurns, harnessEventsPath, err =
			runRealCodexVerification(
				ctx,
				root,
				projectPath,
				pack.Experiences[0].Guidance,
				receipt.AcceptedAt,
				options.CodexExecutable,
			)
		if err != nil {
			return CrossHarnessResult{}, err
		}
	} else {
		destinationSession, destinationTurns = verificationTranscript(
			projectPath,
			receipt.AcceptedAt.Add(time.Millisecond),
		)
	}
	if _, err := store.AppendTranscriptBatch(
		ctx,
		destinationSession,
		destinationTurns,
	); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("retain Codex proof transcript: %w", err)
	}
	if _, err := localapp.AnalyzeTrajectorySessionsOnce(ctx, store, 10); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("derive Codex proof trajectory: %w", err)
	}
	reconciler, err := localapp.NewMissionPackReceiptReconciliationCoordinator(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	if err := reconciler.Reconcile(ctx, time.Now().UTC().Add(2*time.Second), 10); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("bind receipt to Codex session: %w", err)
	}
	materializer, err := localapp.NewMissionPackApplicationMaterializationCoordinator(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	materialized, err := materializer.Materialize(ctx, 10)
	if err != nil || materialized.ApplicationsInserted != 1 {
		return CrossHarnessResult{}, fmt.Errorf(
			"materialize delivered experience: inserted=%d: %w",
			materialized.ApplicationsInserted,
			err,
		)
	}
	evaluator, err := localapp.NewExperienceEvaluationCoordinator(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	evaluated, err := evaluator.Evaluate(ctx, 10)
	if err != nil || evaluated.Applied != 1 {
		return CrossHarnessResult{}, fmt.Errorf(
			"evaluate delivered experience: applied=%d deferred=%d: %w",
			evaluated.Applied,
			evaluated.Deferred,
			err,
		)
	}
	statusService, err := localapp.NewMissionPackStatusService(store)
	if err != nil {
		return CrossHarnessResult{}, err
	}
	status, err := statusService.Get(ctx, accepted.ReceiptID)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("read exact proof status: %w", err)
	}
	if status.ReceiptState != local.ReceiptBound ||
		status.BoundSession != destinationSession.SessionKey ||
		len(status.Items) != 1 ||
		status.Items[0].Status != localapp.MissionPackItemEvaluated ||
		status.Items[0].VerifierState != experience.VerifierSatisfied {
		return CrossHarnessResult{}, fmt.Errorf("unexpected proof status: %+v", status)
	}

	pause, err := lifecycle.Prepare(ctx, ref, experience.LifecycleActionPause)
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("prepare pause: %w", err)
	}
	if _, err := lifecycle.Pause(
		ctx,
		ref,
		pause.ActionToken,
		"c5_evaluation_user",
	); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("pause experience: %w", err)
	}
	if _, err := compiler.Compile(ctx, crossHarnessProjectIdentity); err != nil {
		return CrossHarnessResult{}, fmt.Errorf("compile post-pause generation: %w", err)
	}
	postPause, err := packs.Generate(ctx, missionpack.Request{
		CWD:         projectPath,
		Intent:      missionpack.IntentImplement,
		Harness:     missionpack.HarnessCodex,
		TaskHint:    "Run the project verification command before reporting completion.",
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		return CrossHarnessResult{}, fmt.Errorf("generate post-pause Mission Pack: %w", err)
	}
	item := status.Items[0]
	return CrossHarnessResult{
		SchemaVersion:            "belay.cross-harness-proof.v1",
		ExecutionMode:            executionMode,
		SemanticMode:             semanticMode,
		SemanticModel:            proposal.Provenance.Model,
		SemanticProposalPath:     proposalPath,
		ApprovalMode:             approvalMode,
		ProposedInstruction:      proposal.Proposal.Guidance.Instruction,
		ApprovedInstruction:      approved.Experience.Guidance.Instruction,
		HarnessEventsPath:        harnessEventsPath,
		ProjectPath:              projectPath,
		ProjectIdentity:          crossHarnessProjectIdentity,
		SourceHarness:            experience.HarnessClaude,
		DestinationHarness:       experience.HarnessCodex,
		SourceSession:            sourceSession.SessionKey,
		DestinationSession:       destinationSession.SessionKey,
		CandidateID:              candidate.CandidateID,
		ProposalID:               proposal.ProposalID,
		Experience:               ref,
		Instruction:              approved.Experience.Guidance.Instruction,
		PackID:                   pack.PackID,
		ReceiptID:                accepted.ReceiptID,
		ReceiptState:             status.ReceiptState,
		Status:                   item.Status,
		OpportunityState:         item.OpportunityState,
		ApplicabilityState:       item.ApplicabilityState,
		VerifierState:            item.VerifierState,
		TaskOutcomeState:         item.TaskOutcomeState,
		Evidence:                 item.Evidence,
		PostPauseExperienceCount: len(postPause.Experiences),
		CompletedAt:              maxTime(time.Now().UTC().Round(0), destinationSession.EndedAt),
	}, nil
}

func reviewedCrossHarnessProposal(
	proposal experience.ExperienceProposal,
) experience.ExperienceProposal {
	proposal.Scope = experience.Scope{
		Kind:            experience.ScopeProject,
		ProjectIdentity: crossHarnessProjectIdentity,
		Harnesses:       []experience.Harness{experience.HarnessCodex},
	}
	proposal.Applicability.DeterministicConditions = nil
	proposal.Applicability.Exclusions = nil
	proposal.Applicability.ExpiresAt = nil
	proposal.Verifier = experience.Verifier{
		Kind: experience.VerifierCommandSucceeded,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		Command: &experience.CommandVerifierSpec{
			Command:          "go test ./...",
			CommandClass:     "test",
			ScrubbingVersion: commandScrubbingVersion,
		},
	}
	proposal.Guidance.InterventionStrength =
		experience.InterventionRequireVerification
	proposal.SemanticCompilationPending = false
	return proposal
}

func runRealCodexVerification(
	ctx context.Context,
	root string,
	projectPath string,
	guidance string,
	acceptedAt time.Time,
	executable string,
) (transcript.Session, []transcript.Turn, string, error) {
	if strings.TrimSpace(executable) == "" {
		executable = "codex"
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return transcript.Session{}, nil, "", fmt.Errorf(
			"resolve installed Codex executable: %w",
			err,
		)
	}
	prompt := strings.Join([]string{
		"Mission Pack guidance: " + strings.TrimSpace(guidance),
		"Task: verify this repository.",
		"Do not modify any files.",
		"Run the required command and report the exact command and whether it passed.",
	}, "\n")
	command := exec.CommandContext(
		ctx,
		resolved,
		"exec",
		"--json",
		"--ephemeral",
		"--approve-for-me",
		"--ignore-rules",
		"-C",
		projectPath,
		prompt,
	)
	command.Dir = projectPath
	command.Env = append(
		os.Environ(),
		"GOCACHE="+filepath.Join(root, "go-cache"),
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return transcript.Session{}, nil, "", fmt.Errorf(
			"run ephemeral Codex proof: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	eventsPath := filepath.Join(root, "codex-events.jsonl")
	if err := os.WriteFile(eventsPath, stdout.Bytes(), 0o600); err != nil {
		return transcript.Session{}, nil, "", fmt.Errorf(
			"write Codex proof events: %w",
			err,
		)
	}
	session, turns, err := codexEventsTranscript(
		projectPath,
		acceptedAt.Add(time.Millisecond),
		stdout.Bytes(),
	)
	if err != nil {
		return transcript.Session{}, nil, "", err
	}
	return session, turns, eventsPath, nil
}

func codexEventsTranscript(
	projectPath string,
	base time.Time,
	body []byte,
) (transcript.Session, []transcript.Turn, error) {
	const sessionKey = "ses_c5_codex_real"
	type eventItem struct {
		ID               string `json:"id"`
		Type             string `json:"type"`
		Text             string `json:"text"`
		Command          string `json:"command"`
		AggregatedOutput string `json:"aggregated_output"`
		ExitCode         *int   `json:"exit_code"`
	}
	type eventUsage struct {
		InputTokens           int64 `json:"input_tokens"`
		CachedInputTokens     int64 `json:"cached_input_tokens"`
		CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
	}
	type eventEnvelope struct {
		Type     string     `json:"type"`
		ThreadID string     `json:"thread_id"`
		Item     *eventItem `json:"item"`
		Usage    eventUsage `json:"usage"`
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	turns := make([]transcript.Turn, 0)
	var nativeSessionID string
	var inputTokens, outputTokens, cacheWriteTokens int64
	nextIndex := int64(0)
	nextTime := base
	for scanner.Scan() {
		var event eventEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return transcript.Session{}, nil, fmt.Errorf(
				"decode Codex proof event: %w",
				err,
			)
		}
		if event.Type == "thread.started" {
			nativeSessionID = strings.TrimSpace(event.ThreadID)
		}
		if event.Type == "turn.completed" {
			inputTokens = event.Usage.InputTokens
			outputTokens = event.Usage.OutputTokens
			cacheWriteTokens = event.Usage.CacheWriteInputTokens
		}
		if event.Type != "item.completed" || event.Item == nil {
			continue
		}
		switch event.Item.Type {
		case "agent_message":
			if strings.TrimSpace(event.Item.Text) == "" {
				continue
			}
			turns = append(turns, evaluationTurn(
				sessionKey,
				nextIndex,
				nextTime,
				transcript.RoleAssistant,
				transcript.Payload{Text: event.Item.Text},
			))
			nextIndex++
			nextTime = nextTime.Add(time.Millisecond)
		case "command_execution":
			if !strings.Contains(event.Item.Command, "go test ./...") {
				continue
			}
			callID := strings.TrimSpace(event.Item.ID)
			if callID == "" {
				callID = fmt.Sprintf("codex-command-%d", nextIndex)
			}
			turns = append(turns, evaluationTurn(
				sessionKey,
				nextIndex,
				nextTime,
				transcript.RoleToolCall,
				transcript.Payload{
					RawCommand: "go test ./...",
					ToolCallID: callID,
				},
			))
			nextIndex++
			nextTime = nextTime.Add(time.Millisecond)
			turns = append(turns, evaluationTurn(
				sessionKey,
				nextIndex,
				nextTime,
				transcript.RoleToolResult,
				transcript.Payload{
					ToolResult: event.Item.AggregatedOutput,
					ToolCallID: callID,
					ExitCode:   event.Item.ExitCode,
				},
			))
			nextIndex++
			nextTime = nextTime.Add(time.Millisecond)
		}
	}
	if err := scanner.Err(); err != nil {
		return transcript.Session{}, nil, fmt.Errorf(
			"scan Codex proof events: %w",
			err,
		)
	}
	if nativeSessionID == "" {
		return transcript.Session{}, nil, errors.New(
			"Codex proof did not return a thread identity",
		)
	}
	success := false
	for _, turn := range turns {
		if turn.Role == transcript.RoleToolResult &&
			turn.Payload.ExitCode != nil &&
			*turn.Payload.ExitCode == 0 {
			success = true
			break
		}
	}
	if !success {
		return transcript.Session{}, nil, errors.New(
			"Codex proof did not return a successful go test result",
		)
	}
	endedAt := base
	if len(turns) > 0 {
		endedAt = turns[len(turns)-1].OccurredAt
	}
	totalTokens := inputTokens + outputTokens
	return transcript.Session{
		SessionKey:            sessionKey,
		Agent:                 "codex",
		NativeSessionID:       nativeSessionID,
		ProjectPath:           projectPath,
		GitRemoteURL:          crossHarnessProjectIdentity,
		ProjectIdentity:       crossHarnessProjectIdentity,
		StartedAt:             base,
		EndedAt:               endedAt,
		WallDurationMS:        endedAt.Sub(base).Milliseconds(),
		TotalInputTokens:      int64Pointer(inputTokens),
		TotalOutputTokens:     int64Pointer(outputTokens),
		TotalTokens:           int64Pointer(totalTokens),
		TotalCacheWriteTokens: int64Pointer(cacheWriteTokens),
		TurnCount:             len(turns),
		AssistantTurnCount:    countRole(turns, transcript.RoleAssistant),
		ToolCallCount:         countRole(turns, transcript.RoleToolCall),
		ToolResultCount:       countRole(turns, transcript.RoleToolResult),
		Coverage:              transcript.CoverageComplete,
	}, turns, nil
}

func int64Pointer(value int64) *int64 {
	return &value
}

func countRole(turns []transcript.Turn, role transcript.Role) int {
	count := 0
	for _, turn := range turns {
		if turn.Role == role {
			count++
		}
	}
	return count
}

func maxTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func initializeProject(ctx context.Context, path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create evaluation project: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(path, "go.mod"),
		[]byte("module example.com/belay-cross-harness-eval\n\ngo 1.24\n"),
		0o600,
	); err != nil {
		return fmt.Errorf("write evaluation go.mod: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(path, "main_test.go"),
		[]byte("package eval\n\nimport \"testing\"\n\nfunc TestProof(t *testing.T) {}\n"),
		0o600,
	); err != nil {
		return fmt.Errorf("write evaluation test: %w", err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"remote", "add", "origin", crossHarnessProjectIdentity},
	} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = path
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf(
				"initialize evaluation git project (%v): %w: %s",
				args,
				err,
				output,
			)
		}
	}
	return nil
}

func correctionTranscript(
	projectPath string,
	base time.Time,
) (transcript.Session, []transcript.Turn) {
	const sessionKey = "ses_c5_claude_correction"
	turns := []transcript.Turn{
		evaluationTurn(
			sessionKey,
			0,
			base,
			transcript.RoleAssistant,
			transcript.Payload{
				Text: "The implementation is complete.",
			},
		),
		evaluationTurn(
			sessionKey,
			1,
			base.Add(time.Second),
			transcript.RoleUser,
			transcript.Payload{
				Text: "No, run go test ./... before saying the work is complete.",
			},
		),
	}
	return transcript.Session{
		SessionKey:         sessionKey,
		Agent:              "claude",
		NativeSessionID:    "c5-claude-correction",
		ProjectPath:        projectPath,
		GitRemoteURL:       crossHarnessProjectIdentity,
		ProjectIdentity:    crossHarnessProjectIdentity,
		StartedAt:          base,
		EndedAt:            base.Add(time.Second),
		WallDurationMS:     1000,
		TurnCount:          len(turns),
		UserTurnCount:      1,
		AssistantTurnCount: 1,
		Coverage:           transcript.CoverageComplete,
	}, turns
}

func verificationTranscript(
	projectPath string,
	base time.Time,
) (transcript.Session, []transcript.Turn) {
	const (
		sessionKey = "ses_c5_codex_fresh"
		callID     = "call_c5_go_test"
	)
	exitCode := 0
	durationMS := int64(250)
	turns := []transcript.Turn{
		evaluationTurn(
			sessionKey,
			0,
			base,
			transcript.RoleToolCall,
			transcript.Payload{
				RawCommand: "go test ./...",
				ToolCallID: callID,
			},
		),
		evaluationTurn(
			sessionKey,
			1,
			base.Add(time.Second),
			transcript.RoleToolResult,
			transcript.Payload{
				ToolResult: "ok example.com/belay-cross-harness-eval",
				ToolCallID: callID,
				ExitCode:   &exitCode,
				DurationMS: &durationMS,
			},
		),
		evaluationTurn(
			sessionKey,
			2,
			base.Add(2*time.Second),
			transcript.RoleAssistant,
			transcript.Payload{
				Text: "Implemented and verified with go test ./....",
			},
		),
	}
	return transcript.Session{
		SessionKey:         sessionKey,
		Agent:              "codex",
		NativeSessionID:    "c5-codex-fresh",
		ProjectPath:        projectPath,
		GitRemoteURL:       crossHarnessProjectIdentity,
		ProjectIdentity:    crossHarnessProjectIdentity,
		StartedAt:          base,
		EndedAt:            base.Add(2 * time.Second),
		WallDurationMS:     2000,
		TurnCount:          len(turns),
		AssistantTurnCount: 1,
		ToolCallCount:      1,
		ToolResultCount:    1,
		Coverage:           transcript.CoverageComplete,
	}, turns
}

func evaluationTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	role transcript.Role,
	payload transcript.Payload,
) transcript.Turn {
	return transcript.Turn{
		TurnID:          fmt.Sprintf("trn_%s_%d", sessionKey, index),
		SourceRecordKey: fmt.Sprintf("controlled:%s:%d", sessionKey, index),
		SessionKey:      sessionKey,
		TurnIndex:       index,
		OccurredAt:      occurredAt,
		Role:            role,
		Payload:         payload,
	}
}

func controlledSemanticProposal(
	candidate experience.Candidate,
	generatedAt time.Time,
) (experience.SemanticProposal, error) {
	confidence := 1.0
	proposal := candidate.Proposal
	proposal.Type = experience.ExperienceProcedure
	proposal.Scope = experience.Scope{
		Kind:            experience.ScopeProject,
		ProjectIdentity: candidate.ProjectIdentity,
		Harnesses:       []experience.Harness{experience.HarnessCodex},
	}
	proposal.Applicability = experience.Applicability{
		SemanticDescription: "Implementation tasks in this project that may be reported complete.",
	}
	proposal.Guidance = experience.Guidance{
		Instruction:          "Run go test ./... before reporting implementation work complete.",
		Rationale:            "The user explicitly corrected an unverified completion claim.",
		InterventionStrength: experience.InterventionRequireVerification,
	}
	proposal.Verifier = experience.Verifier{
		Kind: experience.VerifierCommandSucceeded,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		Command: &experience.CommandVerifierSpec{
			Command:          "go test ./...",
			CommandClass:     "test",
			ScrubbingVersion: commandScrubbingVersion,
		},
	}
	proposal.Confidence = &confidence
	proposal.SemanticCompilationPending = false
	inputHash := prefixedSHA256([]byte(candidate.CandidateID))
	output, err := json.Marshal(proposal)
	if err != nil {
		return experience.SemanticProposal{}, fmt.Errorf("encode controlled semantic proposal: %w", err)
	}
	value := experience.SemanticProposal{
		SchemaVersion:   experience.SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        proposal,
		Provenance: experience.SemanticProposalProvenance{
			Harness:       experience.HarnessClaude,
			Model:         "controlled-c5-fixture",
			PromptVersion: experience.SemanticProposalPromptVersion,
			InputHash:     inputHash,
			OutputHash:    prefixedSHA256(output),
			GeneratedAt:   generatedAt,
		},
		Authority: experience.AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		return experience.SemanticProposal{}, fmt.Errorf(
			"validate controlled semantic proposal: %w",
			err,
		)
	}
	return value, nil
}

func prefixedSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type ephemeralKeyProvider struct {
	key []byte
}

func (provider *ephemeralKeyProvider) Load(
	_ context.Context,
	_ string,
) ([]byte, error) {
	if len(provider.key) == 0 {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *ephemeralKeyProvider) Create(
	_ context.Context,
	_ string,
) ([]byte, error) {
	if len(provider.key) != 0 {
		return nil, local.ErrKeyAlreadyExists
	}
	provider.key = make([]byte, 32)
	if _, err := rand.Read(provider.key); err != nil {
		return nil, errors.New("create ephemeral evaluation key")
	}
	return append([]byte(nil), provider.key...), nil
}
