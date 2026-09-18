package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

type fixMonitoringTestRepository struct {
	queryList         func(model.FixMonitoringQuery) (model.FixMonitoringPage, error)
	queryDetail       func(model.FixMonitoringDetailQuery) (model.FixMonitoringDetailPage, error)
	queryObservations func(model.FixRecurrenceObservationQuery) (model.FixRecurrenceObservationPage, error)
}

type fixMonitoringCapableCoreRepository struct {
	issueTestCoreRepository
	*fixMonitoringTestRepository
}

func (repository *fixMonitoringTestRepository) QueryFixMonitoring(
	_ context.Context,
	query model.FixMonitoringQuery,
) (model.FixMonitoringPage, error) {
	if repository.queryList == nil {
		return model.FixMonitoringPage{}, nil
	}
	return repository.queryList(query)
}

func (repository *fixMonitoringTestRepository) QueryIssueFixMonitoring(
	_ context.Context,
	query model.FixMonitoringDetailQuery,
) (model.FixMonitoringDetailPage, error) {
	if repository.queryDetail == nil {
		return model.FixMonitoringDetailPage{}, nil
	}
	return repository.queryDetail(query)
}

func (repository *fixMonitoringTestRepository) QueryFixRecurrenceObservations(
	_ context.Context,
	query model.FixRecurrenceObservationQuery,
) (model.FixRecurrenceObservationPage, error) {
	if repository.queryObservations == nil {
		return model.FixRecurrenceObservationPage{}, nil
	}
	return repository.queryObservations(query)
}

func TestFixMonitoringListNormalizesAndCarriesDedicatedCursorClaims(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	issueID := testIssueID("a")
	annotationID := testFixAnnotationID("b")
	title := "explicit_command_failure"
	severity := "high"
	recordedAfter := now.Add(-time.Hour).In(time.FixedZone("offset", 2*60*60))
	snapshot := testFixMonitoringSnapshot(now)
	var queries []model.FixMonitoringQuery
	repository := &fixMonitoringTestRepository{
		queryList: func(query model.FixMonitoringQuery) (model.FixMonitoringPage, error) {
			queries = append(queries, query)
			return model.FixMonitoringPage{
				Data: []model.FixMonitoringSummary{{
					FixAttemptMonitoring: model.FixAttemptMonitoring{
						AnnotationID:       annotationID,
						IssueID:            issueID,
						Subject:            model.FixMonitoringSubject{TitleCode: &title, Severity: &severity},
						ChangeKind:         model.FixChangeCode,
						RecordedAt:         now.Add(-time.Minute),
						MonitorFrom:        now.Add(-time.Minute),
						FixRecurrenceState: model.FixRecurrenceMatchingEvidence,
						Coverage:           model.FixMonitoringCoverage{Complete: true},
					},
					ActiveAttemptCount:   2,
					ObservedAttemptCount: 1,
				}},
				Snapshot: snapshot,
				HasMore:  len(queries) == 1,
				Position: &model.FixMonitoringPosition{
					StateRank:    1,
					SeverityRank: 4,
					ActivityAt:   now.Add(-time.Minute),
					IssueID:      issueID,
				},
				EvidenceEvaluatedAt: now,
			}, nil
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithFixMonitoringRepository(repository),
		WithClock(func() time.Time { return now }),
	)
	first, err := service.ListFixMonitoring(context.Background(), FixMonitoringListRequest{
		Limit:            1,
		State:            " MATCHING_EVIDENCE_OBSERVED ",
		ChangeKind:       " CODE_CHANGE ",
		Severity:         " HIGH ",
		Harness:          " Codex ",
		RecordedAfter:    &recordedAfter,
		IssueID:          " " + strings.ToUpper(issueID) + " ",
		IncludeRetracted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.SchemaVersion != model.FixMonitoringSchemaVersion ||
		first.NextCursor == nil || first.MonitoringViewCursor == "" ||
		!first.HasMore || first.ReturnedCount != 1 || first.Limit != 1 {
		t.Fatalf("first monitoring page = %+v", first)
	}
	if first.Data[0].TitleCode == nil || *first.Data[0].TitleCode != title ||
		first.Data[0].ActiveAttemptCount != 2 {
		t.Fatalf("first monitoring DTO = %+v", first.Data[0])
	}
	query := queries[0]
	if query.Limit != 1 ||
		query.Filter.State != model.FixRecurrenceMatchingEvidence ||
		query.Filter.ChangeKind != model.FixChangeCode ||
		query.Filter.Severity != "high" ||
		query.Filter.Harness != "Codex" ||
		query.Filter.RecordedAfter == nil ||
		!query.Filter.RecordedAfter.Equal(recordedAfter.UTC()) ||
		query.Filter.IssueID != issueID ||
		!query.Filter.IncludeRetracted {
		t.Fatalf("normalized monitoring query = %+v", query)
	}
	body, err := json.Marshal(first.Data[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, internal := range []string{"fingerprint_scope_id", "scope_capture_status", "negative_comparison_mode"} {
		if strings.Contains(string(body), internal) {
			t.Fatalf("monitoring summary leaked %q: %s", internal, body)
		}
	}

	now = now.Add(5 * time.Minute)
	second, err := service.ListFixMonitoring(context.Background(), FixMonitoringListRequest{
		Cursor: *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || second.NextCursor != nil {
		t.Fatalf("second monitoring page = %+v", second)
	}
	query = queries[1]
	if query.Snapshot != snapshot ||
		query.Limit != 1 ||
		query.Cursor == nil ||
		query.Cursor.IssueID != issueID ||
		query.Filter.Harness != "Codex" ||
		!query.Filter.IncludeRetracted {
		t.Fatalf("continued monitoring query = %+v", query)
	}
	if _, err := service.ListFixMonitoring(context.Background(), FixMonitoringListRequest{
		Cursor: *first.NextCursor,
		State:  model.FixRecurrenceNoLaterMatch,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("changed-filter continuation error = %v", err)
	}
	if _, err := decodeCursor(*first.NextCursor, "sessions", "unused"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("legacy decoder accepted monitoring cursor: %v", err)
	}
}

func TestFixMonitoringDetailAndObservationViewHandoff(t *testing.T) {
	now := time.Date(2026, 9, 8, 13, 0, 0, 0, time.UTC)
	issueID := testIssueID("c")
	annotationID := testFixAnnotationID("d")
	recurrenceID := testFixRecurrenceID("e")
	snapshot := testFixMonitoringSnapshot(now)
	title := "explicit_command_failure"
	severity := "high"
	confidence := "high"
	harness := "codex"
	reason := "future_private_reason"
	var detailQueries []model.FixMonitoringDetailQuery
	var observationQueries []model.FixRecurrenceObservationQuery
	repository := &fixMonitoringTestRepository{}
	repository.queryList = func(model.FixMonitoringQuery) (model.FixMonitoringPage, error) {
		return model.FixMonitoringPage{
			Snapshot:            snapshot,
			Data:                []model.FixMonitoringSummary{},
			EvidenceEvaluatedAt: now,
		}, nil
	}
	repository.queryDetail = func(query model.FixMonitoringDetailQuery) (model.FixMonitoringDetailPage, error) {
		detailQueries = append(detailQueries, query)
		return model.FixMonitoringDetailPage{
			IssueID: issueID,
			Data: []model.FixAttemptMonitoring{{
				AnnotationID: annotationID,
				IssueID:      issueID,
				Subject: model.FixMonitoringSubject{
					TitleCode:          &title,
					Severity:           &severity,
					Confidence:         &confidence,
					AnchorHarness:      &harness,
					Origin:             "belay",
					DetectorID:         "explicit_command_failure",
					DetectorVersion:    "1",
					FingerprintVersion: "1",
				},
				ChangeKind:                        model.FixChangeCode,
				RecordedAt:                        now.Add(-time.Minute),
				MonitorFrom:                       now.Add(-time.Minute),
				State:                             model.FixStateActive,
				FixRecurrenceState:                "future_state",
				FutureComparisonUnavailableReason: &reason,
				AnchorEvidenceCurrentlyRetained:   model.FixRecurrenceEvidenceAvailable,
			}},
			Snapshot: snapshot,
			HasMore:  len(detailQueries) == 1,
			Position: &model.FixMonitoringDetailPosition{
				RecordedAt:   now.Add(-time.Minute),
				AnnotationID: annotationID,
			},
			EvidenceEvaluatedAt: now,
		}, nil
	}
	repository.queryObservations = func(query model.FixRecurrenceObservationQuery) (model.FixRecurrenceObservationPage, error) {
		observationQueries = append(observationQueries, query)
		privateCount := 9
		privateTruncated := true
		return model.FixRecurrenceObservationPage{
			IssueID:      issueID,
			AnnotationID: annotationID,
			Data: []model.FixRecurrenceObservation{{
				RecurrenceID:              recurrenceID,
				OccurrenceID:              "occurrence-1",
				SessionID:                 "session-1",
				FingerprintVersion:        "1",
				Origin:                    "belay",
				DetectorID:                "explicit_command_failure",
				DetectorVersion:           "1",
				FirstQualifyingEventAt:    now.Add(time.Minute),
				LastQualifyingEventAt:     now.Add(time.Minute),
				QualifyingCitationCount:   1,
				RetainedEventIDs:          []string{"PRIVATE_EVENT_CANARY"},
				RetainedEventCount:        &privateCount,
				MissingEventCount:         &privateCount,
				EvidenceTruncated:         &privateTruncated,
				EvidenceCurrentlyRetained: "future_evidence_state",
				ObservedAt:                now.Add(2 * time.Minute),
			}},
			Snapshot:            snapshot,
			EvidenceEvaluatedAt: now,
		}, nil
	}
	service := New(
		issueTestCoreRepository{},
		WithFixMonitoringRepository(repository),
		WithClock(func() time.Time { return now }),
	)
	list, err := service.ListFixMonitoring(context.Background(), FixMonitoringListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.GetIssueFixMonitoring(context.Background(), FixMonitoringDetailRequest{
		IssueID:    strings.ToUpper(issueID),
		Limit:      1,
		ViewCursor: list.MonitoringViewCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.CurrentIssueAvailable || first.CurrentIssue != nil ||
		first.NextCursor == nil || first.MonitoringViewCursor == "" ||
		first.Data[0].ObservationViewCursor == "" ||
		first.Data[0].FixRecurrenceState != "unknown" ||
		first.Data[0].FutureComparisonUnavailableReason == nil ||
		*first.Data[0].FutureComparisonUnavailableReason !=
			model.FixComparisonCapabilityUnavailable {
		t.Fatalf("monitoring detail = %+v", first)
	}
	body, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"current_issue":null`,
		`"retraction_reason":null`,
		`"retracted_at":null`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("detail JSON missing %s: %s", expected, body)
		}
	}
	if detailQueries[0].Snapshot != snapshot || detailQueries[0].Limit != 1 {
		t.Fatalf("view-transferred detail query = %+v", detailQueries[0])
	}

	observations, err := service.ListFixRecurrenceObservations(
		context.Background(),
		FixRecurrenceObservationListRequest{
			IssueID:               issueID,
			AnnotationID:          annotationID,
			Limit:                 1,
			ObservationViewCursor: first.Data[0].ObservationViewCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observationQueries[0].Snapshot != snapshot ||
		observations.Data[0].EvidenceCurrentlyRetained !=
			model.FixRecurrenceEvidenceUnknown ||
		observations.Data[0].RetainedEventIDs == nil ||
		len(observations.Data[0].RetainedEventIDs) != 0 ||
		observations.Data[0].RetainedEventCount != nil ||
		observations.Data[0].MissingEventCount != nil ||
		observations.Data[0].EvidenceTruncated != nil {
		t.Fatalf("unknown observation evidence = %+v", observations)
	}

	now = now.Add(time.Minute)
	second, err := service.GetIssueFixMonitoring(context.Background(), FixMonitoringDetailRequest{
		IssueID: issueID,
		Cursor:  *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || detailQueries[1].Snapshot != snapshot ||
		detailQueries[1].Cursor == nil ||
		detailQueries[1].Cursor.AnnotationID != annotationID {
		t.Fatalf("continued detail=%+v query=%+v", second, detailQueries[1])
	}

	otherIssueID := testIssueID("f")
	if _, err := service.GetIssueFixMonitoring(
		context.Background(),
		FixMonitoringDetailRequest{
			IssueID:    otherIssueID,
			ViewCursor: first.MonitoringViewCursor,
		},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("issue-bound view cursor mismatch = %v", err)
	}
	if _, err := service.GetIssueFixMonitoring(
		context.Background(),
		FixMonitoringDetailRequest{
			IssueID: otherIssueID,
			Cursor:  *first.NextCursor,
		},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("issue-bound page cursor mismatch = %v", err)
	}
	if _, err := service.ListFixRecurrenceObservations(
		context.Background(),
		FixRecurrenceObservationListRequest{
			IssueID:               issueID,
			AnnotationID:          testFixAnnotationID("g"),
			ObservationViewCursor: first.Data[0].ObservationViewCursor,
		},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("annotation-bound observation cursor mismatch = %v", err)
	}
}

func TestFixMonitoringCapabilityAndRepositoryErrorMapping(t *testing.T) {
	repository := &fixMonitoringCapableCoreRepository{
		fixMonitoringTestRepository: &fixMonitoringTestRepository{},
	}
	service := New(repository)
	if _, err := service.ListFixMonitoring(
		context.Background(),
		FixMonitoringListRequest{},
	); err == nil {
		t.Fatal("New(repository) implicitly enabled fix monitoring")
	}

	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "invalid", err: model.ErrFixMonitoringSnapshotInvalid, want: ErrInvalidCursor},
		{name: "expired", err: model.ErrFixMonitoringSnapshotExpired, want: ErrCursorExpired},
		{name: "not found", err: model.ErrFixMonitoringNotFound, want: ErrNotFound},
		{name: "catching up", err: model.ErrFixMonitoringCatchingUp, want: ErrMonitoringCatchingUp},
		{name: "failed", err: model.ErrFixMonitoringCatchupFailed, want: ErrMonitoringCatchupFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository.fixMonitoringTestRepository.queryList = func(
				model.FixMonitoringQuery,
			) (model.FixMonitoringPage, error) {
				return model.FixMonitoringPage{}, test.err
			}
			capable := New(
				issueTestCoreRepository{},
				WithFixMonitoringRepository(repository.fixMonitoringTestRepository),
			)
			if _, err := capable.ListFixMonitoring(
				context.Background(),
				FixMonitoringListRequest{},
			); !errors.Is(err, test.want) {
				t.Fatalf("mapped error = %v, want %v", err, test.want)
			}
		})
	}
}

func testFixMonitoringSnapshot(now time.Time) model.FixMonitoringSnapshot {
	return model.FixMonitoringSnapshot{
		ProjectionGeneration: 7,
		EventGeneration:      8,
		RetentionGeneration:  1,
		AnnotationSequence:   9,
		RetractionSequence:   10,
		JobSequence:          11,
		JobEventSequence:     12,
		ObservationSequence:  13,
		IssuedAt:             now.UTC(),
	}
}

func testFixAnnotationID(character string) string {
	return "fxa_" + strings.Repeat(character, 52)
}

func testFixRecurrenceID(character string) string {
	return "fxo_" + strings.Repeat(character, 52)
}
