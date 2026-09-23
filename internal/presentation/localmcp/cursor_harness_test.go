package localmcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
)

// buildingMissionPackService runs the real Mission Pack builder so the tool
// test exercises cross-harness target adaptation end to end.
type buildingMissionPackService struct {
	input   missionpack.BuildInput
	request missionpack.Request
}

func (s *buildingMissionPackService) Generate(
	_ context.Context,
	request missionpack.Request,
) (missionpack.Pack, error) {
	s.request = request
	input := s.input
	input.Request = request
	input.Request.GeneratedAt = s.input.Request.GeneratedAt
	return missionpack.Build(input)
}

func cursorBuildInput(now time.Time) missionpack.BuildInput {
	usd := 12.0
	issue := issueintel.Issue{
		IssueID:      "issue_cursor_tool",
		DetectorID:   issueintel.DetectorRetryLoop,
		SessionCount: 3,
		LastSeen:     now,
		Cost:         issueintel.Cost{WastedUSD: &usd},
		Project: issueintel.Project{
			Identity: "git@example.test:team/project.git",
		},
		Excerpts: []issueintel.Excerpt{{
			Text: "verbatim evidence",
			Citation: issueintel.Citation{
				SessionKey:      "ses_cursor",
				TurnIndex:       4,
				OccurredAt:      now,
				SourceFileID:    "source_cursor",
				JSONLByteOffset: 128,
			},
		}},
	}
	return missionpack.BuildInput{
		Request: missionpack.Request{
			Intent:      missionpack.IntentImplement,
			TaskHint:    "verification",
			GeneratedAt: now,
		},
		Project: missionpack.ResolvedProject{
			Identity:     "git@example.test:team/project.git",
			IdentityKind: "remote",
			Label:        "project",
		},
		Workspace: missionpack.WorkspaceSnapshot{
			Branch:    "feature/cursor",
			Worktree:  "project-worktree",
			Harnesses: []string{"cursor"},
		},
		SourceState: missionpack.SourceState{
			TranscriptGeneration: 7,
			AnalyzedGeneration:   7,
			AnalysisStatus:       missionpack.AnalysisStatusCurrent,
			DataThrough:          now,
		},
		Candidates: make(map[string]issueintel.CorrectionCandidate),
		Coverage: missionpack.Coverage{
			TranscriptStatus:          missionpack.TranscriptCoverageComplete,
			CanonicalContextAvailable: true,
		},
		Issues: []issueintel.Issue{issue},
		Insight: &issueintel.InsightRecord{
			InsightID:   "insight_cursor_tool",
			Harness:     "claude",
			GeneratedAt: now,
			Result: issueintel.InsightResult{
				Fixes: []issueintel.InsightFix{{
					IssueID:    issue.IssueID,
					RuleText:   "Run verification after the final edit.",
					TargetFile: "CLAUDE.md",
					Confidence: 0.95,
				}},
			},
		},
	}
}

func TestMissionPackToolBuildsCursorCompatiblePack(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	service := &buildingMissionPackService{input: cursorBuildInput(now)}
	session := newMissionPackTestClient(t, service)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd":       "/tmp/example",
		"harness":   "cursor",
		"intent":    "implement",
		"task_hint": "verification",
	})
	if result.IsError {
		t.Fatalf("Cursor get_mission_pack failed: %v", result.Content)
	}
	if service.request.Harness != missionpack.HarnessCursor {
		t.Fatalf("request harness = %q", service.request.Harness)
	}
	readModel := asObject(t, asObject(t, result.StructuredContent)["readmodel"])
	if readModel["harness"] != "cursor" {
		t.Fatalf("pack harness = %#v", readModel["harness"])
	}
	rules, ok := readModel["operating_rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("operating rules = %#v", readModel["operating_rules"])
	}
	rule := asObject(t, rules[0])
	if rule["target_file"] != "AGENTS.md" {
		t.Fatalf("Cursor operating rule target = %#v", rule["target_file"])
	}
	markdown, _ := readModel["rendered_markdown"].(string)
	if markdown == "" {
		t.Fatal("Cursor pack rendered no markdown")
	}
	for _, forbidden := range []string{
		"CLAUDE.md",
		".claude/",
		".codex/",
	} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf(
				"Cursor pack markdown names %q: %s",
				forbidden,
				markdown,
			)
		}
	}
}

func TestMissionPackToolSuppressesClaudeOnlyTargetForCursor(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	input := cursorBuildInput(now)
	input.Insight.Result.Fixes[0].TargetFile = ".claude/settings.json"
	input.Insight.Result.Fixes[0].RuleText = "Allow the verified command."
	service := &buildingMissionPackService{input: input}
	session := newMissionPackTestClient(t, service)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd":     "/tmp/example",
		"harness": "cursor",
		"intent":  "implement",
	})
	if result.IsError {
		t.Fatalf("Cursor get_mission_pack failed: %v", result.Content)
	}
	readModel := asObject(t, asObject(t, result.StructuredContent)["readmodel"])
	if rules, ok := readModel["operating_rules"].([]any); ok &&
		len(rules) != 0 {
		t.Fatalf("Cursor pack kept Claude-only rule: %#v", rules)
	}
}

func TestExperienceLearningToolsAcceptCursorHarness(t *testing.T) {
	now := testExperienceLearningTime()
	turn := int64(7)
	proposal := testExperienceProposal()
	proposal.Scope.Harnesses = []experience.Harness{
		experience.HarnessCursor,
	}
	service := &testExperienceLearningService{
		listResult: localapp.ExperienceLearningListResult{
			ProjectIdentity: proposal.Scope.ProjectIdentity,
			Items: []localapp.ExperienceApprovalPreview{{
				Review: localapp.ExperienceCandidateReview{
					ProposalID:      "exs_cursor_proposal",
					ProjectIdentity: proposal.Scope.ProjectIdentity,
					Family:          experience.CandidateCorrection,
					Proposed:        proposal,
					Evidence: []experience.EvidenceRef{{
						Excerpt:    "verbatim cursor excerpt",
						SessionKey: "ses_cursor",
						TurnIndex:  &turn,
					}},
					SemanticProvenance: experience.SemanticProposalProvenance{
						Harness: experience.HarnessCursor,
					},
					Conflict: experience.ConflictAdvisory{
						State: experience.ConflictNoConflict,
						ReasonCodes: []experience.ConflictReasonCode{
							experience.ConflictReasonNoActiveExperiences,
						},
					},
					Authority: experience.AuthorityNone,
				},
				ActionToken: "approval-token",
				ExpiresAt:   now.Add(15 * time.Minute),
			}},
		},
		approval: ExperienceLearningApprovalResult{
			Experience: testApprovedExperience(t),
		},
		activeResult: localapp.ExperienceLearningActiveListResult{
			ProjectIdentity: proposal.Scope.ProjectIdentity,
			Items: []localapp.ExperienceLearningActiveItem{{
				Experience: experience.ExperienceRef{
					ExperienceID: "exp_cursor_active",
					Version:      2,
				},
				Instruction: "Run the focused verification after editing.",
				Scope: localapp.ExperienceLearningScope{
					Kind:            experience.ScopeProject,
					RepositoryPaths: []string{},
					TaskFamilies:    []string{},
					Harnesses: []experience.Harness{
						experience.HarnessCursor,
					},
					Models: []string{},
				},
				Applicability: localapp.ExperienceLearningApplicability{
					Description:             "Implementation work in Cursor.",
					DeterministicConditions: []experience.DeterministicCondition{},
					Exclusions:              []string{},
				},
				VerifierSummary: "Run go test ./internal/... successfully.",
				LifecycleState:  experience.LifecycleActive,
			}},
		},
	}
	session := newExperienceLearningTestClient(t, service)

	listed := callTool(
		t,
		session,
		"list_experience_proposals",
		map[string]any{
			"cwd":     "/tmp/project",
			"harness": "cursor",
		},
	)
	if listed.IsError {
		t.Fatalf("cursor proposals failed: %v", listed.Content)
	}
	if service.listCalls != 1 {
		t.Fatalf("cursor proposal list calls = %d", service.listCalls)
	}
	items := asObject(
		t,
		asObject(t, listed.StructuredContent)["readmodel"],
	)["items"].([]any)
	if len(items) != 1 ||
		asObject(t, items[0])["instruction"] != proposal.Guidance.Instruction {
		t.Fatalf("cursor proposal items = %#v", items)
	}

	edited := testExperienceProposal()
	edited.Scope.Harnesses = []experience.Harness{
		experience.HarnessCursor,
	}
	approved := callTool(t, session, "approve_experience", map[string]any{
		"proposal_id":      "exs_cursor_proposal",
		"action_token":     "approval-token",
		"approval_mode":    "narrowed",
		"approved_content": edited,
	})
	if approved.IsError {
		t.Fatalf("cursor approval failed: %v", approved.Content)
	}
	if service.approved == nil ||
		len(service.approved.Scope.Harnesses) != 1 ||
		service.approved.Scope.Harnesses[0] != experience.HarnessCursor {
		t.Fatalf("approved cursor scope = %#v", service.approved)
	}

	active := callTool(
		t,
		session,
		"list_active_experiences",
		map[string]any{"cwd": "/tmp/project"},
	)
	if active.IsError {
		t.Fatalf("cursor active list failed: %v", active.Content)
	}
	activeItems := asObject(
		t,
		asObject(t, active.StructuredContent)["readmodel"],
	)["items"].([]any)
	if len(activeItems) != 1 {
		t.Fatalf("cursor active items = %#v", activeItems)
	}
	scope := asObject(t, asObject(t, activeItems[0])["scope"])
	harnesses, ok := scope["harnesses"].([]any)
	if !ok || len(harnesses) != 1 || harnesses[0] != "cursor" {
		t.Fatalf("cursor active scope = %#v", scope)
	}
}
