package localmcp

import (
	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/google/jsonschema-go/jsonschema"
)

func listExperienceProposalsSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"cwd": boundedTextSchema(
				1,
				maxExperienceLearningCWDBytes,
			),
			"harness": enumStringSchema(
				string(experience.HarnessClaude),
				string(experience.HarnessCodex),
				string(experience.HarnessCursor),
				string(experience.HarnessAntigravity),
			),
			"limit": integerSchema(
				1,
				maxExperienceLearningItems,
			),
			"include_deferred": {Type: "boolean"},
		},
		"cwd",
		"harness",
	)
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(listExperienceProposalsOutputSchema()),
	)
}

func listActiveExperiencesSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"cwd": boundedTextSchema(
				1,
				maxExperienceLearningCWDBytes,
			),
			"limit": integerSchema(
				1,
				maxExperienceLearningItems,
			),
		},
		"cwd",
	)
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(listActiveExperiencesOutputSchema()),
	)
}

func approveExperienceSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"proposal_id": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"action_token": boundedTextSchema(
				1,
				maxExperienceLearningTokenBytes,
			),
			"approval_mode": enumStringSchema(
				string(experience.ApprovalAsProposed),
				string(experience.ApprovalNarrowed),
				string(experience.ApprovalUserEdited),
			),
			"approved_content": experienceProposalSchema(),
		},
		"proposal_id",
		"action_token",
		"approval_mode",
	)
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(approveExperienceOutputSchema()),
	)
}

func resolveExperienceProposalSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"proposal_id": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"action_token": boundedTextSchema(
				1,
				maxExperienceLearningTokenBytes,
			),
			"disposition": enumStringSchema(
				string(localapp.ExperienceLearningReviewDefer),
				string(localapp.ExperienceLearningReviewReject),
			),
		},
		"proposal_id",
		"action_token",
		"disposition",
	)
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(resolveExperienceProposalOutputSchema()),
	)
}

func prepareExperienceLifecycleSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"experience": experienceRefSchema(),
			"action": enumStringSchema(
				string(experience.LifecycleActionActivate),
				string(experience.LifecycleActionPause),
			),
		},
		"experience",
		"action",
	)
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(prepareExperienceLifecycleOutputSchema()),
	)
}

func applyExperienceLifecycleSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"experience": experienceRefSchema(),
			"action": enumStringSchema(
				string(experience.LifecycleActionActivate),
				string(experience.LifecycleActionPause),
			),
			"action_token": boundedTextSchema(
				1,
				maxExperienceLearningTokenBytes,
			),
		},
		"experience",
		"action",
		"action_token",
	)
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(applyExperienceLifecycleOutputSchema()),
	)
}

func listExperienceProposalsOutputSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"project": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"items": arraySchema(
				experienceProposalProjectionSchema(),
				0,
				maxExperienceLearningItems,
			),
		},
		"project",
		"items",
	)
}

func listActiveExperiencesOutputSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"project": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"items": arraySchema(
				activeExperienceProjectionSchema(),
				0,
				maxExperienceLearningItems,
			),
		},
		"project",
		"items",
	)
}

func activeExperienceProjectionSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"experience":       experienceRefSchema(),
			"instruction":      boundedTextSchema(1, 2*1024),
			"scope":            activeExperienceScopeSchema(),
			"applicability":    activeExperienceApplicabilitySchema(),
			"verifier_summary": boundedTextSchema(1, 300),
			"lifecycle_state": constSchema(
				"string",
				string(experience.LifecycleActive),
			),
		},
		"experience",
		"instruction",
		"scope",
		"applicability",
		"verifier_summary",
		"lifecycle_state",
	)
}

func activeExperienceScopeSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"kind": enumStringSchema(
				string(experience.ScopeProject),
				string(experience.ScopeSession),
			),
			"session_key": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"repository_paths": arraySchema(
				boundedTextSchema(1, 512),
				0,
				maxExperienceLearningProjectionItems,
			),
			"task_families": arraySchema(
				boundedTextSchema(
					1,
					maxExperienceLearningIDBytes,
				),
				0,
				maxExperienceLearningProjectionItems,
			),
			"harnesses": arraySchema(
				enumStringSchema(
					string(experience.HarnessClaude),
					string(experience.HarnessCodex),
					string(experience.HarnessCursor),
					string(experience.HarnessAntigravity),
				),
				0,
				maxExperienceLearningProjectionItems,
			),
			"models": arraySchema(
				boundedTextSchema(
					1,
					maxExperienceLearningIDBytes,
				),
				0,
				maxExperienceLearningProjectionItems,
			),
		},
		"kind",
		"repository_paths",
		"task_families",
		"harnesses",
		"models",
	)
}

func activeExperienceApplicabilitySchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"description": boundedTextSchema(1, 300),
			"deterministic_conditions": arraySchema(
				activeExperienceConditionSchema(),
				0,
				maxExperienceLearningProjectionItems,
			),
			"exclusions": arraySchema(
				boundedTextSchema(1, 300),
				0,
				maxExperienceLearningProjectionItems,
			),
			"expires_at": timestampSchema(),
		},
		"description",
		"deterministic_conditions",
		"exclusions",
	)
}

func activeExperienceConditionSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"kind": enumStringSchema(
				string(experience.ConditionEventType),
				string(experience.ConditionToolName),
				string(experience.ConditionCommandClass),
				string(experience.ConditionPathPattern),
				string(experience.ConditionLifecyclePhase),
				string(experience.ConditionPriorEventSequence),
			),
			"values": arraySchema(
				boundedTextSchema(
					1,
					512,
				),
				1,
				maxExperienceLearningProjectionItems,
			),
		},
		"kind",
		"values",
	)
}

func experienceProposalProjectionSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"proposal_id": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"candidate_family": enumStringSchema(
				string(experience.CandidateCorrection),
				string(experience.CandidateSuccessfulProcedure),
				string(experience.CandidateFailedApproach),
			),
			"instruction":      boundedTextSchema(1, 2*1024),
			"rationale":        boundedTextSchema(1, 8*1024),
			"proposed_content": experienceProposalSchema(),
			"action_token": boundedTextSchema(
				1,
				maxExperienceLearningTokenBytes,
			),
			"expires_at": timestampSchema(),
			"conflict":   experienceConflictSchema(),
			"evidence": arraySchema(
				experienceEvidenceSchema(),
				0,
				maxExperienceLearningEvidence,
			),
		},
		"proposal_id",
		"candidate_family",
		"instruction",
		"rationale",
		"proposed_content",
		"action_token",
		"expires_at",
		"conflict",
		"evidence",
	)
}

func experienceConflictSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"state": enumStringSchema(
				string(experience.ConflictNoConflict),
				string(experience.ConflictPossibleOverlap),
				string(experience.ConflictPossibleContradiction),
			),
			"reason_codes": arraySchema(
				enumStringSchema(
					string(experience.ConflictReasonNoActiveExperiences),
					string(experience.ConflictReasonDifferentProject),
					string(experience.ConflictReasonDisjointSession),
					string(experience.ConflictReasonDisjointHarnesses),
					string(experience.ConflictReasonDisjointModels),
					string(experience.ConflictReasonDisjointTaskFamilies),
					string(experience.ConflictReasonOverlappingScope),
					string(experience.ConflictReasonFileVerifierOpposition),
					string(experience.ConflictReasonPatternVerifierOpposition),
					string(experience.ConflictReasonExperienceRefsTruncated),
					string(experience.ConflictReasonActiveQueryCapReached),
				),
				1,
				16,
			),
			"conflicting_experience_count": integerSchema(0, 64),
		},
		"state",
		"reason_codes",
		"conflicting_experience_count",
	)
}

func experienceEvidenceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"excerpt": boundedTextSchema(
				1,
				maxExperienceLearningExcerptBytes,
			),
			"truncated":   {Type: "boolean"},
			"session_key": boundedTextSchema(1, 256),
			"turn_index": {
				Type:    "integer",
				Minimum: jsonNumberPointer(0),
			},
			"event_id":    boundedTextSchema(1, 256),
			"outcome_id":  boundedTextSchema(1, 256),
			"path":        boundedTextSchema(1, 512),
			"occurred_at": timestampSchema(),
		},
		"excerpt",
		"truncated",
	)
}

func approveExperienceOutputSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"experience": experienceRefSchema(),
			"lifecycle_state": constSchema(
				"string",
				string(experience.LifecycleApproved),
			),
			"replayed": {Type: "boolean"},
		},
		"experience",
		"lifecycle_state",
		"replayed",
	)
}

func resolveExperienceProposalOutputSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"proposal_id": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"disposition": enumStringSchema(
				string(localapp.ExperienceLearningReviewDefer),
				string(localapp.ExperienceLearningReviewReject),
			),
			"occurred_at":     timestampSchema(),
			"available_after": timestampSchema(),
			"replayed":        {Type: "boolean"},
		},
		"proposal_id",
		"disposition",
		"occurred_at",
		"replayed",
	)
}

func prepareExperienceLifecycleOutputSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"action": enumStringSchema(
				string(experience.LifecycleActionActivate),
				string(experience.LifecycleActionPause),
			),
			"experience": experienceRefSchema(),
			"project_identity": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"current_lifecycle": enumStringSchema(
				string(experience.LifecycleApproved),
				string(experience.LifecycleActive),
			),
			"content_hash": patternStringSchema(
				`^sha256:[a-f0-9]{64}$`,
				71,
				71,
			),
			"action_token": boundedTextSchema(
				1,
				maxExperienceLearningTokenBytes,
			),
			"issued_at":  timestampSchema(),
			"expires_at": timestampSchema(),
		},
		"action",
		"experience",
		"project_identity",
		"current_lifecycle",
		"content_hash",
		"action_token",
		"issued_at",
		"expires_at",
	)
}

func applyExperienceLifecycleOutputSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"transition_committed": constSchema("boolean", true),
			"delivery_ready":       {Type: "boolean"},
			"delivery_status": enumStringSchema(
				"ready",
				"generation_compile_failed",
			),
			"transition": experienceTransitionSchema(),
			"generation": experienceGenerationSchema(),
		},
		"transition_committed",
		"delivery_ready",
		"delivery_status",
		"transition",
	)
}

func experienceTransitionSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"transition_id": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"experience": experienceRefSchema(),
			"from_state": enumStringSchema(
				string(experience.LifecycleApproved),
				string(experience.LifecycleActive),
			),
			"to_state": enumStringSchema(
				string(experience.LifecycleActive),
				string(experience.LifecyclePaused),
			),
			"occurred_at": timestampSchema(),
		},
		"transition_id",
		"experience",
		"from_state",
		"to_state",
		"occurred_at",
	)
}

func experienceGenerationSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"project_identity": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"generation": {
				Type:    "integer",
				Minimum: jsonNumberPointer(1),
			},
			"compiled_hash": patternStringSchema(
				`^sha256:[a-f0-9]{64}$`,
				71,
				71,
			),
			"experience_refs": arraySchema(
				experienceRefSchema(),
				0,
				500,
			),
			"state":        enumStringSchema("active", "inactive"),
			"compiled_at":  timestampSchema(),
			"activated_at": timestampSchema(),
			"replayed":     {Type: "boolean"},
		},
		"project_identity",
		"generation",
		"compiled_hash",
		"experience_refs",
		"state",
		"compiled_at",
		"activated_at",
		"replayed",
	)
}

func experienceRefSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"experience_id": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"version": {
				Type:    "integer",
				Minimum: jsonNumberPointer(1),
			},
		},
		"experience_id",
		"version",
	)
}

func experienceProposalSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"type": enumStringSchema(
				string(experience.ExperiencePreference),
				string(experience.ExperienceProcedure),
				string(experience.ExperienceWarning),
				string(experience.ExperienceConstraint),
				string(experience.ExperienceFact),
			),
			"scope":         experienceScopeSchema(),
			"applicability": experienceApplicabilitySchema(),
			"guidance":      experienceGuidanceSchema(),
			"verifier":      experienceVerifierSchema(),
			"confidence":    unitNumberSchema(),
			"semantic_compilation_pending": {
				Type: "boolean",
			},
		},
		"type",
		"scope",
		"applicability",
		"guidance",
		"verifier",
	)
}

func experienceScopeSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"kind": enumStringSchema(
				string(experience.ScopeProject),
				string(experience.ScopeSession),
			),
			"project_identity": boundedTextSchema(
				1,
				maxExperienceLearningIDBytes,
			),
			"session_key": boundedTextSchema(1, 256),
			"repository_paths": arraySchema(
				boundedTextSchema(1, 512),
				0,
				128,
			),
			"task_families": arraySchema(
				boundedTextSchema(1, 256),
				0,
				128,
			),
			"harnesses": arraySchema(
				enumStringSchema(
					string(experience.HarnessClaude),
					string(experience.HarnessCodex),
					string(experience.HarnessCursor),
					string(experience.HarnessAntigravity),
				),
				0,
				128,
			),
			"models": arraySchema(
				boundedTextSchema(1, 256),
				0,
				128,
			),
		},
		"kind",
		"project_identity",
	)
}

func experienceApplicabilitySchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"semantic_description": boundedTextSchema(1, 8*1024),
			"deterministic_conditions": arraySchema(
				closedObjectSchema(
					map[string]*jsonschema.Schema{
						"kind": enumStringSchema(
							string(experience.ConditionEventType),
							string(experience.ConditionToolName),
							string(experience.ConditionCommandClass),
							string(experience.ConditionPathPattern),
							string(experience.ConditionLifecyclePhase),
							string(experience.ConditionPriorEventSequence),
						),
						"values": arraySchema(
							boundedTextSchema(1, 512),
							1,
							128,
						),
					},
					"kind",
					"values",
				),
				0,
				128,
			),
			"exclusions": arraySchema(
				boundedTextSchema(1, 8*1024),
				0,
				128,
			),
			"expires_at": timestampSchema(),
		},
		"semantic_description",
	)
}

func experienceGuidanceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"instruction": boundedTextSchema(1, 2*1024),
			"rationale":   boundedTextSchema(1, 8*1024),
			"exceptions": arraySchema(
				boundedTextSchema(1, 2*1024),
				0,
				128,
			),
			"intervention_strength": enumStringSchema(
				string(experience.InterventionObserve),
				string(experience.InterventionAdvise),
				string(experience.InterventionClarify),
				string(experience.InterventionRequireVerification),
			),
		},
		"instruction",
		"rationale",
		"intervention_strength",
	)
}

func experienceVerifierSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"kind": enumStringSchema(
				string(experience.VerifierCommandObserved),
				string(experience.VerifierCommandSucceeded),
				string(experience.VerifierFileNotModified),
				string(experience.VerifierFileModified),
				string(experience.VerifierPathPatternNotModified),
				string(experience.VerifierVerificationAfterLastEdit),
				string(experience.VerifierNoRepeatFailure),
				string(experience.VerifierUserCorrectionAbsent),
				string(experience.VerifierObservationOnly),
			),
			"coverage_requirements": arraySchema(
				enumStringSchema(
					string(experience.CoverageTranscriptComplete),
					string(experience.CoverageCanonicalComplete),
					string(experience.CoverageWorkspaceCaptured),
					string(experience.CoverageOutcomesComplete),
				),
				0,
				128,
			),
			"command": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"command": boundedTextSchema(1, 16*1024),
					"command_class": boundedTextSchema(
						1,
						256,
					),
					"scrubbing_version": boundedTextSchema(
						1,
						256,
					),
				},
				"command",
				"scrubbing_version",
			),
			"file": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"path": boundedTextSchema(1, 512),
				},
				"path",
			),
			"path_pattern": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"patterns": arraySchema(
						boundedTextSchema(1, 512),
						1,
						128,
					),
				},
				"patterns",
			),
			"verification_after_last_edit": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"command_classes": arraySchema(
						boundedTextSchema(1, 256),
						0,
						128,
					),
					"require_success": {Type: "boolean"},
				},
				"require_success",
			),
			"no_repeat_failure": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"command_class": boundedTextSchema(
						1,
						256,
					),
					"normalized_pattern": boundedTextSchema(
						1,
						2*1024,
					),
					"window_turns": integerSchema(1, 1000),
				},
				"command_class",
				"normalized_pattern",
				"window_turns",
			),
			"user_correction_absent": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"marker_families": arraySchema(
						boundedTextSchema(1, 256),
						0,
						128,
					),
				},
			),
			"observation_only": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"explanation": boundedTextSchema(
						1,
						8*1024,
					),
				},
				"explanation",
			),
		},
		"kind",
	)
}
