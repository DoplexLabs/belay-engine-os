package detection

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const builtinVersion = "1.0.0"

var safeToolIdentityComponent = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

type builtinDetector struct {
	entry    CatalogEntry
	evaluate func(context.Context, preparedSession) ([]Match, error)
	absence  func(preparedSession) (AbsenceCapability, string)
}

func (d builtinDetector) ID() string                 { return d.entry.DetectorID }
func (d builtinDetector) Version() string            { return d.entry.DetectorVersion }
func (d builtinDetector) FingerprintVersion() string { return d.entry.FingerprintVersion }

func (d builtinDetector) Evaluate(ctx context.Context, input SessionInput) (DetectorResult, error) {
	session, err := prepareSession(ctx, input)
	if err != nil {
		return DetectorResult{}, err
	}
	return d.evaluatePrepared(ctx, session)
}

func (d builtinDetector) evaluatePrepared(
	ctx context.Context,
	session preparedSession,
) (DetectorResult, error) {
	matches, err := d.evaluate(ctx, session)
	if err != nil {
		return DetectorResult{}, err
	}
	capability, reason := d.absence(session)
	return DetectorResult{
		Matches:           matches,
		AbsenceCapability: capability,
		UnavailableReason: reason,
	}, nil
}

func BuiltinDetectors() []Detector {
	return []Detector{
		newBuiltin(
			"explicit_command_failure",
			"command_failure",
			"issue.explicit_command_failure",
			"inspect_command_failure",
			false,
			detectExplicitCommandFailures,
			commandAbsenceCapability,
		),
		newBuiltin(
			"explicit_tool_failure",
			"tool_failure",
			"issue.explicit_tool_failure",
			"inspect_cited_events",
			false,
			detectExplicitToolFailures,
			toolFailureAbsenceCapability,
		),
		newBuiltin(
			"repeated_command_attempts",
			"attention",
			"issue.repeated_command_attempts",
			"inspect_repeated_attempts",
			true,
			detectRepeatedCommandAttempts,
			notApplicableAbsence,
		),
		newBuiltin(
			"explicit_permission_denial",
			"permission",
			"issue.explicit_permission_denial",
			"review_permission_boundary",
			false,
			detectPermissionDenials,
			permissionAbsenceCapability,
		),
		newBuiltin(
			"retained_verification_gap_after_changes",
			"evidence_gap",
			"issue.retained_verification_gap_after_changes",
			"inspect_cited_events",
			false,
			detectRetainedVerificationGap,
			retainedVerificationGapAbsenceCapability,
		),
		newBuiltin(
			"unresolved_verification_failure_at_completion",
			"unresolved_verification",
			"issue.unresolved_verification_failure_at_completion",
			"rerun_verification",
			false,
			detectUnresolvedVerification,
			unresolvedVerificationAbsenceCapability,
		),
	}
}

func newBuiltin(
	id string,
	category string,
	titleCode string,
	action string,
	experimental bool,
	evaluate func(context.Context, preparedSession) ([]Match, error),
	absence func(preparedSession) (AbsenceCapability, string),
) builtinDetector {
	return builtinDetector{
		entry: CatalogEntry{
			DetectorID:          id,
			DetectorVersion:     builtinVersion,
			FingerprintVersion:  "1",
			Category:            category,
			TitleCode:           titleCode,
			SuggestedActionType: action,
			Experimental:        experimental,
		},
		evaluate: evaluate,
		absence:  absence,
	}
}

func notApplicableAbsence(preparedSession) (AbsenceCapability, string) {
	return AbsenceNotApplicable, "experimental_detector"
}

func commandAbsenceCapability(session preparedSession) (AbsenceCapability, string) {
	for _, item := range session.events {
		if item.historical {
			continue
		}
		if (item.event.Observation.Type == "command.exec" ||
			item.event.Observation.Type == "command.result") &&
			item.event.Source.Kind == "hook" &&
			item.event.Coverage.Depth == "tool_call" {
			return AbsenceSupported, ""
		}
	}
	return AbsenceNotApplicable, "command_coverage_unavailable"
}

func toolFailureAbsenceCapability(preparedSession) (AbsenceCapability, string) {
	return AbsenceIncomplete, "tool_failure_absence_unsupported"
}

// harnessProvidesHookCoverage reports whether a harness installs Belay-owned
// hooks deep enough to make absence of evidence meaningful: tool-call depth
// around mutations and approvals plus a lifecycle session terminal. Codex,
// Claude Code, and Cursor all meet that bar; anything else is treated as
// unknown coverage so absence-based detectors stay silent.
//
// Antigravity is deliberately absent. Its evidence is Numbat live hook events
// only (command_exec, file_read, file_write, network_indicator, tool_call,
// tool_result) and Belay has not verified that those hooks deliver a
// lifecycle session terminal or an approval/decision event stream. Until that
// is verified, absence of evidence in an Antigravity session is not
// meaningful, so it must stay out of this list; adding it is a deliberate
// change guarded by a test.
func harnessProvidesHookCoverage(agent string) bool {
	switch agent {
	case "codex", "claude-code", "cursor":
		return true
	default:
		return false
	}
}

func permissionAbsenceCapability(session preparedSession) (AbsenceCapability, string) {
	for _, item := range session.events {
		if !item.historical &&
			item.event.Source.Kind == "hook" &&
			harnessProvidesHookCoverage(item.event.Source.Agent) {
			return AbsenceSupported, ""
		}
	}
	return AbsenceNotApplicable, "permission_coverage_unavailable"
}

func retainedVerificationGapAbsenceCapability(
	session preparedSession,
) (AbsenceCapability, string) {
	if sessionHasHistoricalEvidence(session) {
		return AbsenceIncomplete, "historical_verification_absence_unsupported"
	}
	if retainedVerificationLiveCompatible(session) {
		return AbsenceSupported, ""
	}
	return AbsenceNotApplicable, "verification_coverage_unavailable"
}

func unresolvedVerificationAbsenceCapability(
	session preparedSession,
) (AbsenceCapability, string) {
	for _, item := range session.events {
		if !item.historical &&
			item.event.Observation.Type == "session.end" &&
			item.event.Source.Kind == "hook" &&
			item.event.Coverage.Depth == "lifecycle" {
			return AbsenceSupported, ""
		}
	}
	return AbsenceIncomplete, "session_completion_unavailable"
}

func detectExplicitCommandFailures(
	ctx context.Context,
	session preparedSession,
) ([]Match, error) {
	attempts, err := commandAttempts(ctx, session)
	if err != nil {
		return nil, err
	}
	bySignature := make(map[string][]commandAttempt)
	for _, attempt := range attempts {
		if attempt.outcome == outcomeFailed {
			bySignature[attempt.signature] = append(bySignature[attempt.signature], attempt)
		}
	}
	signatures := sortedKeys(bySignature)
	matches := make([]Match, 0, len(signatures))
	for index, signature := range signatures {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		failed := bySignature[signature]
		failedCount := 0
		var evidence []preparedEvent
		confidence := ConfidenceHigh
		for _, attempt := range failed {
			evidence = append(evidence, attempt.events...)
			if attempt.repetitionEligible {
				failedCount++
			}
			if attempt.trust.failureConfidence() != ConfidenceHigh {
				confidence = ConfidenceLow
			}
		}
		if failedCount == 0 {
			failedCount = 1
		}
		first, last := attemptBounds(failed)
		severity := SeverityLow
		if failedCount >= 3 {
			severity = SeverityMedium
		}
		matches = append(matches, builtinMatch(
			session,
			"explicit_command_failure",
			severity,
			confidence,
			first,
			last,
			evidence,
			FingerprintDimension{Name: "command_signature_id", Value: signature},
		))
	}
	return matches, nil
}

func detectExplicitToolFailures(
	ctx context.Context,
	session preparedSession,
) ([]Match, error) {
	byIdentity := make(map[string][]preparedEvent)
	for index, item := range session.events {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		if item.event.Observation.Type != "tool.result" ||
			explicitEventOutcome(
				item.event.Observation.ExitCode,
				item.event.Observation.Outcome,
			) != outcomeFailed {
			continue
		}
		identity := safeToolIdentity(item.event)
		if identity == "" {
			continue
		}
		byIdentity[identity] = append(byIdentity[identity], item)
	}

	identities := sortedKeys(byIdentity)
	matches := make([]Match, 0, len(identities))
	for index, identity := range identities {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		evidence := byIdentity[identity]
		severity := SeverityLow
		if len(evidence) >= 3 {
			severity = SeverityMedium
		}
		first, last := eventTimeBounds(evidence)
		matches = append(matches, builtinMatch(
			session,
			"explicit_tool_failure",
			severity,
			ConfidenceHigh,
			first,
			last,
			evidence,
			FingerprintDimension{Name: "tool_identity", Value: identity},
		))
	}
	return matches, nil
}

func safeToolIdentity(event model.Event) string {
	if details := event.Observation.Details; details != nil {
		server, serverOK := normalizeToolIdentityComponent(details.MCPServer)
		tool, toolOK := normalizeToolIdentityComponent(details.MCPTool)
		if serverOK && toolOK {
			return "mcp:" + server + "/" + tool
		}
	}
	resource := event.Observation.Resource
	if resource == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(resource.Kind)) {
	case "mcp":
		parts := strings.Split(resource.Name, "/")
		if len(parts) != 2 {
			return ""
		}
		server, serverOK := normalizeToolIdentityComponent(parts[0])
		tool, toolOK := normalizeToolIdentityComponent(parts[1])
		if serverOK && toolOK {
			return "mcp:" + server + "/" + tool
		}
	case "tool":
		if name, ok := normalizeToolIdentityComponent(resource.Name); ok {
			return "tool:" + name
		}
	}
	return ""
}

func normalizeToolIdentityComponent(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	return value, safeToolIdentityComponent.MatchString(value)
}

func detectRepeatedCommandAttempts(
	ctx context.Context,
	session preparedSession,
) ([]Match, error) {
	attempts, err := commandAttempts(ctx, session)
	if err != nil {
		return nil, err
	}
	bySignature := make(map[string][]commandAttempt)
	for _, attempt := range attempts {
		if !attempt.repetitionEligible {
			continue
		}
		bySignature[attempt.signature] = append(bySignature[attempt.signature], attempt)
	}
	signatures := sortedKeys(bySignature)
	var matches []Match
	for index, signature := range signatures {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		repeated := bySignature[signature]
		if len(repeated) < 4 {
			continue
		}
		var evidence []preparedEvent
		allPaired := true
		for _, attempt := range repeated {
			evidence = append(evidence, attempt.events...)
			allPaired = allPaired && attempt.trust.paired()
		}
		confidence := ConfidenceLow
		if allPaired {
			confidence = ConfidenceMedium
		}
		first, last := attemptBounds(repeated)
		matches = append(matches, builtinMatch(
			session,
			"repeated_command_attempts",
			SeverityInfo,
			confidence,
			first,
			last,
			evidence,
			FingerprintDimension{Name: "command_signature_id", Value: signature},
		))
	}
	return matches, nil
}

func detectPermissionDenials(
	ctx context.Context,
	session preparedSession,
) ([]Match, error) {
	byClass := make(map[string][]preparedEvent)
	for index, item := range session.events {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		if item.event.Observation.Type != "permission.denied" {
			continue
		}
		class := strings.TrimSpace(item.enrichment.PermissionClass)
		if class == "" {
			class = "permission.unknown"
		}
		byClass[class] = append(byClass[class], item)
	}
	classes := sortedKeys(byClass)
	matches := make([]Match, 0, len(classes))
	for _, class := range classes {
		evidence := byClass[class]
		first, last := eventTimeBounds(evidence)
		confidence := ConfidenceHigh
		if class == "permission.unknown" {
			confidence = ConfidenceMedium
		}
		matches = append(matches, builtinMatch(
			session,
			"explicit_permission_denial",
			SeverityLow,
			confidence,
			first,
			last,
			evidence,
			FingerprintDimension{Name: "permission_class", Value: class},
		))
	}
	return matches, nil
}

func detectRetainedVerificationGap(
	ctx context.Context,
	session preparedSession,
) ([]Match, error) {
	terminal := -1
	for index, item := range session.events {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		if item.event.Observation.Type == "session.end" {
			terminal = index
		}
	}
	if terminal < 0 {
		return nil, nil
	}

	finalMutation := -1
	for index := 0; index < terminal; index++ {
		switch session.events[index].event.Observation.Type {
		case "file.write", "file.delete":
			finalMutation = index
		}
	}
	if finalMutation < 0 {
		return nil, nil
	}

	for index := finalMutation + 1; index < terminal; index++ {
		item := session.events[index]
		if (item.event.Observation.Type == "command.exec" ||
			item.event.Observation.Type == "command.result") &&
			isVerificationClass(normalizedCommandClass(item.enrichment.CommandClass)) {
			return nil, nil
		}
	}

	confidence, supported := retainedVerificationConfidence(session)
	if !supported {
		return nil, nil
	}
	return []Match{builtinMatch(
		session,
		"retained_verification_gap_after_changes",
		SeverityInfo,
		confidence,
		session.events[finalMutation].event.OccurredAt,
		session.events[terminal].event.OccurredAt,
		[]preparedEvent{session.events[finalMutation], session.events[terminal]},
		FingerprintDimension{Name: "verification_gap", Value: "retained_after_final_change"},
	)}, nil
}

func retainedVerificationConfidence(session preparedSession) (string, bool) {
	if sessionHasHistoricalEvidence(session) {
		return ConfidenceLow, true
	}
	if retainedVerificationLiveCompatible(session) {
		return ConfidenceMedium, true
	}
	return "", false
}

func sessionHasHistoricalEvidence(session preparedSession) bool {
	for _, item := range session.events {
		if item.historical {
			return true
		}
	}
	return false
}

func retainedVerificationLiveCompatible(session preparedSession) bool {
	harness := ""
	hasMutationCoverage := false
	hasTerminalCoverage := false
	for _, item := range session.events {
		event := item.event
		if item.historical {
			return false
		}
		if event.Source.Agent != "" {
			if harness == "" {
				harness = event.Source.Agent
			} else if harness != event.Source.Agent {
				return false
			}
		}
		if event.Source.Kind != "hook" {
			continue
		}
		switch event.Observation.Type {
		case "file.write", "file.delete":
			hasMutationCoverage = hasMutationCoverage || event.Coverage.Depth == "tool_call"
		case "session.end":
			hasTerminalCoverage = hasTerminalCoverage || event.Coverage.Depth == "lifecycle"
		}
	}
	return harnessProvidesHookCoverage(harness) &&
		hasMutationCoverage &&
		hasTerminalCoverage
}

func detectUnresolvedVerification(
	ctx context.Context,
	session preparedSession,
) ([]Match, error) {
	attempts, err := commandAttempts(ctx, session)
	if err != nil {
		return nil, err
	}
	lastTerminal := -1
	var terminalEvent preparedEvent
	for index, item := range session.events {
		if item.event.Observation.Type == "session.end" {
			lastTerminal = index
			terminalEvent = item
		}
	}
	if lastTerminal < 0 {
		return nil, nil
	}

	bySignature := make(map[string][]commandAttempt)
	for _, attempt := range attempts {
		if attempt.lastPos < lastTerminal &&
			attempt.trust.trustworthyPair() &&
			isVerificationClass(attempt.class) {
			bySignature[attempt.signature] = append(bySignature[attempt.signature], attempt)
		}
	}
	signatures := sortedKeys(bySignature)
	var matches []Match
	for index, signature := range signatures {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		series := bySignature[signature]
		var unresolved *commandAttempt
		for attemptIndex := range series {
			attempt := &series[attemptIndex]
			switch attempt.outcome {
			case outcomeFailed:
				unresolved = attempt
			case outcomeSucceeded:
				unresolved = nil
			}
		}
		if unresolved == nil {
			continue
		}
		evidence := append([]preparedEvent(nil), unresolved.events...)
		evidence = append(evidence, terminalEvent)
		matches = append(matches, builtinMatch(
			session,
			"unresolved_verification_failure_at_completion",
			SeverityHigh,
			ConfidenceHigh,
			unresolved.first,
			terminalEvent.event.OccurredAt,
			evidence,
			FingerprintDimension{Name: "command_class", Value: unresolved.class},
			FingerprintDimension{Name: "command_signature_id", Value: signature},
		))
	}
	return matches, nil
}

func builtinMatch(
	session preparedSession,
	detectorID string,
	severity string,
	confidence string,
	first time.Time,
	last time.Time,
	evidence []preparedEvent,
	fingerprint ...FingerprintDimension,
) Match {
	entry := builtinEntry(detectorID)
	citations, complete := citationIDs(evidence...)
	return Match{
		DetectorID:          entry.DetectorID,
		DetectorVersion:     entry.DetectorVersion,
		FingerprintVersion:  entry.FingerprintVersion,
		Category:            entry.Category,
		TitleCode:           entry.TitleCode,
		SuggestedActionType: entry.SuggestedActionType,
		Severity:            severity,
		Confidence:          confidence,
		Experimental:        entry.Experimental,
		FirstObservedAt:     first,
		LastObservedAt:      last,
		CitedEventIDs:       citations,
		EvidenceComplete:    complete,
		Fingerprint:         dimensions(session, fingerprint...),
	}
}

func builtinEntry(detectorID string) CatalogEntry {
	for _, detector := range BuiltinDetectors() {
		builtin := detector.(builtinDetector)
		if builtin.entry.DetectorID == detectorID {
			return builtin.entry
		}
	}
	return CatalogEntry{}
}

func sortedKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func attemptBounds(attempts []commandAttempt) (time.Time, time.Time) {
	if len(attempts) == 0 {
		return time.Time{}, time.Time{}
	}
	first := attempts[0].first
	last := attempts[0].last
	for _, attempt := range attempts[1:] {
		if attempt.first.Before(first) {
			first = attempt.first
		}
		if attempt.last.After(last) {
			last = attempt.last
		}
	}
	return first, last
}
