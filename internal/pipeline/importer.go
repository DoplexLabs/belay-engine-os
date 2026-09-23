// Package pipeline orchestrates acquisition, canonicalization, and persistence
// without letting those planes depend on one another in reverse.
package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/analysis"
	"github.com/DoplexLabs/belay-engine/internal/canonical/numbatmap"
	"github.com/DoplexLabs/belay-engine/internal/sessionidentity"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const MaxUpstreamRecordBytes = 2 << 20

type Importer struct {
	store          *local.Store
	installationID string
	engineVersion  string
	now            func() time.Time
	random         io.Reader
	sequenceBase   int64
}

type Report struct {
	Lines                int64 `json:"lines"`
	EventsAccepted       int   `json:"events_accepted"`
	EventDuplicates      int   `json:"event_duplicates"`
	FindingsAccepted     int   `json:"findings_accepted"`
	FindingDuplicates    int   `json:"finding_duplicates"`
	SummariesAccepted    int   `json:"summaries_accepted"`
	DiagnosticsAccepted  int   `json:"diagnostics_accepted"`
	IndicatorsIgnored    int   `json:"indicators_ignored"`
	EnforcementRejected  int   `json:"enforcement_rejected"`
	SessionLinksAccepted int   `json:"session_links_accepted"`
	Quarantined          int   `json:"quarantined"`
	Malformed            int   `json:"malformed"`
	Oversized            int   `json:"oversized"`
}

func New(store *local.Store, installationID, engineVersion string) *Importer {
	return &Importer{
		store:          store,
		installationID: installationID,
		engineVersion:  engineVersion,
		now:            func() time.Time { return time.Now().UTC() },
		random:         rand.Reader,
	}
}

func (i *Importer) WithClock(now func() time.Time) *Importer {
	i.now = now
	return i
}

func (i *Importer) WithRandom(random io.Reader) *Importer {
	i.random = random
	return i
}

// WithSequenceBase makes source sequence stable across incremental imports.
// A live-file tailer should use the byte offset preceding its batch.
func (i *Importer) WithSequenceBase(sequenceBase int64) *Importer {
	if sequenceBase > 0 {
		i.sequenceBase = sequenceBase
	}
	return i
}

func (i *Importer) Import(ctx context.Context, input io.Reader) (Report, error) {
	if i.store == nil || i.installationID == "" || i.engineVersion == "" {
		return Report{}, errors.New("importer requires store, installation ID, and engine version")
	}
	reader := bufio.NewReaderSize(input, 64*1024)
	var report Report

	for {
		line, err := readBoundedLine(reader, MaxUpstreamRecordBytes)
		if err != nil && !errors.Is(err, io.EOF) {
			return report, fmt.Errorf("read upstream stream: %w", err)
		}
		if len(line.Data) == 0 && !line.Oversized {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		report.Lines++
		if line.Oversized {
			report.Oversized++
			report.Quarantined++
			if recordErr := i.store.RecordQuarantine(ctx, local.Quarantine{
				LineNumber:   report.Lines,
				Category:     "oversized_record",
				Reason:       "upstream record exceeds the configured byte limit",
				RecordSHA256: line.Digest,
			}); recordErr != nil {
				return report, recordErr
			}
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		record, parseErr := numbat.ParseLine(line.Data)
		if parseErr != nil {
			report.Quarantined++
			var typed *numbat.ParseError
			category := "invalid_record"
			reason := "record failed strict validation"
			if errors.As(parseErr, &typed) {
				category = string(typed.Category)
				reason = typed.Reason
				if typed.Category == numbat.IssueMalformedJSON {
					report.Malformed++
				}
			}
			if recordErr := i.store.RecordQuarantine(ctx, local.Quarantine{
				LineNumber:   report.Lines,
				Category:     category,
				Reason:       reason,
				RecordSHA256: line.Digest,
			}); recordErr != nil {
				return report, recordErr
			}
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		if handleErr := i.handle(ctx, report.Lines, line.Digest, record, &report); handleErr != nil {
			return report, handleErr
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return report, nil
}

func (i *Importer) handle(ctx context.Context, line int64, digest string, record any, report *Report) error {
	switch value := record.(type) {
	case numbat.EventRecord:
		if err := i.store.TouchImportRun(ctx, value.RunID); err != nil {
			return err
		}
		event, err := numbatmap.Event(value, numbatmap.Options{
			InstallationID: i.installationID,
			EngineVersion:  i.engineVersion,
			ObservedAt:     i.now(),
			Sequence:       i.sequenceBase + line,
			Random:         i.random,
		})
		if err != nil {
			report.Quarantined++
			return i.store.RecordQuarantine(ctx, local.Quarantine{
				SourceRunID:  value.RunID,
				LineNumber:   line,
				Category:     "mapping_error",
				Reason:       "validated event could not be minimized",
				RecordSHA256: digest,
			})
		}
		appendResult, err := i.store.AppendEventResolved(ctx, event)
		if err != nil {
			return err
		}
		if value.SessionID != "" {
			observation := sessionidentity.Observation{
				SourceKind:            sessionIdentitySourceKind(value.SourceType),
				SourceAgent:           value.SourceAgent,
				SourceSessionKey:      event.Session.Key,
				NativeNamespace:       "numbat_" + value.SourceType,
				NativeSessionID:       value.SessionID,
				ArtifactType:          value.Evidence.ArtifactType,
				ArtifactSHA256:        value.Evidence.SHA256,
				SourceRunID:           value.RunID,
				Coverage:              sessionidentity.CoverageObserved,
				ObservedAt:            event.ObservedAt,
				ParentNativeSessionID: value.ParentSessionID,
			}
			switch value.EventType {
			case "session.start":
				observation.StartedAt = event.OccurredAt
			case "session.end":
				observation.EndedAt = event.OccurredAt
			}
			if err := i.store.UpsertSessionIdentityObservation(
				ctx,
				observation,
			); err != nil {
				return err
			}
		}
		if err := analysis.EnrichEventRecord(
			ctx,
			i.store,
			appendResult.EventID,
			event.Session.Key,
			value,
		); err != nil {
			_ = i.store.RecordAnalysisDiagnostic(
				ctx,
				event.Session.Key,
				"enrichment",
				analysis.EnrichmentDiagnosticCode(err),
			)
		}
		if appendResult.Inserted {
			report.EventsAccepted++
		} else {
			report.EventDuplicates++
		}
	case numbat.SessionLinkRecord:
		if err := i.store.TouchImportRun(ctx, value.RunID); err != nil {
			return err
		}
		switch value.Relationship {
		case "hook_artifact_alias", "rotated_artifact":
			left := numbatmap.SessionKey(
				value.SourceAgent,
				value.Left.SessionID,
				"",
			)
			right := numbatmap.SessionKey(
				value.SourceAgent,
				value.Right.SessionID,
				"",
			)
			if left != right {
				link := sessionidentity.NewSourceLineageLink(
					left,
					right,
					append([]string{value.LinkID}, value.SourceRefs...),
					i.now(),
				)
				if _, err := i.store.UpsertSessionIdentityLink(
					ctx,
					link,
				); err != nil {
					return err
				}
			}
		case "parent_subagent":
			child := value.Right.SessionID
			parent := value.Left.SessionID
			if child != "" && parent != "" && child != parent {
				if err := i.store.UpsertSessionIdentityObservation(
					ctx,
					sessionidentity.Observation{
						SourceKind:            sessionidentity.SourceNumbatArtifact,
						SourceAgent:           value.SourceAgent,
						SourceSessionKey:      numbatmap.SessionKey(value.SourceAgent, child, ""),
						NativeNamespace:       "numbat_" + value.Right.Namespace,
						NativeSessionID:       child,
						ParentNativeSessionID: parent,
						SourceRunID:           value.RunID,
						Coverage:              sessionidentity.CoverageObserved,
						ObservedAt:            i.now(),
					},
				); err != nil {
					return err
				}
			}
		}
		report.SessionLinksAccepted++
	case numbat.FindingRecord:
		if err := i.store.TouchImportRun(ctx, value.RunID); err != nil {
			return err
		}
		detectedAt, err := time.Parse(time.RFC3339Nano, value.DetectedAt)
		if err != nil {
			report.Quarantined++
			return i.store.RecordQuarantine(ctx, local.Quarantine{
				SourceRunID:  value.RunID,
				LineNumber:   line,
				Category:     "invalid_record",
				Reason:       "finding has an invalid detected_at timestamp",
				RecordSHA256: digest,
			})
		}
		sessionKey := ""
		if value.SessionID != "" {
			sessionKey = numbatmap.SessionKey(value.SourceAgent, value.SessionID, "")
		}
		projectScopeHint := ""
		if value.ProjectPathHash != "" {
			scope, err := i.store.DeriveNumbatProjectScopeHash(value.ProjectPathHash)
			if err != nil {
				report.Quarantined++
				return i.store.RecordQuarantine(ctx, local.Quarantine{
					SourceRunID:  value.RunID,
					LineNumber:   line,
					Category:     "invalid_record",
					Reason:       "finding project path hash is invalid",
					RecordSHA256: digest,
				})
			}
			projectScopeHint = scope.ID
		}
		canonicalEventIDs, err := i.store.ResolveCanonicalEventIDs(
			ctx,
			value.RunID,
			sessionKey,
			value.CitedEventIDs,
		)
		if errors.Is(err, local.ErrUnresolvedCitation) {
			report.Quarantined++
			return i.store.RecordQuarantine(ctx, local.Quarantine{
				SourceRunID:  value.RunID,
				LineNumber:   line,
				Category:     "unresolved_finding_citation",
				Reason:       "finding citation did not resolve to one canonical event",
				RecordSHA256: digest,
			})
		}
		if err != nil {
			return err
		}
		findingResult, err := i.store.RecordFindingResolved(ctx, local.Finding{
			FindingID:        value.FindingID,
			SourceRunID:      value.RunID,
			SessionKey:       sessionKey,
			ProjectScopeHint: projectScopeHint,
			DetectedAt:       detectedAt,
			RuleID:           value.RuleID,
			RuleVersion:      value.RuleVersion,
			Severity:         value.Severity,
			SourceAgent:      value.SourceAgent,
			Confidence:       value.Confidence,
			CitedEventIDs:    canonicalEventIDs,
		})
		if errors.Is(err, local.ErrFindingCitationLimit) {
			report.Quarantined++
			return i.store.RecordQuarantine(ctx, local.Quarantine{
				SourceRunID:  value.RunID,
				LineNumber:   line,
				Category:     "finding_citation_limit",
				Reason:       "finding citation count exceeds the configured limit",
				RecordSHA256: digest,
			})
		}
		if errors.Is(err, local.ErrFindingIdentityConflict) {
			report.Quarantined++
			return i.store.RecordQuarantine(ctx, local.Quarantine{
				SourceRunID:  value.RunID,
				LineNumber:   line,
				Category:     "finding_identity_conflict",
				Reason:       "finding ID conflicts with an existing immutable identity",
				RecordSHA256: digest,
			})
		}
		if err != nil {
			return err
		}
		if findingResult.SessionKey != "" {
			if _, err := i.store.MarkSessionDirty(
				ctx,
				findingResult.SessionKey,
				"finding_imported",
			); err != nil {
				return err
			}
		}
		if findingResult.Inserted {
			report.FindingsAccepted++
		} else {
			report.FindingDuplicates++
		}
	case numbat.ScanSummaryRecord:
		if err := i.store.RecordImportSummary(ctx, local.ImportSummary{
			SourceRunID:       value.RunID,
			Status:            value.Status,
			Complete:          value.Status == "complete" && (value.HTTPFailed == nil || !*value.HTTPFailed),
			ArtifactsScanned:  value.ArtifactsScanned,
			EventsEmitted:     value.EventsEmitted,
			FindingsEmitted:   value.FindingsEmitted,
			IndicatorsEmitted: value.IndicatorsEmitted,
			Diagnostics:       value.Diagnostics,
		}); err != nil {
			return err
		}
		report.SummariesAccepted++
	case numbat.DiagnosticRecord:
		if err := i.store.TouchImportRun(ctx, value.RunID); err != nil {
			return err
		}
		if err := i.store.RecordDiagnostic(ctx, value.RunID, line, value.Level, "numbat."+value.Level); err != nil {
			return err
		}
		report.DiagnosticsAccepted++
	case numbat.IndicatorRecord:
		if err := i.store.TouchImportRun(ctx, value.RunID); err != nil {
			return err
		}
		report.IndicatorsIgnored++
	case numbat.EnforcementRecord:
		if err := i.store.TouchImportRun(ctx, value.RunID); err != nil {
			return err
		}
		if err := i.store.RecordQuarantine(ctx, local.Quarantine{
			SourceRunID:  value.RunID,
			LineNumber:   line,
			Category:     "enforcement_rejected",
			Reason:       "Belay V1 does not accept enforcement records",
			RecordSHA256: digest,
		}); err != nil {
			return err
		}
		report.EnforcementRejected++
		report.Quarantined++
	default:
		return fmt.Errorf("internal error: unhandled validated record %T", record)
	}
	return nil
}

func sessionIdentitySourceKind(sourceType string) string {
	switch sourceType {
	case "artifact":
		return sessionidentity.SourceNumbatArtifact
	case "hook":
		return sessionidentity.SourceNumbatHook
	case "otel":
		return sessionidentity.SourceOTLP
	default:
		return "numbat_" + sourceType
	}
}

type boundedLine struct {
	Data      []byte
	Digest    string
	Oversized bool
}

func readBoundedLine(reader *bufio.Reader, limit int) (boundedLine, error) {
	var buffer bytes.Buffer
	digest := sha256.New()
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		writeDigest(digest, fragment)
		if !oversized {
			if buffer.Len()+len(fragment) > limit {
				oversized = true
				buffer.Reset()
			} else {
				buffer.Write(fragment)
			}
		}
		if err == nil {
			return boundedLine{
				Data:      bytes.TrimSpace(buffer.Bytes()),
				Digest:    "sha256:" + hex.EncodeToString(digest.Sum(nil)),
				Oversized: oversized,
			}, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return boundedLine{
				Data:      bytes.TrimSpace(buffer.Bytes()),
				Digest:    "sha256:" + hex.EncodeToString(digest.Sum(nil)),
				Oversized: oversized,
			}, io.EOF
		}
		return boundedLine{}, err
	}
}

func writeDigest(digest hash.Hash, fragment []byte) {
	_, _ = digest.Write(fragment)
}
