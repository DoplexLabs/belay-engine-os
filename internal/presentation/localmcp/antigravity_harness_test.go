package localmcp

import (
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
)

// antigravityRuleFile is the only Antigravity project rule file a Mission
// Pack may target; it mirrors localapp's propose_fix allowlist.
const antigravityRuleFile = ".agents/rules/belay.md"

func antigravityBuildInput(now time.Time) missionpack.BuildInput {
	usd := 12.0
	issue := issueintel.Issue{
		IssueID:      "issue_antigravity_tool",
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
				SessionKey:      "ses_antigravity",
				TurnIndex:       4,
				OccurredAt:      now,
				SourceFileID:    "source_antigravity",
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
			Branch:    "feature/antigravity",
			Worktree:  "project-worktree",
			Harnesses: []string{"antigravity"},
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
			InsightID:   "insight_antigravity_tool",
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

func TestMissionPackToolBuildsAntigravityCompatiblePack(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	service := &buildingMissionPackService{input: antigravityBuildInput(now)}
	session := newMissionPackTestClient(t, service)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd":       "/tmp/example",
		"harness":   "antigravity",
		"intent":    "implement",
		"task_hint": "verification",
	})
	if result.IsError {
		t.Fatalf("Antigravity get_mission_pack failed: %v", result.Content)
	}
	if service.request.Harness != missionpack.HarnessAntigravity {
		t.Fatalf("request harness = %q", service.request.Harness)
	}
	readModel := asObject(t, asObject(t, result.StructuredContent)["readmodel"])
	if readModel["harness"] != "antigravity" {
		t.Fatalf("pack harness = %#v", readModel["harness"])
	}
	rules, ok := readModel["operating_rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("operating rules = %#v", readModel["operating_rules"])
	}
	rule := asObject(t, rules[0])
	if rule["target_file"] != antigravityRuleFile {
		t.Fatalf("Antigravity operating rule target = %#v", rule["target_file"])
	}
	markdown, _ := readModel["rendered_markdown"].(string)
	if markdown == "" {
		t.Fatal("Antigravity pack rendered no markdown")
	}
	for _, forbidden := range []string{
		"CLAUDE.md",
		"AGENTS.md",
		".claude/",
		".codex/",
		".cursor/",
	} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf(
				"Antigravity pack markdown names %q: %s",
				forbidden,
				markdown,
			)
		}
	}
}

func TestMissionPackToolSuppressesClaudeOnlyTargetForAntigravity(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	input := antigravityBuildInput(now)
	input.Insight.Result.Fixes[0].TargetFile = ".claude/settings.json"
	input.Insight.Result.Fixes[0].RuleText = "Allow the verified command."
	service := &buildingMissionPackService{input: input}
	session := newMissionPackTestClient(t, service)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd":     "/tmp/example",
		"harness": "antigravity",
		"intent":  "implement",
	})
	if result.IsError {
		t.Fatalf("Antigravity get_mission_pack failed: %v", result.Content)
	}
	readModel := asObject(t, asObject(t, result.StructuredContent)["readmodel"])
	if rules, ok := readModel["operating_rules"].([]any); ok &&
		len(rules) != 0 {
		t.Fatalf("Antigravity pack kept Claude-only rule: %#v", rules)
	}
}

func TestExperienceLearningToolsAcceptAntigravityHarness(t *testing.T) {
	now := testExperienceLearningTime()
	turn := int64(7)
	proposal := testExperienceProposal()
	proposal.Scope.Harnesses = []experience.Harness{
		experience.HarnessAntigravity,
	}
	service := &testExperienceLearningService{
		listResult: localapp.ExperienceLearningListResult{
			ProjectIdentity: proposal.Scope.ProjectIdentity,
			Items: []localapp.ExperienceApprovalPreview{{
				Review: localapp.ExperienceCandidateReview{
					ProposalID:      "exs_antigravity_proposal",
					ProjectIdentity: proposal.Scope.ProjectIdentity,
					Family:          experience.CandidateCorrection,
					Proposed:        proposal,
					Evidence: []experience.EvidenceRef{{
						Excerpt:    "verbatim antigravity excerpt",
						SessionKey: "ses_antigravity",
						TurnIndex:  &turn,
					}},
					SemanticProvenance: experience.SemanticProposalProvenance{
						Harness: experience.HarnessAntigravity,
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
					ExperienceID: "exp_antigravity_active",
					Version:      2,
				},
				Instruction: "Run the focused verification after editing.",
				Scope: localapp.ExperienceLearningScope{
					Kind:            experience.ScopeProject,
					RepositoryPaths: []string{},
					TaskFamilies:    []string{},
					Harnesses: []experience.Harness{
						experience.HarnessAntigravity,
					},
					Models: []string{},
				},
				Applicability: localapp.ExperienceLearningApplicability{
					Description:             "Implementation work in Antigravity.",
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
			"harness": "antigravity",
		},
	)
	if listed.IsError {
		t.Fatalf("antigravity proposals failed: %v", listed.Content)
	}
	if service.listCalls != 1 {
		t.Fatalf("antigravity proposal list calls = %d", service.listCalls)
	}
	items := asObject(
		t,
		asObject(t, listed.StructuredContent)["readmodel"],
	)["items"].([]any)
	if len(items) != 1 ||
		asObject(t, items[0])["instruction"] != proposal.Guidance.Instruction {
		t.Fatalf("antigravity proposal items = %#v", items)
	}

	edited := testExperienceProposal()
	edited.Scope.Harnesses = []experience.Harness{
		experience.HarnessAntigravity,
	}
	approved := callTool(t, session, "approve_experience", map[string]any{
		"proposal_id":      "exs_antigravity_proposal",
		"action_token":     "approval-token",
		"approval_mode":    "narrowed",
		"approved_content": edited,
	})
	if approved.IsError {
		t.Fatalf("antigravity approval failed: %v", approved.Content)
	}
	if service.approved == nil ||
		len(service.approved.Scope.Harnesses) != 1 ||
		service.approved.Scope.Harnesses[0] != experience.HarnessAntigravity {
		t.Fatalf("approved antigravity scope = %#v", service.approved)
	}

	active := callTool(
		t,
		session,
		"list_active_experiences",
		map[string]any{"cwd": "/tmp/project"},
	)
	if active.IsError {
		t.Fatalf("antigravity active list failed: %v", active.Content)
	}
	activeItems := asObject(
		t,
		asObject(t, active.StructuredContent)["readmodel"],
	)["items"].([]any)
	if len(activeItems) != 1 {
		t.Fatalf("antigravity active items = %#v", activeItems)
	}
	scope := asObject(t, asObject(t, activeItems[0])["scope"])
	harnesses, ok := scope["harnesses"].([]any)
	if !ok || len(harnesses) != 1 || harnesses[0] != "antigravity" {
		t.Fatalf("antigravity active scope = %#v", scope)
	}
}

func TestMissionPackToolSchemaAcceptsOnlySupportedHarnessSpellings(
	t *testing.T,
) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	service := &buildingMissionPackService{input: antigravityBuildInput(now)}
	session := newMissionPackTestClient(t, service)

	for _, harness := range []string{"Antigravity", "agy", "gemini"} {
		result := callTool(t, session, "get_mission_pack", map[string]any{
			"cwd":     "/tmp/example",
			"harness": harness,
		})
		if !result.IsError ||
			missionPackResultErrorCode(result) != string(strictInvalidInput) {
			t.Fatalf(
				"harness %q result = %#v, want %s",
				harness,
				result,
				strictInvalidInput,
			)
		}
	}
	if service.request.Harness != "" {
		t.Fatalf("rejected harness reached the service: %q", service.request.Harness)
	}
}

func TestExperienceLearningSchemaAcceptsOnlySupportedHarnessSpellings(
	t *testing.T,
) {
	service := &testExperienceLearningService{}
	session := newExperienceLearningTestClient(t, service)
	for _, harness := range []string{"Antigravity", "agy", "gemini"} {
		assertStrictToolError(
			t,
			callTool(t, session, "list_experience_proposals", map[string]any{
				"cwd":     "/tmp/project",
				"harness": harness,
			}),
			strictInvalidInput,
		)
	}
	if service.listCalls != 0 {
		t.Fatalf("rejected harness invoked service %d times", service.listCalls)
	}
	// The canonical spelling passes the strict schema and reaches the
	// service; the populated end-to-end path is covered by
	// TestExperienceLearningToolsAcceptAntigravityHarness.
	callTool(t, session, "list_experience_proposals", map[string]any{
		"cwd":     "/tmp/project",
		"harness": "antigravity",
	})
	if service.listCalls != 1 {
		t.Fatalf("antigravity list calls = %d, want 1", service.listCalls)
	}
}
