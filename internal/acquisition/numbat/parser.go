package numbat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/limits"
)

type IssueCategory string

const (
	IssueMalformedJSON     IssueCategory = "malformed_json"
	IssueUnknownField      IssueCategory = "unknown_field"
	IssueUnsupportedSchema IssueCategory = "unsupported_schema"
	IssueUnknownRecordType IssueCategory = "unknown_record_type"
	IssueInvalidRecord     IssueCategory = "invalid_record"
)

type ParseError struct {
	Category IssueCategory
	Reason   string
}

func (e *ParseError) Error() string { return string(e.Category) + ": " + e.Reason }

func ParseLine(line []byte) (any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, issue(IssueMalformedJSON, "invalid JSON")
	}
	schema, err := requiredString(raw, "schema_version")
	if err != nil {
		return nil, err
	}
	if schema != SchemaVersion {
		return nil, issue(IssueUnsupportedSchema, "unsupported schema_version")
	}
	recordType, err := requiredString(raw, "record_type")
	if err != nil {
		return nil, err
	}

	switch recordType {
	case "event":
		var record EventRecord
		if err := strictDecode(line, &record); err != nil {
			return nil, err
		}
		if err := validateEvent(record, raw); err != nil {
			return nil, err
		}
		return record, nil
	case "finding":
		var record FindingRecord
		if err := strictDecode(line, &record); err != nil {
			return nil, err
		}
		if err := validateFinding(record); err != nil {
			return nil, err
		}
		return record, nil
	case "scan_summary":
		var record ScanSummaryRecord
		if err := strictDecode(line, &record); err != nil {
			return nil, err
		}
		if err := validateSummary(record); err != nil {
			return nil, err
		}
		return record, nil
	case "diagnostic":
		var record DiagnosticRecord
		if err := strictDecode(line, &record); err != nil {
			return nil, err
		}
		if err := validateDiagnostic(record); err != nil {
			return nil, err
		}
		return record, nil
	case "indicator":
		var record IndicatorRecord
		if err := strictDecode(line, &record); err != nil {
			return nil, err
		}
		if err := validateIndicator(record); err != nil {
			return nil, err
		}
		return record, nil
	case "enforcement":
		var record EnforcementRecord
		if err := strictDecode(line, &record); err != nil {
			return nil, err
		}
		if err := validateEnforcement(record); err != nil {
			return nil, err
		}
		return record, nil
	default:
		return nil, issue(IssueUnknownRecordType, "unsupported record_type")
	}
}

func strictDecode(line []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return issue(IssueUnknownField, "record contains a field outside the 0.3.0 contract")
		}
		return issue(IssueInvalidRecord, "record does not match its declared type")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return issue(IssueMalformedJSON, "multiple JSON values on one line")
	}
	return nil
}

func validateEvent(record EventRecord, raw map[string]json.RawMessage) error {
	if err := validateEnvelope(record.SchemaVersion, record.RecordType, record.RunID, record.Endpoint); err != nil {
		return err
	}
	if record.RecordType != "event" || record.EventID == "" || record.EventType == "" {
		return issue(IssueInvalidRecord, "event is missing required identity")
	}
	if !validEventTypes[record.EventType] {
		return issue(IssueInvalidRecord, "unknown event_type")
	}
	if !validAgents[record.SourceAgent] || !validSourceTypes[record.SourceType] {
		return issue(IssueInvalidRecord, "invalid event source")
	}
	if record.Actor != "" && !validActors[record.Actor] {
		return issue(IssueInvalidRecord, "invalid actor")
	}
	if !validConfidence[record.Confidence] {
		return issue(IssueInvalidRecord, "invalid confidence")
	}
	if record.Decision != "" && !validDecisions[record.Decision] {
		return issue(IssueInvalidRecord, "invalid decision")
	}
	if record.ApprovalDecision != "" && !validDecisions[record.ApprovalDecision] {
		return issue(IssueInvalidRecord, "invalid approval_decision")
	}
	if err := validateEvidence(record.Evidence); err != nil {
		return err
	}
	if record.DurationMS != nil && *record.DurationMS < 0 || record.DiffBytes < 0 || record.ContentBytes < 0 {
		return issue(IssueInvalidRecord, "negative event counter")
	}
	if record.DiffSHA256 != "" && !lowerHex64.MatchString(record.DiffSHA256) {
		return issue(IssueInvalidRecord, "invalid diff hash")
	}
	if record.Content == "" && (record.ContentBytes != 0 || record.ContentTruncated) ||
		record.Content != "" && record.ContentBytes == 0 {
		return issue(IssueInvalidRecord, "invalid content metadata")
	}
	if duplicates(record.Tags) {
		return issue(IssueInvalidRecord, "duplicate or empty tags")
	}

	allowed := allowedValueFields[record.EventType]
	for field := range valueBearingFields {
		if _, present := raw[field]; present && !allowed[field] {
			return issue(IssueInvalidRecord, "field/event_type combination is not allowed")
		}
	}
	return nil
}

func validateFinding(record FindingRecord) error {
	if err := validateEnvelope(record.SchemaVersion, record.RecordType, record.RunID, record.Endpoint); err != nil {
		return err
	}
	if record.RecordType != "finding" || record.FindingID == "" || record.DetectedAt == "" ||
		record.RuleID == "" || record.RuleVersion == "" || record.Title == "" {
		return issue(IssueInvalidRecord, "finding is missing required fields")
	}
	if !validSeverity[record.Severity] || !validAgents[record.SourceAgent] ||
		!validSourceTypes[record.SourceType] || !validConfidence[record.Confidence] {
		return issue(IssueInvalidRecord, "finding contains an invalid enum")
	}
	if record.ObservedEventType != "" && !validEventTypes[record.ObservedEventType] ||
		record.ObservedActor != "" && !validActors[record.ObservedActor] {
		return issue(IssueInvalidRecord, "finding contains invalid observed metadata")
	}
	if len(record.EvidenceRefs) == 0 || len(record.CitedEventIDs) == 0 ||
		duplicates(record.Tags) || duplicates(record.CitedEventIDs) {
		return issue(IssueInvalidRecord, "finding contains invalid arrays")
	}
	if len(record.CitedEventIDs) > limits.MaxFindingCitedEventIDs {
		return issue(IssueInvalidRecord, "finding cited_event_ids exceeds limit")
	}
	for _, evidence := range record.EvidenceRefs {
		if err := validateEvidence(evidence); err != nil {
			return err
		}
	}
	return nil
}

func validateSummary(record ScanSummaryRecord) error {
	if err := validateEnvelope(record.SchemaVersion, record.RecordType, record.RunID, record.Endpoint); err != nil {
		return err
	}
	if record.RecordType != "scan_summary" ||
		!slices.Contains([]string{"complete", "partial", "error"}, record.Status) {
		return issue(IssueInvalidRecord, "invalid scan summary")
	}
	if record.ArtifactsScanned < 0 || record.EventsEmitted < 0 || record.FindingsEmitted < 0 ||
		record.IndicatorsEmitted < 0 || record.Diagnostics < 0 {
		return issue(IssueInvalidRecord, "negative scan summary counter")
	}
	return nil
}

func validateDiagnostic(record DiagnosticRecord) error {
	if err := validateEnvelope(record.SchemaVersion, record.RecordType, record.RunID, record.Endpoint); err != nil {
		return err
	}
	if record.RecordType != "diagnostic" || record.Timestamp == "" || record.Message == "" ||
		!slices.Contains([]string{"info", "warn", "error"}, record.Level) {
		return issue(IssueInvalidRecord, "invalid diagnostic")
	}
	return nil
}

func validateIndicator(record IndicatorRecord) error {
	if err := validateEnvelope(record.SchemaVersion, record.RecordType, record.RunID, record.Endpoint); err != nil {
		return err
	}
	if record.RecordType != "indicator" || record.Value == "" || record.Count < 1 ||
		!slices.Contains([]string{"domain", "ipv4", "ipv6", "url", "email", "md5", "sha1", "sha256"}, record.Type) {
		return issue(IssueInvalidRecord, "invalid indicator")
	}
	if record.SourceAgent != "" && !validAgents[record.SourceAgent] {
		return issue(IssueInvalidRecord, "invalid indicator source agent")
	}
	return nil
}

func validateEnforcement(record EnforcementRecord) error {
	if err := validateEnvelope(record.SchemaVersion, record.RecordType, record.RunID, record.Endpoint); err != nil {
		return err
	}
	if record.RecordType != "enforcement" || !enforcementID.MatchString(record.DecisionID) ||
		record.Timestamp == "" || record.SourceType != "hook" || !validAgents[record.SourceAgent] ||
		len(record.ActionEventIDs) == 0 || len(record.RuleIDs) == 0 ||
		duplicates(record.ActionEventIDs) || duplicates(record.FindingIDs) || duplicates(record.RuleIDs) {
		return issue(IssueInvalidRecord, "invalid enforcement record")
	}
	switch {
	case record.Mode == "monitor":
		if record.Decision != "no_override" || record.Reason != "monitor_mode" {
			return issue(IssueInvalidRecord, "invalid monitor enforcement combination")
		}
	case record.Mode == "enforce" && record.Decision == "deny":
		if record.Reason != "enforce_rule_match" || record.DenyRuleID == "" ||
			record.DenyRuleVersion == "" || !slices.Contains(record.RuleIDs, record.DenyRuleID) {
			return issue(IssueInvalidRecord, "invalid deny enforcement combination")
		}
	case record.Mode == "enforce" && record.Decision == "no_override":
		if !slices.Contains([]string{"no_enforce_eligible_match", "fail_open"}, record.Reason) ||
			record.DenyRuleID != "" || record.DenyRuleVersion != "" {
			return issue(IssueInvalidRecord, "invalid fail-open enforcement combination")
		}
	default:
		return issue(IssueInvalidRecord, "invalid enforcement mode or decision")
	}
	return nil
}

func validateEnvelope(schema, recordType, runID string, endpoint Endpoint) error {
	if schema != SchemaVersion || recordType == "" || runID == "" {
		return issue(IssueInvalidRecord, "invalid record envelope")
	}
	if endpoint.OS == "" || endpoint.Arch == "" {
		return issue(IssueInvalidRecord, "invalid endpoint envelope")
	}
	return nil
}

func validateEvidence(evidence Evidence) error {
	if evidence.ArtifactType == "" || evidence.Line < 0 || evidence.RowID < 0 {
		return issue(IssueInvalidRecord, "invalid evidence")
	}
	if evidence.SHA256 != "" && !lowerHex64.MatchString(evidence.SHA256) {
		return issue(IssueInvalidRecord, "invalid evidence hash")
	}
	return nil
}

func requiredString(raw map[string]json.RawMessage, key string) (string, error) {
	value, ok := raw[key]
	if !ok {
		return "", issue(IssueInvalidRecord, "missing required envelope field")
	}
	var result string
	if err := json.Unmarshal(value, &result); err != nil || result == "" {
		return "", issue(IssueInvalidRecord, "invalid required envelope field")
	}
	return result, nil
}

func issue(category IssueCategory, reason string) error {
	return &ParseError{Category: category, Reason: reason}
}

func duplicates(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

var (
	lowerHex64    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	enforcementID = regexp.MustCompile(`^enf-[a-f0-9]{24}$`)

	validEventTypes = stringSet(
		"session.start", "session.end", "prompt.user", "message.assistant",
		"message.reasoning", "tool.call", "tool.result", "command.exec",
		"command.result", "file.read", "file.write", "file.delete",
		"permission.requested", "permission.approved", "permission.denied",
		"config.agent", "config.mcp", "network.indicator",
	)
	validAgents = stringSet(
		"claude-code", "cowork", "codex", "gemini-cli", "cursor", "windsurf",
		"copilot", "vscode", "opencode", "openclaw", "antigravity", "factory",
		"grok", "devin-cli", "hermes", "kimi-code", "pi", "qwen-code",
		"cline", "amp", "auggie", "kiro", "goose", "kilo", "openhands",
		"crush", "junie", "unknown",
	)
	validSourceTypes = stringSet("artifact", "hook", "otel")
	validActors      = stringSet("user", "assistant", "system", "tool")
	validConfidence  = stringSet("high", "medium", "low")
	validDecisions   = stringSet("allowed", "denied", "asked")
	validSeverity    = stringSet("info", "low", "medium", "high", "critical")

	valueBearingFields = stringSet(
		"command", "exit_code", "duration_ms", "file_path", "diff_sha256",
		"diff_bytes", "tool_name", "tool_call_id", "mcp_server", "mcp_tool",
		"url", "decision", "approval_required", "approval_decision",
		"approval_reason", "content", "content_bytes", "content_truncated",
	)
	allowedValueFields = map[string]map[string]bool{
		"session.start":        {},
		"session.end":          {},
		"prompt.user":          stringSet("content", "content_bytes", "content_truncated"),
		"message.assistant":    stringSet("content", "content_bytes", "content_truncated"),
		"message.reasoning":    stringSet("content", "content_bytes", "content_truncated"),
		"tool.call":            stringSet("tool_name", "tool_call_id", "mcp_server", "mcp_tool", "url", "file_path", "decision"),
		"tool.result":          stringSet("tool_name", "tool_call_id", "mcp_server", "mcp_tool", "decision"),
		"command.exec":         stringSet("command", "tool_name", "tool_call_id", "decision"),
		"command.result":       stringSet("command", "tool_name", "tool_call_id", "exit_code", "duration_ms", "decision"),
		"file.read":            stringSet("file_path", "tool_name", "tool_call_id", "decision"),
		"file.write":           stringSet("file_path", "tool_name", "tool_call_id", "decision", "diff_sha256", "diff_bytes"),
		"file.delete":          stringSet("file_path", "tool_name", "tool_call_id", "diff_sha256", "diff_bytes"),
		"permission.requested": stringSet("tool_name", "tool_call_id", "decision", "approval_required", "approval_decision", "approval_reason"),
		"permission.approved":  stringSet("tool_name", "tool_call_id", "decision", "approval_required", "approval_decision", "approval_reason"),
		"permission.denied":    stringSet("tool_name", "tool_call_id", "decision", "approval_required", "approval_decision", "approval_reason"),
		"config.agent":         {},
		"config.mcp":           stringSet("mcp_server", "mcp_tool"),
		"network.indicator":    stringSet("url", "tool_name", "mcp_server", "mcp_tool", "tool_call_id", "decision"),
	}
)

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

var _ = fmt.Sprintf
