package analysis

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection"
	"github.com/DoplexLabs/belay-engine/internal/limits"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const (
	reconciliationPageSize   = 501
	findingPageSize          = 101
	numbatDetectorID         = "numbat_finding"
	numbatFingerprintV1      = "1"
	projectionOriginLimit    = 100
	numbatFindingInputLimit  = projectionOriginLimit * 10
	numbatCitationInputLimit = numbatFindingInputLimit
)

type Report struct {
	Claimed     int `json:"claimed"`
	Current     int `json:"current"`
	Truncated   int `json:"truncated"`
	Failed      int `json:"failed"`
	Stale       int `json:"stale"`
	Occurrences int `json:"occurrences"`
}

type Reconciler struct {
	store         *local.Store
	catalog       detection.Catalog
	now           func() time.Time
	lock          *sync.Mutex
	beforePublish func(local.DirtySession)
}

var reconciliationLocks sync.Map

func NewReconciler(store *local.Store) *Reconciler {
	return NewReconcilerWithCatalog(store, detection.DefaultCatalog())
}

func NewReconcilerWithCatalog(store *local.Store, catalog detection.Catalog) *Reconciler {
	lockValue, _ := reconciliationLocks.LoadOrStore(store, &sync.Mutex{})
	return &Reconciler{
		store:   store,
		catalog: catalog,
		now:     func() time.Time { return time.Now().UTC() },
		lock:    lockValue.(*sync.Mutex),
	}
}

// Startup re-dirties the stable session snapshot before draining. Besides
// catalog refresh, this recovers work left in the claimed state by a crash
// without requiring a second storage claim-recovery contract.
func (r *Reconciler) Startup(ctx context.Context) (Report, error) {
	if r == nil || r.store == nil {
		return Report{}, errors.New("analysis reconciler requires a store")
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if err := r.markAllSessionsDirty(ctx); err != nil {
		return Report{}, err
	}
	return r.drainLocked(ctx)
}

func (r *Reconciler) Drain(ctx context.Context) (Report, error) {
	if r == nil || r.store == nil {
		return Report{}, errors.New("analysis reconciler requires a store")
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.drainLocked(ctx)
}

func (r *Reconciler) drainLocked(ctx context.Context) (Report, error) {
	var report Report
	for {
		work, err := r.store.ClaimDirtySession(ctx, r.now())
		if errors.Is(err, local.ErrNoDirtySession) {
			return report, nil
		}
		if err != nil {
			return report, err
		}
		report.Claimed++
		result, err := r.reconcileSession(ctx, work)
		report.Occurrences += result.occurrences
		switch result.status {
		case model.AnalysisCurrent:
			report.Current++
		case model.AnalysisTruncated:
			report.Truncated++
		case model.AnalysisFailed:
			report.Failed++
		}
		if errors.Is(err, local.ErrStaleProjection) {
			report.Stale++
			continue
		}
		if err != nil {
			return report, err
		}
	}
}

type sessionResult struct {
	status      model.AnalysisStatus
	occurrences int
}

func (r *Reconciler) reconcileSession(
	ctx context.Context,
	work local.DirtySession,
) (sessionResult, error) {
	scope, err := r.sessionScope(ctx, work.SessionKey)
	if err != nil {
		return r.fail(ctx, work, "analysis_scope_read_failed", nil)
	}
	events, eventLimitExceeded, eventGeneration, analysisThrough, err :=
		r.sessionEvents(ctx, work.SessionKey)
	if err != nil {
		return r.fail(ctx, work, "analysis_event_read_failed", nil)
	}
	enrichments := map[string]model.EventEnrichment{}
	if !eventLimitExceeded {
		enrichments, err = r.store.EventEnrichments(ctx, work.SessionKey)
		if err != nil {
			return r.fail(ctx, work, "analysis_enrichment_read_failed", nil)
		}
	}
	findings, findingInputTruncated, err := r.sessionFindings(ctx, work.SessionKey)
	if err != nil {
		return r.fail(ctx, work, "analysis_finding_read_failed", nil)
	}

	input := detection.SessionInput{
		SessionID:    work.SessionKey,
		ProjectScope: scope.ProjectScopeID,
		ScopeQuality: string(scope.Quality),
		Events:       events,
		Enrichments:  detectionEnrichments(enrichments),
	}
	catalogResult := detection.CatalogResult{
		CatalogVersion: detection.CatalogVersion,
		Status:         detection.StatusTruncated,
	}
	if !eventLimitExceeded {
		catalogResult = r.catalog.Run(ctx, input)
	}
	numbatOccurrences, numbatTruncated, err := r.numbatOccurrences(
		scope,
		events,
		findings,
	)
	if err != nil {
		return r.fail(ctx, work, "analysis_finding_projection_failed", nil)
	}

	status := model.AnalysisCurrent
	var belayOccurrences []model.IssueOccurrence
	switch catalogResult.Status {
	case detection.StatusTruncated:
		status = model.AnalysisTruncated
	case detection.StatusFailed:
		return r.fail(ctx, work, "detector_catalog_failed", catalogResult.Failures)
	default:
		belayOccurrences, err = r.detectorOccurrences(scope, events, catalogResult)
		if err != nil {
			return r.fail(ctx, work, "analysis_identity_failed", nil)
		}
	}

	if findingInputTruncated || numbatTruncated {
		status = model.AnalysisTruncated
	}
	if r.beforePublish != nil {
		r.beforePublish(work)
	}
	capabilities := r.analysisCapabilities(
		scope,
		catalogResult,
		numbatOccurrences,
		analysisThrough,
		status,
	)
	_, err = r.store.ReplaceSessionProjection(ctx, local.SessionProjectionReplacement{
		SessionKey:              work.SessionKey,
		ClaimedGeneration:       work.TargetGeneration,
		Status:                  status,
		ScopeQuality:            scope.Quality,
		AnalysisThroughOrderNS:  analysisThrough,
		AnalyzedEventGeneration: eventGeneration,
		BelayOccurrences:        belayOccurrences,
		NumbatOccurrences:       numbatOccurrences,
		Capabilities:            capabilities,
	})
	if errors.Is(err, local.ErrStaleProjection) {
		return sessionResult{}, err
	}
	if err != nil {
		return r.fail(ctx, work, "analysis_projection_publish_failed", nil)
	}
	return sessionResult{
		status:      status,
		occurrences: len(numbatOccurrences) + len(belayOccurrences),
	}, nil
}

func (r *Reconciler) fail(
	ctx context.Context,
	work local.DirtySession,
	code string,
	failures []detection.DetectorFailure,
) (sessionResult, error) {
	for _, failure := range failures {
		detectorID := failure.DetectorID
		if detectorID == "" {
			detectorID = "catalog"
		}
		_ = r.store.RecordAnalysisDiagnostic(
			ctx,
			work.SessionKey,
			detectorID,
			failure.Code,
		)
	}
	if len(failures) == 0 {
		_ = r.store.RecordAnalysisDiagnostic(
			ctx,
			work.SessionKey,
			"reconciler",
			code,
		)
	}
	retryAt := r.now().Add(retryDelay(work.AttemptCount))
	_, err := r.store.PublishAnalysisFailure(
		ctx,
		work.SessionKey,
		work.TargetGeneration,
		code,
		retryAt,
	)
	return sessionResult{status: model.AnalysisFailed}, err
}

func (r *Reconciler) sessionScope(
	ctx context.Context,
	sessionID string,
) (local.SessionScope, error) {
	scope, err := r.store.GetSessionScope(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return local.SessionScope{
			SessionKey: sessionID,
			Quality:    model.ScopeUnscoped,
		}, nil
	}
	return scope, err
}

func (r *Reconciler) sessionEvents(
	ctx context.Context,
	sessionID string,
) ([]model.Event, bool, int64, *int64, error) {
	events := make([]model.Event, 0, detection.MaxSessionEvents)
	var snapshot int64
	var cursor *model.EventPosition
	for len(events) < detection.MaxSessionEvents {
		limit := reconciliationPageSize
		remaining := detection.MaxSessionEvents - len(events)
		if remaining < limit {
			limit = remaining
		}
		page, err := r.store.QuerySessionTimeline(ctx, model.TimelineQuery{
			SessionID: sessionID,
			Limit:     limit,
			Snapshot:  snapshot,
			Cursor:    cursor,
		})
		if err != nil {
			return nil, false, 0, nil, err
		}
		if snapshot == 0 {
			snapshot = page.Snapshot
		}
		events = append(events, page.Data...)
		if len(page.Data) < limit {
			return events, false, snapshot, eventWatermark(events), nil
		}
		last := page.Data[len(page.Data)-1]
		cursor = &model.EventPosition{
			OccurredAt:     last.OccurredAt,
			SourceSequence: last.Source.Sequence,
			EventID:        last.EventID,
		}
	}
	lookAhead, err := r.store.QuerySessionTimeline(ctx, model.TimelineQuery{
		SessionID: sessionID,
		Limit:     1,
		Snapshot:  snapshot,
		Cursor:    cursor,
	})
	if err != nil {
		return nil, false, 0, nil, err
	}
	return events, len(lookAhead.Data) > 0, snapshot, eventWatermark(events), nil
}

func eventWatermark(events []model.Event) *int64 {
	if len(events) == 0 {
		return nil
	}
	value := events[0].OccurredAt.UTC().UnixNano()
	for _, event := range events[1:] {
		if candidate := event.OccurredAt.UTC().UnixNano(); candidate > value {
			value = candidate
		}
	}
	return &value
}

func (r *Reconciler) sessionFindings(
	ctx context.Context,
	sessionID string,
) ([]model.FindingSummary, bool, error) {
	result := make([]model.FindingSummary, 0, numbatFindingInputLimit)
	citationCount := 0
	truncated := false
	var snapshot int64
	var cursor *model.FindingPosition
	for len(result) <= numbatFindingInputLimit {
		remainingLookAhead := numbatFindingInputLimit + 1 - len(result)
		limit := findingPageSize
		if remainingLookAhead < limit {
			limit = remainingLookAhead
		}
		page, pageCitationsTruncated, err := r.store.QueryFindingsForAnalysis(
			ctx,
			model.FindingQuery{
				Filter: model.FindingFilter{
					SessionID: sessionID,
					Limit:     limit,
				},
				Snapshot: snapshot,
				Cursor:   cursor,
			},
		)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || pageCitationsTruncated
		if snapshot == 0 {
			snapshot = page.Snapshot
		}
		for _, finding := range page.Data {
			if len(result) == numbatFindingInputLimit {
				return result, true, nil
			}
			remainingCitations := numbatCitationInputLimit - citationCount
			if remainingCitations == 0 {
				return result, true, nil
			}
			if len(finding.CitedEventIDs) > remainingCitations {
				finding.CitedEventIDs = append(
					[]string(nil),
					finding.CitedEventIDs[:remainingCitations]...,
				)
				result = append(result, finding)
				return result, true, nil
			}
			finding.CitedEventIDs = append([]string(nil), finding.CitedEventIDs...)
			citationCount += len(finding.CitedEventIDs)
			result = append(result, finding)
		}
		if len(page.Data) < limit {
			return result, truncated, nil
		}
		last := page.Data[len(page.Data)-1]
		cursor = &model.FindingPosition{
			DetectedAt: last.DetectedAt,
			FindingID:  last.FindingID,
		}
	}
	return result, truncated, nil
}

func (r *Reconciler) markAllSessionsDirty(ctx context.Context) error {
	var snapshot int64
	var endedAt *time.Time
	var sessionID string
	for {
		page, err := r.store.QuerySessions(ctx, model.SessionQuery{
			Limit:           101,
			Snapshot:        snapshot,
			CursorEndedAt:   endedAt,
			CursorSessionID: sessionID,
		})
		if err != nil {
			return err
		}
		if snapshot == 0 {
			snapshot = page.Snapshot
		}
		for _, session := range page.Data {
			if _, err := r.store.MarkSessionDirty(
				ctx,
				session.SessionID,
				"catalog_refresh",
			); err != nil {
				return err
			}
		}
		if len(page.Data) < 101 {
			return nil
		}
		last := page.Data[len(page.Data)-1]
		value := last.EndedAt
		endedAt = &value
		sessionID = last.SessionID
	}
}

func (r *Reconciler) detectorOccurrences(
	scope local.SessionScope,
	events []model.Event,
	result detection.CatalogResult,
) ([]model.IssueOccurrence, error) {
	harness := sessionHarness(events)
	scopeIdentity := issueScopeIdentity(scope)
	occurrences := make([]model.IssueOccurrence, 0, len(result.Matches))
	for _, match := range result.Matches {
		material := make([]string, 0, len(match.Fingerprint)*2)
		evidenceDimensions := make([]string, 0, len(match.Fingerprint)*2)
		for _, dimension := range match.Fingerprint {
			evidenceDimensions = append(
				evidenceDimensions,
				dimension.Name,
				dimension.Value,
			)
			if dimension.Name == "project_scope_id" ||
				dimension.Name == "session_id" {
				continue
			}
			material = append(material, dimension.Name, dimension.Value)
		}
		fingerprintID, issueID, err := r.store.DeriveIssueIdentity(
			match.FingerprintVersion,
			match.DetectorID,
			scopeIdentity,
			material...,
		)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, model.IssueOccurrence{
			IssueID:            issueID,
			FingerprintID:      fingerprintID,
			FingerprintVersion: match.FingerprintVersion,
			Origin:             "belay",
			SessionID:          scope.SessionKey,
			Harness:            harness,
			Provenance: model.DetectorProvenance{
				DetectorID:         match.DetectorID,
				DetectorVersion:    match.DetectorVersion,
				FingerprintVersion: match.FingerprintVersion,
				ProjectionVersion:  result.CatalogVersion,
			},
			Category:           match.Category,
			TitleCode:          match.TitleCode,
			Severity:           match.Severity,
			Confidence:         match.Confidence,
			ScopeQuality:       scope.Quality,
			FirstObservedAt:    match.FirstObservedAt,
			LastObservedAt:     match.LastObservedAt,
			EvidenceComplete:   match.EvidenceComplete,
			Experimental:       match.Experimental,
			FingerprintScopeID: exactFingerprintScope(scope),
			Evidence: model.IssueEvidence{
				CitedEventIDs: append([]string(nil), match.CitedEventIDs...),
				Dimensions:    evidenceDimensions,
			},
		})
	}
	return occurrences, nil
}

func (r *Reconciler) numbatOccurrences(
	scope local.SessionScope,
	events []model.Event,
	findings []model.FindingSummary,
) ([]model.IssueOccurrence, bool, error) {
	eventTypes := make(map[string]string, len(events))
	for _, event := range events {
		eventTypes[event.EventID] = event.Observation.Type
	}
	scopeIdentity := issueScopeIdentity(scope)
	grouped := make(map[string]model.IssueOccurrence)
	truncated := false
	citationCount := 0
	for _, finding := range findings {
		citedEventIDs := finding.CitedEventIDs
		if len(citedEventIDs) > limits.MaxFindingCitedEventIDs {
			citedEventIDs = citedEventIDs[:limits.MaxFindingCitedEventIDs]
			truncated = true
		}
		remainingCitations := numbatCitationInputLimit - citationCount
		if remainingCitations == 0 {
			truncated = true
			break
		}
		if len(citedEventIDs) > remainingCitations {
			citedEventIDs = citedEventIDs[:remainingCitations]
			truncated = true
		}
		citationCount += len(citedEventIDs)
		findingScopeIdentity := scopeIdentity
		findingScopeQuality := scope.Quality
		if scope.Quality != model.ScopeConflict &&
			finding.ProjectScopeHint != "" {
			findingScopeIdentity = finding.ProjectScopeHint
			findingScopeQuality = model.ScopeLexical
		}
		types := make([]string, 0, len(citedEventIDs))
		for _, eventID := range citedEventIDs {
			if eventType := eventTypes[eventID]; eventType != "" {
				types = append(types, eventType)
			}
		}
		sort.Strings(types)
		types = compact(types)
		material := []string{
			"rule_id", finding.RuleID,
			"rule_version", finding.RuleVersion,
		}
		for _, eventType := range types {
			material = append(material, "event_type", eventType)
		}
		fingerprintID, issueID, err := r.store.DeriveIssueIdentity(
			numbatFingerprintV1,
			numbatDetectorID,
			findingScopeIdentity,
			material...,
		)
		if err != nil {
			return nil, false, err
		}
		current, present := grouped[fingerprintID]
		if !present && len(grouped) >= projectionOriginLimit {
			truncated = true
			continue
		}
		if !present {
			current = model.IssueOccurrence{
				IssueID:            issueID,
				FingerprintID:      fingerprintID,
				FingerprintVersion: numbatFingerprintV1,
				Origin:             "numbat",
				OriginRecordID:     finding.FindingID,
				SessionID:          scope.SessionKey,
				Harness:            safeHarness(finding.Harness),
				Provenance: model.DetectorProvenance{
					DetectorID:         numbatDetectorID,
					DetectorVersion:    opaqueVersion(finding.RuleVersion),
					FingerprintVersion: numbatFingerprintV1,
					ProjectionVersion:  detection.CatalogVersion,
				},
				Category:         "numbat_finding",
				TitleCode:        "issue.numbat_finding",
				SourceSignalCode: model.SafeSourceSignalCode(finding.RuleID),
				Severity:         finding.Severity,
				Confidence:       finding.Confidence,
				ScopeQuality:     findingScopeQuality,
				FirstObservedAt:  finding.DetectedAt,
				LastObservedAt:   finding.DetectedAt,
				EvidenceComplete: true,
				FingerprintScopeID: func() string {
					if findingScopeQuality == model.ScopeResolved ||
						findingScopeQuality == model.ScopeLexical {
						return findingScopeIdentity
					}
					return ""
				}(),
				Evidence: model.IssueEvidence{
					Dimensions: append([]string(nil), material...),
				},
			}
		}
		incomingSourceSignalCode := model.SafeSourceSignalCode(finding.RuleID)
		if !sameOptionalString(current.SourceSignalCode, incomingSourceSignalCode) {
			current.SourceSignalCode = nil
		}
		if finding.DetectedAt.Before(current.FirstObservedAt) {
			current.FirstObservedAt = finding.DetectedAt
		}
		if finding.DetectedAt.After(current.LastObservedAt) {
			current.LastObservedAt = finding.DetectedAt
		}
		if finding.FindingID < current.OriginRecordID {
			current.OriginRecordID = finding.FindingID
		}
		current.Severity = higherSeverity(current.Severity, finding.Severity)
		current.Confidence = lowerConfidence(current.Confidence, finding.Confidence)
		current.Evidence.CitedEventIDs = append(
			current.Evidence.CitedEventIDs,
			citedEventIDs...,
		)
		current.Evidence.CitedEventIDs = compactSorted(current.Evidence.CitedEventIDs)
		if len(current.Evidence.CitedEventIDs) > detection.MaxCitations {
			current.Evidence.CitedEventIDs =
				current.Evidence.CitedEventIDs[:detection.MaxCitations]
			current.EvidenceComplete = false
		}
		grouped[fingerprintID] = current
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]model.IssueOccurrence, 0, len(keys))
	for _, key := range keys {
		result = append(result, grouped[key])
	}
	return result, truncated, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (r *Reconciler) analysisCapabilities(
	scope local.SessionScope,
	result detection.CatalogResult,
	numbat []model.IssueOccurrence,
	analysisThrough *int64,
	status model.AnalysisStatus,
) []model.AnalysisCapability {
	if status != model.AnalysisCurrent {
		return nil
	}
	scopeID := exactFingerprintScope(scope)
	capabilities := make(
		[]model.AnalysisCapability,
		0,
		len(result.Applicability)+len(numbat),
	)
	if scopeID != "" {
		for _, applicability := range result.Applicability {
			if applicability.AbsenceCapability != detection.AbsenceSupported {
				continue
			}
			capabilities = append(capabilities, model.AnalysisCapability{
				SessionID:              scope.SessionKey,
				FingerprintScopeID:     scopeID,
				Origin:                 "belay",
				DetectorID:             applicability.DetectorID,
				DetectorVersion:        applicability.DetectorVersion,
				FingerprintVersion:     applicability.FingerprintVersion,
				NegativeComparisonMode: model.FixNegativeComparisonSupported,
				AnalysisThroughOrderNS: analysisThrough,
			})
		}
	}
	seen := make(map[string]struct{})
	for _, occurrence := range numbat {
		if occurrence.FingerprintScopeID == "" {
			continue
		}
		key := occurrence.FingerprintScopeID + "\x00" +
			occurrence.Provenance.DetectorID + "\x00" +
			occurrence.FingerprintVersion
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		capabilities = append(capabilities, model.AnalysisCapability{
			SessionID:              scope.SessionKey,
			FingerprintScopeID:     occurrence.FingerprintScopeID,
			Origin:                 "numbat",
			DetectorID:             occurrence.Provenance.DetectorID,
			DetectorVersion:        occurrence.Provenance.DetectorVersion,
			FingerprintVersion:     occurrence.FingerprintVersion,
			NegativeComparisonMode: model.FixNegativeComparisonPositiveOnly,
			AnalysisThroughOrderNS: analysisThrough,
		})
	}
	return capabilities
}

func exactFingerprintScope(scope local.SessionScope) string {
	if (scope.Quality == model.ScopeResolved || scope.Quality == model.ScopeLexical) &&
		scope.ProjectScopeID != "" {
		return scope.ProjectScopeID
	}
	return ""
}

func detectionEnrichments(
	values map[string]model.EventEnrichment,
) map[string]detection.EventEnrichment {
	result := make(map[string]detection.EventEnrichment, len(values))
	for eventID, value := range values {
		result[eventID] = detection.EventEnrichment{
			CommandSignatureID: value.CommandSignatureID,
			CommandClass:       value.CommandClass,
			PermissionClass:    value.PermissionClass,
			Version:            value.Version,
		}
	}
	return result
}

func issueScopeIdentity(scope local.SessionScope) string {
	switch scope.Quality {
	case model.ScopeResolved, model.ScopeLexical:
		if scope.ProjectScopeID != "" {
			return scope.ProjectScopeID
		}
	}
	return scope.SessionKey
}

func sessionHarness(events []model.Event) string {
	for _, event := range events {
		if event.Source.Agent != "" {
			return safeHarness(event.Source.Agent)
		}
	}
	return "unknown"
}

func safeHarness(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func opaqueVersion(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "rule-" + hex.EncodeToString(sum[:6])
}

func compact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	output := values[:1]
	for _, value := range values[1:] {
		if value != output[len(output)-1] {
			output = append(output, value)
		}
	}
	return output
}

func compactSorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return compact(result)
}

func higherSeverity(left, right string) string {
	rank := map[string]int{"info": 1, "low": 2, "medium": 3, "high": 4, "critical": 5}
	if rank[right] > rank[left] {
		return right
	}
	return left
}

func lowerConfidence(left, right string) string {
	rank := map[string]int{"low": 1, "medium": 2, "high": 3}
	if left == "" || rank[right] < rank[left] {
		return right
	}
	return left
}
