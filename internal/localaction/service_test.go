package localaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const (
	testIssueID      = "iss_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherTestIssueID = "iss_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testAnnotationID = "fxa_cccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testKey          = "12345678-1234-4234-9234-123456789abc"
	testCursorEpoch  = "ice_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type actionTestRepository struct {
	eligibility model.FixEligibility
	eligibleErr error
	record      local.FixAnnotationResult
	recordErr   error
	retraction  local.FixRetractionResult
	retractErr  error
	page        model.FixAnnotationPage
	pageErr     error

	eligibilityQuery model.FixEligibilityQuery
	recordInput      local.FixAnnotationInput
	retractionInput  local.FixRetractionInput
	historyQuery     model.FixAnnotationQuery
	recordCalls      int
}

func (r *actionTestRepository) EvaluateFixEligibility(
	_ context.Context,
	query model.FixEligibilityQuery,
) (model.FixEligibility, error) {
	r.eligibilityQuery = query
	return r.eligibility, r.eligibleErr
}

func (r *actionTestRepository) RecordFixAnnotation(
	_ context.Context,
	input local.FixAnnotationInput,
) (local.FixAnnotationResult, error) {
	r.recordCalls++
	r.recordInput = input
	return r.record, r.recordErr
}

func (r *actionTestRepository) RetractFixAnnotation(
	_ context.Context,
	input local.FixRetractionInput,
) (local.FixRetractionResult, error) {
	r.retractionInput = input
	return r.retraction, r.retractErr
}

func (r *actionTestRepository) QueryFixAnnotations(
	_ context.Context,
	query model.FixAnnotationQuery,
) (model.FixAnnotationPage, error) {
	r.historyQuery = query
	return r.page, r.pageErr
}

type actionTestTokens struct {
	claims      model.FixActionClaims
	decodeErr   error
	issueErr    error
	issued      model.FixActionClaims
	decodeCalls int
}

func (t *actionTestTokens) IssueFixActionToken(claims model.FixActionClaims) (string, error) {
	t.issued = claims
	if t.issueErr != nil {
		return "", t.issueErr
	}
	return "signed-token", nil
}

func (t *actionTestTokens) DecodeFixActionToken(string) (model.FixActionClaims, error) {
	t.decodeCalls++
	return t.claims, t.decodeErr
}

func TestPrepareFixAttemptIssuesSnapshotBoundToken(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	repository := &actionTestRepository{
		eligibility: model.FixEligibility{
			Eligible:            true,
			Reason:              model.FixEligibilityEligible,
			IssueID:             testIssueID,
			CursorEpoch:         testCursorEpoch,
			Snapshot:            41,
			RetentionGeneration: 7,
		},
	}
	tokens := &actionTestTokens{}
	service, err := New(
		repository,
		tokens,
		WithClock(func() time.Time { return now.Add(time.Minute) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareFixAttempt(
		context.Background(),
		strings.ToUpper(testIssueID),
		model.IssueViewClaims{
			CursorEpoch:         testCursorEpoch,
			Snapshot:            41,
			RetentionGeneration: 7,
			IssuedAt:            now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Eligible ||
		result.ActionToken == nil ||
		*result.ActionToken != "signed-token" ||
		result.ExpiresAt == nil ||
		!result.ExpiresAt.Equal(now.Add(actionTokenLifetime)) ||
		result.ChangeCatalogVersion != model.FixChangeCatalogVersion {
		t.Fatalf("eligibility = %+v", result)
	}
	if repository.eligibilityQuery.IssueID != testIssueID ||
		repository.eligibilityQuery.CursorEpoch != testCursorEpoch ||
		repository.eligibilityQuery.Snapshot != 41 ||
		repository.eligibilityQuery.RetentionGeneration != 7 ||
		!repository.eligibilityQuery.IssuedAt.Equal(now) {
		t.Fatalf("eligibility query = %+v", repository.eligibilityQuery)
	}
	if tokens.issued.IssueID != testIssueID ||
		tokens.issued.CursorEpoch != testCursorEpoch ||
		tokens.issued.Snapshot != 41 ||
		tokens.issued.RetentionGeneration != 7 ||
		!tokens.issued.IssuedAt.Equal(now) ||
		!tokens.issued.ExpiresAt.Equal(now.Add(actionTokenLifetime)) {
		t.Fatalf("issued claims = %+v", tokens.issued)
	}

	repository.eligibility = model.FixEligibility{
		Reason:              model.FixEligibilityEvidenceGap,
		IssueID:             testIssueID,
		CursorEpoch:         testCursorEpoch,
		Snapshot:            41,
		RetentionGeneration: 7,
	}
	result, err = service.PrepareFixAttempt(
		context.Background(),
		testIssueID,
		model.IssueViewClaims{
			CursorEpoch:         testCursorEpoch,
			Snapshot:            41,
			RetentionGeneration: 7,
			IssuedAt:            now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Eligible || result.ActionToken != nil || result.ExpiresAt != nil {
		t.Fatalf("ineligible result = %+v", result)
	}
}

func TestPrepareFixAttemptValidatesFreshnessAndMapsErrors(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	repository := &actionTestRepository{}
	service, err := New(
		repository,
		&actionTestTokens{},
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		issue  string
		claims model.IssueViewClaims
		want   error
	}{
		{"malformed issue", "PRIVATE", model.IssueViewClaims{Snapshot: 1, IssuedAt: now}, ErrInvalidRequest},
		{"zero snapshot", testIssueID, model.IssueViewClaims{
			CursorEpoch: testCursorEpoch, RetentionGeneration: 1, IssuedAt: now,
		}, ErrInvalidRequest},
		{"future", testIssueID, model.IssueViewClaims{
			CursorEpoch: testCursorEpoch, Snapshot: 1,
			RetentionGeneration: 1, IssuedAt: now.Add(time.Second),
		}, ErrInvalidRequest},
		{"expired", testIssueID, model.IssueViewClaims{
			CursorEpoch: testCursorEpoch, Snapshot: 1,
			RetentionGeneration: 1,
			IssuedAt:            now.Add(-actionTokenLifetime - time.Nanosecond),
		}, ErrCursorExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.PrepareFixAttempt(context.Background(), test.issue, test.claims)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	repository.eligibleErr = local.ErrFixIssueNotFound
	if _, err := service.PrepareFixAttempt(
		context.Background(),
		testIssueID,
		model.IssueViewClaims{
			CursorEpoch:         testCursorEpoch,
			Snapshot:            1,
			RetentionGeneration: 1,
			IssuedAt:            now,
		},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not-found error = %v", err)
	}
	repository.eligibleErr = model.ErrIssueSnapshotExpired
	if _, err := service.PrepareFixAttempt(
		context.Background(),
		testIssueID,
		model.IssueViewClaims{
			CursorEpoch:         testCursorEpoch,
			Snapshot:            1,
			RetentionGeneration: 1,
			IssuedAt:            now,
		},
	); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestRecordAuthenticatesAndBindsBeforeRepositoryReplay(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	tokens := &actionTestTokens{claims: model.FixActionClaims{
		Version:   model.FixActionTokenVersion,
		IssueID:   testIssueID,
		Snapshot:  7,
		IssuedAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(-45 * time.Minute),
	}}
	repository := &actionTestRepository{
		record: local.FixAnnotationResult{
			Annotation: model.FixAnnotation{
				AnnotationID: testAnnotationID,
				IssueID:      testIssueID,
			},
			Replayed: true,
		},
	}
	service, err := New(repository, tokens)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RecordFixAttempt(
		context.Background(),
		testIssueID,
		"signed-token",
		model.FixChangeCode,
		testKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || repository.recordCalls != 1 ||
		repository.recordInput.Claims != tokens.claims {
		t.Fatalf("result=%+v input=%+v calls=%d", result, repository.recordInput, repository.recordCalls)
	}

	tokens.claims.IssueID = otherTestIssueID
	if _, err := service.RecordFixAttempt(
		context.Background(),
		testIssueID,
		"signed-token",
		model.FixChangeCode,
		testKey,
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("route-binding error = %v", err)
	}
	if repository.recordCalls != 1 {
		t.Fatal("route-mismatched token reached repository replay")
	}
	tokens.decodeErr = local.ErrFixActionTokenInvalid
	if _, err := service.RecordFixAttempt(
		context.Background(),
		testIssueID,
		"tampered-token",
		model.FixChangeCode,
		testKey,
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("MAC error = %v", err)
	}
	if repository.recordCalls != 1 {
		t.Fatal("unauthenticated token reached repository replay")
	}
	tokens.decodeErr = local.ErrFixActionTokenExpired
	if _, err := service.RecordFixAttempt(
		context.Background(),
		testIssueID,
		"stale-token",
		model.FixChangeCode,
		testKey,
	); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("stale token error = %v", err)
	}
}

func TestRecordRetractAndRepositoryErrorMapping(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	repository := &actionTestRepository{}
	tokens := &actionTestTokens{claims: model.FixActionClaims{
		Version:   model.FixActionTokenVersion,
		IssueID:   testIssueID,
		Snapshot:  7,
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	}}
	service, err := New(repository, tokens)
	if err != nil {
		t.Fatal(err)
	}
	recordErrors := []struct {
		source error
		want   error
	}{
		{model.ErrIssueSnapshotExpired, ErrCursorExpired},
		{local.ErrFixIssueNotFound, ErrNotFound},
		{local.ErrFixIneligible, ErrIneligibleIssue},
		{local.ErrFixIdempotencyConflict, ErrIdempotencyConflict},
	}
	for _, test := range recordErrors {
		repository.recordErr = test.source
		if _, err := service.RecordFixAttempt(
			context.Background(),
			testIssueID,
			"signed",
			model.FixChangeCode,
			testKey,
		); !errors.Is(err, test.want) {
			t.Fatalf("record source %v mapped to %v, want %v", test.source, err, test.want)
		}
	}
	repository.recordErr = nil

	repository.retraction = local.FixRetractionResult{
		Retraction: model.FixRetraction{
			RetractionID: "fxr_dddddddddddddddddddddddddddddddddddddddddddddddddddd",
			AnnotationID: testAnnotationID,
			IssueID:      testIssueID,
		},
	}
	result, err := service.RetractFixAttempt(
		context.Background(),
		testIssueID,
		testAnnotationID,
		model.FixRetractionSuperseded,
		testKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Retraction.AnnotationID != testAnnotationID ||
		repository.retractionInput.RecordedVia != model.FixRecordedViaLocalUI {
		t.Fatalf("retraction result=%+v input=%+v", result, repository.retractionInput)
	}
	repository.retractErr = local.ErrFixAlreadyRetracted
	if _, err := service.RetractFixAttempt(
		context.Background(),
		testIssueID,
		testAnnotationID,
		model.FixRetractionOther,
		testKey,
	); !errors.Is(err, ErrAlreadyRetracted) {
		t.Fatalf("already-retracted error = %v", err)
	}
}

func TestFixHistoryCursorIsSnapshotStableAndIssueBound(t *testing.T) {
	recordedAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	repository := &actionTestRepository{
		page: model.FixAnnotationPage{
			Data: []model.FixAnnotation{{
				AnnotationID: testAnnotationID,
				IssueID:      testIssueID,
				RecordedAt:   recordedAt,
			}},
			AnnotationSnapshot:  5,
			RetractionSnapshot:  2,
			HasMore:             true,
			EvidenceEvaluatedAt: recordedAt.Add(time.Minute),
		},
	}
	service, err := New(repository, &actionTestTokens{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ListFixAttempts(context.Background(), testIssueID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == nil || !first.HasMore || first.ReturnedCount != 1 || first.Limit != 1 {
		t.Fatalf("first page = %+v", first)
	}
	repository.page = model.FixAnnotationPage{
		Data:                []model.FixAnnotation{},
		AnnotationSnapshot:  5,
		RetractionSnapshot:  2,
		EvidenceEvaluatedAt: recordedAt.Add(2 * time.Minute),
	}
	second, err := service.ListFixAttempts(
		context.Background(),
		testIssueID,
		1,
		*first.NextCursor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Data == nil ||
		repository.historyQuery.AnnotationSnapshot != 5 ||
		repository.historyQuery.RetractionSnapshot != 2 ||
		repository.historyQuery.Cursor == nil ||
		repository.historyQuery.Cursor.AnnotationID != testAnnotationID {
		t.Fatalf("second=%+v query=%+v", second, repository.historyQuery)
	}
	if _, err := service.ListFixAttempts(
		context.Background(),
		otherTestIssueID,
		1,
		*first.NextCursor,
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-issue cursor error = %v", err)
	}
	if _, err := service.ListFixAttempts(
		context.Background(),
		testIssueID,
		1,
		"PRIVATE_CURSOR",
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed cursor error = %v", err)
	}
}
