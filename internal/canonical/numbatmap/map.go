// Package numbatmap converts a validated upstream Numbat record into Belay's
// minimized canonical event. Prohibited upstream values are never represented
// in the output type.
package numbatmap

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/canonical/commandsafe"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

type Options struct {
	InstallationID string
	EngineVersion  string
	ObservedAt     time.Time
	Sequence       int64
	Random         io.Reader
}

func Event(record numbat.EventRecord, options Options) (model.Event, error) {
	if options.InstallationID == "" || options.Sequence < 1 {
		return model.Event{}, fmt.Errorf("mapping requires installation identity and source sequence")
	}
	if options.ObservedAt.IsZero() {
		options.ObservedAt = time.Now().UTC()
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	eventID, err := model.NewUUIDv7(options.ObservedAt, options.Random)
	if err != nil {
		return model.Event{}, err
	}
	occurredAt := options.ObservedAt
	if parsed, err := time.Parse(time.RFC3339Nano, record.Timestamp); err == nil {
		occurredAt = parsed.UTC()
	}

	dedupKey := sourceDeduplicationKey(record)
	sessionKey := stableSessionKey(record)
	observation, removed, secrets := observation(record)
	historical := record.SourceType == "artifact"
	reconstructionSource := ""
	if historical {
		reconstructionSource = bounded(record.Evidence.ArtifactType, 128)
	}

	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: options.InstallationID,
		OccurredAt:     occurredAt,
		ObservedAt:     options.ObservedAt.UTC(),
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    bounded(options.EngineVersion, 128),
			SchemaVersion:    numbat.SchemaVersion,
			RecordType:       "event",
			RunID:            bounded(record.RunID, 256),
			RecordID:         bounded(record.EventID, 256),
			Kind:             record.SourceType,
			Agent:            record.SourceAgent,
			AdapterVersion:   numbat.AdapterVersion,
			DeduplicationKey: dedupKey,
			Sequence:         options.Sequence,
		},
		Session:     model.SessionRef{Key: sessionKey},
		Observation: observation,
		Coverage: model.Coverage{
			Depth:      coverageDepth(record),
			Confidence: record.Confidence,
		},
		Redaction: model.Redaction{
			PolicyVersion:  model.RedactionVersion,
			FieldsRemoved:  removed + removedEnvelopeFields(record),
			SecretsRemoved: secrets,
		},
		Historical: model.Historical{
			IsHistorical:         historical,
			ReconstructionSource: reconstructionSource,
		},
	}, nil
}

func observation(record numbat.EventRecord) (model.Observation, int, int) {
	result := model.Observation{
		Type:       record.EventType,
		Actor:      actor(record.Actor),
		Action:     action(record.EventType),
		Outcome:    outcome(record),
		ExitCode:   record.ExitCode,
		DurationMS: record.DurationMS,
	}
	removed := 0
	secrets := 0

	details := &model.Details{
		ToolCallID:       sanitizeLabel(record.ToolCallID, 256, &secrets),
		Decision:         record.Decision,
		ApprovalRequired: record.ApprovalRequired,
		ApprovalDecision: record.ApprovalDecision,
		MCPServer:        sanitizeLabel(record.MCPServer, 128, &secrets),
		MCPTool:          sanitizeLabel(record.MCPTool, 128, &secrets),
		Model:            sanitizeLabel(record.Model, 128, &secrets),
		ModelProvider:    sanitizeLabel(record.ModelProvider, 128, &secrets),
		CLIVersion:       sanitizeLabel(record.CLIVersion, 64, &secrets),
		SubAgent:         sanitizeLabel(record.SubAgent, 128, &secrets),
		DiffSHA256:       record.DiffSHA256,
		DiffBytes:        record.DiffBytes,
		Tags:             safeTags(record.Tags),
	}
	if isEmptyDetails(details) {
		details = nil
	}
	result.Details = details

	switch record.EventType {
	case "command.exec", "command.result":
		summary := commandSummary(record.Command)
		result.Summary = summary
		if summary != record.Command && record.Command != "" {
			removed++
			secrets += secretSignals(record.Command)
		}
		if name := commandName(record.Command); name != "" {
			result.Resource = &model.Resource{Kind: "command", Name: name}
		}
	case "file.read", "file.write", "file.delete":
		if name := safePath(record.FilePath, record.ProjectPath); name != "" {
			result.Resource = &model.Resource{Kind: "file", Name: name}
		}
		if record.FilePath != "" {
			removed++
		}
	case "tool.call", "tool.result":
		if record.URL != "" {
			if host := safeURL(record.URL); host != "" {
				result.Resource = &model.Resource{Kind: "network", Name: host}
			}
			removed++
		} else if record.MCPServer != "" || record.MCPTool != "" {
			name := strings.Trim(strings.Join([]string{detailsValue(details, "server"), detailsValue(details, "tool")}, "/"), "/")
			if name != "" {
				result.Resource = &model.Resource{Kind: "mcp", Name: name}
			}
		} else if record.ToolName != "" {
			result.Resource = &model.Resource{Kind: "tool", Name: sanitizeLabel(record.ToolName, 128, &secrets)}
		}
	case "network.indicator":
		if host := safeURL(record.URL); host != "" {
			result.Resource = &model.Resource{Kind: "network", Name: host}
		}
		if record.URL != "" {
			removed++
		}
	case "config.agent":
		if details != nil && details.SubAgent != "" {
			result.Resource = &model.Resource{Kind: "agent", Name: details.SubAgent}
		}
	case "config.mcp":
		if details != nil {
			name := strings.Trim(strings.Join([]string{details.MCPServer, details.MCPTool}, "/"), "/")
			if name != "" {
				result.Resource = &model.Resource{Kind: "mcp", Name: name}
			}
		}
	case "session.start", "session.end":
		result.Resource = &model.Resource{Kind: "session", Name: "agent-session"}
	}
	return result, removed, secrets
}

func sourceDeduplicationKey(record numbat.EventRecord) string {
	parts := []string{
		record.SourceAgent,
		record.SourceType,
		record.EventID,
		record.SessionID,
		record.Evidence.ArtifactType,
		record.Evidence.LocalPath,
		fmt.Sprint(record.Evidence.Line),
		fmt.Sprint(record.Evidence.RowID),
		record.Evidence.SHA256,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stableSessionKey(record numbat.EventRecord) string {
	identity := record.SessionID
	if identity == "" {
		identity = strings.Join([]string{
			record.Evidence.ArtifactType,
			record.Evidence.LocalPath,
			record.Evidence.SHA256,
			record.RunID,
		}, "\x00")
		return SessionKey(record.SourceAgent, identity, record.ProjectPath)
	}
	return SessionKey(record.SourceAgent, identity, "")
}

func SessionKey(sourceAgent, sessionID, fallback string) string {
	sum := sha256.Sum256([]byte(sourceAgent + "\x00" + sessionID + "\x00" + fallback))
	return "ses_" + hex.EncodeToString(sum[:16])
}

func coverageDepth(record numbat.EventRecord) string {
	switch record.SourceType {
	case "artifact":
		return "artifact"
	case "otel":
		return "otlp"
	case "hook":
		switch record.EventType {
		case "tool.call", "tool.result", "command.exec", "command.result",
			"file.read", "file.write", "file.delete", "network.indicator":
			return "tool_call"
		default:
			return "lifecycle"
		}
	default:
		return "artifact"
	}
}

func actor(value string) string {
	switch value {
	case "user", "assistant", "system", "tool":
		return value
	default:
		return "unknown"
	}
}

func action(eventType string) string {
	if before, _, ok := strings.Cut(eventType, "."); ok {
		return before
	}
	return eventType
}

func outcome(record numbat.EventRecord) string {
	terminalOutcome := "unknown"
	if record.ExitCode != nil {
		if *record.ExitCode == 0 {
			terminalOutcome = "succeeded"
		} else {
			terminalOutcome = "failed"
		}
	} else {
		switch record.EventType {
		case "permission.approved":
			terminalOutcome = "succeeded"
		case "permission.denied":
			terminalOutcome = "failed"
		}
	}

	if record.EventType != "tool.result" || !hasExactTag(record.Tags, "tool_error") {
		return terminalOutcome
	}
	if terminalOutcome == "succeeded" {
		return "unknown"
	}
	return "failed"
}

func hasExactTag(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removedEnvelopeFields(record numbat.EventRecord) int {
	removed := 5 // endpoint hostname, username, uid, device id, and raw endpoint identity
	for _, present := range []bool{
		record.ProjectPath != "",
		record.GitBranch != "",
		record.Entrypoint != "",
		record.ContentPreview != "",
		record.Content != "",
		record.ApprovalReason != "",
		record.Evidence.LocalPath != "",
	} {
		if present {
			removed++
		}
	}
	return removed
}

func commandName(command string) string {
	return commandsafe.Normalize(command).Executable
}

func commandSummary(command string) string {
	return commandsafe.Normalize(command).Summary
}

func detailsValue(details *model.Details, field string) string {
	if details == nil {
		return ""
	}
	if field == "server" {
		return details.MCPServer
	}
	return details.MCPTool
}
