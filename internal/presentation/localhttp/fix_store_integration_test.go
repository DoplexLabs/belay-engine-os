package localhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/localaction"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestRealStoreFixEligibilityReplayHistoryRetractionAndExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 21, 0, 0, 0, time.UTC)
	storeNow := now
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &httpIntegrationKeyProvider{},
			Clock:       func() time.Time { return storeNow },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	event := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789b01",
		"session-fix-http-integration",
		1,
		now.Add(-time.Minute),
	)
	appended, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := httpIntegrationOccurrence(
		t,
		store,
		event,
		"explicit_command_failure",
		"command_failure",
		false,
	)
	occurrence.ScopeQuality = model.ScopeResolved
	commit, err := store.ReplaceIssueProjection(ctx, local.ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: appended.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeResolved,
		Occurrences:       []model.IssueOccurrence{occurrence},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.ProjectionGeneration <= 0 {
		t.Fatalf("projection commit = %+v", commit)
	}

	actions, err := localaction.New(
		store,
		store,
		localaction.WithClock(func() time.Time { return storeNow }),
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(
		readmodel.New(
			store,
			readmodel.WithIssueRepository(store),
			readmodel.WithIssueCursorCodec(store),
			readmodel.WithClock(func() time.Time { return storeNow }),
		),
		"launch-secret",
		WithFixService(actions),
	)
	if err != nil {
		t.Fatal(err)
	}
	const listener = "127.0.0.1:43123"
	handler := server.handler(listener)

	var issues readmodel.IssueList
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues",
		&issues,
	); status != http.StatusOK {
		t.Fatalf("issue list status = %d", status)
	}
	if len(issues.Data) != 1 || issues.Data[0].IssueID != occurrence.IssueID {
		t.Fatalf("issues = %+v", issues.Data)
	}

	var eligibility fixEligibilityResponse
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fix-eligibility?view_cursor="+issues.ViewCursor,
		&eligibility,
	); status != http.StatusOK {
		t.Fatalf("eligibility status = %d", status)
	}
	if !eligibility.Data.Eligible ||
		eligibility.Data.ActionToken == nil ||
		eligibility.Data.ExpiresAt == nil {
		t.Fatalf("eligibility = %+v", eligibility)
	}
	actionToken := *eligibility.Data.ActionToken

	create := func(key, kind, token string) (int, fixAnnotationResponse, problem) {
		t.Helper()
		request := validFixWriteRequest(
			http.MethodPost,
			"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes",
			`{"action_token":`+mustJSON(t, token)+`,"change_kind":`+mustJSON(t, kind)+`}`,
			"record-fix-attempt.v1",
		)
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var created fixAnnotationResponse
		var issue problem
		if response.Code < 400 {
			if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
		} else if err := json.NewDecoder(response.Body).Decode(&issue); err != nil {
			t.Fatal(err)
		}
		return response.Code, created, issue
	}

	status, created, _ := create(httpFixKey, "code_change", actionToken)
	if status != http.StatusCreated ||
		created.Replayed ||
		created.Data.AnnotationID == "" ||
		created.Data.RetractionReason != nil ||
		created.Data.RetractedAt != nil {
		t.Fatalf("created status=%d response=%+v", status, created)
	}
	status, replay, _ := create(httpFixKey, "code_change", actionToken)
	if status != http.StatusOK ||
		!replay.Replayed ||
		replay.Data.AnnotationID != created.Data.AnnotationID {
		t.Fatalf("replay status=%d response=%+v", status, replay)
	}
	status, _, conflict := create(httpFixKey, "configuration_change", actionToken)
	if status != http.StatusConflict ||
		conflict.Type != "belay.local/idempotency-conflict" {
		t.Fatalf("conflict status=%d problem=%+v", status, conflict)
	}
	tokenParts := strings.Split(actionToken, ".")
	tamperedSignature := tokenParts[1]
	replacement := byte('A')
	if tamperedSignature[0] == replacement {
		replacement = 'B'
	}
	tamperedSignature = string(replacement) + tamperedSignature[1:]
	tampered := tokenParts[0] + "." + tamperedSignature
	status, _, invalid := create(httpFixKey, "code_change", tampered)
	if status != http.StatusBadRequest || invalid.Type != "about:blank" {
		t.Fatalf("tampered replay status=%d problem=%+v", status, invalid)
	}

	var history fixHistoryResponse
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes?limit=20",
		&history,
	); status != http.StatusOK {
		t.Fatalf("history status = %d", status)
	}
	if len(history.Data) != 1 ||
		history.Data[0].AnnotationID != created.Data.AnnotationID ||
		history.Data[0].State != model.FixStateActive {
		t.Fatalf("history = %+v", history)
	}

	retractionKey := "22345678-1234-4234-9234-123456789abc"
	retractRequest := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fixes/"+created.Data.AnnotationID+"/retractions",
		`{"reason":"recorded_by_mistake"}`,
		"retract-fix-attempt.v1",
	)
	retractRequest.Header.Set("Idempotency-Key", retractionKey)
	retractResponse := httptest.NewRecorder()
	handler.ServeHTTP(retractResponse, retractRequest)
	if retractResponse.Code != http.StatusCreated {
		t.Fatalf("retraction status = %d body=%s", retractResponse.Code, retractResponse.Body.String())
	}
	retractReplay := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fixes/"+created.Data.AnnotationID+"/retractions",
		`{"reason":"recorded_by_mistake"}`,
		"retract-fix-attempt.v1",
	)
	retractReplay.Header.Set("Idempotency-Key", retractionKey)
	retractReplayResponse := httptest.NewRecorder()
	handler.ServeHTTP(retractReplayResponse, retractReplay)
	if retractReplayResponse.Code != http.StatusOK {
		t.Fatalf("retraction replay status = %d body=%s",
			retractReplayResponse.Code, retractReplayResponse.Body.String())
	}
	var replayedRetraction fixRetractionResponse
	if err := json.NewDecoder(retractReplayResponse.Body).Decode(&replayedRetraction); err != nil {
		t.Fatal(err)
	}
	if !replayedRetraction.Replayed {
		t.Fatalf("retraction replay = %+v", replayedRetraction)
	}
	anotherRetraction := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fixes/"+created.Data.AnnotationID+"/retractions",
		`{"reason":"superseded"}`,
		"retract-fix-attempt.v1",
	)
	anotherRetraction.Header.Set(
		"Idempotency-Key",
		"42345678-1234-4234-9234-123456789abc",
	)
	anotherRetractionResponse := httptest.NewRecorder()
	handler.ServeHTTP(anotherRetractionResponse, anotherRetraction)
	if anotherRetractionResponse.Code != http.StatusConflict {
		t.Fatalf("already-retracted status = %d body=%s",
			anotherRetractionResponse.Code, anotherRetractionResponse.Body.String())
	}
	var alreadyRetracted problem
	if err := json.NewDecoder(anotherRetractionResponse.Body).Decode(&alreadyRetracted); err != nil {
		t.Fatal(err)
	}
	if alreadyRetracted.Type != "belay.local/already-retracted" {
		t.Fatalf("already-retracted problem = %+v", alreadyRetracted)
	}

	history = fixHistoryResponse{}
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes",
		&history,
	); status != http.StatusOK {
		t.Fatalf("retracted history status = %d", status)
	}
	if history.Data[0].State != model.FixStateRetracted ||
		history.Data[0].RetractionReason == nil ||
		*history.Data[0].RetractionReason != "recorded_by_mistake" ||
		history.Data[0].RetractedAt == nil {
		t.Fatalf("retracted history = %+v", history.Data[0])
	}

	storeNow = now.Add(16 * time.Minute)
	status, expiredReplay, _ := create(httpFixKey, "code_change", actionToken)
	if status != http.StatusOK || !expiredReplay.Replayed {
		t.Fatalf("expired replay status=%d response=%+v", status, expiredReplay)
	}
	status, _, expired := create(
		"32345678-1234-4234-9234-123456789abc",
		"code_change",
		actionToken,
	)
	if status != http.StatusGone || expired.Type != "belay.local/cursor-expired" {
		t.Fatalf("expired new write status=%d problem=%+v", status, expired)
	}

	if _, err := store.ReplaceIssueProjection(ctx, local.ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: appended.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeResolved,
		Occurrences:       nil,
	}); err != nil {
		t.Fatal(err)
	}
	history = fixHistoryResponse{}
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes",
		&history,
	); status != http.StatusOK || len(history.Data) != 1 {
		t.Fatalf("history after issue disappearance status=%d history=%+v", status, history)
	}
}

func TestRealStoreFixHistoryAndIdempotentReplaySurviveRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 22, 0, 0, 0, time.UTC)
	storeNow := now
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := &httpIntegrationKeyProvider{}
	openStore := func() *local.Store {
		t.Helper()
		store, err := local.OpenWithOptions(
			path,
			local.OpenOptions{
				KeyProvider: provider,
				Clock:       func() time.Time { return storeNow },
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	newHandler := func(store *local.Store) http.Handler {
		t.Helper()
		actions, err := localaction.New(
			store,
			store,
			localaction.WithClock(func() time.Time { return storeNow }),
		)
		if err != nil {
			t.Fatal(err)
		}
		server, err := New(
			readmodel.New(
				store,
				readmodel.WithIssueRepository(store),
				readmodel.WithIssueCursorCodec(store),
				readmodel.WithClock(func() time.Time { return storeNow }),
			),
			"launch-secret",
			WithFixService(actions),
		)
		if err != nil {
			t.Fatal(err)
		}
		return server.handler("127.0.0.1:43123")
	}
	const listener = "127.0.0.1:43123"
	const createKey = "52345678-1234-4234-9234-123456789abc"
	const retractKey = "62345678-1234-4234-9234-123456789abc"

	store := openStore()
	event := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789b02",
		"session-fix-http-restart",
		1,
		now.Add(-time.Minute),
	)
	appended, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := httpIntegrationOccurrence(
		t,
		store,
		event,
		"explicit_command_failure",
		"command_failure",
		false,
	)
	occurrence.ScopeQuality = model.ScopeResolved
	if _, err := store.ReplaceIssueProjection(ctx, local.ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: appended.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeResolved,
		Occurrences:       []model.IssueOccurrence{occurrence},
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(store)

	var issues readmodel.IssueList
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues",
		&issues,
	); status != http.StatusOK || len(issues.Data) != 1 {
		t.Fatalf("issue list before restart status=%d issues=%+v", status, issues)
	}
	var eligibility fixEligibilityResponse
	if status := fixIntegrationGET(
		t,
		handler,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fix-eligibility?view_cursor="+issues.ViewCursor,
		&eligibility,
	); status != http.StatusOK ||
		!eligibility.Data.Eligible ||
		eligibility.Data.ActionToken == nil {
		t.Fatalf("eligibility before restart status=%d response=%+v", status, eligibility)
	}
	actionToken := *eligibility.Data.ActionToken

	createRequest := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes",
		`{"action_token":`+mustJSON(t, actionToken)+`,"change_kind":"code_change"}`,
		"record-fix-attempt.v1",
	)
	createRequest.Header.Set("Idempotency-Key", createKey)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create before restart status=%d body=%s",
			createResponse.Code, createResponse.Body.String())
	}
	var created fixAnnotationResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	retractRequest := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fixes/"+created.Data.AnnotationID+"/retractions",
		`{"reason":"recorded_by_mistake"}`,
		"retract-fix-attempt.v1",
	)
	retractRequest.Header.Set("Idempotency-Key", retractKey)
	retractResponse := httptest.NewRecorder()
	handler.ServeHTTP(retractResponse, retractRequest)
	if retractResponse.Code != http.StatusCreated {
		t.Fatalf("retract before restart status=%d body=%s",
			retractResponse.Code, retractResponse.Body.String())
	}
	var retracted fixRetractionResponse
	if err := json.NewDecoder(retractResponse.Body).Decode(&retracted); err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = nil
	handler = nil

	storeNow = now.Add(16 * time.Minute)
	reopened := openStore()
	defer reopened.Close()
	restartedHandler := newHandler(reopened)

	var history fixHistoryResponse
	if status := fixIntegrationGET(
		t,
		restartedHandler,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes",
		&history,
	); status != http.StatusOK ||
		len(history.Data) != 1 ||
		history.Data[0].AnnotationID != created.Data.AnnotationID ||
		history.Data[0].State != model.FixStateRetracted ||
		history.Data[0].RetractionReason == nil ||
		*history.Data[0].RetractionReason != string(model.FixRetractionRecordedByMistake) ||
		history.Data[0].RetractedAt == nil {
		t.Fatalf("history after restart status=%d history=%+v", status, history)
	}

	replayCreate := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+"/fixes",
		`{"action_token":`+mustJSON(t, actionToken)+`,"change_kind":"code_change"}`,
		"record-fix-attempt.v1",
	)
	replayCreate.Header.Set("Idempotency-Key", createKey)
	replayCreateResponse := httptest.NewRecorder()
	restartedHandler.ServeHTTP(replayCreateResponse, replayCreate)
	if replayCreateResponse.Code != http.StatusOK {
		t.Fatalf("create replay after restart status=%d body=%s",
			replayCreateResponse.Code, replayCreateResponse.Body.String())
	}
	var replayedCreate fixAnnotationResponse
	if err := json.NewDecoder(replayCreateResponse.Body).Decode(&replayedCreate); err != nil {
		t.Fatal(err)
	}
	if !replayedCreate.Replayed ||
		replayedCreate.Data.AnnotationID != created.Data.AnnotationID ||
		replayedCreate.Data.State != model.FixStateRetracted {
		t.Fatalf("create replay after restart = %+v", replayedCreate)
	}

	replayRetraction := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+occurrence.IssueID+
			"/fixes/"+created.Data.AnnotationID+"/retractions",
		`{"reason":"recorded_by_mistake"}`,
		"retract-fix-attempt.v1",
	)
	replayRetraction.Header.Set("Idempotency-Key", retractKey)
	replayRetractionResponse := httptest.NewRecorder()
	restartedHandler.ServeHTTP(replayRetractionResponse, replayRetraction)
	if replayRetractionResponse.Code != http.StatusOK {
		t.Fatalf("retraction replay after restart status=%d body=%s",
			replayRetractionResponse.Code, replayRetractionResponse.Body.String())
	}
	var replayedRetraction fixRetractionResponse
	if err := json.NewDecoder(replayRetractionResponse.Body).Decode(&replayedRetraction); err != nil {
		t.Fatal(err)
	}
	if !replayedRetraction.Replayed ||
		replayedRetraction.Data.RetractionID != retracted.Data.RetractionID {
		t.Fatalf("retraction replay after restart = %+v", replayedRetraction)
	}
}

func fixIntegrationGET(
	t *testing.T,
	handler http.Handler,
	target string,
	value any,
) int {
	t.Helper()
	request := fixHTTPRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if value != nil {
		if err := json.NewDecoder(response.Body).Decode(value); err != nil {
			t.Fatalf("decode %s: %v; body=%s", target, err, response.Body.String())
		}
	}
	return response.Code
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestFixHistoryRejectsUnknownAndRepeatedQueryParameters(t *testing.T) {
	service := &fixHTTPService{page: localaction.FixAttemptPage{
		Data:                []model.FixAnnotation{},
		Limit:               20,
		EvidenceEvaluatedAt: time.Now().UTC(),
	}}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.handler("127.0.0.1:43123")
	for _, query := range []string{
		"unknown=PRIVATE_QUERY",
		"limit=1&limit=2",
		"cursor=a&cursor=b",
		"limit=zero",
		"limit=0",
		"cursor=",
		"bad=%zz",
	} {
		request := fixHTTPRequest(
			http.MethodGet,
			"http://127.0.0.1:43123/v1/issues/"+httpFixIssueID+"/fixes?"+query,
			nil,
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q status=%d body=%s", query, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "PRIVATE_QUERY") {
			t.Fatalf("query reflected: %s", response.Body.String())
		}
	}
}
