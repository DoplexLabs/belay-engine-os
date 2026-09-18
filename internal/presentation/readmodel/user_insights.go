package readmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	DebriefStatus string                      `json:"debrief_status"`
	Debrief       *userinsights.DebriefRecord `json:"debrief"`
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

	for _, session := range candidates {
		if len(result.Sessions) >= limit {
			result.Coverage.HasMore = true
			break
		}
		if session.Coverage != transcript.CoverageComplete {
			result.Coverage.SkippedIncomplete++
			continue
		}
		if session.UserTurnCount < userInsightMinUserTurns {
			result.Coverage.SkippedShort++
			continue
		}
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
