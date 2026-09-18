package detection

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

type testDetector struct {
	id       string
	called   *bool
	evaluate func(context.Context, SessionInput) ([]Match, error)
}

func (detector testDetector) ID() string                 { return detector.id }
func (detector testDetector) Version() string            { return "test-version" }
func (detector testDetector) FingerprintVersion() string { return "test-fingerprint" }

func (detector testDetector) Evaluate(
	ctx context.Context,
	input SessionInput,
) (DetectorResult, error) {
	if detector.called != nil {
		*detector.called = true
	}
	matches, err := detector.evaluate(ctx, input)
	return DetectorResult{
		Matches:           matches,
		AbsenceCapability: AbsenceSupported,
	}, err
}

func TestCatalogIsolatesPanicErrorAndContinues(t *testing.T) {
	goodCalled := false
	catalog, err := NewCatalog(
		testDetector{
			id: "panic",
			evaluate: func(context.Context, SessionInput) ([]Match, error) {
				panic("event-derived secret must not escape")
			},
		},
		testDetector{
			id: "error",
			evaluate: func(context.Context, SessionInput) ([]Match, error) {
				return nil, errors.New("event-derived secret must not escape")
			},
		},
		testDetector{
			id:     "good",
			called: &goodCalled,
			evaluate: func(context.Context, SessionInput) ([]Match, error) {
				return []Match{validTestMatch("good")}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := catalog.Run(context.Background(), testInput())
	if result.Status != StatusFailed || len(result.Matches) != 0 || !goodCalled {
		t.Fatalf("result = %+v, good called = %v", result, goodCalled)
	}
	if got, want := result.Failures, []DetectorFailure{
		{DetectorID: "panic", Code: failurePanic},
		{DetectorID: "error", Code: failureError},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failures = %+v, want %+v", got, want)
	}
	for _, failure := range result.Failures {
		if failure.Code == "event-derived secret must not escape" {
			t.Fatalf("raw error leaked into failure: %+v", failure)
		}
	}
	for _, applicability := range result.Applicability {
		if applicability.DetectorID == "panic" ||
			applicability.DetectorID == "error" {
			if applicability.AbsenceCapability != AbsenceIncomplete {
				t.Fatalf("failed detector applicability = %+v", applicability)
			}
		}
	}
}

func TestBuiltinAbsenceCapabilitiesRequireObservedPrerequisites(t *testing.T) {
	command := testEvent("command", 1, "command.exec")
	result := DefaultCatalog().Run(context.Background(), testInput(command))
	requireCurrent(t, result)
	byID := make(map[string]DetectorApplicability)
	for _, applicability := range result.Applicability {
		byID[applicability.DetectorID] = applicability
	}
	if byID["explicit_command_failure"].AbsenceCapability != AbsenceSupported {
		t.Fatalf("command applicability = %+v", byID["explicit_command_failure"])
	}
	if byID["explicit_tool_failure"].AbsenceCapability != AbsenceIncomplete ||
		byID["explicit_tool_failure"].UnavailableReason != "tool_failure_absence_unsupported" {
		t.Fatalf("tool failure applicability = %+v", byID["explicit_tool_failure"])
	}
	if byID["repeated_command_attempts"].AbsenceCapability != AbsenceNotApplicable {
		t.Fatalf("experimental applicability = %+v", byID["repeated_command_attempts"])
	}
	if byID["retained_verification_gap_after_changes"].AbsenceCapability !=
		AbsenceNotApplicable {
		t.Fatalf("verification applicability = %+v",
			byID["retained_verification_gap_after_changes"])
	}
	if byID["unresolved_verification_failure_at_completion"].AbsenceCapability !=
		AbsenceIncomplete {
		t.Fatalf("completion applicability = %+v",
			byID["unresolved_verification_failure_at_completion"])
	}

	historical := command
	historical.Historical.IsHistorical = true
	historicalResult := DefaultCatalog().Run(
		context.Background(),
		testInput(historical),
	)
	requireCurrent(t, historicalResult)
	for _, applicability := range historicalResult.Applicability {
		if applicability.AbsenceCapability == AbsenceSupported {
			t.Fatalf("historical-only input advertised absence: %+v", applicability)
		}
	}
}

func TestCatalogDetectorDeadline(t *testing.T) {
	catalog, err := NewCatalog(testDetector{
		id: "slow",
		evaluate: func(ctx context.Context, _ SessionInput) ([]Match, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := catalog.WithDetectorTimeout(time.Millisecond).Run(
		context.Background(),
		testInput(),
	)
	if result.Status != StatusFailed ||
		len(result.Failures) != 1 ||
		result.Failures[0].Code != failureTimeout {
		t.Fatalf("deadline result = %+v", result)
	}
}

func TestCatalogBoundsNonCooperativeDetectorWorkers(t *testing.T) {
	blocked := make(chan struct{})
	var started atomic.Int32
	catalog, err := NewCatalog(testDetector{
		id: "stuck",
		evaluate: func(context.Context, SessionInput) ([]Match, error) {
			started.Add(1)
			<-blocked
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog = catalog.WithDetectorTimeout(time.Millisecond)

	first := catalog.Run(context.Background(), testInput())
	if first.Status != StatusFailed ||
		len(first.Failures) != 1 ||
		first.Failures[0].Code != failureTimeout {
		t.Fatalf("first timeout = %+v", first)
	}
	const repeats = 100
	results := make(chan CatalogResult, repeats)
	var wait sync.WaitGroup
	for index := 0; index < repeats; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- catalog.Run(context.Background(), testInput())
		}()
	}
	wait.Wait()
	close(results)
	index := 0
	for result := range results {
		if result.Status != StatusFailed ||
			len(result.Failures) != 1 ||
			result.Failures[0].Code != failureTimeout {
			t.Fatalf("repeat %d = %+v", index, result)
		}
		index++
	}
	if got := started.Load(); got != 1 {
		t.Fatalf("workers started = %d, want 1", got)
	}

	close(blocked)
	deadline := time.Now().Add(time.Second)
	for {
		result := catalog.Run(context.Background(), testInput())
		if result.Status == StatusCurrent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detector slot did not recover: %+v", result)
		}
		time.Sleep(time.Millisecond)
	}
	if got := started.Load(); got != 2 {
		t.Fatalf("workers started after release = %d, want 2", got)
	}
}

func TestCatalogRejectsInvalidAndDuplicateOutput(t *testing.T) {
	tests := []struct {
		name    string
		matches []Match
	}{
		{
			name:    "invalid",
			matches: []Match{{DetectorID: "custom"}},
		},
		{
			name: "duplicate fingerprint",
			matches: []Match{
				validTestMatch("custom"),
				validTestMatch("custom"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := NewCatalog(testDetector{
				id: "custom",
				evaluate: func(context.Context, SessionInput) ([]Match, error) {
					return test.matches, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			result := catalog.Run(context.Background(), testInput())
			if result.Status != StatusFailed ||
				len(result.Matches) != 0 ||
				len(result.Failures) != 1 ||
				result.Failures[0].Code != failureInvalidOutput {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestCatalogConstructorRejectsInvalidDetectors(t *testing.T) {
	valid := testDetector{
		id: "duplicate",
		evaluate: func(context.Context, SessionInput) ([]Match, error) {
			return nil, nil
		},
	}
	if _, err := NewCatalog(valid, valid); err == nil {
		t.Fatal("duplicate detector ID accepted")
	}
	var nilDetector Detector
	if _, err := NewCatalog(nilDetector); err == nil {
		t.Fatal("nil detector accepted")
	}
}

func TestCatalogRejectsInvalidSessionWithoutPanicking(t *testing.T) {
	result := DefaultCatalog().Run(context.Background(), SessionInput{})
	if result.Status != StatusFailed || len(result.Failures) != 1 {
		t.Fatalf("empty input result = %+v", result)
	}

	event := testEvent("event", 1, "command.exec")
	event.Session.Key = "different-session"
	result = DefaultCatalog().Run(context.Background(), testInput(event))
	if result.Status != StatusFailed || len(result.Failures) != 1 {
		t.Fatalf("mismatched session result = %+v", result)
	}

	result = DefaultCatalog().Run(nil, testInput())
	if result.Status != StatusFailed ||
		len(result.Failures) != 1 ||
		result.Failures[0].Code != failureCanceled {
		t.Fatalf("nil context result = %+v", result)
	}
}

func TestCatalogDoesNotMutateInputAndIsDeterministic(t *testing.T) {
	failedA := withOutcome(testEvent("failed-a", 3, "command.result"), "failed", nil)
	failedB := withOutcome(testEvent("failed-b", 1, "command.result"), "failed", nil)
	denied := testEvent("denied", 2, "permission.denied")
	input := testInput(failedA, failedB, denied)
	enrichCommand(&input, "failed-a", "sig-b", CommandClassOther)
	enrichCommand(&input, "failed-b", "sig-a", CommandClassOther)
	input.Enrichments["denied"] = EventEnrichment{PermissionClass: "network"}
	originalEvents := append([]interface{}(nil),
		input.Events[0].EventID,
		input.Events[1].EventID,
		input.Events[2].EventID,
	)

	first := DefaultCatalog().Run(context.Background(), input)
	second := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, first)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("results differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if got := []interface{}{
		input.Events[0].EventID,
		input.Events[1].EventID,
		input.Events[2].EventID,
	}; !reflect.DeepEqual(got, originalEvents) {
		t.Fatalf("input order mutated: got %v, want %v", got, originalEvents)
	}
	for index := 1; index < len(first.Matches); index++ {
		if first.Matches[index-1].DetectorID > first.Matches[index].DetectorID {
			t.Fatalf("matches not deterministically ordered: %+v", first.Matches)
		}
	}
}

func TestFingerprintStableAcrossEventIDsAndImportOrder(t *testing.T) {
	build := func(firstID, secondID string, reverse bool) SessionInput {
		first := withOutcome(testEvent(firstID, 1, "command.result"), "failed", nil)
		second := withOutcome(testEvent(secondID, 2, "command.result"), "failed", nil)
		events := []model.Event{first, second}
		if reverse {
			events[0], events[1] = events[1], events[0]
		}
		input := testInput(events...)
		enrichCommand(&input, firstID, "opaque-signature", CommandClassBuild)
		enrichCommand(&input, secondID, "opaque-signature", CommandClassBuild)
		return input
	}

	first := DefaultCatalog().Run(
		context.Background(),
		build("random-event-a", "random-event-b", false),
	)
	second := DefaultCatalog().Run(
		context.Background(),
		build("different-event-x", "different-event-y", true),
	)
	requireCurrent(t, first)
	requireCurrent(t, second)
	left, leftOK := findMatch(t, first, "explicit_command_failure")
	right, rightOK := findMatch(t, second, "explicit_command_failure")
	if !leftOK || !rightOK || !reflect.DeepEqual(left.Fingerprint, right.Fingerprint) {
		t.Fatalf("fingerprints differ:\nleft=%+v\nright=%+v", left, right)
	}
}

func TestUnscopedInputUsesSessionFingerprintDimension(t *testing.T) {
	event := withOutcome(testEvent("failed", 1, "command.result"), "failed", nil)
	input := testInput(event)
	input.ProjectScope = ""
	input.ScopeQuality = "unscoped"
	enrichCommand(&input, "failed", "sig-a", CommandClassOther)
	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	match, ok := findMatch(t, result, "explicit_command_failure")
	if !ok {
		t.Fatalf("missing match: %+v", result.Matches)
	}
	if got, want := match.Fingerprint[0], (FingerprintDimension{
		Name: "command_signature_id", Value: "sig-a",
	}); got != want {
		t.Fatalf("first dimension = %+v, want %+v", got, want)
	}
	foundSession := false
	for _, dimension := range match.Fingerprint {
		foundSession = foundSession ||
			(dimension.Name == "session_id" && dimension.Value == "session-1")
		if dimension.Name == "project_scope_id" {
			t.Fatalf("unscoped fingerprint contains project scope: %+v", match.Fingerprint)
		}
	}
	if !foundSession {
		t.Fatalf("unscoped fingerprint lacks session ID: %+v", match.Fingerprint)
	}
}

func validTestMatch(detectorID string) Match {
	return Match{
		DetectorID:          detectorID,
		DetectorVersion:     "test-version",
		FingerprintVersion:  "test-fingerprint",
		Category:            "test-category",
		TitleCode:           "test.title",
		SuggestedActionType: "inspect",
		Severity:            SeverityInfo,
		Confidence:          ConfidenceLow,
		FirstObservedAt:     testEpoch,
		LastObservedAt:      testEpoch,
		CitedEventIDs:       []string{"event-1"},
		EvidenceComplete:    true,
		Fingerprint: []FingerprintDimension{{
			Name:  "test",
			Value: "value",
		}},
	}
}

func TestCatalogEntriesDefensivelyCopied(t *testing.T) {
	catalog := DefaultCatalog()
	entries := catalog.Entries()
	entries[0].DetectorID = "mutated"
	if got := catalog.Entries()[0].DetectorID; got != "explicit_command_failure" {
		t.Fatalf("catalog entry mutated through returned slice: %q", got)
	}
}

func TestFailureCodeNeverContainsRawErrorText(t *testing.T) {
	if got := failureCode(fmt.Errorf("sensitive command output")); got != failureError {
		t.Fatalf("failure code = %q", got)
	}
}
