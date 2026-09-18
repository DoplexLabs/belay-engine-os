package readmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	DeveloperBriefProjectionVersion = "belay.developer-brief.v1"

	BriefStatusReady   = "ready"
	BriefStatusLimited = "limited"

	BriefActionReviewedFinding = "reviewed_finding"
	BriefActionEvidenceGap     = "evidence_gap"
	BriefActionSessionOutcome  = "session_outcome"
	BriefActionFailedActivity  = "failed_activity"

	BriefTargetAttentionFamily = "open_attention_family"
	BriefTargetIssue           = "open_issue"
	BriefTargetSession         = "open_session"

	BriefSourceSessions         = "sessions"
	BriefSourceReviewedFindings = "reviewed_findings"
	BriefSourceEvidenceGaps     = "evidence_gaps"

	BriefSourceComplete    = "complete"
	BriefSourceTruncated   = "truncated"
	BriefSourceUnavailable = "unavailable"
)

var ErrDeveloperBriefUnavailable = errors.New("developer brief unavailable")

const (
	briefSessionLimit    = 100
	briefFamilyLimit     = 20
	briefEvidenceLimit   = 20
	briefActionLimit     = 5
	briefRecentWorkLimit = 8
	briefHarnessLimit    = 20
	briefSourceTimeout   = 2 * time.Second
)

type DeveloperBriefWindow struct {
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	DurationHours int       `json:"duration_hours"`
}

type DeveloperBriefOutcomeCounts struct {
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Interrupted int `json:"interrupted"`
	Incomplete  int `json:"incomplete"`
	Unknown     int `json:"unknown"`
}

type DeveloperBriefHistoryCounts struct {
	Live       int `json:"live"`
	Historical int `json:"historical"`
	Mixed      int `json:"mixed"`
}

type DeveloperBriefRecentSummary struct {
	EvaluatedSessionCount    int                         `json:"evaluated_session_count"`
	SessionCountIsLowerBound bool                        `json:"session_count_is_lower_bound"`
	Harnesses                []string                    `json:"harnesses"`
	Outcomes                 DeveloperBriefOutcomeCounts `json:"outcomes"`
	History                  DeveloperBriefHistoryCounts `json:"history"`
	LatestObservedAt         *time.Time                  `json:"latest_observed_at"`
}

type DeveloperBriefNextStep struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	FamilyID   *string `json:"family_id"`
	IssueID    *string `json:"issue_id"`
	SessionID  *string `json:"session_id"`
	ViewCursor *string `json:"view_cursor"`
}

type DeveloperBriefEvidence struct {
	LastObservedAt  time.Time `json:"last_observed_at"`
	SessionCount    *int      `json:"session_count"`
	OccurrenceCount *int      `json:"occurrence_count"`
	Harnesses       []string  `json:"harnesses"`
	SessionOutcome  *string   `json:"session_outcome"`
	EventCount      *int      `json:"event_count"`
}

type DeveloperBriefActionCard struct {
	CardID      string                 `json:"card_id"`
	Kind        string                 `json:"kind"`
	Title       string                 `json:"title"`
	Observation string                 `json:"observation"`
	Limitation  string                 `json:"limitation"`
	NextStep    DeveloperBriefNextStep `json:"next_step"`
	Evidence    DeveloperBriefEvidence `json:"evidence"`
}

type DeveloperBriefRecentWork struct {
	SessionID          string                 `json:"session_id"`
	Harness            string                 `json:"harness"`
	StartedAt          time.Time              `json:"started_at"`
	EndedAt            time.Time              `json:"ended_at"`
	EventCount         int                    `json:"event_count"`
	Outcome            string                 `json:"outcome"`
	History            string                 `json:"history"`
	OutcomeExplanation string                 `json:"outcome_explanation"`
	NextStep           DeveloperBriefNextStep `json:"next_step"`
}

type DeveloperBriefSourceCoverage struct {
	Source         string     `json:"source"`
	Status         string     `json:"status"`
	EvaluatedCount int        `json:"evaluated_count"`
	HasMore        bool       `json:"has_more"`
	AsOf           *time.Time `json:"as_of"`
}

type DeveloperBriefLimitation struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DeveloperBriefCoverage struct {
	Complete      bool                           `json:"complete"`
	Sources       []DeveloperBriefSourceCoverage `json:"sources"`
	IssueAnalysis *model.IssueAnalysisCoverage   `json:"issue_analysis"`
	Limitations   []DeveloperBriefLimitation     `json:"limitations"`
}

type DeveloperBrief struct {
	SchemaVersion     string                      `json:"schema_version"`
	ProjectionVersion string                      `json:"projection_version"`
	GeneratedAt       time.Time                   `json:"generated_at"`
	Window            DeveloperBriefWindow        `json:"window"`
	Status            string                      `json:"status"`
	RecentSummary     DeveloperBriefRecentSummary `json:"recent_summary"`
	ActionCards       []DeveloperBriefActionCard  `json:"action_cards"`
	RecentWork        []DeveloperBriefRecentWork  `json:"recent_work"`
	Coverage          DeveloperBriefCoverage      `json:"coverage"`
}

type briefCandidate struct {
	card            DeveloperBriefActionCard
	rank            int
	sessionCount    int
	occurrenceCount int
	lastObserved    time.Time
	kindOrder       int
	stableID        string
}

type briefSourceResult[T any] struct {
	value T
	err   error
}

func (s *Service) GetDeveloperBrief(ctx context.Context) (DeveloperBrief, error) {
	generatedAt := s.nowUTC()
	windowStart := generatedAt.Add(-24 * time.Hour)
	sourceCtx, cancel := context.WithTimeout(ctx, briefSourceTimeout)
	defer cancel()

	sessionResults := make(chan briefSourceResult[SessionList], 1)
	familyResults := make(chan briefSourceResult[AttentionFamilyList], 1)
	issueResults := make(chan briefSourceResult[IssueList], 1)
	go func() {
		value, err := s.ListSessionsPage(sourceCtx, SessionListRequest{
			Limit:          briefSessionLimit,
			OccurredAfter:  &windowStart,
			OccurredBefore: &generatedAt,
		})
		sessionResults <- briefSourceResult[SessionList]{value: value, err: err}
	}()
	go func() {
		value, err := s.ListAttentionFamilies(sourceCtx, AttentionFamilyListRequest{
			Limit:         briefFamilyLimit,
			ObservedAfter: &windowStart,
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		})
		familyResults <- briefSourceResult[AttentionFamilyList]{value: value, err: err}
	}()
	go func() {
		value, err := s.ListIssues(sourceCtx, IssueListRequest{
			Limit:         briefEvidenceLimit,
			ObservedAfter: &windowStart,
			AttentionKind: model.AttentionKindEvidenceGap,
			Experimental:  model.ExperimentalStable,
		})
		issueResults <- briefSourceResult[IssueList]{value: value, err: err}
	}()

	sessions := <-sessionResults
	families := <-familyResults
	issues := <-issueResults
	if sessions.err != nil && families.err != nil && issues.err != nil {
		return DeveloperBrief{}, ErrDeveloperBriefUnavailable
	}

	validSessions := briefSessionsInWindow(sessions.value.Data, windowStart, generatedAt)
	candidates := make([]briefCandidate, 0, len(validSessions)+len(families.value.Data)+len(issues.value.Data))
	if families.err == nil {
		candidates = append(candidates, briefFamilyCandidates(families.value.Data, windowStart, generatedAt)...)
	}
	if issues.err == nil {
		candidates = append(candidates, briefIssueCandidates(issues.value, windowStart, generatedAt)...)
	}
	if sessions.err == nil {
		candidates = append(candidates, briefSessionOutcomeCandidates(validSessions)...)
	}

	coverage := buildDeveloperBriefCoverage(sessions, families, issues)
	status := BriefStatusReady
	if !coverage.Complete {
		status = BriefStatusLimited
	}
	return DeveloperBrief{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: DeveloperBriefProjectionVersion,
		GeneratedAt:       generatedAt,
		Window: DeveloperBriefWindow{
			StartedAt:     windowStart,
			EndedAt:       generatedAt,
			DurationHours: 24,
		},
		Status:        status,
		RecentSummary: buildDeveloperBriefRecentSummary(validSessions, sessions.err == nil && sessions.value.HasMore),
		ActionCards:   rankBriefCandidates(candidates),
		RecentWork:    buildDeveloperBriefRecentWork(validSessions),
		Coverage:      coverage,
	}, nil
}

func briefSessionsInWindow(values []model.SessionSummary, start, end time.Time) []model.SessionSummary {
	result := make([]model.SessionSummary, 0, len(values))
	for _, value := range values {
		if !validBriefOutcome(value.Outcome) ||
			!validBriefHistory(value.History) ||
			value.SessionID == "" || len(value.SessionID) > 256 ||
			strings.TrimSpace(value.Harness) == "" ||
			value.EventCount < 0 ||
			value.StartedAt.IsZero() || value.EndedAt.IsZero() ||
			value.EndedAt.Before(value.StartedAt) ||
			value.EndedAt.Before(start) || value.StartedAt.After(end) ||
			value.EndedAt.After(end) {
			continue
		}
		result = append(result, value)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if !result[i].EndedAt.Equal(result[j].EndedAt) {
			return result[i].EndedAt.After(result[j].EndedAt)
		}
		return result[i].SessionID < result[j].SessionID
	})
	return result
}

func briefFamilyCandidates(values []AttentionFamilySummary, start, end time.Time) []briefCandidate {
	result := make([]briefCandidate, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		rank := briefFindingRank(value.Severity)
		if _, exists := seen[value.FamilyID]; exists ||
			value.Experimental ||
			value.AnalysisStatus != model.AnalysisCurrent ||
			value.CatalogStatus != "known" ||
			rank == 0 ||
			value.LastObservedAt.Before(start) || value.LastObservedAt.After(end) ||
			value.SessionCount < 1 || value.OccurrenceCount < 1 ||
			!attentionFamilyIDPattern.MatchString(value.FamilyID) ||
			!issueIDPattern.MatchString(value.RepresentativeIssueID) ||
			strings.TrimSpace(value.ViewCursor) == "" {
			continue
		}
		label, ok := briefActionLabel(value.Catalog.NextEvidenceAction)
		if !ok || !completeCatalogText(
			value.Catalog.DisplayTitle,
			value.Catalog.ObservationStatement,
			value.Catalog.Caveat,
		) {
			continue
		}
		next := DeveloperBriefNextStep{
			Label:      label,
			IssueID:    stringPointer(value.RepresentativeIssueID),
			ViewCursor: stringPointer(value.ViewCursor),
		}
		switch value.Kind {
		case model.AttentionFamilyKindMappedUpstream:
			next.Kind = BriefTargetAttentionFamily
			next.FamilyID = stringPointer(value.FamilyID)
		case model.AttentionFamilyKindExactIssue:
			next.Kind = BriefTargetIssue
		default:
			continue
		}
		seen[value.FamilyID] = struct{}{}
		sessionCount, occurrenceCount := value.SessionCount, value.OccurrenceCount
		result = append(result, briefCandidate{
			card: DeveloperBriefActionCard{
				CardID:      "family:" + value.FamilyID,
				Kind:        BriefActionReviewedFinding,
				Title:       value.Catalog.DisplayTitle,
				Observation: value.Catalog.ObservationStatement,
				Limitation:  value.Catalog.Caveat,
				NextStep:    next,
				Evidence: DeveloperBriefEvidence{
					LastObservedAt:  value.LastObservedAt.UTC(),
					SessionCount:    &sessionCount,
					OccurrenceCount: &occurrenceCount,
					Harnesses:       normalizedHarnesses(value.Harnesses),
				},
			},
			rank:            rank,
			sessionCount:    value.SessionCount,
			occurrenceCount: value.OccurrenceCount,
			lastObserved:    value.LastObservedAt,
			kindOrder:       0,
			stableID:        value.FamilyID,
		})
	}
	return result
}

func briefIssueCandidates(list IssueList, start, end time.Time) []briefCandidate {
	result := make([]briefCandidate, 0, len(list.Data))
	seen := make(map[string]struct{}, len(list.Data))
	for _, value := range list.Data {
		catalog := issueCatalog(value)
		if _, exists := seen[value.IssueID]; exists ||
			value.Experimental ||
			value.AnalysisStatus != model.AnalysisCurrent ||
			value.Category != model.AttentionKindEvidenceGap ||
			catalog.CatalogStatus != "known" ||
			value.SessionCount < 1 || value.OccurrenceCount < 1 ||
			value.LastObservedAt.Before(start) || value.LastObservedAt.After(end) ||
			!issueIDPattern.MatchString(value.IssueID) ||
			strings.TrimSpace(list.ViewCursor) == "" {
			continue
		}
		label, ok := briefActionLabel(catalog.NextEvidenceAction)
		if !ok || !completeCatalogText(catalog.DisplayTitle, catalog.ObservationStatement, catalog.Caveat) {
			continue
		}
		seen[value.IssueID] = struct{}{}
		sessionCount, occurrenceCount := value.SessionCount, value.OccurrenceCount
		result = append(result, briefCandidate{
			card: DeveloperBriefActionCard{
				CardID:      "issue:" + value.IssueID,
				Kind:        BriefActionEvidenceGap,
				Title:       catalog.DisplayTitle,
				Observation: catalog.ObservationStatement,
				Limitation:  catalog.Caveat,
				NextStep: DeveloperBriefNextStep{
					Kind:       BriefTargetIssue,
					Label:      label,
					IssueID:    stringPointer(value.IssueID),
					ViewCursor: stringPointer(list.ViewCursor),
				},
				Evidence: DeveloperBriefEvidence{
					LastObservedAt:  value.LastObservedAt.UTC(),
					SessionCount:    &sessionCount,
					OccurrenceCount: &occurrenceCount,
					Harnesses:       normalizedHarnesses(value.Harnesses),
				},
			},
			rank:            300,
			sessionCount:    value.SessionCount,
			occurrenceCount: value.OccurrenceCount,
			lastObserved:    value.LastObservedAt,
			kindOrder:       1,
			stableID:        value.IssueID,
		})
	}
	return result
}

func briefSessionOutcomeCandidates(values []model.SessionSummary) []briefCandidate {
	result := make([]briefCandidate, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		outcome := strings.ToLower(strings.TrimSpace(value.Outcome))
		if outcome != "failed" && outcome != "interrupted" {
			continue
		}
		if _, exists := seen[value.SessionID]; exists {
			continue
		}
		seen[value.SessionID] = struct{}{}
		eventCount := value.EventCount
		result = append(result, briefCandidate{
			card: sessionOutcomeActionCard(value),
			rank: func() int {
				if outcome == "failed" {
					return 600
				}
				return 450
			}(),
			sessionCount:    1,
			occurrenceCount: 1,
			lastObserved:    value.EndedAt,
			kindOrder:       2,
			stableID:        value.SessionID,
		})
		result[len(result)-1].card.Evidence.EventCount = &eventCount
	}
	return result
}

func rankBriefCandidates(values []briefCandidate) []DeveloperBriefActionCard {
	values = consolidateBriefCandidates(values)
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].rank != values[j].rank {
			return values[i].rank > values[j].rank
		}
		if values[i].sessionCount != values[j].sessionCount {
			return values[i].sessionCount > values[j].sessionCount
		}
		if values[i].occurrenceCount != values[j].occurrenceCount {
			return values[i].occurrenceCount > values[j].occurrenceCount
		}
		if !values[i].lastObserved.Equal(values[j].lastObserved) {
			return values[i].lastObserved.After(values[j].lastObserved)
		}
		if values[i].kindOrder != values[j].kindOrder {
			return values[i].kindOrder < values[j].kindOrder
		}
		return values[i].stableID < values[j].stableID
	})
	result := make([]DeveloperBriefActionCard, 0, briefActionLimit)
	outcomes := 0
	for _, value := range values {
		if len(result) == briefActionLimit {
			break
		}
		if value.card.Kind == BriefActionSessionOutcome {
			if outcomes == 2 {
				continue
			}
			outcomes++
		}
		result = append(result, value.card)
	}
	return nonNil(result)
}

func consolidateBriefCandidates(values []briefCandidate) []briefCandidate {
	result := make([]briefCandidate, 0, len(values))
	semanticPositions := make(map[string]int, len(values))
	for _, value := range values {
		semanticKey, ok := briefCandidateSemanticKey(value.card)
		if !ok {
			result = append(result, value)
			continue
		}
		if position, exists := semanticPositions[semanticKey]; exists {
			result[position] = mergeBriefCandidates(result[position], value, semanticKey)
			continue
		}
		value = initializeConsolidatedBriefCandidate(value, semanticKey)
		semanticPositions[semanticKey] = len(result)
		result = append(result, value)
	}
	return result
}

func briefCandidateSemanticKey(card DeveloperBriefActionCard) (string, bool) {
	switch card.Kind {
	case BriefActionReviewedFinding, BriefActionEvidenceGap:
	default:
		return "", false
	}
	parts := []string{
		card.Kind,
		card.Title,
		card.Observation,
		card.Limitation,
		card.NextStep.Kind,
		card.NextStep.Label,
	}
	if !completeCatalogText(parts...) {
		return "", false
	}
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(len(part)))
		key.WriteByte(':')
		key.WriteString(part)
		key.WriteByte('|')
	}
	return key.String(), true
}

func initializeConsolidatedBriefCandidate(
	value briefCandidate,
	semanticKey string,
) briefCandidate {
	stableID := briefSemanticCardID(semanticKey)
	value.card.CardID = stableID
	value.stableID = stableID
	return value
}

func mergeBriefCandidates(
	current briefCandidate,
	incoming briefCandidate,
	semanticKey string,
) briefCandidate {
	representative := current
	if incoming.lastObserved.After(current.lastObserved) ||
		(incoming.lastObserved.Equal(current.lastObserved) &&
			briefNextStepStableKey(incoming.card.NextStep) <
				briefNextStepStableKey(current.card.NextStep)) {
		representative = incoming
	}
	current.card.NextStep = representative.card.NextStep
	if incoming.rank > current.rank {
		current.rank = incoming.rank
	}
	if incoming.kindOrder < current.kindOrder {
		current.kindOrder = incoming.kindOrder
	}
	current.sessionCount = addBriefCount(current.sessionCount, incoming.sessionCount)
	current.occurrenceCount = addBriefCount(current.occurrenceCount, incoming.occurrenceCount)
	if incoming.lastObserved.After(current.lastObserved) {
		current.lastObserved = incoming.lastObserved
	}
	current.card.Evidence.LastObservedAt = current.lastObserved.UTC()
	current.card.Evidence.SessionCount = intPointer(current.sessionCount)
	current.card.Evidence.OccurrenceCount = intPointer(current.occurrenceCount)
	current.card.Evidence.Harnesses = normalizedHarnesses(append(
		append([]string(nil), current.card.Evidence.Harnesses...),
		incoming.card.Evidence.Harnesses...,
	))
	stableID := briefSemanticCardID(semanticKey)
	current.card.CardID = stableID
	current.stableID = stableID
	return current
}

func briefNextStepStableKey(value DeveloperBriefNextStep) string {
	parts := []string{value.Kind, value.Label}
	for _, candidate := range []*string{
		value.FamilyID,
		value.IssueID,
		value.SessionID,
		value.ViewCursor,
	} {
		if candidate == nil {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, *candidate)
	}
	return strings.Join(parts, "\x00")
}

func briefSemanticCardID(semanticKey string) string {
	digest := sha256.Sum256([]byte(semanticKey))
	return "signal:" + hex.EncodeToString(digest[:16])
}

func addBriefCount(first, second int) int {
	const maxInt = int(^uint(0) >> 1)
	if first < 0 || second < 0 {
		return 0
	}
	if second > maxInt-first {
		return maxInt
	}
	return first + second
}

func buildDeveloperBriefRecentSummary(
	values []model.SessionSummary,
	lowerBound bool,
) DeveloperBriefRecentSummary {
	result := DeveloperBriefRecentSummary{
		EvaluatedSessionCount:    len(values),
		SessionCountIsLowerBound: lowerBound,
		Harnesses:                normalizedSessionHarnesses(values),
	}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value.Outcome)) {
		case "succeeded":
			result.Outcomes.Succeeded++
		case "failed":
			result.Outcomes.Failed++
		case "interrupted":
			result.Outcomes.Interrupted++
		case "incomplete":
			result.Outcomes.Incomplete++
		default:
			result.Outcomes.Unknown++
		}
		switch strings.ToLower(strings.TrimSpace(value.History)) {
		case "live":
			result.History.Live++
		case "historical":
			result.History.Historical++
		default:
			result.History.Mixed++
		}
		if result.LatestObservedAt == nil || value.EndedAt.After(*result.LatestObservedAt) {
			at := value.EndedAt.UTC()
			result.LatestObservedAt = &at
		}
	}
	return result
}

func buildDeveloperBriefRecentWork(values []model.SessionSummary) []DeveloperBriefRecentWork {
	limit := len(values)
	if limit > briefRecentWorkLimit {
		limit = briefRecentWorkLimit
	}
	result := make([]DeveloperBriefRecentWork, 0, limit)
	for _, value := range values[:limit] {
		result = append(result, DeveloperBriefRecentWork{
			SessionID:          value.SessionID,
			Harness:            normalizedHarness(value.Harness),
			StartedAt:          value.StartedAt.UTC(),
			EndedAt:            value.EndedAt.UTC(),
			EventCount:         value.EventCount,
			Outcome:            normalizedOutcome(value.Outcome),
			History:            normalizedHistory(value.History),
			OutcomeExplanation: fixedOutcomeExplanation(value.Outcome),
			NextStep: DeveloperBriefNextStep{
				Kind:      BriefTargetSession,
				Label:     "Review session",
				SessionID: stringPointer(value.SessionID),
			},
		})
	}
	return nonNil(result)
}

func buildDeveloperBriefCoverage(
	sessions briefSourceResult[SessionList],
	families briefSourceResult[AttentionFamilyList],
	issues briefSourceResult[IssueList],
) DeveloperBriefCoverage {
	sources := []DeveloperBriefSourceCoverage{
		briefSessionSourceCoverage(sessions),
		briefFamilySourceCoverage(families),
		briefIssueSourceCoverage(issues),
	}
	limitations := make([]DeveloperBriefLimitation, 0, 7)
	if sessions.err != nil {
		limitations = append(limitations, briefLimitation("sessions_unavailable"))
	} else if sessions.value.HasMore {
		limitations = append(limitations, briefLimitation("session_candidate_limit_reached"))
	}
	if families.err != nil {
		limitations = append(limitations, briefLimitation("reviewed_findings_unavailable"))
	} else if families.value.HasMore {
		limitations = append(limitations, briefLimitation("finding_candidate_limit_reached"))
	}
	if issues.err != nil {
		limitations = append(limitations, briefLimitation("evidence_gaps_unavailable"))
	} else if issues.value.HasMore {
		limitations = append(limitations, briefLimitation("evidence_gap_candidate_limit_reached"))
	}
	analysis := selectBriefIssueAnalysis(families, issues)
	if analysis != nil && !analysis.Complete {
		limitations = append(limitations, briefLimitation("issue_analysis_incomplete"))
	}
	complete := analysis != nil && analysis.Complete
	for _, source := range sources {
		complete = complete && source.Status == BriefSourceComplete
	}
	return DeveloperBriefCoverage{
		Complete:      complete,
		Sources:       sources,
		IssueAnalysis: analysis,
		Limitations:   nonNil(limitations),
	}
}

func briefSessionSourceCoverage(result briefSourceResult[SessionList]) DeveloperBriefSourceCoverage {
	coverage := DeveloperBriefSourceCoverage{Source: BriefSourceSessions}
	if result.err != nil {
		coverage.Status = BriefSourceUnavailable
		return coverage
	}
	coverage.Status = BriefSourceComplete
	if result.value.HasMore {
		coverage.Status = BriefSourceTruncated
	}
	coverage.EvaluatedCount = len(result.value.Data)
	coverage.HasMore = result.value.HasMore
	coverage.AsOf = timePointerUTC(result.value.DataThrough)
	return coverage
}

func briefFamilySourceCoverage(result briefSourceResult[AttentionFamilyList]) DeveloperBriefSourceCoverage {
	coverage := DeveloperBriefSourceCoverage{Source: BriefSourceReviewedFindings}
	if result.err != nil {
		coverage.Status = BriefSourceUnavailable
		return coverage
	}
	coverage.Status = BriefSourceComplete
	if result.value.HasMore {
		coverage.Status = BriefSourceTruncated
	}
	coverage.EvaluatedCount = len(result.value.Data)
	coverage.HasMore = result.value.HasMore
	coverage.AsOf = timePointerUTC(result.value.GlobalAnalysisCoverage.AnalysisThrough)
	return coverage
}

func briefIssueSourceCoverage(result briefSourceResult[IssueList]) DeveloperBriefSourceCoverage {
	coverage := DeveloperBriefSourceCoverage{Source: BriefSourceEvidenceGaps}
	if result.err != nil {
		coverage.Status = BriefSourceUnavailable
		return coverage
	}
	coverage.Status = BriefSourceComplete
	if result.value.HasMore {
		coverage.Status = BriefSourceTruncated
	}
	coverage.EvaluatedCount = len(result.value.Data)
	coverage.HasMore = result.value.HasMore
	coverage.AsOf = timePointerUTC(result.value.Analysis.AnalysisThrough)
	return coverage
}

func selectBriefIssueAnalysis(
	families briefSourceResult[AttentionFamilyList],
	issues briefSourceResult[IssueList],
) *model.IssueAnalysisCoverage {
	if families.err == nil && issues.err == nil {
		value := conservativeIssueAnalysis(
			families.value.GlobalAnalysisCoverage,
			issues.value.Analysis,
		)
		return &value
	}
	if families.err == nil {
		value := families.value.GlobalAnalysisCoverage
		return &value
	}
	if issues.err == nil {
		value := issues.value.Analysis
		return &value
	}
	return nil
}

func conservativeIssueAnalysis(
	first model.IssueAnalysisCoverage,
	second model.IssueAnalysisCoverage,
) model.IssueAnalysisCoverage {
	return model.IssueAnalysisCoverage{
		CurrentSessions:   min(first.CurrentSessions, second.CurrentSessions),
		PendingSessions:   max(first.PendingSessions, second.PendingSessions),
		FailedSessions:    max(first.FailedSessions, second.FailedSessions),
		TruncatedSessions: max(first.TruncatedSessions, second.TruncatedSessions),
		UnscopedSessions:  max(first.UnscopedSessions, second.UnscopedSessions),
		AnalysisThrough:   earliestNonZeroTime(first.AnalysisThrough, second.AnalysisThrough),
		Complete:          first.Complete && second.Complete,
	}
}

func earliestNonZeroTime(first, second time.Time) time.Time {
	if first.IsZero() {
		return second
	}
	if second.IsZero() || first.Before(second) {
		return first
	}
	return second
}

func briefLimitation(code string) DeveloperBriefLimitation {
	messages := map[string]string{
		"sessions_unavailable":                 "Recent sessions could not be loaded.",
		"reviewed_findings_unavailable":        "Reviewed findings could not be loaded.",
		"evidence_gaps_unavailable":            "Verification evidence gaps could not be loaded.",
		"session_candidate_limit_reached":      "More recent sessions exist than this brief evaluated.",
		"finding_candidate_limit_reached":      "More reviewed findings exist than this brief evaluated.",
		"evidence_gap_candidate_limit_reached": "More evidence gaps exist than this brief evaluated.",
		"issue_analysis_incomplete":            "Some stored sessions are still pending, failed, or only partially analyzed.",
	}
	return DeveloperBriefLimitation{Code: code, Message: messages[code]}
}

func briefFindingRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 700
	case "high":
		return 650
	case "medium":
		return 500
	case "low":
		return 400
	case "info":
		return 200
	default:
		return 0
	}
}

func briefActionLabel(action string) (string, bool) {
	switch action {
	case "inspect_cited_events":
		return "Review cited evidence", true
	case "inspect_matching_sessions":
		return "Review matching sessions", true
	case "inspect_verification_events":
		return "Review verification evidence", true
	case "review_agent_permissions":
		return "Review permission mode", true
	default:
		return "", false
	}
}

func completeCatalogText(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func sessionOutcomeActionCard(value model.SessionSummary) DeveloperBriefActionCard {
	outcome := normalizedOutcome(value.Outcome)
	title := "Session outcome needs review"
	limitation := "This does not identify why the session ended this way or whether later work succeeded."
	if outcome == "failed" {
		title = "Session reported as failed"
	} else if outcome == "interrupted" {
		title = "Session reported as interrupted"
	}
	sessionCount, occurrenceCount := 1, 1
	return DeveloperBriefActionCard{
		CardID:      "session:" + value.SessionID + ":outcome",
		Kind:        BriefActionSessionOutcome,
		Title:       title,
		Observation: fixedOutcomeExplanation(outcome),
		Limitation:  limitation,
		NextStep: DeveloperBriefNextStep{
			Kind:      BriefTargetSession,
			Label:     "Review session",
			SessionID: stringPointer(value.SessionID),
		},
		Evidence: DeveloperBriefEvidence{
			LastObservedAt:  value.EndedAt.UTC(),
			SessionCount:    &sessionCount,
			OccurrenceCount: &occurrenceCount,
			Harnesses:       []string{normalizedHarness(value.Harness)},
			SessionOutcome:  stringPointer(outcome),
		},
	}
}

func fixedOutcomeExplanation(value string) string {
	switch normalizedOutcome(value) {
	case "succeeded":
		return "The agent reported that this session completed successfully."
	case "failed":
		return "The agent reported that this session ended with a failure."
	case "interrupted":
		return "The agent reported that this session was interrupted."
	case "incomplete":
		return "The agent did not report how this session ended."
	default:
		return "The agent reported that the session ended but did not report an outcome."
	}
}

func normalizedOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "succeeded", "failed", "interrupted", "incomplete":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}

func normalizedHistory(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "live", "historical", "mixed":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "mixed"
	}
}

func validBriefOutcome(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "succeeded", "failed", "interrupted", "incomplete", "unknown":
		return true
	default:
		return false
	}
}

func validBriefHistory(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "live", "historical", "mixed":
		return true
	default:
		return false
	}
}

func normalizedHarness(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "unknown"
	}
	return value
}

func normalizedHarnesses(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[normalizedHarness(value)] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) > briefHarnessLimit {
		result = result[:briefHarnessLimit]
	}
	return nonNil(result)
}

func normalizedSessionHarnesses(values []model.SessionSummary) []string {
	harnesses := make([]string, 0, len(values))
	for _, value := range values {
		harnesses = append(harnesses, value.Harness)
	}
	return normalizedHarnesses(harnesses)
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func intPointer(value int) *int {
	return &value
}

func timePointerUTC(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}
