package transcriptissues

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type conversationFixtureFile struct {
	Cases []conversationFixtureCase `json:"cases"`
}

type conversationFixtureCase struct {
	Name                           string                   `json:"name"`
	Config                         issueintel.ProjectConfig `json:"config"`
	Sessions                       []issueintel.Session     `json:"sessions"`
	WantObservations               int                      `json:"want_observations"`
	WantCandidates                 int                      `json:"want_candidates"`
	WantMarkerCandidates           int                      `json:"want_marker_candidates"`
	WantUnobservedMarkerCandidates int                      `json:"want_unobserved_marker_candidates"`
	WantWastedTokens               int64                    `json:"want_wasted_tokens"`
	WantExcerpt                    string                   `json:"want_excerpt"`
	WantMinExcerpts                int                      `json:"want_min_excerpts"`
}

func TestDetectRepeatedCorrectionsFixtures(t *testing.T) {
	for _, test := range loadConversationFixtures(t, "repeated_corrections.json") {
		t.Run(test.Name, func(t *testing.T) {
			project := prepareConversationFixture(t, test)
			observations, candidates, err := detectRepeatedCorrections(
				context.Background(),
				project,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(observations) != test.WantObservations ||
				len(candidates) != test.WantCandidates {
				t.Fatalf(
					"observations/candidates = %d/%d, want %d/%d",
					len(observations),
					len(candidates),
					test.WantObservations,
					test.WantCandidates,
				)
			}
			markerCandidates := 0
			for _, candidate := range candidates {
				if candidate.Marker != "" {
					markerCandidates++
				}
				if candidate.CandidateID == "" ||
					candidate.Citation.SessionKey == "" ||
					candidate.Text == "" {
					t.Fatalf("incomplete candidate: %+v", candidate)
				}
			}
			if markerCandidates != test.WantMarkerCandidates {
				t.Fatalf(
					"marker candidates = %d, want %d",
					markerCandidates,
					test.WantMarkerCandidates,
				)
			}
			unobservedMarkerCandidates := markerCandidates - len(observations)
			if unobservedMarkerCandidates !=
				test.WantUnobservedMarkerCandidates {
				t.Fatalf(
					"unobserved marker candidates = %d, want %d",
					unobservedMarkerCandidates,
					test.WantUnobservedMarkerCandidates,
				)
			}
			assertConversationObservations(t, observations, test)
		})
	}
}

func TestDetectDoneWithoutVerificationFixtures(t *testing.T) {
	for _, test := range loadConversationFixtures(
		t,
		"done_without_verification.json",
	) {
		t.Run(test.Name, func(t *testing.T) {
			project := prepareConversationFixture(t, test)
			observations, err := detectDoneWithoutVerification(
				context.Background(),
				project,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(observations) != test.WantObservations {
				t.Fatalf(
					"observations = %d, want %d: %+v",
					len(observations),
					test.WantObservations,
					observations,
				)
			}
			assertConversationObservations(t, observations, test)
		})
	}
}

func TestDetectColdStartCostFixtures(t *testing.T) {
	for _, test := range loadConversationFixtures(t, "cold_start_cost.json") {
		t.Run(test.Name, func(t *testing.T) {
			project := prepareConversationFixture(t, test)
			observations, err := detectColdStartCost(
				context.Background(),
				project,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(observations) != test.WantObservations {
				t.Fatalf(
					"observations = %d, want %d: %+v",
					len(observations),
					test.WantObservations,
					observations,
				)
			}
			assertConversationObservations(t, observations, test)
		})
	}
}

func TestDetectCompactionBeforeCompletionFixtures(t *testing.T) {
	for _, test := range loadConversationFixtures(
		t,
		"compaction_before_completion.json",
	) {
		t.Run(test.Name, func(t *testing.T) {
			project := prepareConversationFixture(t, test)
			observations, err := detectCompactionBeforeCompletion(
				context.Background(),
				project,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(observations) != test.WantObservations {
				t.Fatalf(
					"observations = %d, want %d: %+v",
					len(observations),
					test.WantObservations,
					observations,
				)
			}
			assertConversationObservations(t, observations, test)
		})
	}
}

func TestConversationDetectorsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	project := preparedProject{
		project: issueintel.Project{Identity: "cancelled"},
		now:     time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC),
	}

	if _, _, err := detectRepeatedCorrections(ctx, project); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("repeated corrections cancellation = %v", err)
	}
	detectors := []struct {
		name string
		run  func(context.Context, preparedProject) ([]observation, error)
	}{
		{"done without verification", detectDoneWithoutVerification},
		{"cold start cost", detectColdStartCost},
		{"compaction before completion", detectCompactionBeforeCompletion},
	}
	for _, detector := range detectors {
		t.Run(detector.name, func(t *testing.T) {
			if _, err := detector.run(ctx, project); !errors.Is(
				err,
				context.Canceled,
			) {
				t.Fatalf("cancellation = %v", err)
			}
		})
	}
}

func loadConversationFixtures(
	t *testing.T,
	name string,
) []conversationFixtureCase {
	t.Helper()
	path := filepath.Join(
		"..",
		"..",
		"..",
		"testdata",
		"transcriptissues",
		"conversation",
		name,
	)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture conversationFixtureFile
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatalf("%s contains no cases", path)
	}
	return fixture.Cases
}

func prepareConversationFixture(
	t *testing.T,
	fixture conversationFixtureCase,
) preparedProject {
	t.Helper()
	const identity = "conversation-fixture-project"
	project := preparedProject{
		project: issueintel.Project{
			Identity: identity,
			Path:     "/workspace/conversation-fixture",
		},
		config: fixture.Config,
		now: time.Date(
			2026,
			time.September,
			9,
			12,
			0,
			0,
			0,
			time.UTC,
		),
	}
	for sessionIndex := range fixture.Sessions {
		session := &fixture.Sessions[sessionIndex]
		session.Metadata.ProjectIdentity = identity
		session.Metadata.ProjectPath = "/workspace/conversation-fixture"
		for turnIndex := range session.Turns {
			turn := &session.Turns[turnIndex]
			turn.SessionKey = session.Metadata.SessionKey
			turn.TurnIndex = int64(turnIndex)
			if session.Metadata.StartedAt.IsZero() ||
				turn.OccurredAt.Before(session.Metadata.StartedAt) {
				session.Metadata.StartedAt = turn.OccurredAt
			}
			if turn.OccurredAt.After(session.Metadata.EndedAt) {
				session.Metadata.EndedAt = turn.OccurredAt
			}
		}
		project.sessions = append(project.sessions, preparedSession{
			metadata: session.Metadata,
			turns:    append([]transcript.Turn(nil), session.Turns...),
		})
	}
	return project
}

func assertConversationObservations(
	t *testing.T,
	observations []observation,
	fixture conversationFixtureCase,
) {
	t.Helper()
	var wastedTokens int64
	foundExcerpt := fixture.WantExcerpt == ""
	for _, observed := range observations {
		wastedTokens += observed.cost.WastedTokens
		if observed.session.SessionKey == "" ||
			observed.firstSeen.IsZero() ||
			observed.lastSeen.IsZero() ||
			observed.fix.Kind == "" ||
			observed.fix.TargetFile == "" ||
			observed.fix.Rationale == "" {
			t.Fatalf("incomplete observation: %+v", observed)
		}
		if len(observed.excerpts) < fixture.WantMinExcerpts ||
			len(observed.excerpts) > maxExcerpts {
			t.Fatalf(
				"excerpt count = %d, want %d..%d: %+v",
				len(observed.excerpts),
				fixture.WantMinExcerpts,
				maxExcerpts,
				observed.excerpts,
			)
		}
		for _, excerpt := range observed.excerpts {
			if strings.Contains(excerpt.Text, fixture.WantExcerpt) {
				foundExcerpt = true
			}
		}
	}
	if wastedTokens != fixture.WantWastedTokens {
		t.Fatalf(
			"wasted tokens = %d, want %d",
			wastedTokens,
			fixture.WantWastedTokens,
		)
	}
	if !foundExcerpt {
		t.Fatalf("missing excerpt containing %q", fixture.WantExcerpt)
	}
}
