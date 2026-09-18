package readmodel

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

const AttentionFamilyProjectionVersion = "belay.attention-family.v1"

var attentionFamilyIDPattern = regexp.MustCompile(`^atf_[a-z2-7]{52}$`)

type AttentionFamilyListRequest struct {
	Limit          int
	Cursor         string
	Severity       string
	Harness        string
	Origin         string
	AnalysisStatus string
	ObservedAfter  *time.Time
	AttentionKind  string
	Experimental   string
}

type AttentionFamilyDetailRequest struct {
	FamilyID   string
	Limit      int
	Cursor     string
	ViewCursor string
}

type AttentionFamilySelection struct {
	AttentionKind        string `json:"attention_kind"`
	Experimental         string `json:"experimental"`
	IncludesEvidenceGaps bool   `json:"includes_evidence_gaps"`
	IncludesExperimental bool   `json:"includes_experimental"`
}

type AttentionFamilyCatalogMetadata struct {
	CatalogVersion       string `json:"catalog_version"`
	MappingKey           string `json:"mapping_key,omitempty"`
	MappingVersion       string `json:"mapping_version,omitempty"`
	GroupingVersion      string `json:"grouping_version"`
	DisplayTitle         string `json:"display_title"`
	ObservationStatement string `json:"observation_statement"`
	Caveat               string `json:"caveat"`
	NextEvidenceAction   string `json:"next_evidence_action"`
}

type AttentionFamilySummary struct {
	FamilyID              string                           `json:"family_id"`
	Kind                  string                           `json:"kind"`
	RepresentativeIssueID string                           `json:"representative_issue_id"`
	AttentionKind         string                           `json:"attention_kind"`
	Severity              string                           `json:"severity"`
	Confidence            string                           `json:"confidence"`
	FirstObservedAt       time.Time                        `json:"first_observed_at"`
	LastObservedAt        time.Time                        `json:"last_observed_at"`
	SupportingIssueCount  int                              `json:"supporting_issue_count"`
	OccurrenceCount       int                              `json:"occurrence_count"`
	SessionCount          int                              `json:"session_count"`
	Harnesses             []string                         `json:"harnesses"`
	Scope                 model.AttentionFamilyScopeCounts `json:"scope"`
	AnalysisStatus        model.AnalysisStatus             `json:"analysis_status"`
	EvidenceComplete      bool                             `json:"evidence_complete"`
	RetainedHistoryOnly   bool                             `json:"retained_history_only"`
	Experimental          bool                             `json:"experimental"`
	Catalog               AttentionFamilyCatalogMetadata   `json:"catalog"`
	ViewCursor            string                           `json:"view_cursor"`
	CatalogStatus         string                           `json:"-"`
}

type AttentionFamilyList struct {
	SchemaVersion          string                      `json:"schema_version"`
	ProjectionVersion      string                      `json:"projection_version"`
	Data                   []AttentionFamilySummary    `json:"data"`
	GlobalAnalysisCoverage model.IssueAnalysisCoverage `json:"global_analysis_coverage"`
	Selection              AttentionFamilySelection    `json:"selection"`
	NextCursor             *string                     `json:"next_cursor"`
	HasMore                bool                        `json:"has_more"`
	ReturnedCount          int                         `json:"returned_count"`
	Limit                  int                         `json:"limit"`
}

type AttentionFamilyMember struct {
	Issue               model.IssueSummary   `json:"issue"`
	Catalog             IssueCatalogMetadata `json:"catalog"`
	SessionID           string               `json:"session_id"`
	SessionSelection    string               `json:"session_selection"`
	SessionStartedAt    *time.Time           `json:"session_started_at"`
	SessionLastActiveAt *time.Time           `json:"session_last_active_at"`
	CitedEventCount     int                  `json:"cited_event_count"`
	EvidenceFirstAt     *time.Time           `json:"evidence_first_at"`
	EvidenceLastAt      *time.Time           `json:"evidence_last_at"`
	ViewCursor          string               `json:"view_cursor"`
}

type AttentionFamilyDetailData struct {
	Family  AttentionFamilySummary  `json:"family"`
	Members []AttentionFamilyMember `json:"members"`
}

type AttentionFamilyDetail struct {
	SchemaVersion          string                      `json:"schema_version"`
	ProjectionVersion      string                      `json:"projection_version"`
	Data                   AttentionFamilyDetailData   `json:"data"`
	GlobalAnalysisCoverage model.IssueAnalysisCoverage `json:"global_analysis_coverage"`
	ViewCursor             string                      `json:"view_cursor"`
	NextCursor             *string                     `json:"next_cursor"`
	HasMore                bool                        `json:"has_more"`
	ReturnedCount          int                         `json:"returned_count"`
	Limit                  int                         `json:"limit"`
}

func (s *Service) RequireAttentionFamilyCapabilities() error {
	if s == nil ||
		s.attentionFamilyRepository == nil ||
		s.issueCursorCodec == nil {
		return capabilityUnavailable()
	}
	return nil
}

func (s *Service) ListAttentionFamilies(
	ctx context.Context,
	request AttentionFamilyListRequest,
) (AttentionFamilyList, error) {
	if err := s.RequireAttentionFamilyCapabilities(); err != nil {
		return AttentionFamilyList{}, err
	}
	var cursor *attentionFamilyCursorEnvelope
	if strings.TrimSpace(request.Cursor) != "" {
		if request.Limit != 0 ||
			request.Severity != "" ||
			request.Harness != "" ||
			request.Origin != "" ||
			request.AnalysisStatus != "" ||
			request.ObservedAfter != nil ||
			request.AttentionKind != "" ||
			request.Experimental != "" {
			return AttentionFamilyList{}, invalidRequest("cursor continuation accepts only cursor")
		}
		decoded, err := s.openAttentionFamilyCursor(request.Cursor)
		if err != nil {
			return AttentionFamilyList{}, err
		}
		if decoded.Kind != "attention_families" ||
			decoded.FamilyID != "" ||
			decoded.GroupKey == "" ||
			decoded.PositionID != "" ||
			decoded.Time == "" ||
			decoded.SeverityRank < 1 ||
			decoded.SeverityRank > 5 ||
			decoded.SupportingIssueCount < 1 ||
			decoded.AnalysisStatusRank != 0 {
			return AttentionFamilyList{}, invalidCursor()
		}
		if err := validateAttentionFamilyCursorSnapshot(decoded, s.nowUTC()); err != nil {
			return AttentionFamilyList{}, err
		}
		if len(decoded.GroupKey) > 512 {
			return AttentionFamilyList{}, invalidCursor()
		}
		if _, err := parseUTCTime(decoded.Time); err != nil {
			return AttentionFamilyList{}, err
		}
		recovered, err := attentionFamilyListRequestFromCursor(decoded)
		if err != nil {
			return AttentionFamilyList{}, err
		}
		request = recovered
		cursor = &decoded
	} else {
		request = normalizeAttentionFamilyListRequest(request)
		if err := validateAttentionFamilyListRequest(request); err != nil {
			return AttentionFamilyList{}, err
		}
	}

	query := model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			Severity:       request.Severity,
			Harness:        request.Harness,
			Origin:         request.Origin,
			AnalysisStatus: model.AnalysisStatus(request.AnalysisStatus),
			ObservedAfter:  request.ObservedAfter,
			AttentionKind:  request.AttentionKind,
			Experimental:   request.Experimental,
		},
		Limit: request.Limit,
	}
	if cursor != nil {
		positionTime, _ := parseUTCTime(cursor.Time)
		issuedAt, _ := parseUTCTime(cursor.IssuedAt)
		query.CursorEpoch = cursor.CursorEpoch
		query.Snapshot = cursor.Snapshot
		query.RetentionGeneration = cursor.RetentionGeneration
		query.IssuedAt = issuedAt
		query.Cursor = &model.AttentionFamilyPosition{
			SeverityRank:         cursor.SeverityRank,
			SupportingIssueCount: cursor.SupportingIssueCount,
			LastObserved:         positionTime,
			GroupKey:             cursor.GroupKey,
		}
	}
	page, err := s.attentionFamilyRepository.QueryAttentionFamilies(ctx, query)
	if err != nil {
		return AttentionFamilyList{}, mapIssueRepositoryError(err)
	}
	if err := validateIssuePageSnapshot(
		page.CursorEpoch,
		page.Snapshot,
		page.RetentionGeneration,
		page.IssuedAt,
	); err != nil {
		return AttentionFamilyList{}, err
	}
	if cursor != nil && !sameIssueSnapshot(
		page.CursorEpoch,
		page.Snapshot,
		page.RetentionGeneration,
		page.IssuedAt,
		cursor.CursorEpoch,
		cursor.Snapshot,
		cursor.RetentionGeneration,
		query.IssuedAt,
	) {
		return AttentionFamilyList{}, errors.New("family repository changed snapshots during list continuation")
	}
	data, hasMore := boundedAttentionFamilyPage(page.Data, request.Limit, page.HasMore)
	presented := make([]AttentionFamilySummary, 0, len(data))
	for _, family := range data {
		value, err := s.presentAttentionFamily(family, request, page)
		if err != nil {
			return AttentionFamilyList{}, err
		}
		presented = append(presented, value)
	}
	nextCursor, err := s.attentionFamilyNextCursor(data, hasMore, request, page)
	if err != nil {
		return AttentionFamilyList{}, err
	}
	return AttentionFamilyList{
		SchemaVersion:          SchemaVersion,
		ProjectionVersion:      AttentionFamilyProjectionVersion,
		Data:                   nonNil(presented),
		GlobalAnalysisCoverage: page.Analysis,
		Selection:              normalizedAttentionFamilySelection(request),
		NextCursor:             nextCursor,
		HasMore:                hasMore,
		ReturnedCount:          len(presented),
		Limit:                  request.Limit,
	}, nil
}

func (s *Service) GetAttentionFamily(
	ctx context.Context,
	request AttentionFamilyDetailRequest,
) (AttentionFamilyDetail, error) {
	if err := s.RequireAttentionFamilyCapabilities(); err != nil {
		return AttentionFamilyDetail{}, err
	}
	request.FamilyID = strings.ToLower(strings.TrimSpace(request.FamilyID))
	request.Cursor = strings.TrimSpace(request.Cursor)
	request.ViewCursor = strings.TrimSpace(request.ViewCursor)
	if !attentionFamilyIDPattern.MatchString(request.FamilyID) {
		return AttentionFamilyDetail{}, invalidRequest("family_id is malformed")
	}
	if (request.Cursor == "") == (request.ViewCursor == "") {
		return AttentionFamilyDetail{}, invalidRequest("exactly one family cursor is required")
	}
	if request.Cursor != "" && request.Limit != 0 {
		return AttentionFamilyDetail{}, invalidRequest("cursor continuation accepts no limit")
	}
	if request.Limit < 0 || request.Limit > maxIssueLimit {
		return AttentionFamilyDetail{}, invalidRequest("limit must be between one and 100")
	}

	encodedCursor := request.ViewCursor
	expectedKind := "attention_family_view"
	if request.Cursor != "" {
		encodedCursor = request.Cursor
		expectedKind = "attention_family_members"
	}
	cursor, err := s.openAttentionFamilyCursor(encodedCursor)
	if err != nil {
		return AttentionFamilyDetail{}, err
	}
	if cursor.Kind != expectedKind ||
		cursor.FamilyID != request.FamilyID ||
		cursor.GroupKey == "" ||
		len(cursor.GroupKey) > 512 ||
		cursor.Filters == nil {
		return AttentionFamilyDetail{}, invalidCursor()
	}
	if err := validateAttentionFamilyCursorSnapshot(cursor, s.nowUTC()); err != nil {
		return AttentionFamilyDetail{}, err
	}
	filtersRequest, err := attentionFamilyListRequestFromCursor(
		attentionFamilyCursorEnvelope{
			Limit:   defaultIssueLimit,
			Filters: cursor.Filters,
		},
	)
	if err != nil {
		return AttentionFamilyDetail{}, err
	}
	limit := request.Limit
	var position *model.AttentionFamilyMemberPosition
	if request.Cursor != "" {
		if cursor.Limit < 1 ||
			cursor.Limit > maxIssueLimit ||
			cursor.PositionID == "" ||
			!issueIDPattern.MatchString(cursor.PositionID) ||
			cursor.Time == "" ||
			cursor.AnalysisStatusRank < 1 ||
			cursor.AnalysisStatusRank > 4 ||
			cursor.SeverityRank != 0 ||
			cursor.SupportingIssueCount != 0 {
			return AttentionFamilyDetail{}, invalidCursor()
		}
		positionTime, err := parseUTCTime(cursor.Time)
		if err != nil {
			return AttentionFamilyDetail{}, err
		}
		limit = cursor.Limit
		position = &model.AttentionFamilyMemberPosition{
			AnalysisStatusRank: cursor.AnalysisStatusRank,
			LastObserved:       positionTime,
			IssueID:            cursor.PositionID,
		}
	} else {
		if cursor.Limit != 0 ||
			cursor.PositionID != "" ||
			cursor.Time != "" ||
			cursor.SeverityRank != 0 ||
			cursor.SupportingIssueCount != 0 ||
			cursor.AnalysisStatusRank != 0 {
			return AttentionFamilyDetail{}, invalidCursor()
		}
		if limit == 0 {
			limit = defaultIssueLimit
		}
	}
	issuedAt, _ := parseUTCTime(cursor.IssuedAt)
	page, err := s.attentionFamilyRepository.QueryAttentionFamilyMembers(
		ctx,
		model.AttentionFamilyMemberQuery{
			FamilyID: request.FamilyID,
			GroupKey: cursor.GroupKey,
			Filter: model.AttentionFamilyFilter{
				Severity:       filtersRequest.Severity,
				Harness:        filtersRequest.Harness,
				Origin:         filtersRequest.Origin,
				AnalysisStatus: model.AnalysisStatus(filtersRequest.AnalysisStatus),
				ObservedAfter:  filtersRequest.ObservedAfter,
				AttentionKind:  filtersRequest.AttentionKind,
				Experimental:   filtersRequest.Experimental,
			},
			Limit:               limit,
			CursorEpoch:         cursor.CursorEpoch,
			Snapshot:            cursor.Snapshot,
			RetentionGeneration: cursor.RetentionGeneration,
			IssuedAt:            issuedAt,
			Cursor:              position,
		},
	)
	if err != nil {
		return AttentionFamilyDetail{}, mapIssueRepositoryError(err)
	}
	if err := validateIssuePageSnapshot(
		page.CursorEpoch,
		page.Snapshot,
		page.RetentionGeneration,
		page.IssuedAt,
	); err != nil {
		return AttentionFamilyDetail{}, err
	}
	if !sameIssueSnapshot(
		page.CursorEpoch,
		page.Snapshot,
		page.RetentionGeneration,
		page.IssuedAt,
		cursor.CursorEpoch,
		cursor.Snapshot,
		cursor.RetentionGeneration,
		issuedAt,
	) {
		return AttentionFamilyDetail{}, errors.New("family repository changed snapshots during detail read")
	}
	if !page.Found {
		return AttentionFamilyDetail{}, notFound()
	}
	if page.Family.FamilyID != request.FamilyID ||
		page.Family.GroupKey != cursor.GroupKey ||
		page.Family.Kind != model.AttentionFamilyKindMappedUpstream {
		return AttentionFamilyDetail{}, errors.New("family repository returned inconsistent detail")
	}
	members, hasMore := boundedAttentionFamilyMembers(page.Data, limit, page.HasMore)
	presentedMembers := make([]AttentionFamilyMember, 0, len(members))
	for _, member := range members {
		if strings.TrimSpace(member.SessionKey) == "" ||
			member.SessionSelection != model.AttentionFamilyMemberSessionSelectionLatest ||
			member.CitedEventCount < 0 {
			return AttentionFamilyDetail{}, errors.New("family repository returned invalid member context")
		}
		normalizeIssueSummary(&member.IssueSummary)
		viewCursor, err := s.sealExactIssueViewCursor(
			page.CursorEpoch,
			page.Snapshot,
			page.RetentionGeneration,
			page.IssuedAt,
		)
		if err != nil {
			return AttentionFamilyDetail{}, err
		}
		presentedMembers = append(presentedMembers, AttentionFamilyMember{
			Issue:               member.IssueSummary,
			Catalog:             issueCatalog(member.IssueSummary),
			SessionID:           member.SessionKey,
			SessionSelection:    member.SessionSelection,
			SessionStartedAt:    utcTime(member.SessionStartedAt),
			SessionLastActiveAt: utcTime(member.SessionLastActiveAt),
			CitedEventCount:     member.CitedEventCount,
			EvidenceFirstAt:     utcTime(member.EvidenceFirstAt),
			EvidenceLastAt:      utcTime(member.EvidenceLastAt),
			ViewCursor:          viewCursor,
		})
	}
	family, err := s.presentAttentionFamily(
		page.Family,
		filtersRequest,
		model.AttentionFamilyPage{
			CursorEpoch:         page.CursorEpoch,
			Snapshot:            page.Snapshot,
			RetentionGeneration: page.RetentionGeneration,
			IssuedAt:            page.IssuedAt,
		},
	)
	if err != nil {
		return AttentionFamilyDetail{}, err
	}
	nextCursor, err := s.attentionFamilyMemberNextCursor(
		members,
		hasMore,
		limit,
		page.Family,
		filtersRequest,
		page,
	)
	if err != nil {
		return AttentionFamilyDetail{}, err
	}
	return AttentionFamilyDetail{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: AttentionFamilyProjectionVersion,
		Data: AttentionFamilyDetailData{
			Family:  family,
			Members: nonNil(presentedMembers),
		},
		GlobalAnalysisCoverage: page.Analysis,
		ViewCursor:             family.ViewCursor,
		NextCursor:             nextCursor,
		HasMore:                hasMore,
		ReturnedCount:          len(presentedMembers),
		Limit:                  limit,
	}, nil
}

func (s *Service) presentAttentionFamily(
	family model.AttentionFamilySummary,
	request AttentionFamilyListRequest,
	page model.AttentionFamilyPage,
) (AttentionFamilySummary, error) {
	if !attentionFamilyIDPattern.MatchString(family.FamilyID) ||
		family.GroupKey == "" ||
		len(family.GroupKey) > 512 ||
		!issueIDPattern.MatchString(family.RepresentativeIssueID) ||
		family.SupportingIssueCount < 1 ||
		family.OccurrenceCount < 1 ||
		family.SessionCount < 1 ||
		family.LastObservedAt.IsZero() {
		return AttentionFamilySummary{}, errors.New("family repository returned invalid summary")
	}
	var (
		catalog       AttentionFamilyCatalogMetadata
		catalogStatus string
		viewCursor    string
		err           error
	)
	switch family.Kind {
	case model.AttentionFamilyKindExactIssue:
		issueMetadata := issueCatalog(family.Representative)
		catalogStatus = issueMetadata.CatalogStatus
		catalog = AttentionFamilyCatalogMetadata{
			CatalogVersion:       issueMetadata.CatalogVersion,
			GroupingVersion:      family.GroupingVersion,
			DisplayTitle:         issueMetadata.DisplayTitle,
			ObservationStatement: issueMetadata.ObservationStatement,
			Caveat:               issueMetadata.Caveat,
			NextEvidenceAction:   issueMetadata.NextEvidenceAction,
		}
		viewCursor, err = s.sealExactIssueViewCursor(
			page.CursorEpoch,
			page.Snapshot,
			page.RetentionGeneration,
			page.IssuedAt,
		)
	case model.AttentionFamilyKindMappedUpstream:
		catalogStatus = "known"
		mapping, ok := sourcecatalog.MappingByKey(
			family.MappingKey,
			family.MappingVersion,
			family.GroupingVersion,
		)
		if !ok {
			return AttentionFamilySummary{}, errors.New("family repository returned unknown mapping")
		}
		catalog = AttentionFamilyCatalogMetadata{
			CatalogVersion:       sourcecatalog.CatalogVersion,
			MappingKey:           mapping.Key,
			MappingVersion:       mapping.Version,
			GroupingVersion:      mapping.GroupingVersion,
			DisplayTitle:         mapping.DisplayTitle,
			ObservationStatement: mapping.ObservationStatement,
			Caveat:               mapping.Caveat,
			NextEvidenceAction:   mapping.NextEvidenceAction,
		}
		filters := attentionFamilyCursorFiltersFromRequest(request)
		viewCursor, err = s.sealAttentionFamilyCursor(attentionFamilyCursorEnvelope{
			Version:             attentionFamilyCursorVersion,
			Kind:                "attention_family_view",
			CatalogVersion:      sourcecatalog.CatalogVersion,
			CursorEpoch:         page.CursorEpoch,
			Snapshot:            page.Snapshot,
			RetentionGeneration: page.RetentionGeneration,
			IssuedAt:            page.IssuedAt.UTC().Format(time.RFC3339Nano),
			Filters:             &filters,
			FamilyID:            family.FamilyID,
			GroupKey:            family.GroupKey,
		})
	default:
		return AttentionFamilySummary{}, errors.New("family repository returned unknown family kind")
	}
	if err != nil {
		return AttentionFamilySummary{}, err
	}
	return AttentionFamilySummary{
		FamilyID:              family.FamilyID,
		Kind:                  family.Kind,
		RepresentativeIssueID: family.RepresentativeIssueID,
		AttentionKind:         family.AttentionKind,
		Severity:              family.Severity,
		Confidence:            family.Confidence,
		FirstObservedAt:       family.FirstObservedAt,
		LastObservedAt:        family.LastObservedAt,
		SupportingIssueCount:  family.SupportingIssueCount,
		OccurrenceCount:       family.OccurrenceCount,
		SessionCount:          family.SessionCount,
		Harnesses:             nonNil(family.Harnesses),
		Scope:                 family.Scope,
		AnalysisStatus:        family.AnalysisStatus,
		EvidenceComplete:      family.EvidenceComplete,
		RetainedHistoryOnly:   family.RetainedHistoryOnly,
		Experimental:          family.Experimental,
		Catalog:               catalog,
		ViewCursor:            viewCursor,
		CatalogStatus:         catalogStatus,
	}, nil
}

func (s *Service) sealExactIssueViewCursor(
	epoch string,
	snapshot int64,
	retention int64,
	issuedAt time.Time,
) (string, error) {
	return s.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_view",
		CursorEpoch:         epoch,
		Snapshot:            snapshot,
		RetentionGeneration: retention,
		IssuedAt:            issuedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Service) attentionFamilyNextCursor(
	data []model.AttentionFamilySummary,
	hasMore bool,
	request AttentionFamilyListRequest,
	page model.AttentionFamilyPage,
) (*string, error) {
	if !hasMore {
		return nil, nil
	}
	if len(data) == 0 {
		return nil, errors.New("family repository reported unusable continuation")
	}
	last := data[len(data)-1]
	rank := severityRank(last.Severity)
	if rank == 0 ||
		last.SupportingIssueCount < 1 ||
		last.LastObservedAt.IsZero() ||
		last.GroupKey == "" {
		return nil, errors.New("family repository returned invalid position")
	}
	filters := attentionFamilyCursorFiltersFromRequest(request)
	encoded, err := s.sealAttentionFamilyCursor(attentionFamilyCursorEnvelope{
		Version:              attentionFamilyCursorVersion,
		Kind:                 "attention_families",
		CatalogVersion:       sourcecatalog.CatalogVersion,
		CursorEpoch:          page.CursorEpoch,
		Snapshot:             page.Snapshot,
		RetentionGeneration:  page.RetentionGeneration,
		IssuedAt:             page.IssuedAt.UTC().Format(time.RFC3339Nano),
		Limit:                request.Limit,
		Filters:              &filters,
		GroupKey:             last.GroupKey,
		Time:                 last.LastObservedAt.UTC().Format(time.RFC3339Nano),
		SeverityRank:         rank,
		SupportingIssueCount: last.SupportingIssueCount,
	})
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func (s *Service) attentionFamilyMemberNextCursor(
	data []model.AttentionFamilyMemberRecord,
	hasMore bool,
	limit int,
	family model.AttentionFamilySummary,
	request AttentionFamilyListRequest,
	page model.AttentionFamilyMemberPage,
) (*string, error) {
	if !hasMore {
		return nil, nil
	}
	if len(data) == 0 {
		return nil, errors.New("family repository reported unusable member continuation")
	}
	last := data[len(data)-1]
	rank := attentionFamilyAnalysisStatusRank(last.AnalysisStatus)
	if !issueIDPattern.MatchString(last.IssueID) ||
		rank == 0 ||
		last.LastObservedAt.IsZero() {
		return nil, errors.New("family repository returned invalid member position")
	}
	filters := attentionFamilyCursorFiltersFromRequest(request)
	encoded, err := s.sealAttentionFamilyCursor(attentionFamilyCursorEnvelope{
		Version:             attentionFamilyCursorVersion,
		Kind:                "attention_family_members",
		CatalogVersion:      sourcecatalog.CatalogVersion,
		CursorEpoch:         page.CursorEpoch,
		Snapshot:            page.Snapshot,
		RetentionGeneration: page.RetentionGeneration,
		IssuedAt:            page.IssuedAt.UTC().Format(time.RFC3339Nano),
		Limit:               limit,
		Filters:             &filters,
		FamilyID:            family.FamilyID,
		GroupKey:            family.GroupKey,
		Time:                last.LastObservedAt.UTC().Format(time.RFC3339Nano),
		PositionID:          last.IssueID,
		AnalysisStatusRank:  rank,
	})
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func normalizeAttentionFamilyListRequest(
	request AttentionFamilyListRequest,
) AttentionFamilyListRequest {
	request.Limit = boundedLimit(request.Limit, defaultIssueLimit, maxIssueLimit)
	request.Severity = strings.ToLower(strings.TrimSpace(request.Severity))
	request.Harness = strings.TrimSpace(request.Harness)
	request.Origin = strings.ToLower(strings.TrimSpace(request.Origin))
	request.AnalysisStatus = strings.ToLower(strings.TrimSpace(request.AnalysisStatus))
	request.ObservedAfter = utcTime(request.ObservedAfter)
	request.AttentionKind = strings.ToLower(strings.TrimSpace(request.AttentionKind))
	request.Experimental = strings.ToLower(strings.TrimSpace(request.Experimental))
	if request.AttentionKind == "" {
		request.AttentionKind = model.AttentionKindIssue
	}
	if request.Experimental == "" {
		request.Experimental = model.ExperimentalStable
	}
	return request
}

func validateAttentionFamilyListRequest(request AttentionFamilyListRequest) error {
	if request.Limit < 1 || request.Limit > maxIssueLimit {
		return invalidRequest("limit must be between one and 100")
	}
	switch request.Severity {
	case "", "info", "low", "medium", "high", "critical":
	default:
		return invalidRequest("severity is unsupported")
	}
	if len(request.Harness) > 128 {
		return invalidRequest("harness must not exceed 128 bytes")
	}
	switch request.Origin {
	case "", "belay", "numbat":
	default:
		return invalidRequest("origin is unsupported")
	}
	switch request.AnalysisStatus {
	case "", string(model.AnalysisCurrent), string(model.AnalysisPending),
		string(model.AnalysisFailed), string(model.AnalysisTruncated):
	default:
		return invalidRequest("analysis_status is unsupported")
	}
	switch request.AttentionKind {
	case model.AttentionKindIssue, model.AttentionKindEvidenceGap:
	default:
		return invalidRequest("attention_kind is unsupported")
	}
	switch request.Experimental {
	case model.ExperimentalStable, model.ExperimentalInclude:
	default:
		return invalidRequest("experimental is unsupported")
	}
	return nil
}

func normalizedAttentionFamilySelection(
	request AttentionFamilyListRequest,
) AttentionFamilySelection {
	return AttentionFamilySelection{
		AttentionKind:        request.AttentionKind,
		Experimental:         request.Experimental,
		IncludesEvidenceGaps: request.AttentionKind == model.AttentionKindEvidenceGap,
		IncludesExperimental: request.Experimental == model.ExperimentalInclude,
	}
}

func boundedAttentionFamilyPage(
	data []model.AttentionFamilySummary,
	limit int,
	repositoryHasMore bool,
) ([]model.AttentionFamilySummary, bool) {
	if len(data) > limit {
		return data[:limit], true
	}
	return data, repositoryHasMore
}

func boundedAttentionFamilyMembers(
	data []model.AttentionFamilyMemberRecord,
	limit int,
	repositoryHasMore bool,
) ([]model.AttentionFamilyMemberRecord, bool) {
	if len(data) > limit {
		return data[:limit], true
	}
	return data, repositoryHasMore
}

func attentionFamilyAnalysisStatusRank(status model.AnalysisStatus) int {
	switch status {
	case model.AnalysisFailed:
		return 4
	case model.AnalysisPending:
		return 3
	case model.AnalysisTruncated:
		return 2
	case model.AnalysisCurrent:
		return 1
	default:
		return 0
	}
}
