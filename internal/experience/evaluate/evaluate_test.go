package evaluate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

var evaluationTestTime = time.Date(
	2026,
	time.September,
	10,
	18,
	0,
	0,
	0,
	time.UTC,
)

func TestVerifierCatalogBusinessRules(t *testing.T) {
	tests := []struct {
		name      string
		verifier  experience.Verifier
		configure func(*Input)
		want      experience.VerifierState
		wantCited int
	}{
		{
			name: "command observed satisfied",
			verifier: experience.Verifier{
				Kind: experience.VerifierCommandObserved,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				Command: &experience.CommandVerifierSpec{
					Command:          "go test ./internal/experience/evaluate",
					CommandClass:     "test",
					ScrubbingVersion: "belay.redaction.v1",
				},
			},
			configure: func(input *Input) {
				input.NormalizedVerifier = NormalizedVerifier{
					DeclaredCommand:         "go test ./internal/experience/evaluate",
					CommandSignature:        "go test ./internal/experience/evaluate",
					CommandScrubbingVersion: "belay.redaction.v1",
				}
				input.Facts.Commands = []CommandFact{{
					FactMeta:  factMeta(4, 4, 5),
					Signature: "go test ./internal/experience/evaluate",
					Class:     "test",
					Result:    FactResultUnknown,
				}}
			},
			want:      experience.VerifierSatisfied,
			wantCited: 2,
		},
		{
			name: "command observed violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierCommandObserved,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				Command: &experience.CommandVerifierSpec{
					Command:          "go test ./internal/experience/evaluate",
					CommandClass:     "test",
					ScrubbingVersion: "belay.redaction.v1",
				},
			},
			configure: func(input *Input) {
				input.NormalizedVerifier = NormalizedVerifier{
					DeclaredCommand:         "go test ./internal/experience/evaluate",
					CommandSignature:        "go test ./internal/experience/evaluate",
					CommandScrubbingVersion: "belay.redaction.v1",
				}
			},
			want:      experience.VerifierViolated,
			wantCited: 1,
		},
		{
			name:     "command succeeded satisfied",
			verifier: commandSucceededVerifier(),
			configure: func(input *Input) {
				input.NormalizedVerifier = commandMatcher()
				input.Facts.Commands = []CommandFact{{
					FactMeta:  factMeta(5, 5),
					Signature: "go test ./...",
					Class:     "test",
					Result:    FactResultSucceeded,
				}}
			},
			want:      experience.VerifierSatisfied,
			wantCited: 1,
		},
		{
			name: "command succeeded violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierCommandSucceeded,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				Command: &experience.CommandVerifierSpec{
					Command:          "go test ./...",
					CommandClass:     "test",
					ScrubbingVersion: "belay.redaction.v1",
				},
			},
			configure: func(input *Input) {
				input.NormalizedVerifier = NormalizedVerifier{
					DeclaredCommand:         "go test ./...",
					CommandSignature:        "go test ./...",
					CommandScrubbingVersion: "belay.redaction.v1",
				}
				input.Facts.Commands = []CommandFact{{
					FactMeta:  factMeta(5, 5, 6),
					Signature: "go test ./...",
					Class:     "test",
					Result:    FactResultFailed,
				}}
			},
			want:      experience.VerifierViolated,
			wantCited: 2,
		},
		{
			name: "file not modified satisfied",
			verifier: experience.Verifier{
				Kind: experience.VerifierFileNotModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageWorkspaceCaptured,
				},
				File: &experience.FileVerifierSpec{
					Path: "generated/client.go",
				},
			},
			want:      experience.VerifierSatisfied,
			wantCited: 1,
		},
		{
			name: "file not modified violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierFileNotModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageWorkspaceCaptured,
				},
				File: &experience.FileVerifierSpec{
					Path: "generated/client.go",
				},
			},
			configure: func(input *Input) {
				input.Facts.FileChanges = []FileChangeFact{{
					FactMeta: factMeta(7, 7),
					Path:     "generated/client.go",
				}}
			},
			want:      experience.VerifierViolated,
			wantCited: 1,
		},
		{
			name: "file modified satisfied",
			verifier: experience.Verifier{
				Kind: experience.VerifierFileModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageCanonicalComplete,
				},
				File: &experience.FileVerifierSpec{
					Path: "internal/service.go",
				},
			},
			configure: func(input *Input) {
				input.Facts.FileChanges = []FileChangeFact{{
					FactMeta: factMeta(7, 7),
					Path:     "internal/service.go",
				}}
			},
			want:      experience.VerifierSatisfied,
			wantCited: 1,
		},
		{
			name: "file modified violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierFileModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageCanonicalComplete,
				},
				File: &experience.FileVerifierSpec{
					Path: "internal/service.go",
				},
			},
			want:      experience.VerifierViolated,
			wantCited: 1,
		},
		{
			name: "path pattern satisfied",
			verifier: experience.Verifier{
				Kind: experience.VerifierPathPatternNotModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageWorkspaceCaptured,
				},
				PathPattern: &experience.PathPatternVerifierSpec{
					Patterns: []string{"generated/**"},
				},
			},
			want:      experience.VerifierSatisfied,
			wantCited: 1,
		},
		{
			name: "path pattern violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierPathPatternNotModified,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageWorkspaceCaptured,
				},
				PathPattern: &experience.PathPatternVerifierSpec{
					Patterns: []string{"generated/**"},
				},
			},
			configure: func(input *Input) {
				input.Facts.FileChanges = []FileChangeFact{{
					FactMeta: factMeta(8, 8),
					Path:     "generated/api/client.go",
				}}
			},
			want:      experience.VerifierViolated,
			wantCited: 1,
		},
		{
			name: "verification after last edit satisfied",
			verifier: experience.Verifier{
				Kind: experience.VerifierVerificationAfterLastEdit,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
					experience.CoverageCanonicalComplete,
				},
				VerificationAfterLastEdit: &experience.VerificationAfterLastEditSpec{
					CommandClasses: []string{"test"},
					RequireSuccess: true,
				},
			},
			configure: func(input *Input) {
				input.Facts.FileChanges = []FileChangeFact{{
					FactMeta: factMeta(9, 9),
					Path:     "internal/service.go",
				}}
				input.Facts.Verifications = []VerificationFact{{
					FactMeta:     factMeta(12, 12, 13),
					CommandClass: "test",
					Result:       FactResultSucceeded,
				}}
			},
			want:      experience.VerifierSatisfied,
			wantCited: 3,
		},
		{
			name: "verification after last edit violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierVerificationAfterLastEdit,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
					experience.CoverageCanonicalComplete,
				},
				VerificationAfterLastEdit: &experience.VerificationAfterLastEditSpec{
					CommandClasses: []string{"test"},
					RequireSuccess: true,
				},
			},
			configure: func(input *Input) {
				input.Facts.FileChanges = []FileChangeFact{{
					FactMeta: factMeta(9, 9),
					Path:     "internal/service.go",
				}}
				input.Facts.Verifications = []VerificationFact{{
					FactMeta:     factMeta(12, 12, 13),
					CommandClass: "test",
					Result:       FactResultFailed,
				}}
			},
			want:      experience.VerifierViolated,
			wantCited: 3,
		},
		{
			name:     "no repeat failure satisfied",
			verifier: noRepeatFailureVerifier(20),
			configure: func(input *Input) {
				input.Facts.Failures = []FailureFact{{
					FactMeta:          factMeta(14, 14),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				}}
			},
			want:      experience.VerifierSatisfied,
			wantCited: 2,
		},
		{
			name:     "no repeat failure violated",
			verifier: noRepeatFailureVerifier(20),
			configure: func(input *Input) {
				input.Facts.Failures = []FailureFact{
					{
						FactMeta:          factMeta(4, 4),
						CommandClass:      "test",
						NormalizedPattern: "generated client mismatch",
					},
					{
						FactMeta:          factMeta(14, 14),
						CommandClass:      "test",
						NormalizedPattern: "generated client mismatch",
					},
				}
			},
			want:      experience.VerifierViolated,
			wantCited: 2,
		},
		{
			name: "user correction absent satisfied",
			verifier: experience.Verifier{
				Kind: experience.VerifierUserCorrectionAbsent,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				UserCorrectionAbsent: &experience.UserCorrectionAbsentSpec{
					MarkerFamilies: []string{"explicit_correction"},
				},
			},
			want:      experience.VerifierSatisfied,
			wantCited: 1,
		},
		{
			name: "user correction absent violated",
			verifier: experience.Verifier{
				Kind: experience.VerifierUserCorrectionAbsent,
				CoverageRequirements: []experience.CoverageRequirement{
					experience.CoverageTranscriptComplete,
				},
				UserCorrectionAbsent: &experience.UserCorrectionAbsentSpec{
					MarkerFamilies: []string{"explicit_correction"},
				},
			},
			configure: func(input *Input) {
				input.Facts.Corrections = []CorrectionFact{{
					FactMeta:     factMeta(15, 15),
					MarkerFamily: "explicit_correction",
				}}
			},
			want:      experience.VerifierViolated,
			wantCited: 1,
		},
		{
			name: "observation only",
			verifier: experience.Verifier{
				Kind: experience.VerifierObservationOnly,
				ObservationOnly: &experience.ObservationOnlySpec{
					Explanation: "Retain the observation for review.",
				},
			},
			want: experience.VerifierUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(t, test.verifier)
			if test.configure != nil {
				test.configure(&input)
			}
			got, err := Evaluate(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.VerifierState != test.want {
				t.Fatalf(
					"verifier state = %q, want %q; result = %+v",
					got.VerifierState,
					test.want,
					got,
				)
			}
			if cited := componentEvidence(
				got.Evidence,
				ComponentVerifier,
			); len(cited) != test.wantCited {
				t.Fatalf(
					"verifier evidence = %d, want %d: %+v",
					len(cited),
					test.wantCited,
					got.Evidence,
				)
			}
		})
	}
}

func TestMissingDeclaredCoverageReturnsUnknownWithAvailableCoverageEvidence(
	t *testing.T,
) {
	verifier := commandSucceededVerifier()
	input := validInput(t, verifier)
	input.Coverage.TranscriptComplete = false
	input.NormalizedVerifier = commandMatcher()
	input.Facts.Commands = []CommandFact{{
		FactMeta:  factMeta(3, 3, 4),
		Signature: "go test ./...",
		Class:     "test",
		Result:    FactResultSucceeded,
	}}

	got, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifierState != experience.VerifierUnknown ||
		len(componentEvidence(got.Evidence, ComponentVerifier)) != 1 ||
		!hasGap(
			got.Gaps,
			ComponentVerifier,
			GapCoverageMissing,
		) {
		t.Fatalf("partial coverage result = %+v", got)
	}
}

func TestNoRepeatFailureWindowSemantics(t *testing.T) {
	tests := []struct {
		name             string
		failures         []FailureFact
		lastTurn         int64
		coverageComplete bool
		want             experience.VerifierState
		wantCited        int
		wantGap          GapCode
	}{
		{
			name:             "zero matching failures",
			lastTurn:         30,
			coverageComplete: true,
			want:             experience.VerifierSatisfied,
			wantCited:        1,
		},
		{
			name: "one matching failure",
			failures: []FailureFact{{
				FactMeta:          factMeta(10, 10),
				CommandClass:      "test",
				NormalizedPattern: "generated client mismatch",
			}},
			lastTurn:         30,
			coverageComplete: true,
			want:             experience.VerifierSatisfied,
			wantCited:        2,
		},
		{
			name: "two matching failures inside window",
			failures: []FailureFact{
				{
					FactMeta:          factMeta(4, 4),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				},
				{
					FactMeta:          factMeta(14, 14),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				},
			},
			lastTurn:         30,
			coverageComplete: true,
			want:             experience.VerifierViolated,
			wantCited:        2,
		},
		{
			name: "two matching failures outside window",
			failures: []FailureFact{
				{
					FactMeta:          factMeta(1, 1),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				},
				{
					FactMeta:          factMeta(22, 22),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				},
			},
			lastTurn:         30,
			coverageComplete: true,
			want:             experience.VerifierSatisfied,
			wantCited:        3,
		},
		{
			name: "ambiguous relevant failure",
			failures: []FailureFact{{
				FactMeta: FactMeta{
					Order:     10,
					Ambiguous: true,
					Evidence:  []experience.EvidenceRef{turnEvidence(10)},
				},
				CommandClass:      "test",
				NormalizedPattern: "generated client mismatch",
			}},
			lastTurn:         30,
			coverageComplete: true,
			want:             experience.VerifierUnknown,
			wantCited:        2,
			wantGap:          GapAmbiguousMatch,
		},
		{
			name:             "absence with incomplete coverage",
			lastTurn:         30,
			coverageComplete: false,
			want:             experience.VerifierUnknown,
			wantCited:        1,
			wantGap:          GapCoverageMissing,
		},
		{
			name: "repeat evidence with incomplete coverage",
			failures: []FailureFact{
				{
					FactMeta:          factMeta(4, 4),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				},
				{
					FactMeta:          factMeta(14, 14),
					CommandClass:      "test",
					NormalizedPattern: "generated client mismatch",
				},
			},
			lastTurn:         30,
			coverageComplete: false,
			want:             experience.VerifierUnknown,
			wantCited:        1,
			wantGap:          GapCoverageMissing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(t, noRepeatFailureVerifier(20))
			input.Context.LastTurnIndex = &test.lastTurn
			input.Coverage.TranscriptComplete = test.coverageComplete
			input.Facts.Failures = test.failures

			got, err := Evaluate(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.VerifierState != test.want ||
				len(componentEvidence(
					got.Evidence,
					ComponentVerifier,
				)) != test.wantCited ||
				(test.wantGap != "" &&
					!hasGap(
						got.Gaps,
						ComponentVerifier,
						test.wantGap,
					)) {
				t.Fatalf("no-repeat result = %+v", got)
			}
		})
	}
}

func TestVerifierAndTaskOutcomeRemainIndependent(t *testing.T) {
	tests := []struct {
		name         string
		command      FactResult
		task         experience.TaskOutcomeState
		wantVerifier experience.VerifierState
	}{
		{
			name:         "verifier satisfied task failed",
			command:      FactResultSucceeded,
			task:         experience.TaskOutcomeFailed,
			wantVerifier: experience.VerifierSatisfied,
		},
		{
			name:         "verifier violated task succeeded",
			command:      FactResultFailed,
			task:         experience.TaskOutcomeSucceeded,
			wantVerifier: experience.VerifierViolated,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(t, commandSucceededVerifier())
			input.NormalizedVerifier = commandMatcher()
			input.Facts.Commands = []CommandFact{{
				FactMeta:  factMeta(5, 5, 6),
				Signature: "go test ./...",
				Class:     "test",
				Result:    test.command,
			}}
			input.Facts.TaskOutcomes = []TaskOutcomeFact{{
				FactMeta: factMeta(8, 8),
				State:    test.task,
			}}

			got, err := Evaluate(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.VerifierState != test.wantVerifier ||
				got.TaskOutcomeState != test.task {
				t.Fatalf("independence result = %+v", got)
			}
		})
	}
}

func TestCompleteOutcomeCoverageDoesNotInventTaskOutcome(t *testing.T) {
	input := validInput(t, commandSucceededVerifier())
	input.NormalizedVerifier = commandMatcher()
	input.Facts.Commands = []CommandFact{{
		FactMeta:  factMeta(5, 5, 6),
		Signature: "go test ./...",
		Class:     "test",
		Result:    FactResultSucceeded,
	}}
	input.Facts.TaskOutcomes = nil
	input.Coverage.OutcomeObservationsComplete = true

	got, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifierState != experience.VerifierSatisfied ||
		got.TaskOutcomeState != experience.TaskOutcomeUnknown {
		t.Fatalf("independent no-task-outcome result = %+v", got)
	}
}

func TestOpportunityApplicabilityAndVerifierAreIndependent(t *testing.T) {
	tests := []struct {
		name              string
		opportunity       experience.OpportunityState
		condition         *ConditionFact
		completeCondition bool
		wantApplicability experience.ApplicabilityState
	}{
		{
			name:        "not observed opportunity can still be applicable",
			opportunity: experience.OpportunityNotObserved,
			condition: &ConditionFact{
				FactMeta: factMeta(2, 2),
				Kind:     experience.ConditionToolName,
				Values:   []string{"Edit"},
			},
			wantApplicability: experience.ApplicabilityApplicable,
		},
		{
			name:              "observed opportunity can be not applicable",
			opportunity:       experience.OpportunityObserved,
			completeCondition: true,
			wantApplicability: experience.ApplicabilityNotApplicable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(t, commandSucceededVerifier())
			input.Experience.Applicability.DeterministicConditions =
				[]experience.DeterministicCondition{{
					Kind:   experience.ConditionToolName,
					Values: []string{"Edit"},
				}}
			resealExperienceAndApplication(t, &input)
			input.NormalizedVerifier = commandMatcher()
			input.Facts.Opportunities = []OpportunityFact{{
				FactMeta: factMeta(1, 1),
				State:    test.opportunity,
			}}
			if test.condition != nil {
				input.Facts.Conditions = []ConditionFact{*test.condition}
			}
			if test.completeCondition {
				input.Context.CompleteConditionKinds =
					[]experience.DeterministicConditionKind{
						experience.ConditionToolName,
					}
			}
			input.Facts.Commands = []CommandFact{{
				FactMeta:  factMeta(6, 6, 7),
				Signature: "go test ./...",
				Class:     "test",
				Result:    FactResultSucceeded,
			}}

			got, err := Evaluate(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.OpportunityState != test.opportunity ||
				got.ApplicabilityState != test.wantApplicability ||
				got.VerifierState != experience.VerifierSatisfied {
				t.Fatalf("independent states = %+v", got)
			}
		})
	}
}

func TestAmbiguousMatchingFailsClosed(t *testing.T) {
	input := validInput(t, commandSucceededVerifier())
	input.NormalizedVerifier = commandMatcher()
	input.Facts.Commands = []CommandFact{
		{
			FactMeta:  factMeta(5, 5),
			Signature: "go test ./...",
			Class:     "test",
			Result:    FactResultSucceeded,
		},
		{
			FactMeta:  factMeta(5, 6),
			Signature: "go test ./...",
			Class:     "test",
			Result:    FactResultFailed,
		},
	}

	got, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifierState != experience.VerifierUnknown ||
		!hasGap(got.Gaps, ComponentVerifier, GapAmbiguousMatch) {
		t.Fatalf("ambiguous command result = %+v", got)
	}
}

func TestCommandMatcherMustBindTheDeclaredCommand(t *testing.T) {
	input := validInput(t, commandSucceededVerifier())
	input.NormalizedVerifier = commandMatcher()
	input.NormalizedVerifier.DeclaredCommand = "go test ./internal/other"

	if _, err := Evaluate(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mismatched command binding error = %v", err)
	}
}

func TestApplicationBindingIsRevalidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
	}{
		{
			name: "experience version",
			mutate: func(input *Input) {
				input.Application.Experience.Version++
				input.Application.ApplicationID =
					input.Application.DeterministicID()
			},
		},
		{
			name: "project",
			mutate: func(input *Input) {
				input.Context.ProjectIdentity = "another_project"
			},
		},
		{
			name: "session",
			mutate: func(input *Input) {
				input.Context.SessionKey = "another_session"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(t, commandSucceededVerifier())
			test.mutate(&input)
			if _, err := Evaluate(input); !errors.Is(
				err,
				ErrInvalidInput,
			) {
				t.Fatalf("binding error = %v", err)
			}
		})
	}
}

func TestPathMatchingIsSlashSeparatedAndRejectsUnsafeFacts(t *testing.T) {
	verifier := experience.Verifier{
		Kind: experience.VerifierPathPatternNotModified,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageWorkspaceCaptured,
		},
		PathPattern: &experience.PathPatternVerifierSpec{
			Patterns: []string{"generated/**"},
		},
	}
	input := validInput(t, verifier)
	input.Facts.FileChanges = []FileChangeFact{{
		FactMeta: factMeta(3, 3),
		Path:     "generated/api/client.go",
	}}
	got, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifierState != experience.VerifierViolated {
		t.Fatalf("nested path did not match: %+v", got)
	}

	unsafe := validInput(t, verifier)
	unsafe.Facts.FileChanges = []FileChangeFact{{
		FactMeta: factMeta(3, 3),
		Path:     "../generated/client.go",
	}}
	if _, err := Evaluate(unsafe); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe path error = %v", err)
	}
}

func TestEvidenceIsStableDeduplicatedAndBounded(t *testing.T) {
	verifier := experience.Verifier{
		Kind: experience.VerifierPathPatternNotModified,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageWorkspaceCaptured,
		},
		PathPattern: &experience.PathPatternVerifierSpec{
			Patterns: []string{"generated/**"},
		},
	}
	first := validInput(t, verifier)
	first.Context.LastTurnIndex = int64Pointer(200)
	first.Facts.Opportunities = nil
	for index := int64(0); index < 80; index++ {
		first.Facts.Opportunities = append(
			first.Facts.Opportunities,
			OpportunityFact{
				FactMeta: factMeta(index, index),
				State:    experience.OpportunityObserved,
			},
		)
		first.Facts.FileChanges = append(
			first.Facts.FileChanges,
			FileChangeFact{
				FactMeta: factMeta(index+80, index+80),
				Path: fmt.Sprintf(
					"generated/client-%02d.go",
					index,
				),
			},
		)
	}
	second := first
	second.Facts.Opportunities = slices.Clone(first.Facts.Opportunities)
	slices.Reverse(second.Facts.Opportunities)
	second.Facts.FileChanges = slices.Clone(first.Facts.FileChanges)
	slices.Reverse(second.Facts.FileChanges)

	firstResult, err := Evaluate(first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := Evaluate(second)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(firstResult)
	secondJSON, _ := json.Marshal(secondResult)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf(
			"reordered facts changed result:\n%s\n%s",
			firstJSON,
			secondJSON,
		)
	}
	if len(firstResult.Evidence) != MaxResultEvidence ||
		!hasGap(
			firstResult.Gaps,
			ComponentVerifier,
			GapEvidenceTruncated,
		) ||
		len(componentEvidence(
			firstResult.Evidence,
			ComponentOpportunity,
		)) == 0 ||
		len(componentEvidence(
			firstResult.Evidence,
			ComponentApplicability,
		)) == 0 ||
		len(componentEvidence(
			firstResult.Evidence,
			ComponentVerifier,
		)) == 0 {
		t.Fatalf("bounded evidence result = %+v", firstResult)
	}
}

func TestLegitimateUnknownMayHaveNoEvidence(t *testing.T) {
	input := validInput(t, experience.Verifier{
		Kind: experience.VerifierObservationOnly,
		ObservationOnly: &experience.ObservationOnlySpec{
			Explanation: "No deterministic verifier is available.",
		},
	})
	input.Facts.Opportunities = nil
	input.Coverage.OutcomeObservationsComplete = false
	input.Context.Evidence = nil
	input.Application.SourceEvidence = nil

	got, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifierState != experience.VerifierUnknown ||
		len(got.Evidence) != 0 {
		t.Fatalf("unknown result = %+v", got)
	}
}

func validInput(
	t *testing.T,
	verifier experience.Verifier,
) Input {
	t.Helper()
	ref := turnEvidence(0)
	evidence := experience.EvidenceSet{
		Availability: experience.EvidenceAvailable,
		Refs:         []experience.EvidenceRef{ref},
	}
	evidence.EvidenceSetID = evidence.DeterministicID()
	projectIdentity := "project_evaluate_fixture"
	originCandidateID := "candidate_evaluate_fixture"
	value := experience.Experience{
		SchemaVersion: experience.ExperienceSchemaVersion,
		ExperienceID: experience.DeriveExperienceID(
			projectIdentity,
			originCandidateID,
		),
		OriginCandidateID: originCandidateID,
		Version:           1,
		Type:              experience.ExperienceProcedure,
		Scope: experience.Scope{
			Kind:            experience.ScopeProject,
			ProjectIdentity: projectIdentity,
		},
		Applicability: experience.Applicability{
			SemanticDescription: "Applies to deterministic evaluation fixtures.",
		},
		Guidance: experience.Guidance{
			Instruction:          "Follow the deterministic fixture rule.",
			Rationale:            "The fixture exercises evaluator business rules.",
			InterventionStrength: experience.InterventionAdvise,
		},
		Verifier: verifier,
		Evidence: evidence,
		Provenance: experience.Provenance{
			ExtractorVersion:  "evaluate.fixture.v1",
			InputHash:         testSHA256("evaluate fixture"),
			SourceCandidateID: originCandidateID,
			GeneratedAt:       evaluationTestTime.Add(-2 * time.Hour),
		},
		Governance: experience.Governance{
			LifecycleState: experience.LifecycleActive,
			Authority:      experience.AuthorityUserApproved,
			Approval: &experience.ApprovalProvenance{
				ApprovedBy:          "fixture-user",
				ApprovedAt:          evaluationTestTime.Add(-90 * time.Minute),
				Mode:                experience.ApprovalAsProposed,
				CandidateID:         originCandidateID,
				ProposedContentHash: testSHA256("placeholder"),
				ApprovedContentHash: testSHA256("placeholder"),
			},
		},
		CreatedAt: evaluationTestTime.Add(-2 * time.Hour),
	}
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval.ProposedContentHash = value.ContentHash
	value.Governance.Approval.ApprovedContentHash = value.ContentHash
	if err := value.Validate(); err != nil {
		t.Fatalf("fixture experience: %v", err)
	}
	deliveredAt := evaluationTestTime.Add(-time.Hour)
	application := experience.Application{
		SchemaVersion:      experience.ApplicationSchemaVersion,
		Experience:         experience.ExperienceRef{ExperienceID: value.ExperienceID, Version: value.Version},
		ProjectIdentity:    value.Scope.ProjectIdentity,
		SessionKey:         "ses_evaluate_fixture",
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryDelivered,
		DeliveredAt:        &deliveredAt,
		OpportunityState:   experience.OpportunityUnknown,
		ApplicabilityState: experience.ApplicabilityUnknown,
		VerifierState:      experience.VerifierNotEvaluated,
		TaskOutcomeState:   experience.TaskOutcomeNotObserved,
		SourceEvidence:     []experience.EvidenceRef{ref},
	}
	application.ApplicationID = application.DeterministicID()
	if err := application.Validate(); err != nil {
		t.Fatalf("fixture application: %v", err)
	}
	lastTurnIndex := int64(20)
	return Input{
		Application: application,
		Experience:  value,
		Context: Context{
			ProjectIdentity: value.Scope.ProjectIdentity,
			SessionKey:      application.SessionKey,
			Harness:         experience.HarnessCodex,
			Model:           "gpt-5",
			LastTurnIndex:   &lastTurnIndex,
			AsOf:            evaluationTestTime,
		},
		Coverage: Coverage{
			TranscriptComplete:          true,
			CanonicalEventsComplete:     true,
			WorkspaceStateCaptured:      true,
			OutcomeObservationsComplete: true,
			Evidence: []CoverageEvidence{
				{
					Requirement: experience.CoverageTranscriptComplete,
					Evidence:    []experience.EvidenceRef{turnEvidence(16)},
				},
				{
					Requirement: experience.CoverageCanonicalComplete,
					Evidence:    []experience.EvidenceRef{turnEvidence(17)},
				},
				{
					Requirement: experience.CoverageWorkspaceCaptured,
					Evidence:    []experience.EvidenceRef{turnEvidence(18)},
				},
				{
					Requirement: experience.CoverageOutcomesComplete,
					Evidence:    []experience.EvidenceRef{turnEvidence(19)},
				},
			},
		},
		Facts: Facts{
			Opportunities: []OpportunityFact{{
				FactMeta: factMeta(1, 1),
				State:    experience.OpportunityObserved,
			}},
		},
	}
}

func resealExperienceAndApplication(t *testing.T, input *Input) {
	t.Helper()
	input.Experience.ContentHash = input.Experience.CanonicalContentHash()
	input.Experience.Governance.Approval.ProposedContentHash =
		input.Experience.ContentHash
	input.Experience.Governance.Approval.ApprovedContentHash =
		input.Experience.ContentHash
	if err := input.Experience.Validate(); err != nil {
		t.Fatalf("resealed experience: %v", err)
	}
	input.Application.Experience = experience.ExperienceRef{
		ExperienceID: input.Experience.ExperienceID,
		Version:      input.Experience.Version,
	}
	input.Application.ApplicationID = input.Application.DeterministicID()
}

func commandSucceededVerifier() experience.Verifier {
	return experience.Verifier{
		Kind: experience.VerifierCommandSucceeded,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		Command: &experience.CommandVerifierSpec{
			Command:          "go test ./...",
			CommandClass:     "test",
			ScrubbingVersion: "belay.redaction.v1",
		},
	}
}

func noRepeatFailureVerifier(windowTurns int) experience.Verifier {
	return experience.Verifier{
		Kind: experience.VerifierNoRepeatFailure,
		CoverageRequirements: []experience.CoverageRequirement{
			experience.CoverageTranscriptComplete,
		},
		NoRepeatFailure: &experience.NoRepeatFailureSpec{
			CommandClass:      "test",
			NormalizedPattern: "generated client mismatch",
			WindowTurns:       windowTurns,
		},
	}
}

func commandMatcher() NormalizedVerifier {
	return NormalizedVerifier{
		DeclaredCommand:         "go test ./...",
		CommandSignature:        "go test ./...",
		CommandScrubbingVersion: "belay.redaction.v1",
	}
}

func factMeta(order int64, evidenceTurns ...int64) FactMeta {
	evidence := make([]experience.EvidenceRef, 0, len(evidenceTurns))
	for _, turn := range evidenceTurns {
		evidence = append(evidence, turnEvidence(turn))
	}
	return FactMeta{
		Order:    order,
		Evidence: evidence,
	}
}

func turnEvidence(turn int64) experience.EvidenceRef {
	occurredAt := evaluationTestTime.Add(time.Duration(turn) * time.Second)
	index := turn
	return experience.EvidenceRef{
		Kind:       experience.EvidenceTranscriptTurn,
		SessionKey: "ses_evaluate_fixture",
		TurnIndex:  &index,
		OccurredAt: &occurredAt,
	}
}

func componentEvidence(
	values []CitedEvidence,
	component Component,
) []CitedEvidence {
	var result []CitedEvidence
	for _, value := range values {
		if value.Component == component {
			result = append(result, value)
		}
	}
	return result
}

func hasGap(values []Gap, component Component, code GapCode) bool {
	for _, value := range values {
		if value.Component == component && value.Code == code {
			return true
		}
	}
	return false
}

func testSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func int64Pointer(value int64) *int64 {
	return &value
}
