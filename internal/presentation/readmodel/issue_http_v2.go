package readmodel

import (
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const IssueHTTPProjectionVersion = "belay.issue.v2"

type IssueEvidenceV2 struct {
	CitedEventIDs []string `json:"cited_event_ids"`
}

type IssueOccurrenceV2 struct {
	OccurrenceID        string                   `json:"occurrence_id"`
	IssueID             string                   `json:"issue_id"`
	FingerprintID       string                   `json:"fingerprint_id"`
	FingerprintVersion  string                   `json:"fingerprint_version"`
	Origin              string                   `json:"origin"`
	SessionID           string                   `json:"session_id"`
	Harness             string                   `json:"harness"`
	Provenance          model.DetectorProvenance `json:"provenance"`
	Category            string                   `json:"category"`
	TitleCode           string                   `json:"title_code"`
	SourceSignalCode    *string                  `json:"source_signal_code"`
	Severity            string                   `json:"severity"`
	Confidence          string                   `json:"confidence"`
	ScopeQuality        model.ScopeQuality       `json:"scope_quality"`
	FirstObservedAt     time.Time                `json:"first_observed_at"`
	LastObservedAt      time.Time                `json:"last_observed_at"`
	EvidenceComplete    bool                     `json:"evidence_complete"`
	RetainedHistoryOnly bool                     `json:"retained_history_only"`
	Experimental        bool                     `json:"experimental"`
	AnalysisStatus      model.AnalysisStatus     `json:"analysis_status"`
	Evidence            IssueEvidenceV2          `json:"evidence"`
}

type IssueDetailDataV2 struct {
	Issue       model.IssueSummary  `json:"issue"`
	Occurrences []IssueOccurrenceV2 `json:"occurrences"`
}

type IssueDetailV2 struct {
	SchemaVersion          string                      `json:"schema_version"`
	ProjectionVersion      string                      `json:"projection_version"`
	Data                   IssueDetailDataV2           `json:"data"`
	Catalog                IssueCatalogMetadata        `json:"catalog"`
	GlobalAnalysisCoverage model.IssueAnalysisCoverage `json:"global_analysis_coverage"`
	ViewCursor             string                      `json:"view_cursor"`
	NextCursor             *string                     `json:"next_cursor"`
	HasMore                bool                        `json:"has_more"`
	ReturnedCount          int                         `json:"returned_count"`
	Limit                  int                         `json:"limit"`
}

func PresentIssueDetailV2(detail IssueDetail) IssueDetailV2 {
	occurrences := make([]IssueOccurrenceV2, 0, len(detail.Data.Occurrences))
	for _, occurrence := range detail.Data.Occurrences {
		var sourceSignalCode *string
		if occurrence.SourceSignalCode != nil {
			sourceSignalCode = model.SafeSourceSignalCode(*occurrence.SourceSignalCode)
		}
		occurrences = append(occurrences, IssueOccurrenceV2{
			OccurrenceID:        occurrence.OccurrenceID,
			IssueID:             occurrence.IssueID,
			FingerprintID:       occurrence.FingerprintID,
			FingerprintVersion:  occurrence.FingerprintVersion,
			Origin:              occurrence.Origin,
			SessionID:           occurrence.SessionID,
			Harness:             occurrence.Harness,
			Provenance:          occurrence.Provenance,
			Category:            occurrence.Category,
			TitleCode:           occurrence.TitleCode,
			SourceSignalCode:    sourceSignalCode,
			Severity:            occurrence.Severity,
			Confidence:          occurrence.Confidence,
			ScopeQuality:        occurrence.ScopeQuality,
			FirstObservedAt:     occurrence.FirstObservedAt,
			LastObservedAt:      occurrence.LastObservedAt,
			EvidenceComplete:    occurrence.EvidenceComplete,
			RetainedHistoryOnly: occurrence.RetainedHistoryOnly,
			Experimental:        occurrence.Experimental,
			AnalysisStatus:      occurrence.AnalysisStatus,
			Evidence: IssueEvidenceV2{
				CitedEventIDs: nonNil(append(
					[]string(nil),
					occurrence.Evidence.CitedEventIDs...,
				)),
			},
		})
	}
	return IssueDetailV2{
		SchemaVersion:     detail.SchemaVersion,
		ProjectionVersion: IssueHTTPProjectionVersion,
		Data: IssueDetailDataV2{
			Issue:       detail.Data.Issue,
			Occurrences: nonNil(occurrences),
		},
		Catalog:                detail.Catalog,
		GlobalAnalysisCoverage: detail.GlobalAnalysisCoverage,
		ViewCursor:             detail.ViewCursor,
		NextCursor:             detail.NextCursor,
		HasMore:                detail.HasMore,
		ReturnedCount:          detail.ReturnedCount,
		Limit:                  detail.Limit,
	}
}
