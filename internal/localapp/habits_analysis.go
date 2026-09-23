package localapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

const (
	habitDebriefTurnLimit       = 10000
	habitDebriefSessionLimit    = 100
	habitDebriefDefaultBatch    = 5
	habitDebriefMinUserTurns    = 2
	habitDebriefRunTimeout      = 6 * time.Minute
	habitDebriefProjectLabelMax = 80
	habitDebriefPrecomputeCount = 3
	habitDebriefPrecomputeTick  = 30 * time.Second
	habitDebriefRetryBackoff    = time.Hour
)

var (
	ErrHabitHarnessUnavailable = errors.New("no Claude Code, Codex, Cursor CLI, or Antigravity CLI harness is installed for Habits")
	ErrHabitSessionNotFound    = errors.New("habit debrief session not found")
	ErrHabitSessionNotReady    = errors.New("habit debrief session is not complete yet")
	ErrHabitGenerationFailed   = errors.New("habit debrief generation failed")
)

// HabitDebriefStore is the storage surface the Habits debrief needs. The
// encrypted local store satisfies it.
type HabitDebriefStore interface {
	GetTranscriptSession(context.Context, string) (transcript.Session, error)
	QueryTranscriptTurns(context.Context, string, int) ([]transcript.Turn, error)
	QueryTranscriptSessions(context.Context, transcript.SessionQuery) ([]transcript.Session, error)
	QuerySessionTimeline(context.Context, model.TimelineQuery) (model.EventPage, error)
	GetHabitDebrief(context.Context, string) (userinsights.DebriefRecord, error)
	ReplaceHabitDebrief(context.Context, userinsights.DebriefRecord) error
}

// HabitDebriefService generates one session's debrief through the user's own
// installed harness. It makes no Belay network or model calls of its own.
type HabitDebriefService struct {
	store     HabitDebriefStore
	preferred string
	runner    ExperienceSemanticHarnessRunner
	harness   func(string) (SemanticHarness, bool)
	now       func() time.Time
}

type HabitDebriefServiceOption func(*HabitDebriefService)

// WithHabitDebriefRunner replaces the harness runner (tests, eval runners).
func WithHabitDebriefRunner(runner ExperienceSemanticHarnessRunner) HabitDebriefServiceOption {
	return func(service *HabitDebriefService) {
		if runner != nil {
			service.runner = runner
		}
	}
}

// WithHabitDebriefHarnessLookup replaces installed-harness detection.
func WithHabitDebriefHarnessLookup(lookup func(string) (SemanticHarness, bool)) HabitDebriefServiceOption {
	return func(service *HabitDebriefService) {
		if lookup != nil {
			service.harness = lookup
		}
	}
}

// WithHabitDebriefPreferredHarness sets the preferred harness name (auto,
// claude, codex, cursor, or antigravity).
func WithHabitDebriefPreferredHarness(preferred string) HabitDebriefServiceOption {
	return func(service *HabitDebriefService) {
		service.preferred = strings.TrimSpace(preferred)
	}
}

func WithHabitDebriefClock(clock func() time.Time) HabitDebriefServiceOption {
	return func(service *HabitDebriefService) {
		if clock != nil {
			service.now = clock
		}
	}
}

func NewHabitDebriefService(store HabitDebriefStore, options ...HabitDebriefServiceOption) (*HabitDebriefService, error) {
	if store == nil {
		return nil, errors.New("habit debrief service requires a store")
	}
	service := &HabitDebriefService{
		store:     store,
		preferred: "auto",
		runner:    RunInstalledExperienceSemanticHarness,
		harness:   InstalledSemanticHarness,
		now:       time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Harness reports which installed harness would generate debriefs.
func (s *HabitDebriefService) Harness() (string, bool) {
	harness, ok := s.harness(s.preferred)
	if !ok {
		return "", false
	}
	return string(harness), true
}

// Generate builds the evidence packet for one complete session, runs the
// harness, and stores the sanitized debrief. When refresh is false and a
// current debrief already exists for the same input, it is returned as is.
func (s *HabitDebriefService) Generate(ctx context.Context, sessionKey string, refresh bool) (userinsights.DebriefRecord, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(sessionKey) > 256 {
		return userinsights.DebriefRecord{}, ErrHabitSessionNotFound
	}
	harness, ok := s.harness(s.preferred)
	if !ok {
		return userinsights.DebriefRecord{}, ErrHabitHarnessUnavailable
	}
	session, err := s.store.GetTranscriptSession(ctx, sessionKey)
	if err != nil {
		return userinsights.DebriefRecord{}, ErrHabitSessionNotFound
	}
	if session.Coverage != transcript.CoverageComplete || session.UserTurnCount < habitDebriefMinUserTurns {
		return userinsights.DebriefRecord{}, ErrHabitSessionNotReady
	}
	turns, err := s.store.QueryTranscriptTurns(ctx, sessionKey, habitDebriefTurnLimit)
	if err != nil {
		return userinsights.DebriefRecord{}, err
	}
	events, _, err := loadTrajectoryCanonicalEvents(ctx, s.store, sessionKey)
	if err != nil {
		events = nil
	}
	packet := userinsights.BuildPacket(
		session,
		turns,
		events,
		habitProjectLabel(session),
		loadProjectConfig(session.ProjectPath),
	)
	prompt, err := userinsights.Prompt(packet)
	if err != nil {
		return userinsights.DebriefRecord{}, err
	}
	schema := userinsights.OutputSchema()
	inputHash := userinsights.InputHash(prompt, schema, string(harness))
	if !refresh {
		existing, err := s.store.GetHabitDebrief(ctx, sessionKey)
		if err == nil && existing.Harness == string(harness) &&
			existing.PromptVersion == userinsights.DebriefPromptVersion &&
			existing.InputHash == inputHash {
			return existing, nil
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, habitDebriefRunTimeout)
	defer cancel()
	result, err := s.runner(runCtx, harness, prompt, schema)
	if err != nil {
		return userinsights.DebriefRecord{}, fmt.Errorf("%w: %v", ErrHabitGenerationFailed, err)
	}
	debrief, notes, err := userinsights.DecodeDebrief(result.Output, packet)
	if err != nil {
		return userinsights.DebriefRecord{}, fmt.Errorf("%w: %v", ErrHabitGenerationFailed, err)
	}
	record := userinsights.DebriefRecord{
		SchemaVersion:   userinsights.DebriefSchemaVersion,
		SessionKey:      sessionKey,
		ProjectIdentity: session.ProjectIdentity,
		Harness:         string(harness),
		Model:           semanticModelName(result.Model),
		PromptVersion:   userinsights.DebriefPromptVersion,
		InputHash:       inputHash,
		GeneratedAt:     s.now().UTC(),
		Debrief:         debrief,
		Sanitization:    notes,
	}
	if err := s.store.ReplaceHabitDebrief(ctx, record); err != nil {
		return userinsights.DebriefRecord{}, err
	}
	return record, nil
}

type HabitAnalysisReport struct {
	Considered int      `json:"habit_sessions_considered"`
	Generated  int      `json:"habit_debriefs_generated"`
	Current    int      `json:"habit_debriefs_current"`
	Skipped    int      `json:"habit_sessions_skipped"`
	Failed     int      `json:"habit_debriefs_failed"`
	Failures   []string `json:"habit_failure_details,omitempty"`
}

// AnalyzeHabitSessionsOnce generates debriefs for the most recent complete
// sessions that do not yet have a current one. It is the batch path used by
// belay analyze; the browser uses Generate for one session at a time.
func (s *HabitDebriefService) AnalyzeHabitSessionsOnce(ctx context.Context, limit int) (HabitAnalysisReport, error) {
	if limit <= 0 {
		limit = habitDebriefDefaultBatch
	}
	var report HabitAnalysisReport
	if _, ok := s.harness(s.preferred); !ok {
		return report, ErrHabitHarnessUnavailable
	}
	sessions, err := s.store.QueryTranscriptSessions(ctx, transcript.SessionQuery{Limit: habitDebriefSessionLimit})
	if err != nil {
		return report, err
	}
	var failures []error
	for _, session := range sessions {
		if report.Generated >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(failures, err)...)
		}
		report.Considered++
		if session.Coverage != transcript.CoverageComplete || session.UserTurnCount < habitDebriefMinUserTurns {
			report.Skipped++
			continue
		}
		existing, existingErr := s.store.GetHabitDebrief(ctx, session.SessionKey)
		if existingErr == nil && existing.PromptVersion == userinsights.DebriefPromptVersion {
			report.Current++
			continue
		}
		if _, err := s.Generate(ctx, session.SessionKey, false); err != nil {
			report.Failed++
			report.Failures = append(report.Failures, clipSemanticText(err.Error(), maxSemanticErrorDetail))
			failures = append(failures, err)
			continue
		}
		report.Generated++
	}
	return report, errors.Join(failures...)
}

func habitProjectLabel(session transcript.Session) string {
	identity := strings.TrimSpace(session.ProjectIdentity)
	candidates := []string{identity, strings.TrimSpace(session.GitRemoteURL), strings.TrimSpace(session.ProjectPath)}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		candidate = strings.TrimSuffix(strings.TrimRight(strings.ReplaceAll(candidate, "\\", "/"), "/"), ".git")
		if index := strings.LastIndexAny(candidate, "/:"); index >= 0 {
			candidate = candidate[index+1:]
		}
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && candidate != "." {
			if len(candidate) > habitDebriefProjectLabelMax {
				candidate = candidate[:habitDebriefProjectLabelMax]
			}
			return candidate
		}
	}
	return strings.TrimSpace(session.Agent) + " project"
}

// PrecomputeOnce writes at most one missing debrief for the developer's most
// recent complete sessions, so the Report can show them without waiting.
// Failures are remembered in the supplied map and retried after a backoff.
// It returns true when a debrief was generated.
func (s *HabitDebriefService) PrecomputeOnce(
	ctx context.Context,
	limit int,
	failures map[string]time.Time,
) (bool, error) {
	if limit <= 0 {
		limit = habitDebriefPrecomputeCount
	}
	if _, ok := s.harness(s.preferred); !ok {
		return false, nil
	}
	sessions, err := s.store.QueryTranscriptSessions(ctx, transcript.SessionQuery{Limit: habitDebriefSessionLimit})
	if err != nil {
		return false, err
	}
	picked := 0
	for _, session := range sessions {
		if picked >= limit {
			break
		}
		if session.Coverage != transcript.CoverageComplete || session.UserTurnCount < habitDebriefMinUserTurns {
			continue
		}
		picked++
		key := session.SessionKey
		if failedAt, ok := failures[key]; ok && s.now().Sub(failedAt) < habitDebriefRetryBackoff {
			continue
		}
		existing, err := s.store.GetHabitDebrief(ctx, key)
		if err == nil && existing.PromptVersion == userinsights.DebriefPromptVersion {
			continue
		}
		if _, err := s.Generate(ctx, key, false); err != nil {
			if failures != nil {
				failures[key] = s.now()
			}
			return false, err
		}
		delete(failures, key)
		return true, nil
	}
	return false, nil
}

// PollHabitDebriefs keeps the most recent sessions debriefed in the
// background. It runs one generation per tick and stops with the context.
func PollHabitDebriefs(
	ctx context.Context,
	service *HabitDebriefService,
	interval time.Duration,
	limit int,
	onError func(error),
) {
	if service == nil {
		return
	}
	if interval <= 0 {
		interval = habitDebriefPrecomputeTick
	}
	failures := make(map[string]time.Time)
	run := func() {
		_, err := service.PrecomputeOnce(ctx, limit, failures)
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
