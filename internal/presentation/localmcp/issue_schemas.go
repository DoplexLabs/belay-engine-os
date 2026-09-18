package localmcp

import "github.com/google/jsonschema-go/jsonschema"

const (
	opaqueIssueIDPattern       = `^iss_[a-z2-7]{52}$`
	opaqueFingerprintIDPattern = `^ifp_[a-z2-7]{52}$`
	opaqueOccurrenceIDPattern  = `^occ_[a-z2-7]{52}$`
	canonicalUUIDv7Pattern     = `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
)

func listIssuesSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(map[string]*jsonschema.Schema{
		"limit":           integerSchema(1, 100),
		"cursor":          boundedTextSchema(1, maxStrictToolArgumentBytes),
		"severity":        enumStringSchema("info", "low", "medium", "high", "critical"),
		"category":        patternStringSchema(`^[a-z0-9_]{1,64}$`, 1, 64),
		"harness":         boundedTextSchema(1, 128),
		"origin":          enumStringSchema("belay", "numbat"),
		"analysis_status": enumStringSchema("current", "pending", "failed", "truncated"),
		"observed_after":  timestampSchema(),
		"recurrence":      enumStringSchema("single", "repeated"),
		"session_id":      boundedTextSchema(1, 256),
		"fingerprint_id":  patternStringSchema(opaqueFingerprintIDPattern, 56, 56),
		"attention_kind":  enumStringSchema("issue", "evidence_gap", "all"),
		"experimental":    enumStringSchema("stable", "include", "only"),
	})
	return newStrictToolSchemas(input, strictSuccessSchema(issueListSchema()))
}

func getIssueSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"issue_id":    patternStringSchema(opaqueIssueIDPattern, 56, 56),
			"limit":       integerSchema(1, 100),
			"cursor":      boundedTextSchema(1, maxStrictToolArgumentBytes),
			"view_cursor": boundedTextSchema(1, maxStrictToolArgumentBytes),
		},
		"issue_id",
	)
	return newStrictToolSchemas(input, strictSuccessSchema(issueDetailSchema()))
}

func lookupSessionEventsSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"session_id": boundedTextSchema(1, 256),
			"event_ids": arraySchema(
				patternStringSchema(canonicalUUIDv7Pattern, 36, 36),
				1,
				50,
			),
		},
		"session_id",
		"event_ids",
	)
	return newStrictToolSchemas(input, strictSuccessSchema(eventLookupSchema()))
}

func strictSuccessSchema(readmodelSchema *jsonschema.Schema) *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"untrusted_observations": constSchema("boolean", true),
			"trust": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"classification": constSchema("string", "untrusted_observations"),
					"instruction_authority": constSchema(
						"string",
						"none",
					),
					"must_not_authorize_actions": constSchema("boolean", true),
				},
				"classification",
				"instruction_authority",
				"must_not_authorize_actions",
			),
			"readmodel": readmodelSchema,
		},
		"untrusted_observations",
		"trust",
		"readmodel",
	)
}

func issueListSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"schema_version":     constSchema("string", "belay.read.v1"),
			"projection_version": constSchema("string", "belay.issue.v1"),
			"data":               arraySchema(issueSummarySchema(), 0, 100),
			"analysis":           issueAnalysisSchema(),
			"selection":          issueSelectionSchema(),
			"view_cursor":        boundedTextSchema(1, maxStrictToolArgumentBytes),
			"next_cursor":        nullableBoundedTextSchema(maxStrictToolArgumentBytes),
			"has_more":           {Type: "boolean"},
			"returned_count":     integerSchema(0, 100),
			"limit":              integerSchema(1, 100),
		},
		"schema_version",
		"projection_version",
		"data",
		"analysis",
		"selection",
		"view_cursor",
		"next_cursor",
		"has_more",
		"returned_count",
		"limit",
	)
}

func issueDetailSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"schema_version":     constSchema("string", "belay.read.v1"),
			"projection_version": constSchema("string", "belay.issue.v1"),
			"data": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"issue":       issueSummarySchema(),
					"occurrences": arraySchema(issueOccurrenceSchema(), 0, 100),
				},
				"issue",
				"occurrences",
			),
			"catalog":                  issueCatalogSchema(),
			"global_analysis_coverage": issueAnalysisSchema(),
			"view_cursor":              boundedTextSchema(1, maxStrictToolArgumentBytes),
			"next_cursor":              nullableBoundedTextSchema(maxStrictToolArgumentBytes),
			"has_more":                 {Type: "boolean"},
			"returned_count":           integerSchema(0, 100),
			"limit":                    integerSchema(1, 100),
		},
		"schema_version",
		"projection_version",
		"data",
		"catalog",
		"global_analysis_coverage",
		"view_cursor",
		"next_cursor",
		"has_more",
		"returned_count",
		"limit",
	)
}

func issueSelectionSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"attention_kind":         enumStringSchema("issue", "evidence_gap", "all"),
			"experimental":           enumStringSchema("stable", "include", "only"),
			"includes_evidence_gaps": {Type: "boolean"},
			"includes_experimental":  {Type: "boolean"},
		},
		"attention_kind",
		"experimental",
		"includes_evidence_gaps",
		"includes_experimental",
	)
}

func issueAnalysisSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"current_sessions":   nonNegativeIntegerSchema(),
			"pending_sessions":   nonNegativeIntegerSchema(),
			"failed_sessions":    nonNegativeIntegerSchema(),
			"truncated_sessions": nonNegativeIntegerSchema(),
			"unscoped_sessions":  nonNegativeIntegerSchema(),
			"analysis_through":   timestampSchema(),
			"complete":           {Type: "boolean"},
		},
		"current_sessions",
		"pending_sessions",
		"failed_sessions",
		"truncated_sessions",
		"unscoped_sessions",
		"analysis_through",
		"complete",
	)
}

func issueSummarySchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"issue_id":              patternStringSchema(opaqueIssueIDPattern, 56, 56),
			"fingerprint_id":        patternStringSchema(opaqueFingerprintIDPattern, 56, 56),
			"fingerprint_version":   boundedTextSchema(1, 128),
			"origin":                enumStringSchema("belay", "numbat"),
			"detector_id":           boundedTextSchema(1, 128),
			"detector_version":      boundedTextSchema(1, 128),
			"category":              patternStringSchema(`^[a-z0-9_]{1,64}$`, 1, 64),
			"title_code":            boundedTextSchema(1, 128),
			"source_signal_code":    nullablePatternStringSchema(`^[a-z0-9][a-z0-9_.-]{0,63}$`, 64),
			"severity":              enumStringSchema("info", "low", "medium", "high", "critical"),
			"confidence":            enumStringSchema("low", "medium", "high"),
			"scope_quality":         enumStringSchema("resolved", "lexical", "unscoped", "conflict"),
			"first_observed_at":     timestampSchema(),
			"last_observed_at":      timestampSchema(),
			"occurrence_count":      integerSchema(1, 1_000_000_000),
			"session_count":         integerSchema(1, 1_000_000_000),
			"harnesses":             arraySchema(boundedTextSchema(1, 128), 0, 64),
			"analysis_status":       enumStringSchema("current", "pending", "failed", "truncated"),
			"evidence_complete":     {Type: "boolean"},
			"retained_history_only": {Type: "boolean"},
			"experimental":          {Type: "boolean"},
		},
		"issue_id",
		"fingerprint_id",
		"fingerprint_version",
		"origin",
		"detector_id",
		"detector_version",
		"category",
		"title_code",
		"source_signal_code",
		"severity",
		"confidence",
		"scope_quality",
		"first_observed_at",
		"last_observed_at",
		"occurrence_count",
		"session_count",
		"harnesses",
		"analysis_status",
		"evidence_complete",
		"retained_history_only",
		"experimental",
	)
}

func issueOccurrenceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"occurrence_id":         patternStringSchema(opaqueOccurrenceIDPattern, 56, 56),
			"issue_id":              patternStringSchema(opaqueIssueIDPattern, 56, 56),
			"fingerprint_id":        patternStringSchema(opaqueFingerprintIDPattern, 56, 56),
			"fingerprint_version":   boundedTextSchema(1, 128),
			"origin":                enumStringSchema("belay", "numbat"),
			"session_id":            boundedTextSchema(1, 256),
			"harness":               boundedTextSchema(1, 128),
			"provenance":            detectorProvenanceSchema(),
			"category":              patternStringSchema(`^[a-z0-9_]{1,64}$`, 1, 64),
			"title_code":            boundedTextSchema(1, 128),
			"source_signal_code":    nullablePatternStringSchema(`^[a-z0-9][a-z0-9_.-]{0,63}$`, 64),
			"severity":              enumStringSchema("info", "low", "medium", "high", "critical"),
			"confidence":            enumStringSchema("low", "medium", "high"),
			"scope_quality":         enumStringSchema("resolved", "lexical", "unscoped", "conflict"),
			"first_observed_at":     timestampSchema(),
			"last_observed_at":      timestampSchema(),
			"evidence_complete":     {Type: "boolean"},
			"retained_history_only": {Type: "boolean"},
			"experimental":          {Type: "boolean"},
			"analysis_status":       enumStringSchema("current", "pending", "failed", "truncated"),
			"evidence": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"cited_event_ids": arraySchema(
						patternStringSchema(canonicalUUIDv7Pattern, 36, 36),
						0,
						50,
					),
				},
				"cited_event_ids",
			),
		},
		"occurrence_id",
		"issue_id",
		"fingerprint_id",
		"fingerprint_version",
		"origin",
		"session_id",
		"harness",
		"provenance",
		"category",
		"title_code",
		"source_signal_code",
		"severity",
		"confidence",
		"scope_quality",
		"first_observed_at",
		"last_observed_at",
		"evidence_complete",
		"retained_history_only",
		"experimental",
		"analysis_status",
		"evidence",
	)
}

func detectorProvenanceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"detector_id":         boundedTextSchema(1, 128),
			"detector_version":    boundedTextSchema(1, 128),
			"fingerprint_version": boundedTextSchema(1, 128),
			"projection_version":  boundedTextSchema(1, 128),
		},
		"detector_id",
		"detector_version",
		"fingerprint_version",
		"projection_version",
	)
}

func issueCatalogSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"catalog_version":       constSchema("string", "belay.issue-explanations.v1"),
			"catalog_status":        enumStringSchema("known", "unknown"),
			"title_code":            boundedTextSchema(1, 128),
			"display_title":         boundedTextSchema(1, 256),
			"observation_statement": boundedTextSchema(1, 1024),
			"caveat":                boundedTextSchema(1, 1024),
			"next_evidence_action": enumStringSchema(
				"inspect_cited_events",
				"inspect_matching_sessions",
				"inspect_verification_events",
				"review_agent_permissions",
			),
			"source_signal_code": nullablePatternStringSchema(
				`^[a-z0-9][a-z0-9_.-]{0,63}$`,
				64,
			),
			"source_signal_catalog_version": constSchema(
				"string",
				"belay.source-signals.v1",
			),
			"source_signal_catalog_status": enumStringSchema(
				"known",
				"unknown",
				"not_applicable",
			),
		},
		"catalog_version",
		"catalog_status",
		"title_code",
		"display_title",
		"observation_statement",
		"caveat",
		"next_evidence_action",
		"source_signal_code",
		"source_signal_catalog_version",
		"source_signal_catalog_status",
	)
}

func eventLookupSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"schema_version":        constSchema("string", "belay.read.v1"),
			"data":                  arraySchema(mcpEventEvidenceSchema(), 0, 50),
			"requested_count":       integerSchema(1, 50),
			"found_count":           integerSchema(0, 50),
			"missing_count":         integerSchema(0, 50),
			"missing_event_ids":     arraySchema(patternStringSchema(canonicalUUIDv7Pattern, 36, 36), 0, 50),
			"data_through":          timestampSchema(),
			"snapshot_scope":        constSchema("string", "current_ingestion"),
			"issue_snapshot_bound":  constSchema("boolean", false),
			"evidence_evaluated_at": timestampSchema(),
			"missing_semantics": constSchema(
				"string",
				"unavailable_from_selected_retained_session",
			),
		},
		"schema_version",
		"data",
		"requested_count",
		"found_count",
		"missing_count",
		"missing_event_ids",
		"data_through",
		"snapshot_scope",
		"issue_snapshot_bound",
		"evidence_evaluated_at",
		"missing_semantics",
	)
}

func mcpEventEvidenceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"schema_version": constSchema("string", "belay.event.v1"),
			"event_id":       patternStringSchema(canonicalUUIDv7Pattern, 36, 36),
			"occurred_at":    timestampSchema(),
			"observed_at":    timestampSchema(),
			"session_id":     boundedTextSchema(1, 256),
			"source":         mcpEventSourceSchema(),
			"observation":    mcpEventObservationSchema(),
			"coverage": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"depth":      boundedTextSchema(1, 64),
					"confidence": boundedTextSchema(1, 64),
				},
				"depth",
				"confidence",
			),
			"redaction": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"policy_version":  boundedTextSchema(1, 128),
					"fields_removed":  nonNegativeIntegerSchema(),
					"secrets_removed": nonNegativeIntegerSchema(),
				},
				"policy_version",
				"fields_removed",
				"secrets_removed",
			),
			"historical": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"is_historical":         {Type: "boolean"},
					"reconstruction_source": boundedTextSchema(1, 128),
				},
				"is_historical",
			),
		},
		"schema_version",
		"event_id",
		"occurred_at",
		"observed_at",
		"session_id",
		"source",
		"observation",
		"coverage",
		"redaction",
		"historical",
	)
}

func mcpEventSourceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"engine":          boundedTextSchema(1, 128),
			"engine_version":  boundedTextSchema(0, 128),
			"schema_version":  boundedTextSchema(1, 128),
			"record_type":     boundedTextSchema(1, 64),
			"kind":            boundedTextSchema(0, 128),
			"agent":           boundedTextSchema(0, 128),
			"adapter_version": boundedTextSchema(1, 128),
			"sequence":        nonNegativeIntegerSchema(),
		},
		"engine",
		"engine_version",
		"schema_version",
		"record_type",
		"kind",
		"agent",
		"adapter_version",
		"sequence",
	)
}

func mcpEventObservationSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"type":        boundedTextSchema(1, 128),
			"actor":       boundedTextSchema(0, 128),
			"action":      boundedTextSchema(0, 128),
			"outcome":     boundedTextSchema(0, 64),
			"exit_code":   integerSchema(-2_147_483_648, 2_147_483_647),
			"duration_ms": integerSchema(0, 86_400_000),
			"summary":     boundedTextSchema(0, 4096),
			"resource": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"kind": boundedTextSchema(1, 128),
					"name": boundedTextSchema(0, 1024),
				},
				"kind",
				"name",
			),
			"details": closedObjectSchema(map[string]*jsonschema.Schema{
				"decision":          boundedTextSchema(0, 128),
				"approval_required": {Type: "boolean"},
				"approval_decision": boundedTextSchema(0, 128),
				"mcp_server":        boundedTextSchema(0, 256),
				"mcp_tool":          boundedTextSchema(0, 256),
				"model":             boundedTextSchema(0, 256),
				"model_provider":    boundedTextSchema(0, 256),
				"cli_version":       boundedTextSchema(0, 128),
				"sub_agent":         boundedTextSchema(0, 256),
				"diff_bytes":        nonNegativeIntegerSchema(),
				"tags":              arraySchema(boundedTextSchema(0, 128), 0, 32),
			}),
		},
		"type",
		"actor",
		"action",
		"outcome",
	)
}

func boundedTextSchema(minimum, maximum int) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:      "string",
		MinLength: intPointer(minimum),
		MaxLength: intPointer(maximum),
	}
}

func nullableBoundedTextSchema(maximum int) *jsonschema.Schema {
	return &jsonschema.Schema{
		Types:     []string{"string", "null"},
		MinLength: intPointer(1),
		MaxLength: intPointer(maximum),
	}
}

func nullablePatternStringSchema(pattern string, maximum int) *jsonschema.Schema {
	schema := nullableBoundedTextSchema(maximum)
	schema.Pattern = pattern
	return schema
}

func patternStringSchema(pattern string, minimum, maximum int) *jsonschema.Schema {
	schema := boundedTextSchema(minimum, maximum)
	schema.Pattern = pattern
	return schema
}

func enumStringSchema(values ...string) *jsonschema.Schema {
	enum := make([]any, len(values))
	for index, value := range values {
		enum[index] = value
	}
	return &jsonschema.Schema{Type: "string", Enum: enum}
}

func timestampSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:      "string",
		Format:    "date-time",
		MinLength: intPointer(20),
		MaxLength: intPointer(64),
	}
}

func integerSchema(minimum, maximum int) *jsonschema.Schema {
	minimumValue := float64(minimum)
	maximumValue := float64(maximum)
	return &jsonschema.Schema{
		Type:    "integer",
		Minimum: &minimumValue,
		Maximum: &maximumValue,
	}
}

func nonNegativeIntegerSchema() *jsonschema.Schema {
	return integerSchema(0, 1_000_000_000)
}

func arraySchema(items *jsonschema.Schema, minimum, maximum int) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     "array",
		Items:    items,
		MinItems: intPointer(minimum),
		MaxItems: intPointer(maximum),
	}
}

func constSchema(schemaType string, value any) *jsonschema.Schema {
	return &jsonschema.Schema{Type: schemaType, Const: &value}
}

func intPointer(value int) *int {
	return &value
}

func closedObjectSchema(
	properties map[string]*jsonschema.Schema,
	required ...string,
) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}
