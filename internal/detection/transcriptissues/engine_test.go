package transcriptissues

import (
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestAggregateIssuesRanksMeasuredUSDThenSessionCount(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	knownUSD := 2.5
	project := preparedProject{
		project: issueintel.Project{Identity: "project", Path: "/project"},
		now:     now,
	}
	observations := []observation{
		{
			detectorID:  issueintel.DetectorRetryLoop,
			fingerprint: "known",
			subject:     "`go`",
			cost: issueintel.Cost{
				WastedTokens: 100,
				WastedUSD:    &knownUSD,
			},
			session: issueintel.SessionRef{
				SessionKey: "ses_known",
				StartedAt:  now.Add(-time.Hour),
			},
			firstSeen:  now.Add(-time.Hour),
			lastSeen:   now,
			occurredAt: now,
			excerpts: []issueintel.Excerpt{
				{
					Citation: issueintel.Citation{
						SessionKey: "ses_known",
						TurnIndex:  1,
					},
					Role: transcript.RoleToolCall,
					Text: "go test ./...",
				},
				{
					Citation: issueintel.Citation{
						SessionKey: "ses_known",
						TurnIndex:  2,
					},
					Role: transcript.RoleToolResult,
					Text: "error: known",
				},
			},
		},
	}
	for index := 0; index < 3; index++ {
		sessionKey := "ses_unknown_" + string(rune('a'+index))
		observations = append(observations, observation{
			detectorID:  issueintel.DetectorRecurringError,
			fingerprint: "unknown",
			subject:     "The same error",
			cost: issueintel.Cost{
				WastedTokens: 1000,
				LowerBound:   true,
			},
			session: issueintel.SessionRef{
				SessionKey: sessionKey,
				StartedAt:  now.Add(-time.Duration(index+2) * time.Hour),
			},
			firstSeen:  now.Add(-time.Duration(index+2) * time.Hour),
			lastSeen:   now,
			occurredAt: now,
			excerpts: []issueintel.Excerpt{{
				Citation: issueintel.Citation{SessionKey: sessionKey},
				Role:     transcript.RoleToolResult,
				Text:     "error: unknown",
			}},
		})
	}
	issues := aggregateIssues(project, observations)
	if len(issues) != 2 ||
		issues[0].Fingerprint != "known" ||
		issues[1].Fingerprint != "unknown" {
		t.Fatalf("issue order = %+v", issues)
	}
	if len(issues[0].Trend) != 8 || issues[1].SessionCount != 3 {
		t.Fatalf("aggregated issues = %+v", issues)
	}
}

func TestAggregateIssuesSumsCostBoundsAndDeduplicatesExcerpts(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	firstUSD, secondUSD := 1.25, 0.75
	project := preparedProject{
		project: issueintel.Project{Identity: "project", Path: "/project"},
		now:     now,
	}
	makeObservation := func(
		session string,
		occurred time.Time,
		usd *float64,
		lowerBound bool,
	) observation {
		return observation{
			detectorID:  issueintel.DetectorFileThrash,
			fingerprint: "file.go",
			subject:     "`file.go`",
			cost: issueintel.Cost{
				WastedMinutes: 2,
				WastedTokens:  50,
				WastedUSD:     usd,
				LowerBound:    lowerBound,
			},
			session:    issueintel.SessionRef{SessionKey: session, StartedAt: occurred},
			firstSeen:  occurred,
			lastSeen:   occurred,
			occurredAt: occurred,
			excerpts: []issueintel.Excerpt{
				{
					Citation: issueintel.Citation{
						SessionKey: session,
						TurnIndex:  1,
					},
					Role: transcript.RoleToolCall,
					Text: "edit file.go",
				},
				{
					Citation: issueintel.Citation{
						SessionKey: session,
						TurnIndex:  1,
					},
					Role: transcript.RoleToolCall,
					Text: "edit file.go",
				},
			},
		}
	}
	issues := aggregateIssues(project, []observation{
		makeObservation("ses_a", now.Add(-8*24*time.Hour), &firstUSD, false),
		makeObservation("ses_b", now, &secondUSD, true),
	})
	if len(issues) != 1 {
		t.Fatalf("issues = %+v", issues)
	}
	issue := issues[0]
	if issue.Cost.WastedUSD == nil ||
		*issue.Cost.WastedUSD != 2 ||
		issue.Cost.WastedTokens != 100 ||
		!issue.Cost.LowerBound ||
		len(issue.Excerpts) != 2 ||
		issue.Trend[6].Count != 1 ||
		issue.Trend[7].Count != 1 {
		t.Fatalf("issue = %+v", issue)
	}
}

func TestAttributionUnionsOverlappingWindowsAndCrossDetectorSpend(
	t *testing.T,
) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	project := preparedProject{
		project: issueintel.Project{Identity: "project", Path: "/project"},
		now:     now,
		sessions: []preparedSession{{
			metadata: transcript.Session{
				SessionKey: "ses_overlap",
				StartedAt:  now,
				EndedAt:    now.Add(3 * time.Hour),
			},
			turns: []transcript.Turn{
				attributionTestTurn("ses_overlap", 0, now, 1),
				attributionTestTurn(
					"ses_overlap",
					1,
					now.Add(time.Minute),
					2,
				),
				attributionTestTurn(
					"ses_overlap",
					2,
					now.Add(2*time.Hour),
					3,
				),
				attributionTestTurn(
					"ses_overlap",
					3,
					now.Add(2*time.Hour+time.Minute),
					4,
				),
			},
		}},
	}
	ref := sessionRef(project.sessions[0])
	makeObservation := func(
		detector, fingerprint string,
		start, end int,
	) observation {
		return observation{
			detectorID:  detector,
			fingerprint: fingerprint,
			subject:     "overlap",
			costSpans: []costSpan{{
				session: ref,
				start:   start,
				end:     end,
			}},
			session:    ref,
			firstSeen:  now,
			lastSeen:   now.Add(2*time.Hour + time.Minute),
			occurredAt: now,
			excerpts: []issueintel.Excerpt{
				{
					Citation: issueintel.Citation{
						SessionKey: ref.SessionKey,
						TurnIndex:  int64(start),
					},
					Role: transcript.RoleAssistant,
					Text: "first",
				},
				{
					Citation: issueintel.Citation{
						SessionKey: ref.SessionKey,
						TurnIndex:  int64(end),
					},
					Role: transcript.RoleAssistant,
					Text: "last",
				},
			},
		}
	}
	observations := []observation{
		makeObservation(
			issueintel.DetectorCompactionBeforeCompletion,
			"same",
			0,
			3,
		),
		makeObservation(
			issueintel.DetectorCompactionBeforeCompletion,
			"same",
			2,
			3,
		),
		makeObservation(
			issueintel.DetectorDoneWithoutVerification,
			"other",
			1,
			2,
		),
	}

	issues := aggregateIssues(project, observations)
	if len(issues) != 2 {
		t.Fatalf("issues = %+v", issues)
	}
	var compaction issueintel.Issue
	for _, issue := range issues {
		if issue.DetectorID ==
			issueintel.DetectorCompactionBeforeCompletion {
			compaction = issue
		}
	}
	if compaction.Cost.WastedUSD == nil ||
		*compaction.Cost.WastedUSD != 10 ||
		compaction.Cost.WastedTokens != 1000 ||
		compaction.Cost.WastedMinutes != 2 {
		t.Fatalf("overlap-safe compaction cost = %+v", compaction.Cost)
	}
	total := attributedCost(
		project,
		observationCostSpans(observations),
		observationTimeSpans(observations),
	)
	if total.WastedUSD == nil ||
		*total.WastedUSD != 10 ||
		total.WastedTokens != 1000 ||
		total.WastedMinutes != 2 {
		t.Fatalf("cross-detector union cost = %+v", total)
	}
}

func attributionTestTurn(
	session string,
	index int64,
	occurredAt time.Time,
	usd float64,
) transcript.Turn {
	tokens := int64(250)
	return transcript.Turn{
		TurnID:      session + string(rune('a'+index)),
		SessionKey:  session,
		TurnIndex:   index,
		OccurredAt:  occurredAt,
		Role:        transcript.RoleAssistant,
		InputTokens: &tokens,
		CostUSD:     &usd,
	}
}
