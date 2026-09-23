package readmodel

import (
	"context"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
)

const (
	sessionEpisodeLimit   = 50
	sessionEpisodeTimeout = 500 * time.Millisecond
)

type EvidenceEpisodeRepository interface {
	QuerySessionEvidenceEpisodes(
		context.Context,
		string,
		int,
	) ([]evidenceepisode.Episode, error)
}

type EvidenceEpisodeBatchRepository interface {
	QueryEvidenceEpisodesForSessions(
		context.Context,
		[]string,
		int,
	) (map[string][]evidenceepisode.Episode, error)
}

func WithEvidenceEpisodeRepository(
	repository EvidenceEpisodeRepository,
) Option {
	return func(service *Service) {
		service.evidenceEpisodeRepository = repository
	}
}

func (s *Service) sessionEvidenceEpisodes(
	ctx context.Context,
	sessionKey string,
) []evidenceepisode.Episode {
	if s == nil || s.evidenceEpisodeRepository == nil {
		return []evidenceepisode.Episode{}
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(sessionKey) > 256 {
		return []evidenceepisode.Episode{}
	}
	queryCtx, cancel := context.WithTimeout(ctx, sessionEpisodeTimeout)
	defer cancel()
	values, err := s.evidenceEpisodeRepository.QuerySessionEvidenceEpisodes(
		queryCtx,
		sessionKey,
		sessionEpisodeLimit,
	)
	if err != nil {
		return []evidenceepisode.Episode{}
	}
	if len(values) == 0 &&
		s.fusedSessionReads &&
		s.sessionIdentityRepository != nil {
		aliases, aliasErr := s.sessionIdentityRepository.
			QueryActiveSessionIdentityAliases(
				queryCtx,
				[]string{sessionKey},
			)
		if aliasErr == nil {
			if alias, ok := aliases[sessionKey]; ok {
				values, err = s.evidenceEpisodeRepository.
					QuerySessionEvidenceEpisodes(
						queryCtx,
						alias.LinkedSessionKey,
						sessionEpisodeLimit,
					)
			}
		}
	}
	if err != nil || values == nil {
		return []evidenceepisode.Episode{}
	}
	return values
}

func (s *Service) evidenceEpisodesForSessions(
	ctx context.Context,
	sessionKeys []string,
) map[string][]evidenceepisode.Episode {
	result := make(map[string][]evidenceepisode.Episode)
	if s == nil || s.evidenceEpisodeRepository == nil ||
		len(sessionKeys) == 0 {
		return result
	}
	if repository, ok := s.evidenceEpisodeRepository.(EvidenceEpisodeBatchRepository); ok {
		queryCtx, cancel := context.WithTimeout(ctx, sessionEpisodeTimeout)
		defer cancel()
		values, err := repository.QueryEvidenceEpisodesForSessions(
			queryCtx,
			sessionKeys,
			sessionEpisodeLimit*min(len(sessionKeys), 10),
		)
		if err == nil && values != nil {
			return values
		}
	}
	for _, sessionKey := range sessionKeys {
		values := s.sessionEvidenceEpisodes(ctx, sessionKey)
		if len(values) > 0 {
			result[sessionKey] = values
		}
	}
	return result
}
