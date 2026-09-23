package readmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

// UserInsightsProjectionVersion identifies the Habits read model. It is
// independent of every issue, report, and Mission Pack projection.
const UserInsightsProjectionVersion = "belay.user-insights.v1"

const (
	defaultUserInsightSessionLimit = 8
	maxUserInsightSessionLimit     = 25
	userInsightCandidateLimit      = 100
	userInsightTurnLimit           = 10000
	userInsightMinUserTurns        = 2
	userInsightSignalLimit         = 5
)

// ErrUserInsightsUnavailable is returned when Local has no transcript
// repository to read from.
var ErrUserInsightsUnavailable = errors.New("user insights unavailable")

// UserInsightRepository is the narrow transcript read surface the Habits view
// needs. The encrypted local store satisfies it.
type UserInsightRepository interface {
	QueryTranscriptSessions(
		context.Context,
		transcript.SessionQuery,
	) ([]transcript.Session, error)
	QueryTranscriptTurns(context.Context, string, int) ([]transcript.Turn, error)
}

// HabitDebriefRepository reads harness-generated debriefs for sessions.
type HabitDebriefRepository interface {
	QueryHabitDebriefs(context.Context, []string) (map[string]userinsights.DebriefRecord, error)
}

// WithHabitDebriefRepository wires stored debriefs into the Habits view.
func WithHabitDebriefRepository(repository HabitDebriefRepository) Option {
	return func(service *Service) {
		if service != nil {
			service.habitDebriefRepository = repository
		}
	}
}

// WithUserInsightHarness reports which installed harness can generate
// debriefs, so the browser can offer generation only when it will work.
func WithUserInsightHarness(lookup func() (string, bool)) Option {
	return func(service *Service) {
		if service != nil {
			service.userInsightHarness = lookup
		}
	}
}

// WithUserInsightRepository wires the Habits read model explicitly. Passing
// the transcript repository through WithTranscriptRepository also wires it
// when the repository exposes turn reads.
func WithUserInsightRepository(repository UserInsightRepository) Option {
	return func(service *Service) {
		if service != nil {
			service.userInsightRepository = repository
			if debriefs, ok := repository.(HabitDebriefRepository); ok &&
				service.habitDebriefRepository == nil {
				service.habitDebriefRepository = debriefs
			}
		}
	}
}

type UserInsightsRequest struct {
	Limit int
}

// UserInsightSession is one session debrief with a safe project label. It
// never carries transcript prose, raw commands, or file paths.
type UserInsightSession struct {
	userinsights.Retro
	Project       string                      `json:"project"`
	Signals       []UserInsightSignal         `json:"signals"`
	DebriefStatus string                      `json:"debrief_status"`
	Debrief       *userinsights.DebriefRecord `json:"debrief"`
}

// UserInsightSignal is a safe, deterministic summary of one persisted
// evidence episode. It intentionally excludes commands, paths, transcript
// text, and internal derivation metadata.
type UserInsightSignal struct {
	EpisodeID string          `json:"episode_id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Summary   string          `json:"summary"`
	FirstTurn int64           `json:"first_turn"`
	LastTurn  int64           `json:"last_turn"`
	Cost      issueintel.Cost `json:"cost"`
}

const (
	DebriefStatusReady   = "ready"
	DebriefStatusStale   = "stale"
	DebriefStatusMissing = "missing"
)

type UserInsightsHarness struct {
	Available bool   `json:"available"`
	Name      string `json:"name"`
}

type UserInsightsCoverage struct {
	CandidateSessions int  `json:"candidate_sessions"`
	EvaluatedSessions int  `json:"evaluated_sessions"`
	SkippedIncomplete int  `json:"skipped_incomplete"`
	SkippedShort      int  `json:"skipped_short"`
	SkippedUnreadable int  `json:"skipped_unreadable"`
	TurnsTruncated    int  `json:"turns_truncated"`
	HasMore           bool `json:"has_more"`
}

type UserInsights struct {
	SchemaVersion     string               `json:"schema_version"`
	ProjectionVersion string               `json:"projection_version"`
	AnalysisVersion   string               `json:"analysis_version"`
	GeneratedAt       time.Time            `json:"generated_at"`
	Sessions          []UserInsightSession `json:"sessions"`
	Harness           UserInsightsHarness  `json:"harness"`
	Coverage          UserInsightsCoverage `json:"coverage"`
	Limitations       []string             `json:"limitations"`
}

// GetUserInsights builds debriefs for the developer's most recent complete
// sessions. Each debrief reads only that session's own turns, and each
// baseline reads only session metadata from the same project.
func (s *Service) GetUserInsights(
	ctx context.Context,
	request UserInsightsRequest,
) (UserInsights, error) {
	if s == nil || s.userInsightRepository == nil {
		return UserInsights{}, fmt.Errorf(
			"%w: transcript repository is required",
			ErrUserInsightsUnavailable,
		)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = defaultUserInsightSessionLimit
	}
	if limit > maxUserInsightSessionLimit {
		return UserInsights{}, ErrInvalidRequest
	}
	candidates, err := s.userInsightRepository.QueryTranscriptSessions(
		ctx,
		transcript.SessionQuery{Limit: userInsightCandidateLimit},
	)
	if err != nil {
		return UserInsights{}, err
	}
	result := UserInsights{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: UserInsightsProjectionVersion,
		AnalysisVersion:   userinsights.AnalysisVersion,
		GeneratedAt:       s.nowUTC(),
		Sessions:          make([]UserInsightSession, 0, limit),
		Limitations:       []string{},
	}
	result.Coverage.CandidateSessions = len(candidates)
	result.Coverage.HasMore = len(candidates) >= userInsightCandidateLimit
	if s.userInsightHarness != nil {
		name, ok := s.userInsightHarness()
		result.Harness = UserInsightsHarness{Available: ok, Name: strings.TrimSpace(name)}
	}

	eligible := make([]transcript.Session, 0, len(candidates))
	for _, session := range candidates {
		if session.Coverage != transcript.CoverageComplete {
			result.Coverage.SkippedIncomplete++
			continue
		}
		if session.UserTurnCount < userInsightMinUserTurns {
			result.Coverage.SkippedShort++
			continue
		}
		eligible = append(eligible, session)
	}
	sessionKeys := make([]string, 0, len(eligible))
	for _, session := range eligible {
		sessionKeys = append(sessionKeys, session.SessionKey)
	}
	episodes := s.evidenceEpisodesForSessions(ctx, sessionKeys)
	selected := prioritizeUserInsightSessions(eligible, episodes, limit)
	if len(eligible) > len(selected) {
		result.Coverage.HasMore = true
	}

	for _, session := range selected {
		turns, err := s.userInsightRepository.QueryTranscriptTurns(
			ctx,
			session.SessionKey,
			userInsightTurnLimit,
		)
		if err != nil {
			if ctx.Err() != nil {
				return UserInsights{}, err
			}
			result.Coverage.SkippedUnreadable++
			continue
		}
		if len(turns) >= userInsightTurnLimit {
			result.Coverage.TurnsTruncated++
		}
		measurements := userinsights.Analyze(
			session,
			turns,
			issueintel.ProjectConfig{},
		)
		baseline := userinsights.ComputeBaseline(session, measurements, candidates)
		retro := userinsights.BuildRetro(session, measurements, baseline)
		result.Sessions = append(result.Sessions, UserInsightSession{
			Retro:         retro,
			Project:       transcriptProjectLabel(session),
			Signals:       userInsightSignals(episodes[session.SessionKey]),
			DebriefStatus: DebriefStatusMissing,
		})
		result.Coverage.EvaluatedSessions++
	}
	if s.habitDebriefRepository != nil && len(result.Sessions) > 0 {
		keys := make([]string, 0, len(result.Sessions))
		for _, session := range result.Sessions {
			keys = append(keys, session.SessionKey)
		}
		debriefs, err := s.habitDebriefRepository.QueryHabitDebriefs(ctx, keys)
		if err != nil {
			result.Limitations = append(result.Limitations, "Stored debriefs could not be read for this view.")
		}
		for index := range result.Sessions {
			record, ok := debriefs[result.Sessions[index].SessionKey]
			if !ok {
				continue
			}
			copied := PublicDebriefRecord(record)
			result.Sessions[index].Debrief = &copied
			if record.PromptVersion == userinsights.DebriefPromptVersion {
				result.Sessions[index].DebriefStatus = DebriefStatusReady
			} else {
				result.Sessions[index].DebriefStatus = DebriefStatusStale
			}
		}
	}

	if result.Coverage.TurnsTruncated > 0 {
		result.Limitations = append(
			result.Limitations,
			"Some sessions were longer than Belay reads for a debrief, so their later turns were not counted.",
		)
	}
	if result.Coverage.SkippedIncomplete > 0 {
		result.Limitations = append(
			result.Limitations,
			"Live and partially captured sessions are skipped until they finish.",
		)
	}
	if len(result.Sessions) == 0 && len(candidates) == 0 {
		result.Limitations = append(
			result.Limitations,
			"No retained transcript sessions were found yet.",
		)
	}
	for index := range result.Sessions {
		result.Sessions[index].Project = strings.TrimSpace(result.Sessions[index].Project)
	}
	return result, nil
}

func prioritizeUserInsightSessions(
	sessions []transcript.Session,
	episodes map[string][]evidenceepisode.Episode,
	limit int,
) []transcript.Session {
	if limit <= 0 || len(sessions) == 0 {
		return []transcript.Session{}
	}
	result := make([]transcript.Session, 0, min(limit, len(sessions)))
	selected := make(map[string]bool, min(limit, len(sessions)))
	priorityLimit := min(3, limit)
	for _, session := range sessions {
		if len(userInsightSignals(episodes[session.SessionKey])) == 0 {
			continue
		}
		result = append(result, session)
		selected[session.SessionKey] = true
		if len(result) >= priorityLimit {
			break
		}
	}
	for _, session := range sessions {
		if len(result) >= limit {
			break
		}
		if selected[session.SessionKey] {
			continue
		}
		result = append(result, session)
	}
	return result
}

func userInsightSignals(
	episodes []evidenceepisode.Episode,
) []UserInsightSignal {
	result := make([]UserInsightSignal, 0, len(episodes))
	for _, episode := range episodes {
		signal := UserInsightSignal{
			EpisodeID: episode.EpisodeID,
			Kind:      episode.Kind,
			FirstTurn: episode.FirstTurn,
			LastTurn:  episode.LastTurn,
			Cost:      episode.Cost,
		}
		switch episode.Kind {
		case evidenceepisode.KindFailureRepair:
			signal.Title = "A failed command was recovered"
			signal.Summary = "Belay observed an explicit failure followed by a different successful command in the same repair family."
		case evidenceepisode.KindMutationVerification:
			signal.Title = "Changes were followed by a passing check"
			signal.Summary = "Belay observed file changes followed by an explicit successful verification result."
		default:
			continue
		}
		result = append(result, signal)
		if len(result) >= userInsightSignalLimit {
			break
		}
	}
	if result == nil {
		return []UserInsightSignal{}
	}
	return result
}

// GetUserInsightDebrief reads one stored debrief without generating.
func (s *Service) GetUserInsightDebrief(ctx context.Context, sessionKey string) (userinsights.DebriefRecord, error) {
	if s == nil || s.habitDebriefRepository == nil {
		return userinsights.DebriefRecord{}, fmt.Errorf("%w: debrief repository is required", ErrUserInsightsUnavailable)
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(sessionKey) > 256 {
		return userinsights.DebriefRecord{}, ErrInvalidRequest
	}
	records, err := s.habitDebriefRepository.QueryHabitDebriefs(ctx, []string{sessionKey})
	if err != nil {
		return userinsights.DebriefRecord{}, err
	}
	record, ok := records[sessionKey]
	if !ok {
		return userinsights.DebriefRecord{}, ErrNotFound
	}
	return PublicDebriefRecord(record), nil
}

// PublicDebriefRecord strips storage-only identity from a debrief before it
// crosses the Local API boundary. The project is identified by its safe
// label on the session card, never by its raw path or remote.
func PublicDebriefRecord(record userinsights.DebriefRecord) userinsights.DebriefRecord {
	record.ProjectIdentity = ""
	return record
}
