package localhttp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

//go:embed assets/*
var assetFiles embed.FS

type Server struct {
	read         *readmodel.Service
	fix          FixService
	costFix      CostIssueFixService
	missionPacks MissionPackService
	token        string
	experience   Experience
	habits       HabitDebriefService
	updates      UpdateService
}

type RunningServer struct {
	URL   string
	Token string
	done  chan error
	http  *http.Server
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id"`
}

type Option func(*Server)

func WithFixService(service FixService) Option {
	return func(server *Server) {
		server.fix = service
	}
}

type CostIssueFixService interface {
	ProposeFix(
		context.Context,
		string,
		string,
		string,
	) (issueintel.FixRecord, error)
	RecordApplied(
		context.Context,
		string,
		string,
		string,
		string,
	) (issueintel.FixRecord, error)
	Status(context.Context, string) (issueintel.FixStatus, error)
}

func WithCostIssueFixService(service CostIssueFixService) Option {
	return func(server *Server) {
		server.costFix = service
	}
}

func WithExperience(experience Experience) Option {
	return func(server *Server) {
		server.experience = experience
	}
}

func New(read *readmodel.Service, token string, options ...Option) (*Server, error) {
	if read == nil {
		return nil, errors.New("local HTTP server requires a read service")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("local HTTP server requires a launch token")
	}
	server := &Server{
		read:       read,
		token:      token,
		experience: ExperienceCurrent,
	}
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	if !server.experience.Valid() {
		return nil, ErrInvalidExperience
	}
	return server, nil
}

func NewLaunchToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate local launch token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Server) Handler() http.Handler {
	return s.handler("")
}

func (s *Server) handler(trustedListener string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /v1/runtime", s.authorize(http.HandlerFunc(s.getRuntime)))
	if s.updates != nil {
		mux.Handle("GET /v1/update", s.authorize(http.HandlerFunc(s.getUpdate)))
		mux.Handle("POST /v1/update", s.authorize(http.HandlerFunc(s.postUpdate)))
	}
	mux.Handle("GET /v1/transcript-status", s.authorize(http.HandlerFunc(s.getTranscriptStatus)))
	mux.Handle("GET /v1/developer-brief", s.authorize(http.HandlerFunc(s.getDeveloperBrief)))
	mux.Handle("GET /v1/report", s.authorize(http.HandlerFunc(s.getReport)))
	mux.Handle("GET /v1/user-insights", s.authorize(http.HandlerFunc(s.getUserInsights)))
	mux.Handle("GET /v1/user-insights/{session_key}/debrief", s.authorize(http.HandlerFunc(s.getUserInsightDebrief)))
	if s.missionPacks != nil {
		mux.Handle(
			"GET /v1/mission-pack",
			s.authorize(http.HandlerFunc(s.getMissionPack)),
		)
	}
	mux.Handle("GET /v1/cost-issues", s.authorize(http.HandlerFunc(s.listCostIssues)))
	mux.Handle("GET /v1/cost-issues/{id}", s.authorize(http.HandlerFunc(s.getCostIssue)))
	if s.costFix != nil {
		s.registerCostIssueFixRoutes(mux, trustedListener)
	}
	mux.Handle("GET /v1/sessions", s.authorize(http.HandlerFunc(s.listSessions)))
	mux.Handle("GET /v1/sessions/{id}", s.authorize(http.HandlerFunc(s.getSession)))
	mux.Handle("GET /v1/sessions/{id}/events", s.authorize(http.HandlerFunc(s.getTimeline)))
	mux.Handle("GET /v1/sessions/{id}/events/lookup", s.authorize(http.HandlerFunc(s.lookupEvents)))
	mux.Handle("GET /v1/activity", s.authorize(http.HandlerFunc(s.queryActivity)))
	mux.Handle("GET /v1/findings", s.authorize(http.HandlerFunc(s.listFindings)))
	mux.Handle("GET /v1/issues", s.authorize(http.HandlerFunc(s.listIssues)))
	mux.Handle("GET /v1/issues/{id}/occurrences", s.authorize(http.HandlerFunc(s.getIssue)))
	mux.Handle("GET /v1/attention-families", s.authorize(http.HandlerFunc(s.listAttentionFamilies)))
	mux.Handle("GET /v1/attention-families/{family_id}", s.authorize(http.HandlerFunc(s.getAttentionFamily)))
	s.registerFixMonitoringRoutes(mux)
	mux.Handle("GET /v1/initialization", s.authorize(http.HandlerFunc(s.getInitialization)))
	mux.Handle("GET /v1/stats", s.authorize(http.HandlerFunc(s.getStats)))
	if s.fix != nil {
		s.registerFixRoutes(mux, trustedListener)
	}

	assets, err := fs.Sub(assetFiles, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServerFS(assets))
	return securityHeaders(loopbackOnly(mux))
}

func (s *Server) Start(ctx context.Context, address string) (*RunningServer, error) {
	if address == "" {
		address = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse Local listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("Belay Local may bind only to a loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for Belay Local: %w", err)
	}
	trustedListener := listener.Addr().String()
	if _, ok := trustedOrigin(trustedListener); !ok {
		_ = listener.Close()
		return nil, errors.New("Belay Local listener is not numeric loopback")
	}
	server := &http.Server{
		Handler:           s.handler(trustedListener),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	running := &RunningServer{
		URL:   "http://" + trustedListener,
		Token: s.token,
		done:  make(chan error, 1),
		http:  server,
	}
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		running.done <- err
		close(running.done)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return running, nil
}

func (r *RunningServer) BrowserURL() string {
	return r.URL + "/#token=" + url.QueryEscape(r.Token)
}

func (r *RunningServer) Wait() error {
	return <-r.done
}

func (r *RunningServer) Close(ctx context.Context) error {
	return r.http.Shutdown(ctx)
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(s.token)) != 1 {
			writeProblem(w, r, http.StatusUnauthorized, "Unauthorized", "A valid per-launch Local token is required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	after, err := optionalTime(r, "occurred_after")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "occurred_after must be an RFC3339 timestamp.")
		return
	}
	before, err := optionalTime(r, "occurred_before")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "occurred_before must be an RFC3339 timestamp.")
		return
	}
	response, err := s.read.ListSessionsPage(r.Context(), readmodel.SessionListRequest{
		Limit:          boundedInt(r, "limit", 20, 100),
		Cursor:         queryValue(r, "cursor"),
		Harness:        queryValue(r, "harness"),
		Outcome:        queryValue(r, "outcome"),
		History:        queryValue(r, "history"),
		OccurredAfter:  after,
		OccurredBefore: before,
		Query:          queryValue(r, "query"),
	})
	writeReadResult(w, r, response, err)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	detail, err := s.read.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeReadResult(w, r, nil, err)
		return
	}
	writeReadResult(w, r, readmodel.SessionDetailWithDiagnosis{
		SchemaVersion: detail.SchemaVersion,
		Data:          detail.Data,
		Episodes:      detail.Episodes,
		Diagnosis:     s.read.DiagnoseSession(r.Context(), detail),
		DataThrough:   detail.DataThrough,
	}, nil)
}

func (s *Server) getTimeline(w http.ResponseWriter, r *http.Request) {
	response, err := s.read.GetSessionTimelinePage(
		r.Context(),
		readmodel.TimelineRequest{
			SessionID: r.PathValue("id"),
			Limit:     boundedInt(r, "limit", 100, 500),
			Cursor:    queryValue(r, "cursor"),
		},
	)
	writeReadResult(w, r, response, err)
}

func (s *Server) lookupEvents(w http.ResponseWriter, r *http.Request) {
	parameters, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "The supplied read cursor or filters are invalid.")
		return
	}
	for name := range parameters {
		if name != "event_id" {
			writeProblem(w, r, http.StatusBadRequest, "Invalid request", "The supplied read cursor or filters are invalid.")
			return
		}
	}
	response, err := s.read.LookupSessionEvents(
		r.Context(),
		readmodel.EventLookupRequest{
			SessionID: r.PathValue("id"),
			EventIDs:  parameters["event_id"],
		},
	)
	writeReadResult(w, r, response, err)
}

func (s *Server) queryActivity(w http.ResponseWriter, r *http.Request) {
	filter := model.ActivityFilter{
		Harness:      queryValue(r, "harness"),
		ResourceKind: queryValue(r, "resource_kind"),
		Outcome:      queryValue(r, "outcome"),
		Limit:        boundedInt(r, "limit", 50, 200),
	}
	var err error
	filter.OccurredAfter, err = optionalTime(r, "occurred_after")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "occurred_after must be an RFC3339 timestamp.")
		return
	}
	filter.OccurredBefore, err = optionalTime(r, "occurred_before")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "occurred_before must be an RFC3339 timestamp.")
		return
	}
	response, err := s.read.QueryActivityPage(r.Context(), readmodel.ActivityRequest{
		Filter: filter,
		Cursor: queryValue(r, "cursor"),
	})
	writeReadResult(w, r, response, err)
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	since, err := optionalTime(r, "since")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "since must be an RFC3339 timestamp.")
		return
	}
	response, err := s.read.ListFindingsPage(r.Context(), readmodel.FindingListRequest{
		Limit:     boundedInt(r, "limit", 20, 100),
		Cursor:    queryValue(r, "cursor"),
		Since:     since,
		Severity:  queryValue(r, "severity"),
		SessionID: queryValue(r, "session_id"),
	})
	writeReadResult(w, r, response, err)
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(
		r,
		"limit",
		"cursor",
		"severity",
		"category",
		"harness",
		"origin",
		"analysis_status",
		"observed_after",
		"recurrence",
		"session_id",
		"fingerprint_id",
		"attention_kind",
		"experimental",
	)
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	cursor, cursorPresent, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	if cursorPresent {
		if len(parameters) != 1 {
			writeReadInvalidRequest(w, r)
			return
		}
		response, err := s.read.ListIssues(
			r.Context(),
			readmodel.IssueListRequest{Cursor: cursor},
		)
		writeReadResult(w, r, response, err)
		return
	}
	limit, err := optionalIssueLimit(parameters)
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	values := make(map[string]string, len(parameters))
	for _, name := range []string{
		"severity",
		"category",
		"harness",
		"origin",
		"analysis_status",
		"recurrence",
		"session_id",
		"fingerprint_id",
		"attention_kind",
		"experimental",
	} {
		value, present, err := optionalExactParameter(parameters, name)
		if err != nil {
			writeReadInvalidRequest(w, r)
			return
		}
		if present {
			values[name] = value
		}
	}
	var observedAfter *time.Time
	observedAfterValue, present, err := optionalExactParameter(
		parameters,
		"observed_after",
	)
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	if present {
		parsed, err := time.Parse(time.RFC3339Nano, observedAfterValue)
		if err != nil {
			writeReadInvalidRequest(w, r)
			return
		}
		parsed = parsed.UTC()
		observedAfter = &parsed
	}
	response, err := s.read.ListIssues(r.Context(), readmodel.IssueListRequest{
		Limit:          limit,
		Severity:       values["severity"],
		Category:       values["category"],
		Harness:        values["harness"],
		Origin:         values["origin"],
		AnalysisStatus: values["analysis_status"],
		ObservedAfter:  observedAfter,
		Recurrence:     values["recurrence"],
		SessionID:      values["session_id"],
		FingerprintID:  values["fingerprint_id"],
		AttentionKind:  values["attention_kind"],
		Experimental:   values["experimental"],
	})
	writeReadResult(w, r, response, err)
}

func optionalIssueLimit(parameters url.Values) (int, error) {
	value, present, err := optionalExactParameter(parameters, "limit")
	if err != nil || !present {
		return 0, err
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("invalid issue limit")
	}
	return limit, nil
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(r, "limit", "cursor", "view_cursor")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	cursor, cursorPresent, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	viewCursor, viewPresent, err := optionalExactParameter(parameters, "view_cursor")
	if err != nil || (cursorPresent && viewPresent) {
		writeReadInvalidRequest(w, r)
		return
	}
	if cursorPresent && len(parameters) != 1 {
		writeReadInvalidRequest(w, r)
		return
	}
	limit := 0
	if !cursorPresent {
		value, present, err := optionalExactParameter(parameters, "limit")
		if err != nil {
			writeReadInvalidRequest(w, r)
			return
		}
		if present {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 100 {
				writeReadInvalidRequest(w, r)
				return
			}
		}
	}
	response, err := s.read.GetIssue(r.Context(), readmodel.IssueDetailRequest{
		IssueID:    r.PathValue("id"),
		Limit:      limit,
		Cursor:     cursor,
		ViewCursor: viewCursor,
	})
	if err != nil {
		writeReadResult(w, r, nil, err)
		return
	}
	writeReadResult(w, r, readmodel.PresentIssueDetailV2(response), nil)
}

func writeReadInvalidRequest(w http.ResponseWriter, r *http.Request) {
	writeProblem(
		w,
		r,
		http.StatusBadRequest,
		"Invalid request",
		"The supplied read cursor or filters are invalid.",
	)
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	response, err := s.read.GetStats(r.Context())
	writeReadResult(w, r, response, err)
}

func (s *Server) getInitialization(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	response, err := s.read.GetInitialization()
	writeReadResult(w, r, response, err)
}

func writeReadResult(w http.ResponseWriter, r *http.Request, response any, err error) {
	if errors.Is(err, readmodel.ErrInvalidCursor) ||
		errors.Is(err, readmodel.ErrInvalidRequest) {
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", "The supplied read cursor or filters are invalid.")
		return
	}
	if errors.Is(err, readmodel.ErrCursorExpired) {
		writeProblemType(
			w,
			r,
			http.StatusGone,
			"belay.local/cursor-expired",
			"Cursor expired",
			"The issue view changed. Restart pagination without a cursor.",
		)
		return
	}
	if errors.Is(err, readmodel.ErrNotFound) {
		writeProblem(w, r, http.StatusNotFound, "Not found", "The requested issue is not available in this view.")
		return
	}
	if err != nil {
		writeProblem(w, r, http.StatusInternalServerError, "Local read failed", "Belay could not complete the local read.")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func queryValue(r *http.Request, name string) string {
	return strings.TrimSpace(r.URL.Query().Get(name))
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			writeProblem(w, r, http.StatusForbidden, "Forbidden", "Belay Local accepts loopback requests only.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func boundedInt(r *http.Request, name string, fallback, maximum int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func optionalTime(r *http.Request, name string) (*time.Time, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	writeProblemType(w, r, status, "about:blank", title, detail)
}

func writeProblemType(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	problemType string,
	title string,
	detail string,
) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:      problemType,
		Title:     title,
		Status:    status,
		Detail:    detail,
		RequestID: requestID(r),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestID(r *http.Request) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "local-request"
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
