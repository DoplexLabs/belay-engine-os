package readmodel

import (
	"context"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/sessionidentity"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const TranscriptStatusSchemaVersion = "belay.transcript-status.v1"

const (
	defaultTranscriptSessionLimit = 10
	activeTranscriptWindow        = 5 * time.Minute
	recentTranscriptWindow        = 24 * time.Hour
	maxProjectLabelRunes          = 80
)

type TranscriptRepository interface {
	TranscriptCoverage(context.Context) (transcript.CoverageCounts, error)
	QueryTranscriptSessions(
		context.Context,
		transcript.SessionQuery,
	) ([]transcript.Session, error)
}

// SessionProjectRepository joins transcript metadata (project label,
// duration, cost) onto canonical session rows by session key.
type SessionProjectRepository interface {
	QueryTranscriptSessionsByKeys(
		context.Context,
		[]string,
	) (map[string]transcript.Session, error)
}

type SessionIdentityRepository interface {
	QueryActiveSessionIdentityAliases(
		context.Context,
		[]string,
	) (map[string]sessionidentity.ActiveAlias, error)
}

// WithSessionProjectRepository wires the transcript join for session rows.
func WithSessionProjectRepository(repository SessionProjectRepository) Option {
	return func(service *Service) {
		if service != nil {
			service.sessionProjectRepository = repository
		}
	}
}

func WithFusedSessionReads(enabled bool) Option {
	return func(service *Service) {
		if service != nil {
			service.fusedSessionReads = enabled
		}
	}
}

const sessionProjectJoinTimeout = 500 * time.Millisecond

// decorateSessionProjects adds the safe project label, duration, and cost to
// session rows when a transcript exists. Any failure leaves the rows as they
// were: the join is additive and never blocks the session list.
func (s *Service) decorateSessionProjects(
	ctx context.Context,
	sessions []model.SessionSummary,
) {
	if s == nil || s.sessionProjectRepository == nil || len(sessions) == 0 {
		return
	}
	keys := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if key := strings.TrimSpace(session.SessionID); key != "" && len(key) <= 256 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 || len(keys) > 100 {
		return
	}
	joinCtx, cancel := context.WithTimeout(ctx, sessionProjectJoinTimeout)
	defer cancel()
	records, err := s.sessionProjectRepository.QueryTranscriptSessionsByKeys(joinCtx, keys)
	if err != nil {
		return
	}
	aliases := make(map[string]sessionidentity.ActiveAlias)
	if s.fusedSessionReads && s.sessionIdentityRepository != nil {
		var missing []string
		for _, key := range keys {
			if _, ok := records[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			aliases, err = s.sessionIdentityRepository.
				QueryActiveSessionIdentityAliases(joinCtx, missing)
			if err == nil && len(aliases) > 0 {
				linkedKeys := make([]string, 0, len(aliases))
				seen := make(map[string]bool)
				for _, alias := range aliases {
					if alias.LinkedSessionKey != "" &&
						!seen[alias.LinkedSessionKey] {
						seen[alias.LinkedSessionKey] = true
						linkedKeys = append(linkedKeys, alias.LinkedSessionKey)
					}
				}
				linked, linkedErr := s.sessionProjectRepository.
					QueryTranscriptSessionsByKeys(joinCtx, linkedKeys)
				if linkedErr == nil {
					for key, value := range linked {
						records[key] = value
					}
				}
			}
		}
	}
	for index := range sessions {
		sessionKey := strings.TrimSpace(sessions[index].SessionID)
		record, ok := records[sessionKey]
		if !ok {
			if alias, exists := aliases[sessionKey]; exists {
				record, ok = records[alias.LinkedSessionKey]
			}
		}
		if !ok {
			continue
		}
		sessions[index].Project = transcriptProjectLabel(record)
		switch {
		case record.WallDurationMS > 0:
			sessions[index].DurationMS = record.WallDurationMS
		case !record.StartedAt.IsZero() && record.EndedAt.After(record.StartedAt):
			sessions[index].DurationMS = record.EndedAt.Sub(record.StartedAt).Milliseconds()
		}
		if record.TotalCostUSD != nil {
			cost := *record.TotalCostUSD
			sessions[index].CostUSD = &cost
		}
	}
}

type TranscriptSessionStatus struct {
	SessionKey     string                     `json:"session_key"`
	Agent          string                     `json:"agent"`
	Project        string                     `json:"project"`
	StartedAt      time.Time                  `json:"started_at"`
	EndedAt        *time.Time                 `json:"ended_at"`
	LastActivityAt time.Time                  `json:"last_activity_at"`
	Coverage       transcript.SessionCoverage `json:"coverage"`
	TurnCount      int                        `json:"turn_count"`
	Active         bool                       `json:"active"`
}

type TranscriptCoverageStatus struct {
	WithTranscript int `json:"with_transcript"`
	Partial        int `json:"partial"`
	Without        int `json:"without_transcript"`
	TranscriptOnly int `json:"transcript_only"`
}

type TranscriptStatus struct {
	SchemaVersion string                    `json:"schema_version"`
	Coverage      TranscriptCoverageStatus  `json:"coverage"`
	Sessions      []TranscriptSessionStatus `json:"sessions"`
}

func WithTranscriptRepository(repository TranscriptRepository) Option {
	return func(service *Service) {
		if service != nil {
			service.transcriptRepository = repository
			if insights, ok := repository.(UserInsightRepository); ok &&
				service.userInsightRepository == nil {
				service.userInsightRepository = insights
			}
			if projects, ok := repository.(SessionProjectRepository); ok &&
				service.sessionProjectRepository == nil {
				service.sessionProjectRepository = projects
			}
			if identities, ok := repository.(SessionIdentityRepository); ok &&
				service.sessionIdentityRepository == nil {
				service.sessionIdentityRepository = identities
			}
		}
	}
}

func (s *Service) GetTranscriptStatus(
	ctx context.Context,
) (TranscriptStatus, error) {
	if s == nil || s.transcriptRepository == nil {
		return TranscriptStatus{}, capabilityUnavailable()
	}
	coverage, err := s.transcriptRepository.TranscriptCoverage(ctx)
	if err != nil {
		return TranscriptStatus{}, err
	}
	sessions, err := s.transcriptRepository.QueryTranscriptSessions(
		ctx,
		transcript.SessionQuery{Limit: defaultTranscriptSessionLimit},
	)
	if err != nil {
		return TranscriptStatus{}, err
	}
	now := s.nowUTC()
	projected := make([]TranscriptSessionStatus, 0, len(sessions))
	for _, session := range sessions {
		lastActivity := session.EndedAt
		if lastActivity.IsZero() {
			lastActivity = session.StartedAt
		}
		age := now.Sub(lastActivity)
		recentlyActive := !lastActivity.IsZero() &&
			age >= 0 &&
			age <= activeTranscriptWindow
		active := recentlyActive &&
			(session.Coverage == transcript.CoverageLive ||
				session.EndedAt.IsZero())
		recent := !lastActivity.IsZero() &&
			age >= 0 &&
			age <= recentTranscriptWindow
		if !active && !recent {
			continue
		}
		var endedAt *time.Time
		if !session.EndedAt.IsZero() {
			value := session.EndedAt.UTC()
			endedAt = &value
		}
		projected = append(projected, TranscriptSessionStatus{
			SessionKey:     strings.TrimSpace(session.SessionKey),
			Agent:          strings.TrimSpace(session.Agent),
			Project:        transcriptProjectLabel(session),
			StartedAt:      session.StartedAt.UTC(),
			EndedAt:        endedAt,
			LastActivityAt: lastActivity.UTC(),
			Coverage:       session.Coverage,
			TurnCount:      session.TurnCount,
			Active:         active,
		})
	}
	sort.SliceStable(projected, func(left, right int) bool {
		if projected[left].Active != projected[right].Active {
			return projected[left].Active
		}
		return projected[left].LastActivityAt.After(
			projected[right].LastActivityAt,
		)
	})
	if len(projected) > defaultTranscriptSessionLimit {
		projected = projected[:defaultTranscriptSessionLimit]
	}
	return TranscriptStatus{
		SchemaVersion: TranscriptStatusSchemaVersion,
		Coverage: TranscriptCoverageStatus{
			WithTranscript: coverage.CanonicalCompleteOrLiveWithTranscript,
			Partial:        coverage.CanonicalPartial,
			Without:        coverage.CanonicalWithoutTranscript,
			TranscriptOnly: coverage.TranscriptOnly,
		},
		Sessions: nonNil(projected),
	}, nil
}

func transcriptProjectLabel(session transcript.Session) string {
	identity := strings.TrimSpace(session.ProjectIdentity)
	var label string
	if strings.TrimSpace(session.GitRemoteURL) != "" ||
		looksLikeRemote(identity) {
		label = remoteRepositoryBase(identity)
		if label == "" {
			label = remoteRepositoryBase(session.GitRemoteURL)
		}
	} else {
		label = localProjectBase(identity)
		if label == "" {
			label = localProjectBase(session.ProjectPath)
		}
	}
	label = strings.TrimSpace(label)
	if strings.HasSuffix(strings.ToLower(label), ".git") {
		label = label[:len(label)-len(".git")]
	}
	label = boundedSafeLabel(label)
	if label != "" {
		return label
	}
	return boundedSafeLabel(harnessProjectLabel(session.Agent))
}

func looksLikeRemote(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "git@") ||
		strings.Contains(lower, "@") && strings.Contains(lower, ":")
}

func remoteRepositoryBase(value string) string {
	value = strings.TrimSpace(strings.TrimRight(value, "/\\"))
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil &&
		parsed.Scheme != "" &&
		parsed.Path != "" {
		return path.Base(strings.TrimRight(parsed.Path, "/"))
	}
	if separator := strings.Index(value, ":"); separator >= 0 &&
		strings.Contains(value[:separator], "@") {
		value = value[separator+1:]
	}
	return path.Base(strings.ReplaceAll(value, "\\", "/"))
}

func localProjectBase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(value))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func boundedSafeLabel(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) ||
			character == '/' ||
			character == '\\' {
			return -1
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxProjectLabelRunes {
		value = strings.TrimSpace(string(runes[:maxProjectLabelRunes]))
	}
	return value
}

func harnessProjectLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "claude", "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "cursor", "cursor-agent":
		return "Cursor"
	case "antigravity":
		return "Antigravity"
	default:
		return strings.TrimSpace(value)
	}
}
