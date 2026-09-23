package localapp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
	"github.com/google/jsonschema-go/jsonschema"
)

type semanticAnalysisTestStore struct {
	projects []issueintel.Project
	inputs   map[string]issueintel.SemanticInput
	existing map[string]issueintel.InsightRecord
	stored   []issueintel.InsightRecord
}

func (s *semanticAnalysisTestStore) ListInsightProjects(
	context.Context,
	int,
) ([]issueintel.Project, error) {
	return append([]issueintel.Project(nil), s.projects...), nil
}

func (s *semanticAnalysisTestStore) LoadSemanticInput(
	_ context.Context,
	project issueintel.Project,
) (issueintel.SemanticInput, error) {
	return s.inputs[project.Identity], nil
}

func (s *semanticAnalysisTestStore) ReplaceProjectInsight(
	_ context.Context,
	record issueintel.InsightRecord,
) error {
	s.stored = append(s.stored, record)
	return nil
}

func (s *semanticAnalysisTestStore) GetProjectInsight(
	_ context.Context,
	projectIdentity string,
) (issueintel.InsightRecord, error) {
	record, ok := s.existing[projectIdentity]
	if !ok {
		return issueintel.InsightRecord{}, sql.ErrNoRows
	}
	return record, nil
}

func TestAnalyzeSemanticProjectsSendsOnlyCandidatesAndExcerpts(t *testing.T) {
	input := semanticTestInput()
	store := &semanticAnalysisTestStore{
		projects: []issueintel.Project{input.Project},
		inputs: map[string]issueintel.SemanticInput{
			input.Project.Identity: input,
		},
		existing: make(map[string]issueintel.InsightRecord),
	}
	runner := func(
		_ context.Context,
		harness SemanticHarness,
		prompt []byte,
		schema []byte,
	) (SemanticHarnessResult, error) {
		if harness != SemanticHarnessClaude ||
			strings.Contains(string(prompt), input.Project.Path) ||
			strings.Contains(string(prompt), input.Issues[0].Fingerprint) ||
			!strings.Contains(string(prompt), "verbatim failure") ||
			!json.Valid(schema) {
			t.Fatalf("unexpected prompt/schema: %s / %s", prompt, schema)
		}
		return SemanticHarnessResult{
			Model: "claude-test",
			Result: issueintel.InsightResult{
				Clusters: []issueintel.InsightCluster{{
					CandidateIDs: []string{"candidate-1"},
					Topic:        "Editing workflow",
					RuleText:     "Inspect the whole file before editing it.",
					TargetFile:   "CLAUDE.md",
					Confidence:   0.9,
				}},
				Fixes: []issueintel.InsightFix{{
					IssueID:    "csi_test",
					RuleText:   "Stop after two identical failures and diagnose the cause.",
					TargetFile: "AGENTS.md",
					Confidence: 0.95,
				}},
			},
		}, nil
	}
	report, err := AnalyzeSemanticProjects(
		context.Background(),
		store,
		SemanticHarnessClaude,
		runner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Projects != 1 ||
		report.Clusters != 1 ||
		report.Fixes != 1 ||
		len(store.stored) != 1 ||
		store.stored[0].PromptVersion != InsightPromptVersion ||
		store.stored[0].InputHash == "" ||
		store.stored[0].Model != "claude-test" {
		t.Fatalf("semantic report/store = %+v/%+v", report, store.stored)
	}
}

func TestAnalyzeSemanticProjectsSkipsMatchingProvenance(t *testing.T) {
	input := semanticTestInput()
	_, _, inputHash, err := semanticPrompt(SemanticHarnessCodex, input)
	if err != nil {
		t.Fatal(err)
	}
	store := &semanticAnalysisTestStore{
		projects: []issueintel.Project{input.Project},
		inputs: map[string]issueintel.SemanticInput{
			input.Project.Identity: input,
		},
		existing: map[string]issueintel.InsightRecord{
			input.Project.Identity: {
				Harness:       string(SemanticHarnessCodex),
				PromptVersion: InsightPromptVersion,
				InputHash:     inputHash,
			},
		},
	}
	report, err := AnalyzeSemanticProjects(
		context.Background(),
		store,
		SemanticHarnessCodex,
		func(
			context.Context,
			SemanticHarness,
			[]byte,
			[]byte,
		) (SemanticHarnessResult, error) {
			t.Fatal("matching insight invoked harness")
			return SemanticHarnessResult{}, nil
		},
	)
	if err != nil || report.Skipped != 1 || len(store.stored) != 0 {
		t.Fatalf("skip report/store/error = %+v/%+v/%v", report, store.stored, err)
	}
}

func TestAnalyzeSemanticProjectsRecordsUnknownModelWithoutGuessing(t *testing.T) {
	input := semanticTestInput()
	store := &semanticAnalysisTestStore{
		projects: []issueintel.Project{input.Project},
		inputs: map[string]issueintel.SemanticInput{
			input.Project.Identity: input,
		},
		existing: make(map[string]issueintel.InsightRecord),
	}
	_, err := AnalyzeSemanticProjects(
		context.Background(),
		store,
		SemanticHarnessCodex,
		func(
			context.Context,
			SemanticHarness,
			[]byte,
			[]byte,
		) (SemanticHarnessResult, error) {
			return SemanticHarnessResult{
				Result: issueintel.InsightResult{
					Fixes: []issueintel.InsightFix{{
						IssueID:    "csi_test",
						RuleText:   "Run tests before claiming completion.",
						TargetFile: "AGENTS.md",
						Confidence: 0.8,
					}},
				},
			}, nil
		},
	)
	if err != nil || len(store.stored) != 1 ||
		store.stored[0].Model != "unknown" {
		t.Fatalf("stored insight/error = %+v/%v", store.stored, err)
	}
}

func TestValidateSemanticResultAcceptsZeroFixesAndRejectsMultilineRules(
	t *testing.T,
) {
	input := semanticTestInput()
	if err := validateSemanticResult(
		input,
		issueintel.InsightResult{},
	); err != nil {
		t.Fatalf("zero fixes were rejected: %v", err)
	}
	if err := validateSemanticResult(
		input,
		issueintel.InsightResult{
			Fixes: []issueintel.InsightFix{{
				IssueID:    "csi_test",
				RuleText:   "first line\nsecond line",
				TargetFile: "AGENTS.md",
				Confidence: 0.9,
			}},
		},
	); err == nil {
		t.Fatal("multiline rule was accepted")
	}
}

func TestValidateSemanticResultAcceptsFixSubset(t *testing.T) {
	input := semanticTestInput()
	secondIssue := input.Issues[0]
	secondIssue.IssueID = "csi_second"
	input.Issues = append(input.Issues, secondIssue)

	err := validateSemanticResult(
		input,
		issueintel.InsightResult{
			Fixes: []issueintel.InsightFix{{
				IssueID:    "csi_test",
				RuleText:   "Run tests before claiming completion.",
				TargetFile: "AGENTS.md",
				Confidence: 0.9,
			}},
		},
	)
	if err != nil {
		t.Fatalf("valid fix subset was rejected: %v", err)
	}
}

func TestValidateSemanticResultRejectsUnknownAndDuplicateFixes(t *testing.T) {
	input := semanticTestInput()
	if err := validateSemanticResult(
		input,
		issueintel.InsightResult{
			Fixes: []issueintel.InsightFix{{
				IssueID:    "csi_unknown",
				RuleText:   "Run tests before claiming completion.",
				TargetFile: "AGENTS.md",
				Confidence: 0.9,
			}},
		},
	); err == nil {
		t.Fatal("unknown issue ID was accepted")
	}
	if err := validateSemanticResult(
		input,
		issueintel.InsightResult{
			Fixes: []issueintel.InsightFix{
				{
					IssueID:    "csi_test",
					RuleText:   "Run tests before claiming completion.",
					TargetFile: "AGENTS.md",
					Confidence: 0.9,
				},
				{
					IssueID:    "csi_test",
					RuleText:   "Diagnose failures before retrying.",
					TargetFile: "CLAUDE.md",
					Confidence: 0.8,
				},
			},
		},
	); err == nil {
		t.Fatal("duplicate issue fix was accepted")
	}
}

func TestValidateSemanticResultRejectsUnknownCandidateIDs(t *testing.T) {
	input := semanticTestInput()
	if err := validateSemanticResult(
		input,
		issueintel.InsightResult{
			Fixes: []issueintel.InsightFix{{
				IssueID:    "csi_test",
				RuleText:   "Run tests before claiming completion.",
				TargetFile: "AGENTS.md",
				Confidence: 0.9,
			}},
			Clusters: []issueintel.InsightCluster{{
				CandidateIDs: []string{"candidate-unknown"},
				Topic:        "Verification",
				RuleText:     "Run tests before claiming completion.",
				TargetFile:   "AGENTS.md",
				Confidence:   0.9,
			}},
		},
	); err == nil {
		t.Fatal("unknown candidate ID was accepted")
	}
}

func TestSanitizeSemanticResultKeepsIndependentValidItems(t *testing.T) {
	input := semanticTestInput()
	secondCandidate := input.CorrectionCandidates[0]
	secondCandidate.CandidateID = "candidate-2"
	thirdCandidate := input.CorrectionCandidates[0]
	thirdCandidate.CandidateID = "candidate-3"
	input.CorrectionCandidates = append(
		input.CorrectionCandidates,
		secondCandidate,
		thirdCandidate,
	)
	secondIssue := input.Issues[0]
	secondIssue.IssueID = "csi_second"
	input.Issues = append(input.Issues, secondIssue)

	result, diagnostics := sanitizeSemanticResult(
		input,
		issueintel.InsightResult{
			Clusters: []issueintel.InsightCluster{
				{
					CandidateIDs: []string{"candidate-1"},
					Topic:        "Verification",
					RuleText:     "Run verification before claiming completion.",
					TargetFile:   "AGENTS.md",
					Confidence:   0.9,
				},
				{
					CandidateIDs: []string{"candidate-2"},
					Topic:        "First interpretation",
					RuleText:     "Inspect the relevant configuration first.",
					TargetFile:   "AGENTS.md",
					Confidence:   0.8,
				},
				{
					CandidateIDs: []string{"candidate-2", "candidate-3"},
					Topic:        "Second interpretation",
					RuleText:     "Confirm the applicable scope before editing.",
					TargetFile:   "AGENTS.md",
					Confidence:   0.8,
				},
			},
			Fixes: []issueintel.InsightFix{
				{
					IssueID:    "csi_test",
					RuleText:   "Diagnose the cause before repeating a failed command.",
					TargetFile: "AGENTS.md",
					Confidence: 0.9,
				},
				{
					IssueID:    "csi_second",
					RuleText:   "First conflicting rule.",
					TargetFile: "AGENTS.md",
					Confidence: 0.8,
				},
				{
					IssueID:    "csi_second",
					RuleText:   "Second conflicting rule.",
					TargetFile: "CLAUDE.md",
					Confidence: 0.8,
				},
			},
		},
	)
	if len(result.Clusters) != 1 ||
		result.Clusters[0].CandidateIDs[0] != "candidate-1" ||
		len(result.Fixes) != 1 ||
		result.Fixes[0].IssueID != "csi_test" ||
		diagnostics.DroppedClusters != 2 ||
		diagnostics.AmbiguousClusterCandidates != 2 ||
		diagnostics.DroppedFixes != 2 ||
		diagnostics.AmbiguousFixIssues != 2 {
		t.Fatalf("sanitized result/diagnostics = %+v/%+v", result, diagnostics)
	}
}

func TestAnalyzeSemanticProjectsPersistsSanitizedEmptyResult(t *testing.T) {
	input := semanticTestInput()
	store := &semanticAnalysisTestStore{
		projects: []issueintel.Project{input.Project},
		inputs: map[string]issueintel.SemanticInput{
			input.Project.Identity: input,
		},
		existing: make(map[string]issueintel.InsightRecord),
	}
	report, err := AnalyzeSemanticProjects(
		context.Background(),
		store,
		SemanticHarnessCodex,
		func(
			context.Context,
			SemanticHarness,
			[]byte,
			[]byte,
		) (SemanticHarnessResult, error) {
			return SemanticHarnessResult{
				Model: "codex-test",
				Result: issueintel.InsightResult{
					Clusters: []issueintel.InsightCluster{{
						CandidateIDs: []string{"candidate-unknown"},
						Topic:        "Untrusted output",
						RuleText:     "Do not persist this generated rule.",
						TargetFile:   "AGENTS.md",
						Confidence:   0.9,
					}},
				},
			}, nil
		},
	)
	if err != nil ||
		report.Projects != 1 ||
		report.SanitizedProjects != 1 ||
		report.DroppedClusters != 1 ||
		report.Clusters != 0 ||
		len(store.stored) != 1 ||
		len(store.stored[0].Result.Clusters) != 0 ||
		store.stored[0].Sanitization.UnknownClusterCandidates != 1 {
		t.Fatalf("sanitized report/store/error = %+v/%+v/%v", report, store.stored, err)
	}
}

func TestPrepareSemanticPromptBoundsLargeInputDeterministically(t *testing.T) {
	input := largeSemanticTestInput()
	bounded, prompt, schema, inputHash, err := prepareSemanticPrompt(
		SemanticHarnessCodex,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Issues) != maxSemanticIssues ||
		len(bounded.CorrectionCandidates) != maxSemanticCandidates {
		t.Fatalf(
			"bounded counts = issues %d, candidates %d",
			len(bounded.Issues),
			len(bounded.CorrectionCandidates),
		)
	}
	if len(prompt)+len(schema) > maxSemanticPromptAndSchema {
		t.Fatalf(
			"prompt+schema = %d, budget = %d",
			len(prompt)+len(schema),
			maxSemanticPromptAndSchema,
		)
	}
	if len(schema) > maxClaudeInlineSchema {
		t.Fatalf(
			"schema = %d, inline cap = %d",
			len(schema),
			maxClaudeInlineSchema,
		)
	}
	if bounded.Issues[0].IssueID != "csi_0000" ||
		bounded.Issues[len(bounded.Issues)-1].IssueID != "csi_0031" {
		t.Fatalf("issue priority order changed: %s ... %s",
			bounded.Issues[0].IssueID,
			bounded.Issues[len(bounded.Issues)-1].IssueID,
		)
	}
	if bounded.CorrectionCandidates[0].CandidateID != "candidate_linked_0000" {
		t.Fatalf(
			"linked candidate was not prioritized: %s",
			bounded.CorrectionCandidates[0].CandidateID,
		)
	}
	assertSemanticClipping(t, bounded)
	for _, forbidden := range []string{
		input.Project.Path,
		input.Issues[0].Fingerprint,
		"$schema",
		"json-schema.org",
		"csi_0032",
		"candidate_unlinked_1499",
	} {
		if bytes.Contains(prompt, []byte(forbidden)) {
			t.Fatalf("prompt exposed %q", forbidden)
		}
	}
	if !bytes.Contains(schema, []byte(`"csi_0000"`)) ||
		!bytes.Contains(schema, []byte(`"candidate_linked_0000"`)) ||
		bytes.Contains(schema, []byte(`"csi_0032"`)) {
		t.Fatalf("schema enums do not match bounded input: %s", schema)
	}

	reordered := input
	reordered.CorrectionCandidates = reverseSemanticCandidates(
		input.CorrectionCandidates,
	)
	secondBounded, secondPrompt, secondSchema, secondHash, err :=
		prepareSemanticPrompt(SemanticHarnessCodex, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if inputHash != secondHash ||
		!bytes.Equal(prompt, secondPrompt) ||
		!bytes.Equal(schema, secondSchema) ||
		!equalSemanticCandidateOrder(
			bounded.CorrectionCandidates,
			secondBounded.CorrectionCandidates,
		) {
		t.Fatal("candidate input order changed bounded prompt or hash")
	}
}

func TestBoundedSemanticInputRetainsMarkerBearingHumanCorrections(t *testing.T) {
	input := semanticTestInput()
	input.CorrectionCandidates = []issueintel.CorrectionCandidate{
		{
			CandidateID: "candidate-marker-ack",
			Text:        "Thanks!",
			Marker:      "no",
		},
		{
			CandidateID: "candidate-marker-correction",
			Text:        "No, use the focused package test first.",
			Marker:      "wrong",
		},
	}

	bounded := boundedSemanticInput(input)
	if got := semanticCandidateIDs(bounded.CorrectionCandidates); !slicesEqual(
		got,
		[]string{
			"candidate-marker-ack",
			"candidate-marker-correction",
		},
	) {
		t.Fatalf("marker-bearing human candidates = %v", got)
	}
	for _, candidate := range input.CorrectionCandidates {
		if candidate.Marker == "" {
			t.Fatalf("source candidate %q was mutated", candidate.CandidateID)
		}
	}
}

func TestBoundedSemanticInputExcludesMarkerBearingMachineEnvelopes(
	t *testing.T,
) {
	envelopes := []string{
		"<task-notification><status>completed</status></task-notification>",
		"<task_notification><status>completed</status></task_notification>",
		"[task-notification] completed",
		"[task_notification] completed",
		"<system-reminder>Use task tools.</system-reminder>",
		"<system_reminder>Use task tools.</system_reminder>",
		"[system-reminder] Use task tools.",
		"[system_reminder] Use task tools.",
		"<subagent-notification><status>completed</status></subagent-notification>",
		"<subagent_notification><status>completed</status></subagent_notification>",
		"[subagent-notification] completed",
		"[subagent_notification] completed",
		`{"type":"task_notification","status":"completed"}`,
		`{"type":"task-notification","status":"completed"}`,
		`{"type":"system_reminder","status":"completed"}`,
		`{"type":"system-reminder","status":"completed"}`,
		`{"type":"subagent_notification","status":"completed"}`,
		`{"type":"subagent-notification","status":"completed"}`,
	}
	input := semanticTestInput()
	input.CorrectionCandidates = make(
		[]issueintel.CorrectionCandidate,
		0,
		len(envelopes)+1,
	)
	for index, envelope := range envelopes {
		input.CorrectionCandidates = append(
			input.CorrectionCandidates,
			issueintel.CorrectionCandidate{
				CandidateID: fmt.Sprintf("candidate-envelope-%02d", index),
				Text:        envelope,
				Marker:      "wrong",
			},
		)
	}
	input.CorrectionCandidates = append(
		input.CorrectionCandidates,
		issueintel.CorrectionCandidate{
			CandidateID: "candidate-human-correction",
			Text:        "Wrong—run the focused package test first.",
			Marker:      "wrong",
		},
	)

	bounded, prompt, _, _, err := prepareSemanticPrompt(
		SemanticHarnessCodex,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := semanticCandidateIDs(bounded.CorrectionCandidates); !slicesEqual(
		got,
		[]string{"candidate-human-correction"},
	) {
		t.Fatalf("bounded candidates = %v", got)
	}
	for _, candidate := range input.CorrectionCandidates[:len(envelopes)] {
		if bytes.Contains(prompt, []byte(candidate.CandidateID)) ||
			bytes.Contains(prompt, []byte(candidate.Text)) {
			t.Fatalf(
				"prompt retained marker-bearing machine envelope %q",
				candidate.CandidateID,
			)
		}
		if candidate.Marker != "wrong" {
			t.Fatalf("source candidate %q was mutated", candidate.CandidateID)
		}
	}
}

func TestBoundedSemanticInputExcludesAcknowledgementsAndMachineEnvelopes(
	t *testing.T,
) {
	input := semanticTestInput()
	input.CorrectionCandidates = []issueintel.CorrectionCandidate{
		{
			CandidateID: "candidate-thanks",
			Text:        "Thanks for the update!",
		},
		{
			CandidateID: "candidate-okay",
			Text:        "Okay.",
		},
		{
			CandidateID: "candidate-task-notification",
			Text: "<task-notification>\n" +
				"<task-id>worker-1</task-id>\n" +
				"<status>completed</status>\n" +
				"</task-notification>",
		},
		{
			CandidateID: "candidate-system-reminder",
			Text: "<system-reminder>\n" +
				"Use the task tools when appropriate.\n" +
				"</system-reminder>",
		},
		{
			CandidateID: "candidate-json-envelope",
			Text:        `{"type": "task_notification", "status": "completed"}`,
		},
		{
			CandidateID: "candidate-subagent-notification",
			Text: "<subagent_notification>\n" +
				"<status>completed</status>\n" +
				"</subagent_notification>",
		},
		{
			CandidateID: "candidate-json-subagent-envelope",
			Text:        `{"type": "subagent-notification", "status": "completed"}`,
		},
	}

	bounded, prompt, _, _, err := prepareSemanticPrompt(
		SemanticHarnessCodex,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.CorrectionCandidates) != 0 {
		t.Fatalf(
			"noise candidates reached bounded payload: %v",
			semanticCandidateIDs(bounded.CorrectionCandidates),
		)
	}
	for _, candidate := range input.CorrectionCandidates {
		if bytes.Contains(prompt, []byte(candidate.CandidateID)) ||
			bytes.Contains(prompt, []byte(candidate.Text)) {
			t.Fatalf("prompt retained noise candidate %q", candidate.CandidateID)
		}
	}
}

func TestBoundedSemanticInputRetainsTerseUnmarkedCandidates(t *testing.T) {
	input := semanticTestInput()
	input.CorrectionCandidates = []issueintel.CorrectionCandidate{
		{
			CandidateID: "candidate-terse",
			Text:        "Tests first.",
			ShortTurn:   true,
		},
		{
			CandidateID: "candidate-contextual-thanks",
			Text:        "Thanks, but run the focused package test first.",
			ShortTurn:   true,
		},
	}
	originalCount := len(input.CorrectionCandidates)

	bounded := boundedSemanticInput(input)
	if got := semanticCandidateIDs(bounded.CorrectionCandidates); !slicesEqual(
		got,
		[]string{
			"candidate-contextual-thanks",
			"candidate-terse",
		},
	) {
		t.Fatalf("terse candidates = %v", got)
	}
	if len(input.CorrectionCandidates) != originalCount {
		t.Fatalf(
			"source candidates changed: got %d, want %d",
			len(input.CorrectionCandidates),
			originalCount,
		)
	}
}

func TestSemanticPromptIsHarnessAwareAndVersioned(t *testing.T) {
	input := semanticTestInput()
	claudePrompt, claudeSchema, claudeHash, err := semanticPrompt(
		SemanticHarnessClaude,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	codexPrompt, codexSchema, codexHash, err := semanticPrompt(
		SemanticHarnessCodex,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if InsightPromptVersion != "belay.insight-prompt.v5" {
		t.Fatalf("prompt version = %q", InsightPromptVersion)
	}
	if !bytes.Contains(claudePrompt, []byte("prefer CLAUDE.md")) ||
		!bytes.Contains(codexPrompt, []byte("prefer AGENTS.md")) ||
		!bytes.Contains(
			claudePrompt,
			[]byte("candidate ID at most once"),
		) {
		t.Fatalf(
			"harness preferences missing:\nclaude=%s\ncodex=%s",
			claudePrompt,
			codexPrompt,
		)
	}
	if claudeHash == codexHash {
		t.Fatal("harness-specific prompts shared a provenance hash")
	}
	if !bytes.Equal(claudeSchema, codexSchema) {
		t.Fatal("harness preference unexpectedly changed output schema")
	}
	for _, required := range []string{
		"Return a fix only when the supplied evidence supports a specific, durable rule.",
		"You may omit issues whose evidence does not support such a rule.",
		"Never invent a rule merely to cover every issue.",
	} {
		if !bytes.Contains(claudePrompt, []byte(required)) {
			t.Fatalf("prompt missing abstention instruction %q", required)
		}
	}

	var schema map[string]any
	if err := json.Unmarshal(claudeSchema, &schema); err != nil {
		t.Fatal(err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	fixes, ok := properties["fixes"].(map[string]any)
	if !ok {
		t.Fatalf("fix schema = %#v", properties["fixes"])
	}
	if fixes["minItems"] != float64(0) ||
		fixes["maxItems"] != float64(len(input.Issues)) {
		t.Fatalf(
			"fix item bounds = min %#v, max %#v",
			fixes["minItems"],
			fixes["maxItems"],
		)
	}
	description, _ := fixes["description"].(string)
	if !strings.Contains(description, "omit unsupported issues") {
		t.Fatalf("fix schema missing abstention guidance: %q", description)
	}
}

func TestSemanticSchemaEnumsRejectUnknownReferences(t *testing.T) {
	bounded, _, schemaBody, _, err := prepareSemanticPrompt(
		SemanticHarnessClaude,
		semanticTestInput(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBody, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := semanticSchemaTestInstance(
		bounded,
		bounded.CorrectionCandidates[0].CandidateID,
		bounded.Issues[0].IssueID,
	)
	if err := resolved.Validate(valid); err != nil {
		t.Fatalf("valid bounded IDs rejected: %v", err)
	}
	invalidCandidate := semanticSchemaTestInstance(
		bounded,
		"candidate_unknown",
		bounded.Issues[0].IssueID,
	)
	if err := resolved.Validate(invalidCandidate); err == nil {
		t.Fatal("schema accepted an unknown candidate ID")
	}
	invalidIssue := semanticSchemaTestInstance(
		bounded,
		bounded.CorrectionCandidates[0].CandidateID,
		"csi_unknown",
	)
	if err := resolved.Validate(invalidIssue); err == nil {
		t.Fatal("schema accepted an unknown issue ID")
	}
}

func TestDecodeClaudeSemanticStructuredOutput(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"structured_output":{
			"clusters":[],
			"fixes":[{
				"issue_id":"csi_test",
				"rule_text":"Run tests before claiming completion.",
				"target_file":"CLAUDE.md",
				"confidence":0.8
			}]
		}
	}`)
	result, err := decodeClaudeSemanticResult(body)
	if err != nil || result.Model != "claude-test" ||
		len(result.Result.Fixes) != 1 {
		t.Fatalf("decoded Claude result/error = %+v/%v", result, err)
	}
}

func TestRunInstalledSemanticHarnessStreamsLargeClaudePrompt(t *testing.T) {
	harnessDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	writeSemanticHarness(t, harnessDir, "claude", `
for arg in "$@"; do
	printf '<%s>\n' "$arg" >> "$BELAY_TEST_ARGS_PATH"
done
cat > "$BELAY_TEST_STDIN_PATH"
printf '%s\n' '{"model":"claude-fake","structured_output":{"clusters":[],"fixes":[]}}'
`)
	t.Setenv("PATH", harnessDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BELAY_TEST_ARGS_PATH", argsPath)
	t.Setenv("BELAY_TEST_STDIN_PATH", stdinPath)
	prompt := []byte("LARGE_PROMPT_MARKER_" + strings.Repeat("x", 512<<10))
	schema := []byte(`{"type":"object"}`)

	result, err := RunInstalledSemanticHarness(
		context.Background(),
		SemanticHarnessClaude,
		prompt,
		schema,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "claude-fake" {
		t.Fatalf("model = %q", result.Model)
	}
	assertSemanticPromptTransport(t, argsPath, stdinPath, prompt)
	args := readSemanticTestFile(t, argsPath)
	for _, required := range []string{
		"<-p>",
		"<--input-format>",
		"<text>",
		"<--json-schema>",
		"<" + string(schema) + ">",
		"<--tools>",
		"<>",
		"<--no-session-persistence>",
	} {
		if !bytes.Contains(args, []byte(required)) {
			t.Fatalf("Claude args missing %q:\n%s", required, args)
		}
	}
}

func TestRunInstalledSemanticHarnessStreamsCodexPromptAndUsesSchemaFile(
	t *testing.T,
) {
	harnessDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	schemaCopyPath := filepath.Join(t.TempDir(), "schema")
	writeSemanticHarness(t, harnessDir, "codex", `
expect=""
output_path=""
schema_path=""
for arg in "$@"; do
	printf '<%s>\n' "$arg" >> "$BELAY_TEST_ARGS_PATH"
	if [ "$expect" = "output" ]; then
		output_path="$arg"
		expect=""
	elif [ "$expect" = "schema" ]; then
		schema_path="$arg"
		expect=""
	elif [ "$arg" = "--output-last-message" ]; then
		expect="output"
	elif [ "$arg" = "--output-schema" ]; then
		expect="schema"
	fi
done
cat > "$BELAY_TEST_STDIN_PATH"
cp "$schema_path" "$BELAY_TEST_SCHEMA_PATH"
printf '%s\n' '{"clusters":[],"fixes":[]}' > "$output_path"
`)
	t.Setenv("PATH", harnessDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BELAY_TEST_ARGS_PATH", argsPath)
	t.Setenv("BELAY_TEST_STDIN_PATH", stdinPath)
	t.Setenv("BELAY_TEST_SCHEMA_PATH", schemaCopyPath)
	prompt := []byte("CODEX_LARGE_PROMPT_MARKER_" + strings.Repeat("y", 512<<10))
	schema := []byte(`{"type":"object","additionalProperties":false}`)

	_, err := RunInstalledSemanticHarness(
		context.Background(),
		SemanticHarnessCodex,
		prompt,
		schema,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticPromptTransport(t, argsPath, stdinPath, prompt)
	if got := readSemanticTestFile(t, schemaCopyPath); !bytes.Equal(got, schema) {
		t.Fatalf("schema file = %q, want %q", got, schema)
	}
	args := readSemanticTestFile(t, argsPath)
	if !bytes.Contains(args, []byte("<--output-schema>")) ||
		!bytes.Contains(args, []byte("<--ephemeral>")) ||
		!bytes.Contains(args, []byte("<--sandbox>")) ||
		!bytes.Contains(args, []byte("<read-only>")) ||
		!bytes.Contains(args, []byte("<->")) {
		t.Fatalf("Codex safety/schema args missing:\n%s", args)
	}
}

func TestRunInstalledSemanticHarnessBoundsClaudeSchemaArgument(t *testing.T) {
	_, err := RunInstalledSemanticHarness(
		context.Background(),
		SemanticHarnessClaude,
		[]byte("prompt"),
		bytes.Repeat([]byte("x"), maxClaudeInlineSchema+1),
	)
	if err == nil ||
		!strings.Contains(err.Error(), "schema exceeds inline safety limit") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunInstalledSemanticHarnessSanitizesStderr(t *testing.T) {
	harnessDir := t.TempDir()
	writeSemanticHarness(t, harnessDir, "claude", `
prompt="$(cat)"
printf 'corporate gateway failed; prompt=%s; token=super-secret\n' "$prompt" >&2
exit 9
`)
	t.Setenv("PATH", harnessDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prompt := []byte("PRIVATE_PROMPT_CONTENT")

	_, err := RunInstalledSemanticHarness(
		context.Background(),
		SemanticHarnessClaude,
		prompt,
		[]byte(`{"type":"object"}`),
	)
	if err == nil {
		t.Fatal("expected harness failure")
	}
	message := err.Error()
	if !strings.Contains(message, "harness stderr:") ||
		!strings.Contains(message, "corporate gateway failed") {
		t.Fatalf("error missing stderr distinction: %v", err)
	}
	for _, forbidden := range []string{
		string(prompt),
		"super-secret",
	} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("error leaked %q: %v", forbidden, err)
		}
	}
	if len([]rune(message)) > 1024 {
		t.Fatalf("error is not bounded: %d runes", len([]rune(message)))
	}
}

func writeSemanticHarness(
	t *testing.T,
	directory string,
	name string,
	body string,
) {
	t.Helper()
	path := filepath.Join(directory, name)
	script := "#!/bin/sh\nset -eu\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertSemanticPromptTransport(
	t *testing.T,
	argsPath string,
	stdinPath string,
	prompt []byte,
) {
	t.Helper()
	if got := readSemanticTestFile(t, stdinPath); !bytes.Equal(got, prompt) {
		t.Fatalf("stdin length = %d, want %d", len(got), len(prompt))
	}
	args := readSemanticTestFile(t, argsPath)
	if bytes.Contains(args, prompt) ||
		bytes.Contains(args, []byte("LARGE_PROMPT_MARKER_")) ||
		bytes.Contains(args, []byte("CODEX_LARGE_PROMPT_MARKER_")) {
		t.Fatalf("prompt appeared in argv: %s", args)
	}
}

func readSemanticTestFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func largeSemanticTestInput() issueintel.SemanticInput {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	input := issueintel.SemanticInput{
		Project: issueintel.Project{
			Identity: "git@example.test:team/large-project.git",
			Path:     "/private/large/project/path",
		},
		Issues: make([]issueintel.Issue, 0, 1500),
		CorrectionCandidates: make(
			[]issueintel.CorrectionCandidate,
			0,
			3000,
		),
	}
	largeExcerpt := "EXCERPT_HEAD_" + strings.Repeat("e", 4096) +
		"_EXCERPT_TAIL"
	largeCandidate := "CANDIDATE_HEAD_" + strings.Repeat("c", 4096) +
		"_CANDIDATE_TAIL"
	for index := 0; index < 1500; index++ {
		issueID := fmt.Sprintf("csi_%04d", index)
		citation := issueintel.Citation{
			SessionKey:      fmt.Sprintf("ses_link_%04d", index),
			TurnIndex:       int64(index + 1),
			OccurredAt:      now.Add(-time.Duration(index) * time.Minute),
			SourceFileID:    fmt.Sprintf("source_%04d", index),
			JSONLByteOffset: int64(index * 100),
		}
		excerpts := make([]issueintel.Excerpt, 0, 5)
		for excerptIndex := 0; excerptIndex < 5; excerptIndex++ {
			excerpts = append(excerpts, issueintel.Excerpt{
				Citation: citation,
				Role:     transcript.RoleToolResult,
				Text:     largeExcerpt,
			})
		}
		input.Issues = append(input.Issues, issueintel.Issue{
			IssueID:     issueID,
			DetectorID:  issueintel.DetectorRepeatedCorrection,
			Fingerprint: "PRIVATE_FINGERPRINT_" + issueID,
			Headline: "HEADLINE_HEAD_" + strings.Repeat("h", 2048) +
				"_HEADLINE_TAIL",
			Excerpts: excerpts,
			SuggestedFix: issueintel.SuggestedFix{
				Kind:       "harness_rule",
				TargetFile: "AGENTS.md",
				Rationale: "RATIONALE_HEAD_" +
					strings.Repeat("r", 2048) +
					"_RATIONALE_TAIL",
			},
		})
		input.CorrectionCandidates = append(
			input.CorrectionCandidates,
			issueintel.CorrectionCandidate{
				CandidateID: fmt.Sprintf(
					"candidate_linked_%04d",
					index,
				),
				Citation:   citation,
				Text:       largeCandidate,
				OccurredAt: now.Add(-time.Duration(index) * time.Minute),
			},
			issueintel.CorrectionCandidate{
				CandidateID: fmt.Sprintf(
					"candidate_unlinked_%04d",
					index,
				),
				Citation: issueintel.Citation{
					SessionKey: fmt.Sprintf("ses_other_%04d", index),
					TurnIndex:  int64(index + 1),
					OccurredAt: now.Add(
						time.Duration(1500-index) * time.Minute,
					),
				},
				Text: largeCandidate,
				OccurredAt: now.Add(
					time.Duration(1500-index) * time.Minute,
				),
			},
		)
	}
	return input
}

func assertSemanticClipping(
	t *testing.T,
	input issueintel.SemanticInput,
) {
	t.Helper()
	if len(input.Issues[0].Excerpts) != maxSemanticExcerptsPerIssue {
		t.Fatalf("excerpt count = %d", len(input.Issues[0].Excerpts))
	}
	excerpt := input.Issues[0].Excerpts[0].Text
	if len(excerpt) > maxSemanticEvidenceTextBytes ||
		!strings.HasPrefix(excerpt, "EXCERPT_HEAD_") ||
		!strings.HasSuffix(excerpt, "_EXCERPT_TAIL") ||
		!strings.Contains(excerpt, semanticClipMarker) {
		t.Fatalf("excerpt clipping = %q", excerpt)
	}
	candidate := input.CorrectionCandidates[0].Text
	if len(candidate) > maxSemanticEvidenceTextBytes ||
		!strings.HasPrefix(candidate, "CANDIDATE_HEAD_") ||
		!strings.HasSuffix(candidate, "_CANDIDATE_TAIL") ||
		!strings.Contains(candidate, semanticClipMarker) {
		t.Fatalf("candidate clipping = %q", candidate)
	}
}

func reverseSemanticCandidates(
	values []issueintel.CorrectionCandidate,
) []issueintel.CorrectionCandidate {
	result := append([]issueintel.CorrectionCandidate(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right =
		left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func equalSemanticCandidateOrder(
	left []issueintel.CorrectionCandidate,
	right []issueintel.CorrectionCandidate,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].CandidateID != right[index].CandidateID {
			return false
		}
	}
	return true
}

func semanticCandidateIDs(
	candidates []issueintel.CorrectionCandidate,
) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.CandidateID)
	}
	return result
}

func slicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func semanticSchemaTestInstance(
	input issueintel.SemanticInput,
	candidateID string,
	issueIDOverride string,
) map[string]any {
	fixes := make([]any, 0, len(input.Issues))
	for _, issue := range input.Issues {
		issueID := issue.IssueID
		if issue.IssueID == input.Issues[0].IssueID {
			issueID = issueIDOverride
		}
		fixes = append(fixes, map[string]any{
			"issue_id":    issueID,
			"rule_text":   "Run verification before claiming completion.",
			"target_file": "AGENTS.md",
			"confidence":  0.9,
		})
	}
	return map[string]any{
		"clusters": []any{map[string]any{
			"candidate_ids": []any{candidateID},
			"topic":         "Verification",
			"rule_text":     "Run verification before claiming completion.",
			"target_file":   "AGENTS.md",
			"confidence":    0.9,
		}},
		"fixes": fixes,
	}
}

func semanticTestInput() issueintel.SemanticInput {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	project := issueintel.Project{
		Identity: "git@example.test:team/project.git",
		Path:     "/private/project/path",
	}
	return issueintel.SemanticInput{
		Project: project,
		Issues: []issueintel.Issue{{
			IssueID:     "csi_test",
			DetectorID:  issueintel.DetectorRetryLoop,
			Fingerprint: "PRIVATE_FINGERPRINT",
			Headline:    "Tests failed three times.",
			Excerpts: []issueintel.Excerpt{
				{
					Citation: issueintel.Citation{
						SessionKey: "ses_a",
						TurnIndex:  1,
						OccurredAt: now,
					},
					Role: transcript.RoleToolCall,
					Text: "go test ./...",
				},
				{
					Citation: issueintel.Citation{
						SessionKey: "ses_a",
						TurnIndex:  2,
						OccurredAt: now,
					},
					Role: transcript.RoleToolResult,
					Text: "verbatim failure",
				},
			},
			SuggestedFix: issueintel.SuggestedFix{
				Kind:       "harness_rule",
				TargetFile: "AGENTS.md",
				Rationale:  "Stop repeated retries.",
			},
		}},
		CorrectionCandidates: []issueintel.CorrectionCandidate{{
			CandidateID: "candidate-1",
			Project:     project,
			Citation: issueintel.Citation{
				SessionKey: "ses_a",
				TurnIndex:  3,
				OccurredAt: now,
			},
			Text:       "No, inspect the whole file first.",
			Marker:     "no",
			OccurredAt: now,
		}},
	}
}

func TestSemanticHarnessDescribesCursorAndPrefersAgentsFile(t *testing.T) {
	if !SemanticHarnessCursor.Valid() {
		t.Fatal("cursor is not a valid semantic harness")
	}
	for harness, want := range map[SemanticHarness]string{
		SemanticHarnessClaude: "Claude Code",
		SemanticHarnessCodex:  "Codex",
		SemanticHarnessCursor: "Cursor Agent",
	} {
		if got := semanticHarnessLabel(harness); got != want {
			t.Fatalf("semanticHarnessLabel(%q) = %q, want %q", harness, got, want)
		}
	}
	for harness, want := range map[SemanticHarness]string{
		SemanticHarnessClaude: "CLAUDE.md",
		SemanticHarnessCodex:  "AGENTS.md",
		SemanticHarnessCursor: "AGENTS.md",
	} {
		if got := semanticPreferredTarget(harness); got != want {
			t.Fatalf("semanticPreferredTarget(%q) = %q, want %q", harness, got, want)
		}
	}

	cursorPrompt, _, cursorHash, err := semanticPrompt(
		SemanticHarnessCursor,
		semanticTestInput(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(
		cursorPrompt,
		[]byte("in this Cursor Agent analysis, prefer AGENTS.md"),
	) {
		t.Fatalf("cursor prompt = %s", cursorPrompt)
	}
	_, _, codexHash, err := semanticPrompt(
		SemanticHarnessCodex,
		semanticTestInput(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cursorHash == codexHash {
		t.Fatal("cursor and codex prompts shared a provenance hash")
	}
}
