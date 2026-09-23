package localapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/google/jsonschema-go/jsonschema"
)

type experienceSemanticTestStore struct {
	candidates []experience.Candidate
	existing   map[string]bool
	stored     []experience.SemanticResult
	replay     bool
}

func (store *experienceSemanticTestStore) QueryExperienceCandidates(
	_ context.Context,
	projectIdentity string,
	limit int,
) ([]experience.Candidate, error) {
	result := make([]experience.Candidate, 0, len(store.candidates))
	for _, candidate := range store.candidates {
		if candidate.ProjectIdentity == projectIdentity {
			result = append(result, candidate)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (store *experienceSemanticTestStore) HasExperienceSemanticResult(
	_ context.Context,
	candidateID string,
	harness experience.Harness,
	promptVersion string,
) (bool, error) {
	return store.existing[experienceSemanticExistingKey(
		candidateID,
		harness,
		promptVersion,
	)], nil
}

func (store *experienceSemanticTestStore) StoreExperienceSemanticResults(
	_ context.Context,
	values []experience.SemanticResult,
) (
	experience.SemanticDispositionCounts,
	experience.SemanticDispositionCounts,
	error,
) {
	var inserted experience.SemanticDispositionCounts
	for _, value := range values {
		if err := value.Validate(); err != nil {
			return experience.SemanticDispositionCounts{},
				experience.SemanticDispositionCounts{},
				err
		}
		incrementExperienceSemanticCounts(
			&inserted,
			value.Decision.Disposition,
		)
		store.existing[experienceSemanticExistingKey(
			value.Decision.CandidateID,
			value.Decision.Provenance.Harness,
			value.Decision.Provenance.PromptVersion,
		)] = true
	}
	store.stored = append(store.stored, values...)
	if store.replay {
		return experience.SemanticDispositionCounts{}, inserted, nil
	}
	return inserted, experience.SemanticDispositionCounts{}, nil
}

func TestPrepareExperienceSemanticPromptIsBoundedAndCandidateOnly(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("prompt")
	candidate.ObservedBehavior = "/Users/private/work/task.py " +
		strings.Repeat("observation-", 600)
	candidate.UserFeedback = "/home/private-user/scratch " +
		strings.Repeat("feedback-", 1000)
	candidate.Proposal.Scope.RepositoryPaths = []string{"task.py"}
	candidate.Evidence.Refs[0].Excerpt =
		"Updated /Users/private/work/task.py and /tmp/private-scratch."
	candidate.Proposal.Guidance.Rationale =
		"UNRELATED_PENDING_GUIDANCE_CANARY"
	candidate.OutcomeRefs = make([]string, 0, 30)
	for index := 0; index < 30; index++ {
		candidate.OutcomeRefs = append(
			candidate.OutcomeRefs,
			fmt.Sprintf("out_prompt_%02d", index),
		)
	}
	for index := int64(2); index < 9; index++ {
		turnIndex := index
		occurredAt := candidate.CreatedAt.Add(time.Duration(index) * time.Second)
		candidate.Evidence.Refs = append(
			candidate.Evidence.Refs,
			experience.EvidenceRef{
				Kind:       experience.EvidenceTranscriptTurn,
				SessionKey: "ses_prompt",
				TurnIndex:  &turnIndex,
				OccurredAt: &occurredAt,
				Excerpt:    strings.Repeat("bounded-excerpt-", 300),
			},
		)
	}
	candidate.Evidence.EvidenceSetID = candidate.Evidence.DeterministicID()
	candidate.CandidateID = candidate.DeterministicID()

	bounded, prompt, schema, inputHash, skipped, err :=
		prepareExperienceSemanticPrompt(
			SemanticHarnessClaude,
			[]experience.Candidate{candidate},
		)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) != 1 || skipped != 0 ||
		len(prompt)+len(schema) > maxExperienceSemanticPromptAndSchema ||
		len(schema) > maxClaudeInlineSchema ||
		!strings.HasPrefix(inputHash, "sha256:") {
		t.Fatalf(
			"bounded prompt = candidates %d skipped %d bytes %d/%d hash %q",
			len(bounded),
			skipped,
			len(prompt),
			len(schema),
			inputHash,
		)
	}
	for _, forbidden := range []string{
		"UNRELATED_PENDING_GUIDANCE_CANARY",
		candidate.Proposal.Guidance.Instruction,
		candidate.Provenance.InputHash,
		"/Users/private",
		"/home/private-user",
		"/tmp/private-scratch",
	} {
		if bytes.Contains(prompt, []byte(forbidden)) {
			t.Fatalf("experience prompt exposed unrelated value %q", forbidden)
		}
	}
	parts := bytes.SplitN(prompt, []byte("\n\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("experience prompt has no bounded JSON payload: %s", prompt)
	}
	var payload experienceSemanticPromptPayload
	if err := json.Unmarshal(parts[1], &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Candidates) != 1 ||
		payload.Candidates[0].ProjectIdentity != candidate.ProjectIdentity ||
		payload.Candidates[0].EvidenceSessionCount != 1 ||
		len(payload.Candidates[0].Excerpts) != maxExperienceSemanticExcerpts ||
		len(payload.Candidates[0].OutcomeIDs) != maxExperienceSemanticOutcomeIDs ||
		len(payload.Candidates[0].ObservedBehavior) >
			maxExperienceSemanticObservationBytes ||
		len(payload.Candidates[0].UserFeedback) >
			maxExperienceSemanticFeedbackBytes {
		t.Fatalf("bounded experience payload = %+v", payload)
	}
	encodedProject, err := json.Marshal([]string{candidate.ProjectIdentity})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(
		schema,
		append([]byte(`"enum":`), encodedProject...),
	) {
		t.Fatalf("semantic schema does not constrain project: %s", schema)
	}
	if !bytes.Contains(prompt, []byte("Evaluate each candidate independently")) ||
		!bytes.Contains(prompt, []byte("project-relative")) ||
		!bytes.Contains(prompt, []byte("task.py")) ||
		!bytes.Contains(schema, []byte(`"pattern"`)) {
		t.Fatalf("semantic prompt/schema do not constrain repository paths")
	}
	for _, excerpt := range payload.Candidates[0].Excerpts {
		if excerpt.SessionKey == "" || excerpt.TurnIndex == nil ||
			len(excerpt.Text) > maxExperienceSemanticExcerptBytes {
			t.Fatalf("uncited or oversized experience excerpt = %+v", excerpt)
		}
	}
}

func TestBoundedExperienceSemanticExcerptsPrioritizesEvidenceRoles(t *testing.T) {
	candidate := experienceSemanticTestCandidate("role-aware")
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	ref := func(
		index int64,
		role experience.EvidenceTurnRole,
		toolName string,
		text string,
	) experience.EvidenceRef {
		occurredAt := base.Add(time.Duration(index) * time.Second)
		return experience.EvidenceRef{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "ses_role_aware",
			TurnIndex:  &index,
			TurnRole:   role,
			ToolName:   toolName,
			OccurredAt: &occurredAt,
			Excerpt:    text,
		}
	}
	candidate.Family = experience.CandidateSuccessfulProcedure
	candidate.Proposal.Verifier = experience.Verifier{
		Kind: experience.VerifierCommandSucceeded,
		Command: &experience.CommandVerifierSpec{
			Command:          "go test ./...",
			CommandClass:     "test",
			ScrubbingVersion: "belay.redaction.v1",
		},
	}
	candidate.Evidence.Refs = []experience.EvidenceRef{
		ref(8, experience.EvidenceTurnAssistant, "", "Explain the reusable rule."),
		ref(11, experience.EvidenceTurnToolResult, "Bash", "ok"),
		ref(0, experience.EvidenceTurnUser, "", "Implement the bounded change."),
		ref(12, experience.EvidenceTurnAssistant, "", "Implemented and verified."),
		ref(2, experience.EvidenceTurnUser, "", "Keep retries idempotent."),
		ref(10, experience.EvidenceTurnToolCall, "Bash", "go test ./..."),
		ref(1, experience.EvidenceTurnToolCall, "Edit", `{"file_path":"a.go"}`),
		ref(4, experience.EvidenceTurnUser, "", "Except when the request is read-only."),
		ref(9, experience.EvidenceTurnSystem, "", "system noise"),
	}

	got := boundedExperienceSemanticExcerpts(candidate)
	if len(got) != maxExperienceSemanticExcerpts {
		t.Fatalf(
			"role-aware excerpts = %d, want %d: %+v",
			len(got),
			maxExperienceSemanticExcerpts,
			got,
		)
	}
	wantTurns := map[int64]bool{
		0:  true,
		1:  true,
		2:  true,
		4:  true,
		8:  true,
		10: true,
		11: true,
		12: true,
	}
	for _, excerpt := range got {
		if excerpt.TurnIndex == nil || !wantTurns[*excerpt.TurnIndex] {
			t.Fatalf("unexpected role-aware excerpt: %+v", excerpt)
		}
		delete(wantTurns, *excerpt.TurnIndex)
	}
	if len(wantTurns) != 0 {
		t.Fatalf("role-aware excerpts omitted turns: %+v", wantTurns)
	}
}

func TestExperienceSemanticInputHashCoversCompleteModelInput(t *testing.T) {
	prompt := []byte("prompt")
	schema := []byte(`{"type":"object"}`)
	base := experienceSemanticInputHash(
		prompt,
		schema,
		SemanticHarnessClaude,
		ExperiencePromptVersion,
	)
	values := []string{
		experienceSemanticInputHash(
			[]byte("changed prompt"),
			schema,
			SemanticHarnessClaude,
			ExperiencePromptVersion,
		),
		experienceSemanticInputHash(
			prompt,
			[]byte(`{"type":"array"}`),
			SemanticHarnessClaude,
			ExperiencePromptVersion,
		),
		experienceSemanticInputHash(
			prompt,
			schema,
			SemanticHarnessCodex,
			ExperiencePromptVersion,
		),
		experienceSemanticInputHash(
			prompt,
			schema,
			SemanticHarnessClaude,
			experience.SemanticProposalPromptVersionV1,
		),
	}
	for index, value := range values {
		if value == base {
			t.Fatalf("model input hash did not change for component %d", index)
		}
	}
}

func TestAnalyzeExperienceCandidatesRejectsInvalidOutputWithoutWrites(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("invalid")
	original := candidate
	valid := experienceSemanticValidOutput(candidate)
	var validObject map[string]any
	if err := json.Unmarshal(valid, &validObject); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
		raw    []byte
	}{
		{
			name: "root extra field",
			mutate: func(value map[string]any) {
				value["unexpected"] = true
			},
		},
		{
			name: "candidate extra field",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["unexpected"] = true
			},
		},
		{
			name: "propose branch contains decision field",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["reason_code"] = "reusable_supported"
			},
		},
		{
			name: "reject branch contains proposal field",
			mutate: func(value map[string]any) {
				value["candidates"] = []any{
					map[string]any{
						"candidate_id": candidate.CandidateID,
						"disposition":  "reject",
						"reason_code":  "not_reusable",
						"explanation":  "This is not reusable.",
						"confidence":   0.8,
						"guidance":     "Unexpected proposal guidance.",
					},
				}
			},
		},
		{
			name: "reject branch uses defer reason",
			mutate: func(value map[string]any) {
				value["candidates"] = []any{
					map[string]any{
						"candidate_id": candidate.CandidateID,
						"disposition":  "reject",
						"reason_code":  "insufficient_context",
						"explanation":  "The citations are insufficient.",
						"confidence":   0.8,
					},
				}
			},
		},
		{
			name: "defer branch uses reject reason",
			mutate: func(value map[string]any) {
				value["candidates"] = []any{
					map[string]any{
						"candidate_id": candidate.CandidateID,
						"disposition":  "defer",
						"reason_code":  "not_reusable",
						"explanation":  "This is not reusable.",
						"confidence":   0.8,
					},
				}
			},
		},
		{
			name: "unknown candidate",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["candidate_id"] = "exc_unknown"
			},
		},
		{
			name: "missing candidate",
			mutate: func(value map[string]any) {
				value["candidates"] = []any{}
			},
		},
		{
			name: "duplicate candidate",
			mutate: func(value map[string]any) {
				first := firstSemanticOutput(value)
				value["candidates"] = []any{first, first}
			},
		},
		{
			name: "project mismatch",
			mutate: func(value map[string]any) {
				firstSemanticScope(value)["project_identity"] = "other-project"
			},
		},
		{
			name: "project scope contains session key",
			mutate: func(value map[string]any) {
				firstSemanticScope(value)["session_key"] = "ses_invalid"
			},
		},
		{
			name: "session scope omits session key",
			mutate: func(value map[string]any) {
				firstSemanticScope(value)["kind"] = "session"
			},
		},
		{
			name: "path traversal",
			mutate: func(value map[string]any) {
				firstSemanticApplicability(value)["path_hints"] =
					[]any{"../secret"}
			},
		},
		{
			name: "absolute path",
			mutate: func(value map[string]any) {
				firstSemanticApplicability(value)["path_hints"] =
					[]any{"/private/secret"}
			},
		},
		{
			name: "learned deny",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["intervention_strength"] = "deny"
			},
		},
		{
			name: "unsupported harness",
			mutate: func(value map[string]any) {
				firstSemanticApplicability(value)["harnesses"] =
					[]any{"windsurf"}
			},
		},
		{
			name: "unsupported experience type",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["experience_type"] = "memory"
			},
		},
		{
			name: "unsupported verifier",
			mutate: func(value map[string]any) {
				firstSemanticVerifier(value)["kind"] = "magic"
			},
		},
		{
			name: "verifier extra parameter",
			mutate: func(value map[string]any) {
				parameters := firstSemanticVerifier(value)["parameters"].(map[string]any)
				parameters["unexpected"] = true
			},
		},
		{
			name: "absence verifier has empty coverage",
			mutate: func(value map[string]any) {
				firstSemanticVerifier(value)["coverage_requirements"] = []any{}
			},
		},
		{
			name: "multiline guidance",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["guidance"] = "first\nsecond"
			},
		},
		{
			name: "malformed confidence",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["confidence"] = "high"
			},
		},
		{
			name: "missing confidence",
			mutate: func(value map[string]any) {
				delete(firstSemanticOutput(value), "confidence")
			},
		},
		{
			name: "oversized output",
			raw: bytes.Repeat(
				[]byte("x"),
				maxExperienceSemanticOutputBytes+1,
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.raw
			if body == nil {
				var value map[string]any
				encoded, _ := json.Marshal(validObject)
				if err := json.Unmarshal(encoded, &value); err != nil {
					t.Fatal(err)
				}
				test.mutate(value)
				body, _ = json.Marshal(value)
			}
			store := &experienceSemanticTestStore{
				candidates: []experience.Candidate{candidate},
				existing:   make(map[string]bool),
			}
			_, err := analyzeExperienceCandidates(
				context.Background(),
				store,
				candidate.ProjectIdentity,
				SemanticHarnessClaude,
				func(
					context.Context,
					SemanticHarness,
					[]byte,
					[]byte,
				) (ExperienceSemanticHarnessResult, error) {
					return ExperienceSemanticHarnessResult{
						Output: body,
						Model:  "claude-test",
					}, nil
				},
				func() time.Time {
					return time.Date(
						2026,
						9,
						10,
						17,
						0,
						0,
						0,
						time.UTC,
					)
				},
			)
			if err == nil {
				t.Fatal("invalid semantic output was accepted")
			}
			if len(store.stored) != 0 {
				t.Fatalf("invalid output stored proposals: %+v", store.stored)
			}
			if !reflect.DeepEqual(candidate, original) {
				t.Fatal("invalid output mutated candidate authority or lifecycle")
			}
		})
	}
}

func TestExperienceSemanticOutputSchemaMatchesDomainStructure(t *testing.T) {
	candidate := experienceSemanticTestCandidate("schema-parity")
	schemaBody, err := experienceSemanticOutputSchema(
		experienceSemanticPromptPayload{
			Candidates: []experienceSemanticPromptCandidate{
				{
					CandidateID:     candidate.CandidateID,
					ProjectIdentity: candidate.ProjectIdentity,
					Excerpts: boundedExperienceSemanticExcerpts(
						candidate,
					),
				},
			},
		},
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
	var valid map[string]any
	if err := json.Unmarshal(experienceSemanticValidOutput(candidate), &valid); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(valid); err != nil {
		t.Fatalf("valid semantic output rejected by schema: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{
			name: "file absence coverage is required",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["verifier"] = map[string]any{
					"kind":                  "file_not_modified",
					"coverage_requirements": []any{},
					"parameters": map[string]any{
						"path": "generated/client.go",
					},
				}
			},
			wantErr: true,
		},
		{
			name: "path pattern absence coverage is required",
			mutate: func(value map[string]any) {
				firstSemanticVerifier(value)["coverage_requirements"] = []any{}
			},
			wantErr: true,
		},
		{
			name: "repeat failure absence coverage is required",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["verifier"] = map[string]any{
					"kind":                  "no_repeat_failure",
					"coverage_requirements": []any{},
					"parameters": map[string]any{
						"command_class":      "test",
						"normalized_pattern": "assertion failed",
						"window_turns":       10,
					},
				}
			},
			wantErr: true,
		},
		{
			name: "correction absence coverage is required",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["verifier"] = map[string]any{
					"kind":                  "user_correction_absent",
					"coverage_requirements": []any{},
					"parameters":            map[string]any{},
				}
			},
			wantErr: true,
		},
		{
			name: "non-absence coverage may be empty",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["verifier"] = map[string]any{
					"kind":                  "observation_only",
					"coverage_requirements": []any{},
					"parameters": map[string]any{
						"explanation": "Observe the cited behavior.",
					},
				}
			},
		},
		{
			name: "project scope forbids session key",
			mutate: func(value map[string]any) {
				firstSemanticScope(value)["session_key"] = "ses_schema-parity"
			},
			wantErr: true,
		},
		{
			name: "session scope requires session key",
			mutate: func(value map[string]any) {
				firstSemanticScope(value)["kind"] = "session"
			},
			wantErr: true,
		},
		{
			name: "session scope accepts session key",
			mutate: func(value map[string]any) {
				firstSemanticScope(value)["kind"] = "session"
				firstSemanticScope(value)["session_key"] = "ses_schema-parity"
			},
		},
		{
			name: "verification command classes are optional",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["verifier"] = map[string]any{
					"kind":                  "verification_after_last_edit",
					"coverage_requirements": []any{},
					"parameters": map[string]any{
						"require_success": true,
					},
				}
			},
		},
		{
			name: "correction marker families are optional",
			mutate: func(value map[string]any) {
				firstSemanticOutput(value)["verifier"] = map[string]any{
					"kind": "user_correction_absent",
					"coverage_requirements": []any{
						"transcript_complete",
					},
					"parameters": map[string]any{},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			body, err := json.Marshal(valid)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			err = resolved.Validate(value)
			if test.wantErr && err == nil {
				t.Fatal("schema accepted structurally invalid semantic output")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("schema rejected domain-valid structure: %v", err)
			}
		})
	}
}

func TestExperienceSemanticOutputSchemaAllowsEmptyCorrectionParameters(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("empty-correction-parameters")
	schemaBody, err := experienceSemanticOutputSchema(
		experienceSemanticPromptPayload{
			Candidates: []experienceSemanticPromptCandidate{
				{
					CandidateID:     candidate.CandidateID,
					ProjectIdentity: candidate.ProjectIdentity,
					Excerpts: boundedExperienceSemanticExcerpts(
						candidate,
					),
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(schemaBody, []byte(`"required":null`)) {
		t.Fatalf("semantic schema contains required:null: %s", schemaBody)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBody, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(experienceSemanticValidOutput(candidate), &output); err != nil {
		t.Fatal(err)
	}
	firstSemanticOutput(output)["verifier"] = map[string]any{
		"kind": "user_correction_absent",
		"coverage_requirements": []any{
			"transcript_complete",
		},
		"parameters": map[string]any{},
	}
	if err := resolved.Validate(output); err != nil {
		t.Fatalf(
			"schema rejected user_correction_absent parameters {}: %v",
			err,
		)
	}
}

func TestCompileExperienceSemanticResultsDefersUnknownEvidenceSupport(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("unknown-support")
	output, err := decodeExperienceSemanticOutput(
		experienceSemanticValidOutput(candidate),
	)
	if err != nil {
		t.Fatal(err)
	}
	output[0].Proposal.GuidanceSupportRefs = []string{"evr_unknown"}
	results, counts, err := compileExperienceSemanticResults(
		[]experience.Candidate{candidate},
		output,
		SemanticHarnessClaude,
		"claude-test",
		sha256Prefixed([]byte("input")),
		sha256Prefixed([]byte("output")),
		time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 ||
		results[0].Proposal != nil ||
		results[0].Decision.Disposition !=
			experience.SemanticDispositionDefer ||
		results[0].Decision.ReasonCode !=
			experience.SemanticReasonInsufficientContext ||
		counts.Deferred != 1 ||
		counts.Proposed != 0 {
		t.Fatalf("unsupported proposal did not fail soft: %+v / %+v", results, counts)
	}
}

func TestAnalyzeExperienceCandidatesStoresProvenanceForClaudeAndCodex(
	t *testing.T,
) {
	for _, harness := range []SemanticHarness{
		SemanticHarnessClaude,
		SemanticHarnessCodex,
	} {
		t.Run(string(harness), func(t *testing.T) {
			candidate := experienceSemanticTestCandidate(string(harness))
			store := &experienceSemanticTestStore{
				candidates: []experience.Candidate{candidate},
				existing:   make(map[string]bool),
			}
			generatedAt := time.Date(
				2026,
				9,
				10,
				18,
				0,
				0,
				0,
				time.UTC,
			)
			report, err := analyzeExperienceCandidates(
				context.Background(),
				store,
				candidate.ProjectIdentity,
				harness,
				func(
					_ context.Context,
					gotHarness SemanticHarness,
					prompt []byte,
					schema []byte,
				) (ExperienceSemanticHarnessResult, error) {
					if gotHarness != harness ||
						!bytes.Contains(prompt, []byte(candidate.CandidateID)) ||
						!json.Valid(schema) {
						t.Fatalf(
							"runner input = %q/%s/%s",
							gotHarness,
							prompt,
							schema,
						)
					}
					return ExperienceSemanticHarnessResult{
						Output: experienceSemanticValidOutput(candidate),
						Model:  string(harness) + "-test",
					}, nil
				},
				func() time.Time { return generatedAt },
			)
			if err != nil {
				t.Fatal(err)
			}
			if report.ProposalsInserted != 1 ||
				len(store.stored) != 1 {
				t.Fatalf("semantic report/store = %+v/%+v", report, store.stored)
			}
			proposal := store.stored[0].Proposal
			decision := store.stored[0].Decision
			if proposal == nil {
				t.Fatal("propose result did not store a proposal")
			}
			if proposal.CandidateID != candidate.CandidateID ||
				proposal.Authority != experience.AuthorityNone ||
				proposal.Provenance.Harness != experience.Harness(harness) ||
				proposal.Provenance.Model != string(harness)+"-test" ||
				proposal.Provenance.PromptVersion != ExperiencePromptVersion ||
				!strings.HasPrefix(proposal.Provenance.InputHash, "sha256:") ||
				!strings.HasPrefix(proposal.Provenance.OutputHash, "sha256:") ||
				!proposal.Provenance.GeneratedAt.Equal(generatedAt) ||
				decision.Disposition != experience.SemanticDispositionPropose ||
				decision.ProjectIdentity != candidate.ProjectIdentity ||
				decision.ProposalID != proposal.ProposalID {
				t.Fatalf("stored semantic result = %+v", store.stored[0])
			}
		})
	}
}

func TestAnalyzeExperienceCandidatesStoresExactlyOneResultPerBranch(
	t *testing.T,
) {
	proposed := experienceSemanticTestCandidate("branch-propose")
	rejected := experienceSemanticTestCandidate("branch-reject")
	deferred := experienceSemanticTestCandidate("branch-defer")
	store := &experienceSemanticTestStore{
		candidates: []experience.Candidate{proposed, rejected, deferred},
		existing:   make(map[string]bool),
	}
	var envelope map[string]any
	if err := json.Unmarshal(experienceSemanticValidOutput(proposed), &envelope); err != nil {
		t.Fatal(err)
	}
	propose := firstSemanticOutput(envelope)
	output, err := json.Marshal(map[string]any{
		"candidates": []any{
			propose,
			map[string]any{
				"candidate_id": rejected.CandidateID,
				"disposition":  "reject",
				"reason_code":  "temporary_or_task_specific",
				"explanation":  "The correction applies only to the cited task.",
				"confidence":   0.85,
			},
			map[string]any{
				"candidate_id": deferred.CandidateID,
				"disposition":  "defer",
				"reason_code":  "insufficient_context",
				"explanation":  "The citations do not establish reusable guidance.",
				"confidence":   0.6,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		proposed.ProjectIdentity,
		SemanticHarnessClaude,
		func(
			_ context.Context,
			_ SemanticHarness,
			prompt []byte,
			_ []byte,
		) (ExperienceSemanticHarnessResult, error) {
			if !bytes.Contains(
				prompt,
				[]byte("necessary exact command or path"),
			) {
				t.Fatalf("v11 exact-detail instruction missing: %s", prompt)
			}
			return ExperienceSemanticHarnessResult{
				Output: output,
				Model:  "claude-test",
			}, nil
		},
	)
	if err != nil ||
		report.Proposed != 1 ||
		report.Rejected != 1 ||
		report.Deferred != 1 ||
		report.ResultsInserted != 3 ||
		report.ProposalsInserted != 1 ||
		len(store.stored) != 3 {
		t.Fatalf("branch report/store/error = %+v/%+v/%v", report, store.stored, err)
	}
	proposalCount := 0
	for _, result := range store.stored {
		if result.Proposal != nil {
			proposalCount++
		}
	}
	if proposalCount != 1 {
		t.Fatalf("proposal rows represented by results = %d, want 1", proposalCount)
	}
}

func TestAnalyzeExperienceCandidatesReportsDispositionReplays(t *testing.T) {
	candidate := experienceSemanticTestCandidate("replay-report")
	store := &experienceSemanticTestStore{
		candidates: []experience.Candidate{candidate},
		existing:   make(map[string]bool),
		replay:     true,
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		candidate.ProjectIdentity,
		SemanticHarnessClaude,
		func(
			_ context.Context,
			_ SemanticHarness,
			prompt []byte,
			_ []byte,
		) (ExperienceSemanticHarnessResult, error) {
			if !bytes.Contains(
				prompt,
				[]byte("necessary exact command or path"),
			) {
				t.Fatalf("v11 exact-detail instruction missing: %s", prompt)
			}
			return ExperienceSemanticHarnessResult{
				Output: experienceSemanticValidOutput(candidate),
				Model:  "claude-test",
			}, nil
		},
	)
	if err != nil ||
		report.Proposed != 1 ||
		report.ResultsInserted != 0 ||
		report.ResultsReplayed != 1 ||
		report.ProposedReplayed != 1 ||
		report.ProposalsReplayed != 1 {
		t.Fatalf("replay report/error = %+v/%v", report, err)
	}
}

func TestAnalyzeExperienceCandidatesRequestedHarnessIsIndependent(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("selected-harness")
	store := &experienceSemanticTestStore{
		candidates: []experience.Candidate{candidate},
		existing: map[string]bool{
			experienceSemanticExistingKey(
				candidate.CandidateID,
				experience.HarnessClaude,
				ExperiencePromptVersion,
			): true,
		},
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		candidate.ProjectIdentity,
		SemanticHarnessCodex,
		func(
			_ context.Context,
			harness SemanticHarness,
			_ []byte,
			_ []byte,
		) (ExperienceSemanticHarnessResult, error) {
			if harness != SemanticHarnessCodex {
				t.Fatalf("selected harness = %q", harness)
			}
			return ExperienceSemanticHarnessResult{
				Output: experienceSemanticValidOutput(candidate),
				Model:  "codex-test",
			}, nil
		},
	)
	if err != nil ||
		report.CandidatesSkippedExisting != 0 ||
		report.ProposalsInserted != 1 ||
		len(store.stored) != 1 ||
		store.stored[0].Decision.Provenance.Harness !=
			experience.HarnessCodex {
		t.Fatalf(
			"harness-scoped report/store/error = %+v/%+v/%v",
			report,
			store.stored,
			err,
		)
	}
}

func TestAnalyzeExperienceCandidatesReanalyzesV11WithV12Provenance(
	t *testing.T,
) {
	if ExperiencePromptVersion != experience.SemanticProposalPromptVersionV12 {
		t.Fatalf("experience prompt version = %q, want v12", ExperiencePromptVersion)
	}
	candidate := experienceSemanticTestCandidate("prompt-upgrade")
	store := &experienceSemanticTestStore{
		candidates: []experience.Candidate{candidate},
		existing: map[string]bool{
			experienceSemanticExistingKey(
				candidate.CandidateID,
				experience.HarnessClaude,
				experience.SemanticProposalPromptVersionV11,
			): true,
		},
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		candidate.ProjectIdentity,
		SemanticHarnessClaude,
		func(
			context.Context,
			SemanticHarness,
			[]byte,
			[]byte,
		) (ExperienceSemanticHarnessResult, error) {
			return ExperienceSemanticHarnessResult{
				Output: experienceSemanticValidOutput(candidate),
				Model:  "claude-test",
			}, nil
		},
	)
	if err != nil || report.CandidatesSkippedExisting != 0 ||
		report.ProposalsInserted != 1 || len(store.stored) != 1 ||
		store.stored[0].Proposal == nil ||
		store.stored[0].Proposal.Provenance.PromptVersion !=
			experience.SemanticProposalPromptVersionV12 ||
		store.stored[0].Decision.Provenance.PromptVersion !=
			experience.SemanticProposalPromptVersionV12 {
		t.Fatalf(
			"prompt-upgrade report/store/error = %+v/%+v/%v",
			report,
			store.stored,
			err,
		)
	}
}

func TestSemanticProposalPreservesUserRuleAndExceptionBoundary(t *testing.T) {
	candidate := experienceSemanticTestCandidate("boundary")
	candidate.Family = experience.CandidateSuccessfulProcedure
	candidate.Evidence.Refs[0].TurnRole = experience.EvidenceTurnUser
	candidate.Evidence.Refs[0].Excerpt =
		"Rule: Every lease-protected write must carry a monotonically increasing fencing token. " +
			"Exception: A passive observer that performs no protected write does not require a fencing token."
	candidate.Evidence.Refs[1].TurnRole = experience.EvidenceTurnAssistant
	candidate.Evidence.Refs[1].Excerpt =
		"Fence writes, but observers must accept and ignore stale tokens."
	candidate.Evidence.EvidenceSetID = candidate.Evidence.DeterministicID()

	excerpts := boundedExperienceSemanticExcerpts(candidate)
	if len(excerpts) != 2 {
		t.Fatalf("boundary excerpts = %+v", excerpts)
	}
	userRef := excerpts[0].EvidenceRefID
	assistantRef := excerpts[1].EvidenceRefID
	if excerpts[0].TurnRole != experience.EvidenceTurnUser {
		userRef, assistantRef = assistantRef, userRef
	}

	supported := experienceSemanticProposeOutputCandidate{
		Guidance: "Every lease-protected write must carry a monotonically increasing fencing token.",
		Exceptions: []string{
			"A passive observer that performs no protected write does not require a fencing token.",
		},
	}
	support := &experience.EvidenceSupport{
		GuidanceRefs:  []string{userRef},
		ExceptionRefs: [][]string{{userRef}},
		VerifierRefs:  []string{assistantRef},
	}
	if err := semanticProposalPreservesEvidenceBoundaries(
		candidate,
		supported,
		support,
	); err != nil {
		t.Fatalf("exact user boundary rejected: %v", err)
	}

	harmful := supported
	harmful.Guidance =
		"Fence every write, but accept and ignore any token passed to an observer."
	harmful.Exceptions = []string{
		"A passive observer must not reject a stale token.",
	}
	harmfulSupport := &experience.EvidenceSupport{
		GuidanceRefs:  []string{assistantRef},
		ExceptionRefs: [][]string{{assistantRef}},
		VerifierRefs:  []string{assistantRef},
	}
	if err := semanticProposalPreservesEvidenceBoundaries(
		candidate,
		harmful,
		harmfulSupport,
	); !errors.Is(err, errExperienceSemanticEvidenceSupport) {
		t.Fatalf("strengthened assistant restatement error = %v", err)
	}
}

func TestSemanticProposalHarnessesTransferRepositoryProcedures(t *testing.T) {
	candidate := experienceSemanticTestCandidate("repository-procedure")
	candidate.Family = experience.CandidateSuccessfulProcedure
	value := experienceSemanticProposeOutputCandidate{
		Applicability: experienceSemanticOutputApplicability{
			PathHints: []string{"task.py"},
			Harnesses: []experience.Harness{experience.HarnessCodex},
			Models:    []string{},
		},
	}
	got := semanticProposalHarnesses(candidate, value)
	if !reflect.DeepEqual(
		got,
		[]experience.Harness{
			experience.HarnessClaude,
			experience.HarnessCodex,
			experience.HarnessCursor,
			experience.HarnessAntigravity,
		},
	) {
		t.Fatalf("repository procedure harnesses = %v", got)
	}

	value.Applicability.PathHints = []string{".codex/config.toml"}
	got = semanticProposalHarnesses(candidate, value)
	if !reflect.DeepEqual(got, []experience.Harness{experience.HarnessCodex}) {
		t.Fatalf("harness-specific procedure harnesses = %v", got)
	}

	value.Applicability.PathHints = []string{".cursor/rules/belay.mdc"}
	value.Applicability.Harnesses = []experience.Harness{
		experience.HarnessCursor,
	}
	got = semanticProposalHarnesses(candidate, value)
	if !reflect.DeepEqual(got, []experience.Harness{experience.HarnessCursor}) {
		t.Fatalf("Cursor-specific procedure harnesses = %v", got)
	}

	value.Applicability.PathHints = []string{".agents/rules/belay.md"}
	value.Applicability.Harnesses = []experience.Harness{
		experience.HarnessAntigravity,
	}
	got = semanticProposalHarnesses(candidate, value)
	if !reflect.DeepEqual(
		got,
		[]experience.Harness{experience.HarnessAntigravity},
	) {
		t.Fatalf("Antigravity-specific procedure harnesses = %v", got)
	}
}

func TestSemanticProposalPlainTextAllowsComparisons(t *testing.T) {
	for _, value := range []string{
		"Advance only when incoming version > stored version.",
		"Select rows observed_at <= example_time.",
	} {
		if !semanticProposalPlainText(value) {
			t.Fatalf("comparison text rejected: %q", value)
		}
	}
	for _, value := range []string{
		"See https://example.test/rule",
		"Use <script>alert(1)</script>",
		"Read [the rule](docs/rule.md)",
		"Run `go test ./...`",
	} {
		if semanticProposalPlainText(value) {
			t.Fatalf("markup text accepted: %q", value)
		}
	}
}

func TestAnalyzeExperienceCandidatesFiltersExistingBeforeBatchLimit(
	t *testing.T,
) {
	const existingCount = maxExperienceSemanticCandidates
	candidates := make([]experience.Candidate, 0, existingCount+4)
	existing := make(map[string]bool, existingCount)
	for index := 0; index < existingCount+4; index++ {
		candidate := experienceSemanticTestCandidate(
			fmt.Sprintf("page-%02d", index),
		)
		candidates = append(candidates, candidate)
		if index < existingCount {
			existing[experienceSemanticExistingKey(
				candidate.CandidateID,
				experience.HarnessClaude,
				ExperiencePromptVersion,
			)] = true
		}
	}
	store := &experienceSemanticTestStore{
		candidates: candidates,
		existing:   existing,
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		candidates[0].ProjectIdentity,
		SemanticHarnessClaude,
		func(
			_ context.Context,
			_ SemanticHarness,
			prompt []byte,
			_ []byte,
		) (ExperienceSemanticHarnessResult, error) {
			return ExperienceSemanticHarnessResult{
				Output: experienceSemanticOutputForPrompt(t, prompt, candidates),
				Model:  "claude-test",
			}, nil
		},
	)
	if err != nil ||
		report.CandidatesConsidered != existingCount+4 ||
		report.CandidatesSkippedExisting != existingCount ||
		report.PendingCandidatesDeferred != 0 ||
		report.ProposalsInserted != 4 ||
		len(store.stored) != 4 {
		t.Fatalf(
			"existing-page report/store/error = %+v/%d/%v",
			report,
			len(store.stored),
			err,
		)
	}
}

func TestAnalyzeExperienceCandidatesReportsQueryCapAndDeferredPending(
	t *testing.T,
) {
	candidates := make([]experience.Candidate, 0, maxExperienceSemanticCandidateQuery)
	for index := 0; index < maxExperienceSemanticCandidateQuery; index++ {
		candidates = append(
			candidates,
			experienceSemanticTestCandidate(fmt.Sprintf("cap-%03d", index)),
		)
	}
	store := &experienceSemanticTestStore{
		candidates: candidates,
		existing:   make(map[string]bool),
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		candidates[0].ProjectIdentity,
		SemanticHarnessCodex,
		func(
			_ context.Context,
			_ SemanticHarness,
			prompt []byte,
			_ []byte,
		) (ExperienceSemanticHarnessResult, error) {
			return ExperienceSemanticHarnessResult{
				Output: experienceSemanticOutputForPrompt(t, prompt, candidates),
				Model:  "codex-test",
			}, nil
		},
	)
	if err != nil ||
		!report.CandidateQueryCapReached ||
		report.PendingCandidatesDeferred !=
			maxExperienceSemanticCandidateQuery-maxExperienceSemanticCandidates ||
		report.ProposalsInserted != maxExperienceSemanticCandidates {
		t.Fatalf("capped candidate report/error = %+v/%v", report, err)
	}
}

func TestRunInstalledExperienceSemanticHarnessReusesClaudeAndCodexTransport(
	t *testing.T,
) {
	harnessDir := t.TempDir()
	writeSemanticHarness(t, harnessDir, "claude", `
cat >/dev/null
printf '%s\n' '{"model":"claude-fake","structured_output":{"candidates":[]}}'
`)
	writeSemanticHarness(t, harnessDir, "codex", `
output_path=""
expect=""
for arg in "$@"; do
	if [ "$expect" = "output" ]; then
		output_path="$arg"
		expect=""
	elif [ "$arg" = "--output-last-message" ]; then
		expect="output"
	fi
done
cat >/dev/null
printf '%s\n' '{"candidates":[]}' > "$output_path"
`)
	t.Setenv(
		"PATH",
		harnessDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	for _, test := range []struct {
		harness SemanticHarness
		model   string
	}{
		{SemanticHarnessClaude, "claude-fake"},
		{SemanticHarnessCodex, ""},
	} {
		result, err := RunInstalledExperienceSemanticHarness(
			context.Background(),
			test.harness,
			[]byte("bounded prompt"),
			[]byte(`{"type":"object"}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Model != test.model ||
			!bytes.Equal(
				bytes.TrimSpace(result.Output),
				[]byte(`{"candidates":[]}`),
			) {
			t.Fatalf("raw harness result = %+v", result)
		}
	}
}

func TestAnalyzeExperienceCandidatesDurablyDefersInsufficientEvidence(
	t *testing.T,
) {
	existing := experienceSemanticTestCandidate("existing")
	insufficient := experienceSemanticTestCandidate("insufficient")
	insufficient.Evidence.Refs = insufficient.Evidence.Refs[:1]
	insufficient.Evidence.EvidenceSetID =
		insufficient.Evidence.DeterministicID()
	insufficient.CandidateID = insufficient.DeterministicID()
	store := &experienceSemanticTestStore{
		candidates: []experience.Candidate{existing, insufficient},
		existing: map[string]bool{
			experienceSemanticExistingKey(
				existing.CandidateID,
				experience.HarnessCodex,
				ExperiencePromptVersion,
			): true,
		},
	}
	report, err := AnalyzeExperienceCandidates(
		context.Background(),
		store,
		existing.ProjectIdentity,
		SemanticHarnessCodex,
		func(
			_ context.Context,
			_ SemanticHarness,
			prompt []byte,
			_ []byte,
		) (ExperienceSemanticHarnessResult, error) {
			if !bytes.Contains(prompt, []byte(insufficient.CandidateID)) {
				t.Fatal("insufficient candidate was omitted from semantic input")
			}
			return ExperienceSemanticHarnessResult{
				Output: experienceSemanticDecisionOutput(
					insufficient,
					experience.SemanticDispositionDefer,
					experience.SemanticReasonInsufficientContext,
				),
				Model: "codex-test",
			}, nil
		},
	)
	if err != nil ||
		report.CandidatesConsidered != 2 ||
		report.CandidatesSkippedExisting != 1 ||
		report.CandidatesSkippedInsufficient != 0 ||
		report.Deferred != 1 ||
		report.ResultsInserted != 1 ||
		len(store.stored) != 1 ||
		store.stored[0].Proposal != nil {
		t.Fatalf("defer report/store/error = %+v/%+v/%v", report, store.stored, err)
	}
	report, err = AnalyzeExperienceCandidates(
		context.Background(),
		store,
		existing.ProjectIdentity,
		SemanticHarnessCodex,
		func(
			context.Context,
			SemanticHarness,
			[]byte,
			[]byte,
		) (ExperienceSemanticHarnessResult, error) {
			return ExperienceSemanticHarnessResult{}, errors.New(
				"durable defer must suppress retry",
			)
		},
	)
	if err != nil || report.CandidatesSkippedExisting != 2 ||
		len(store.stored) != 1 {
		t.Fatalf("durable defer retry report/store/error = %+v/%+v/%v", report, store.stored, err)
	}
}

func experienceSemanticExistingKey(
	candidateID string,
	harness experience.Harness,
	promptVersion string,
) string {
	return candidateID + "\x00" + string(harness) + "\x00" + promptVersion
}

func experienceSemanticTestCandidate(seed string) experience.Candidate {
	project := "git@example.test:doplexlabs/belay-engine.git"
	firstIndex := int64(1)
	secondIndex := int64(2)
	firstTime := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Second)
	evidence := experience.EvidenceSet{
		Availability: experience.EvidenceAvailable,
		Refs: []experience.EvidenceRef{
			{
				Kind:       experience.EvidenceTranscriptTurn,
				SessionKey: "ses_" + seed,
				TurnIndex:  &firstIndex,
				OccurredAt: &firstTime,
				Excerpt:    "The assistant changed the generated file.",
			},
			{
				Kind:       experience.EvidenceTranscriptTurn,
				SessionKey: "ses_" + seed,
				TurnIndex:  &secondIndex,
				OccurredAt: &secondTime,
				Excerpt:    "No, edit the schema source instead.",
			},
		},
	}
	evidence.EvidenceSetID = evidence.DeterministicID()
	value := experience.Candidate{
		SchemaVersion:    experience.CandidateSchemaVersion,
		Family:           experience.CandidateCorrection,
		ProjectIdentity:  project,
		ObservedBehavior: "The user corrected the cited agent behavior.",
		UserFeedback:     "No, edit the schema source instead.",
		Evidence:         evidence,
		OutcomeRefs:      []string{"out_" + seed},
		Proposal: experience.ExperienceProposal{
			Type: experience.ExperiencePreference,
			Scope: experience.Scope{
				Kind:            experience.ScopeProject,
				ProjectIdentity: project,
			},
			Applicability: experience.Applicability{
				SemanticDescription: "Pending semantic compilation.",
			},
			Guidance: experience.Guidance{
				Instruction:          "Pending semantic compilation.",
				Rationale:            "No semantic rule has been generated.",
				InterventionStrength: experience.InterventionObserve,
			},
			Verifier: experience.Verifier{
				Kind: experience.VerifierObservationOnly,
				ObservationOnly: &experience.ObservationOnlySpec{
					Explanation: "Await semantic compilation.",
				},
			},
			SemanticCompilationPending: true,
		},
		Provenance: experience.Provenance{
			ExtractorVersion: "candidate.det.v1",
			InputHash:        sha256Prefixed([]byte("candidate " + seed)),
			GeneratedAt:      secondTime,
		},
		Authority:      experience.AuthorityNone,
		LifecycleState: experience.LifecycleCandidate,
		CreatedAt:      secondTime,
	}
	value.CandidateID = value.DeterministicID()
	return value
}

func experienceSemanticValidOutput(
	candidate experience.Candidate,
) []byte {
	return experienceSemanticValidOutputs([]experience.Candidate{candidate})
}

func experienceSemanticValidOutputs(
	candidates []experience.Candidate,
) []byte {
	values := make([]any, 0, len(candidates))
	for _, candidate := range candidates {
		excerpts := boundedExperienceSemanticExcerpts(candidate)
		if len(excerpts) == 0 {
			panic("semantic test candidate has no evidence excerpts")
		}
		guidanceSupport := []any{excerpts[0].EvidenceRefID}
		verifierSupport := []any{
			excerpts[len(excerpts)-1].EvidenceRefID,
		}
		values = append(values, map[string]any{
			"candidate_id":    candidate.CandidateID,
			"disposition":     "propose",
			"experience_type": "preference",
			"scope": map[string]any{
				"kind":             "project",
				"project_identity": candidate.ProjectIdentity,
			},
			"guidance":  "Edit the schema source instead of generated files.",
			"rationale": "The cited correction redirected the edit to its source.",
			"applicability": map[string]any{
				"task_families":        []any{"code_change"},
				"path_hints":           []any{"generated/**"},
				"harnesses":            []any{"claude", "codex"},
				"models":               []any{},
				"semantic_description": "Tasks that modify generated code.",
			},
			"exceptions":             []any{},
			"guidance_support_refs":  guidanceSupport,
			"exception_support_refs": []any{},
			"verifier_support_refs":  verifierSupport,
			"intervention_strength":  "advise",
			"verifier": map[string]any{
				"kind":                  "path_pattern_not_modified",
				"coverage_requirements": []any{"workspace_state_captured"},
				"parameters": map[string]any{
					"patterns": []any{"generated/**"},
				},
			},
			"confidence": 0.9,
		})
	}
	value := map[string]any{
		"candidates": values,
	}
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func experienceSemanticDecisionOutput(
	candidate experience.Candidate,
	disposition experience.SemanticDisposition,
	reason experience.SemanticDecisionReasonCode,
) []byte {
	body, err := json.Marshal(map[string]any{
		"candidates": []any{
			map[string]any{
				"candidate_id": candidate.CandidateID,
				"disposition":  disposition,
				"reason_code":  reason,
				"explanation":  "The supplied citations do not support reusable guidance.",
				"confidence":   0.7,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

func experienceSemanticOutputForPrompt(
	t *testing.T,
	prompt []byte,
	candidates []experience.Candidate,
) []byte {
	t.Helper()
	parts := bytes.SplitN(prompt, []byte("\n\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("experience prompt has no JSON payload: %s", prompt)
	}
	var payload experienceSemanticPromptPayload
	if err := json.Unmarshal(parts[1], &payload); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]experience.Candidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.CandidateID] = candidate
	}
	selected := make([]experience.Candidate, 0, len(payload.Candidates))
	for _, value := range payload.Candidates {
		candidate, ok := byID[value.CandidateID]
		if !ok {
			t.Fatalf("prompt candidate %q not found", value.CandidateID)
		}
		selected = append(selected, candidate)
	}
	return experienceSemanticValidOutputs(selected)
}

func firstSemanticOutput(value map[string]any) map[string]any {
	return value["candidates"].([]any)[0].(map[string]any)
}

func firstSemanticScope(value map[string]any) map[string]any {
	return firstSemanticOutput(value)["scope"].(map[string]any)
}

func firstSemanticApplicability(value map[string]any) map[string]any {
	return firstSemanticOutput(value)["applicability"].(map[string]any)
}

func firstSemanticVerifier(value map[string]any) map[string]any {
	return firstSemanticOutput(value)["verifier"].(map[string]any)
}

func TestExperienceSemanticPromptDoesNotUseFilesystemContent(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, "secret.txt"),
		[]byte("FILESYSTEM_SECRET_CANARY"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	candidate := experienceSemanticTestCandidate("filesystem")
	_, prompt, _, _, _, err := prepareExperienceSemanticPrompt(
		SemanticHarnessCodex,
		[]experience.Candidate{candidate},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(prompt, []byte("FILESYSTEM_SECRET_CANARY")) {
		t.Fatal("experience semantic prompt read unrelated filesystem content")
	}
}

func TestExperienceSemanticOutputAcceptsCursorHarness(t *testing.T) {
	candidate := experienceSemanticTestCandidate("cursor-harness")
	schemaBody, err := experienceSemanticOutputSchema(
		experienceSemanticPromptPayload{
			Candidates: []experienceSemanticPromptCandidate{{
				CandidateID:     candidate.CandidateID,
				ProjectIdentity: candidate.ProjectIdentity,
				Excerpts: boundedExperienceSemanticExcerpts(
					candidate,
				),
			}},
		},
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
	var value map[string]any
	if err := json.Unmarshal(
		experienceSemanticValidOutput(candidate),
		&value,
	); err != nil {
		t.Fatal(err)
	}
	firstSemanticApplicability(value)["harnesses"] = []any{
		"claude",
		"codex",
		"cursor",
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("cursor harness rejected by output schema: %v", err)
	}

	firstSemanticApplicability(value)["harnesses"] = []any{"cursor"}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("cursor-only harness rejected by output schema: %v", err)
	}
}

func TestExperienceSemanticPromptDescribesEveryHarness(t *testing.T) {
	candidate := experienceSemanticTestCandidate("cursor-prompt")
	_, prompt, _, _, _, err := prepareExperienceSemanticPrompt(
		SemanticHarnessClaude,
		[]experience.Candidate{candidate},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"include claude, codex, cursor, and antigravity",
		"cursor for Cursor Agent",
		"antigravity for Antigravity",
	} {
		if !strings.Contains(string(prompt), required) {
			t.Fatalf("prompt missing harness description %q", required)
		}
	}
}

func TestSemanticProposalHarnessSpecificPathsCoverCursor(t *testing.T) {
	for _, value := range []string{
		"CLAUDE.md",
		"AGENTS.md",
		".claude/settings.json",
		".codex/rules/default.rules",
		".cursorrules",
		".cursor/rules/belay.mdc",
		".agents/rules/belay.md",
		".agents/rules/other.md",
		".agent/rules/belay.md",
	} {
		if !semanticProposalHarnessSpecificPath(value) {
			t.Fatalf("%q was not treated as harness specific", value)
		}
	}
	if semanticProposalHarnessSpecificPath("internal/example.go") {
		t.Fatal("source path was treated as harness specific")
	}
}

func TestExperienceSemanticOutputAcceptsAntigravityHarness(t *testing.T) {
	candidate := experienceSemanticTestCandidate("antigravity-harness")
	schemaBody, err := experienceSemanticOutputSchema(
		experienceSemanticPromptPayload{
			Candidates: []experienceSemanticPromptCandidate{{
				CandidateID:     candidate.CandidateID,
				ProjectIdentity: candidate.ProjectIdentity,
				Excerpts: boundedExperienceSemanticExcerpts(
					candidate,
				),
			}},
		},
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
	var value map[string]any
	if err := json.Unmarshal(
		experienceSemanticValidOutput(candidate),
		&value,
	); err != nil {
		t.Fatal(err)
	}
	firstSemanticApplicability(value)["harnesses"] = []any{
		"claude",
		"codex",
		"cursor",
		"antigravity",
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("every harness rejected by output schema: %v", err)
	}

	firstSemanticApplicability(value)["harnesses"] = []any{"antigravity"}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("antigravity-only harness rejected by output schema: %v", err)
	}

	firstSemanticApplicability(value)["harnesses"] = []any{"windsurf"}
	if err := resolved.Validate(value); err == nil {
		t.Fatal("unsupported harness accepted by output schema")
	}
}
