package readmodel

import (
	"context"
	"sort"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	SessionDiagnosisProjectionVersion = "belay.session-diagnosis.v1"

	SessionDiagnosisReady              = "ready"
	SessionDiagnosisLimited            = "limited"
	SessionDiagnosisInsufficientDetail = "insufficient_detail"

	SessionDiagnosisReviewedFindings  = "reviewed_findings_available"
	SessionDiagnosisReportedFailure   = "reported_failure"
	SessionDiagnosisReportedInterrupt = "reported_interruption"
	SessionDiagnosisFailedActivity    = "failed_activity_observed"
	SessionDiagnosisNoReviewedAction  = "no_reviewed_action_available"
	SessionDiagnosisInsufficient      = "insufficient_detail"
)

const sessionDiagnosisTimeout = time.Second

type SessionDiagnosisSummary struct {
	State  string `json:"state"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type SessionDiagnosisCoverage struct {
	IssueSource              string                       `json:"issue_source"`
	EvaluatedIssueCount      int                          `json:"evaluated_issue_count"`
	IssueCandidatesTruncated bool                         `json:"issue_candidates_truncated"`
	Analysis                 *model.IssueAnalysisCoverage `json:"analysis"`
	Limitations              []DeveloperBriefLimitation   `json:"limitations"`
}

type SessionDiagnosis struct {
	ProjectionVersion string                     `json:"projection_version"`
	Status            string                     `json:"status"`
	Summary           SessionDiagnosisSummary    `json:"summary"`
	ActionCards       []DeveloperBriefActionCard `json:"action_cards"`
	Coverage          SessionDiagnosisCoverage   `json:"coverage"`
}

type SessionDetailWithDiagnosis struct {
	SchemaVersion string               `json:"schema_version"`
	Data          model.SessionSummary `json:"data"`
	Diagnosis     SessionDiagnosis     `json:"diagnosis"`
	DataThrough   time.Time            `json:"data_through"`
}

func (s *Service) DiagnoseSession(ctx context.Context, detail SessionDetail) SessionDiagnosis {
	diagnosisCtx, cancel := context.WithTimeout(ctx, sessionDiagnosisTimeout)
	defer cancel()
	list, err := s.ListIssues(diagnosisCtx, IssueListRequest{
		Limit:         20,
		SessionID:     detail.Data.SessionID,
		AttentionKind: model.AttentionKindAll,
		Experimental:  model.ExperimentalStable,
	})

	coverage := SessionDiagnosisCoverage{
		IssueSource: BriefSourceUnavailable,
		Limitations: make([]DeveloperBriefLimitation, 0, 3),
	}
	cards := make([]DeveloperBriefActionCard, 0, briefActionLimit)
	reviewedExplicitFailure := false
	reviewedCards := 0
	if err != nil {
		coverage.Limitations = append(
			coverage.Limitations,
			sessionDiagnosisLimitation("session_issue_analysis_unavailable"),
		)
	} else {
		coverage.IssueSource = BriefSourceComplete
		coverage.EvaluatedIssueCount = len(list.Data)
		coverage.IssueCandidatesTruncated = list.HasMore
		analysis := list.Analysis
		coverage.Analysis = &analysis
		if list.HasMore {
			coverage.IssueSource = BriefSourceTruncated
			coverage.Limitations = append(
				coverage.Limitations,
				sessionDiagnosisLimitation("session_issue_candidate_limit_reached"),
			)
		}
		if !list.Analysis.Complete {
			coverage.Limitations = append(
				coverage.Limitations,
				sessionDiagnosisLimitation("session_issue_analysis_incomplete"),
			)
		}
		issueCards, explicitFailure := sessionIssueCards(list, detail.Data.SessionID)
		reviewedExplicitFailure = explicitFailure
		reviewedCards = len(issueCards)
		cards = append(cards, issueCards...)
	}

	if detail.Data.Overview == nil {
		coverage.Limitations = append(
			coverage.Limitations,
			sessionDiagnosisLimitation("session_overview_unavailable"),
		)
	}
	if len(cards) < briefActionLimit &&
		(detail.Data.Outcome == "failed" || detail.Data.Outcome == "interrupted") {
		cards = append(cards, sessionOutcomeActionCard(detail.Data))
	}
	if len(cards) < briefActionLimit &&
		!reviewedExplicitFailure &&
		detail.Data.Overview != nil &&
		detail.Data.Overview.Counts.ExplicitFailedEvents > 0 {
		cards = append(cards, failedActivityActionCard(detail.Data))
	}
	if len(cards) > briefActionLimit {
		cards = cards[:briefActionLimit]
	}

	completeIssueRead := err == nil && !list.HasMore && list.Analysis.Complete
	status := SessionDiagnosisReady
	if len(coverage.Limitations) > 0 {
		status = SessionDiagnosisLimited
	}
	state := SessionDiagnosisNoReviewedAction
	switch {
	case reviewedCards > 0:
		state = SessionDiagnosisReviewedFindings
	case detail.Data.Outcome == "failed":
		state = SessionDiagnosisReportedFailure
	case detail.Data.Outcome == "interrupted":
		state = SessionDiagnosisReportedInterrupt
	case detail.Data.Overview != nil && detail.Data.Overview.Counts.ExplicitFailedEvents > 0:
		state = SessionDiagnosisFailedActivity
	case !completeIssueRead || detail.Data.Overview == nil:
		state = SessionDiagnosisInsufficient
		status = SessionDiagnosisInsufficientDetail
	}
	return SessionDiagnosis{
		ProjectionVersion: SessionDiagnosisProjectionVersion,
		Status:            status,
		Summary:           sessionDiagnosisSummary(state),
		ActionCards:       nonNil(cards),
		Coverage: SessionDiagnosisCoverage{
			IssueSource:              coverage.IssueSource,
			EvaluatedIssueCount:      coverage.EvaluatedIssueCount,
			IssueCandidatesTruncated: coverage.IssueCandidatesTruncated,
			Analysis:                 coverage.Analysis,
			Limitations:              nonNil(coverage.Limitations),
		},
	}
}

type sessionIssueCandidate struct {
	card         DeveloperBriefActionCard
	severityRank int
	kindOrder    int
	lastObserved time.Time
	issueID      string
	titleCode    string
}

func sessionIssueCards(list IssueList, sessionID string) ([]DeveloperBriefActionCard, bool) {
	candidates := make([]sessionIssueCandidate, 0, len(list.Data))
	seen := make(map[string]struct{}, len(list.Data))
	for _, issue := range list.Data {
		rank := severityRank(issue.Severity)
		if _, exists := seen[issue.IssueID]; exists ||
			!issueIDPattern.MatchString(issue.IssueID) ||
			issue.Experimental ||
			issue.AnalysisStatus != model.AnalysisCurrent ||
			rank == 0 ||
			issue.LastObservedAt.IsZero() {
			continue
		}
		catalog := issueCatalog(issue)
		_, ok := briefActionLabel(catalog.NextEvidenceAction)
		if catalog.CatalogStatus != "known" || !ok ||
			!completeCatalogText(catalog.DisplayTitle, catalog.ObservationStatement, catalog.Caveat) {
			continue
		}
		kind := BriefActionReviewedFinding
		kindOrder := 0
		if issue.Category == model.AttentionKindEvidenceGap {
			kind = BriefActionEvidenceGap
			kindOrder = 1
		}
		seen[issue.IssueID] = struct{}{}
		candidates = append(candidates, sessionIssueCandidate{
			card: DeveloperBriefActionCard{
				CardID:      "issue:" + issue.IssueID,
				Kind:        kind,
				Title:       catalog.DisplayTitle,
				Observation: catalog.ObservationStatement,
				Limitation:  catalog.Caveat,
				NextStep: DeveloperBriefNextStep{
					Kind:      BriefTargetSession,
					Label:     "Review session activity",
					SessionID: stringPointer(sessionID),
				},
				Evidence: DeveloperBriefEvidence{
					LastObservedAt: issue.LastObservedAt.UTC(),
					Harnesses:      []string{},
				},
			},
			severityRank: rank,
			kindOrder:    kindOrder,
			lastObserved: issue.LastObservedAt,
			issueID:      issue.IssueID,
			titleCode:    issue.TitleCode,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].severityRank != candidates[j].severityRank {
			return candidates[i].severityRank > candidates[j].severityRank
		}
		if candidates[i].kindOrder != candidates[j].kindOrder {
			return candidates[i].kindOrder < candidates[j].kindOrder
		}
		if !candidates[i].lastObserved.Equal(candidates[j].lastObserved) {
			return candidates[i].lastObserved.After(candidates[j].lastObserved)
		}
		return candidates[i].issueID < candidates[j].issueID
	})
	limit := len(candidates)
	if limit > briefActionLimit {
		limit = briefActionLimit
	}
	result := make([]DeveloperBriefActionCard, 0, limit)
	explicitFailure := false
	for _, candidate := range candidates[:limit] {
		result = append(result, candidate.card)
		explicitFailure = explicitFailure ||
			candidate.titleCode == "issue.explicit_command_failure"
	}
	return result, explicitFailure
}

func failedActivityActionCard(session model.SessionSummary) DeveloperBriefActionCard {
	eventCount := 0
	if session.Overview != nil {
		eventCount = session.Overview.Counts.ExplicitFailedEvents
	}
	return DeveloperBriefActionCard{
		CardID:      "session:" + session.SessionID + ":failed-activity",
		Kind:        BriefActionFailedActivity,
		Title:       "Failed activity was recorded",
		Observation: "One or more stored events explicitly reported failure.",
		Limitation:  "This does not identify which activity failed, why it failed, or whether a later attempt succeeded.",
		NextStep: DeveloperBriefNextStep{
			Kind:      BriefTargetSession,
			Label:     "Review session",
			SessionID: stringPointer(session.SessionID),
		},
		Evidence: DeveloperBriefEvidence{
			LastObservedAt: session.EndedAt.UTC(),
			EventCount:     &eventCount,
			Harnesses:      []string{normalizedHarness(session.Harness)},
		},
	}
}

func sessionDiagnosisSummary(state string) SessionDiagnosisSummary {
	switch state {
	case SessionDiagnosisReviewedFindings:
		return SessionDiagnosisSummary{
			State:  state,
			Title:  "This session has findings worth reviewing",
			Detail: "Belay found reviewed deterministic evidence linked to this session.",
		}
	case SessionDiagnosisReportedFailure:
		return SessionDiagnosisSummary{
			State:  state,
			Title:  "The agent reported that this session failed",
			Detail: "Review the session activity and any cited findings for recorded failure evidence.",
		}
	case SessionDiagnosisReportedInterrupt:
		return SessionDiagnosisSummary{
			State:  state,
			Title:  "The agent reported that this session was interrupted",
			Detail: "Review the last recorded activity to understand where the record ended.",
		}
	case SessionDiagnosisFailedActivity:
		return SessionDiagnosisSummary{
			State:  state,
			Title:  "Failed activity was recorded",
			Detail: "One or more stored events explicitly reported failure.",
		}
	case SessionDiagnosisNoReviewedAction:
		return SessionDiagnosisSummary{
			State:  state,
			Title:  "No reviewed action was identified",
			Detail: "Belay did not find a reviewed actionable item in the bounded analysis available for this session.",
		}
	default:
		return SessionDiagnosisSummary{
			State:  SessionDiagnosisInsufficient,
			Title:  "Belay has limited detail for this session",
			Detail: "Available metadata is not sufficient to provide a useful deterministic diagnosis.",
		}
	}
}

func sessionDiagnosisLimitation(code string) DeveloperBriefLimitation {
	messages := map[string]string{
		"session_issue_analysis_unavailable":    "Reviewed issue analysis could not be loaded for this session.",
		"session_issue_candidate_limit_reached": "More session findings exist than this diagnosis evaluated.",
		"session_issue_analysis_incomplete":     "Issue analysis is incomplete for stored sessions.",
		"session_overview_unavailable":          "The metadata overview is unavailable for this session.",
	}
	return DeveloperBriefLimitation{Code: code, Message: messages[code]}
}
