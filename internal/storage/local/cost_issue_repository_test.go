package local

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestReplaceProjectIssueAnalysisEncryptsReplacesAndAdvancesGeneration(
	t *testing.T,
) {
	const issueCanary = "COST_ISSUE_SECRET_CANARY_6e63"
	const candidateCanary = "CORRECTION_SECRET_CANARY_53b7"
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	project := issueintel.Project{
		Identity: "git@example.test:team/project.git",
		Path:     "/work/project",
	}
	session := costIssueTestSession(
		"ses_cost_issue_replace",
		project,
		time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC),
	)
	turn := transcriptTestTurn(
		"turn-cost-issue-replace",
		"source-cost-issue-replace",
		session.SessionKey,
		0,
		session.StartedAt,
		transcript.RoleUser,
		transcript.Payload{
			Text:            "stored transcript",
			SourceFileID:    "source-file",
			JSONLByteOffset: 10,
		},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}

	knownUSD := 6.40
	attributedUSD := 5.25
	issue := costIssueTestIssue(
		"issue-retry-loop",
		issueintel.DetectorRetryLoop,
		"shell\x00normalized failure",
		project,
		session,
		12,
		4200,
		&knownUSD,
		issueCanary,
	)
	candidate := issueintel.CorrectionCandidate{
		CandidateID: "candidate-correction",
		Project:     project,
		Citation: issueintel.Citation{
			SessionKey:      session.SessionKey,
			TurnIndex:       turn.TurnIndex,
			OccurredAt:      turn.OccurredAt,
			SourceFileID:    turn.Payload.SourceFileID,
			JSONLByteOffset: turn.Payload.JSONLByteOffset,
		},
		Text:       candidateCanary,
		Marker:     "no",
		ShortTurn:  true,
		OccurredAt: turn.OccurredAt,
	}
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		project,
		1,
		issueintel.Analysis{
			Issues:               []issueintel.Issue{issue},
			CorrectionCandidates: []issueintel.CorrectionCandidate{candidate},
			AttributedCost: issueintel.Cost{
				WastedMinutes: 8,
				WastedTokens:  3600,
				WastedUSD:     &attributedUSD,
			},
		},
	); err != nil {
		t.Fatal(err)
	}

	var issueCiphertext, candidateCiphertext []byte
	var issueEncoding, candidateEncoding string
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload, payload_encoding
		FROM cost_issues WHERE issue_id = ?`,
		issue.IssueID,
	).Scan(&issueCiphertext, &issueEncoding); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload, payload_encoding
		FROM correction_candidates WHERE candidate_id = ?`,
		candidate.CandidateID,
	).Scan(&candidateCiphertext, &candidateEncoding); err != nil {
		t.Fatal(err)
	}
	if issueEncoding != payloadEncodingAESGCM ||
		candidateEncoding != payloadEncodingAESGCM ||
		bytes.Contains(issueCiphertext, []byte(issueCanary)) ||
		bytes.Contains(candidateCiphertext, []byte(candidateCanary)) {
		t.Fatal("cost issue intelligence was not encrypted at rest")
	}
	if _, err := store.cipher.open(
		"cost_issue",
		"wrong-issue",
		"payload",
		issueEncoding,
		issueCiphertext,
	); err == nil {
		t.Fatal("cost issue payload authenticated with wrong identity")
	}

	got, err := store.GetCostIssue(ctx, issue.IssueID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Headline != issueCanary ||
		len(got.Excerpts) != 2 ||
		got.Excerpts[1].Text != issueCanary ||
		got.Cost.WastedUSD == nil ||
		*got.Cost.WastedUSD != knownUSD {
		t.Fatalf("decrypted cost issue = %+v", got)
	}
	filtered, err := store.QueryCostIssues(ctx, CostIssueQuery{
		ProjectIdentity: project.Identity,
		DetectorID:      issue.DetectorID,
	})
	if err != nil || len(filtered) != 1 || filtered[0].IssueID != issue.IssueID {
		t.Fatalf("filtered cost issues = %+v/%v", filtered, err)
	}
	var transcriptGeneration, analyzedGeneration int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT transcript_generation, analyzed_generation
		FROM transcript_project_analysis_state
		WHERE project_identity = ?`,
		project.Identity,
	).Scan(&transcriptGeneration, &analyzedGeneration); err != nil {
		t.Fatal(err)
	}
	if transcriptGeneration != 1 || analyzedGeneration != 1 {
		t.Fatalf(
			"analysis generations = %d/%d, want 1/1",
			transcriptGeneration,
			analyzedGeneration,
		)
	}
	totals, err := store.ReadCostIssueTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if totals.AttributedUSD != attributedUSD ||
		totals.LowerBound ||
		totals.IssueCount != 1 {
		t.Fatalf("union issue totals = %+v", totals)
	}

	replacement := costIssueTestIssue(
		"issue-recurring-error",
		issueintel.DetectorRecurringError,
		"normalized recurring error",
		project,
		session,
		3,
		900,
		nil,
		"replacement",
	)
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		project,
		1,
		issueintel.Analysis{Issues: []issueintel.Issue{replacement}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCostIssue(ctx, issue.IssueID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replaced issue lookup error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetCostIssue(ctx, replacement.IssueID); err != nil {
		t.Fatal(err)
	}
	var candidateCount int
	if err := store.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM correction_candidates",
	).Scan(&candidateCount); err != nil || candidateCount != 0 {
		t.Fatalf("replacement candidate count/error = %d/%v", candidateCount, err)
	}

	invalid := replacement
	invalid.IssueID = "invalid-replacement"
	invalid.SessionCount = 2
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		project,
		1,
		issueintel.Analysis{Issues: []issueintel.Issue{invalid}},
	); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	if _, err := store.GetCostIssue(ctx, replacement.IssueID); err != nil {
		t.Fatalf("failed replacement changed existing issue: %v", err)
	}

	secondProject := issueintel.Project{
		Identity: "git@example.test:team/second-project.git",
		Path:     "/work/second-project",
	}
	secondSession := costIssueTestSession(
		"ses_cost_issue_atomic_conflict",
		secondProject,
		session.StartedAt.Add(time.Hour),
	)
	secondTurn := transcriptTestTurn(
		"turn-cost-issue-atomic-conflict",
		"source-cost-issue-atomic-conflict",
		secondSession.SessionKey,
		0,
		secondSession.StartedAt,
		transcript.RoleUser,
		transcript.Payload{Text: "second project", JSONLByteOffset: 1},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		secondSession,
		[]transcript.Turn{secondTurn},
	); err != nil {
		t.Fatal(err)
	}
	conflict := costIssueTestIssue(
		replacement.IssueID,
		issueintel.DetectorRetryLoop,
		"second-project-conflict",
		secondProject,
		secondSession,
		1,
		1,
		nil,
		"conflict",
	)
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		secondProject,
		1,
		issueintel.Analysis{Issues: []issueintel.Issue{conflict}},
	); err == nil {
		t.Fatal("cross-project issue ID conflict succeeded")
	}
	if _, err := store.GetCostIssue(ctx, replacement.IssueID); err != nil {
		t.Fatalf("atomic conflict removed original issue: %v", err)
	}
	assertDirtyProjectGenerations(t, store, secondProject.Identity, 1, 0)

	_, encryptedCount, err := store.payloadEncodingCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if encryptedCount < 2 {
		t.Fatalf("encrypted payload count = %d, want transcript and issue", encryptedCount)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteFilesExclude(t, path, issueCanary, candidateCanary)
}

func TestQueryCostIssuesOrdersKnownUSDThenSessions(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	project := issueintel.Project{Identity: "project-order", Path: "/project-order"}
	session := costIssueTestSession(
		"ses_cost_issue_order",
		project,
		time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
	)
	turn := transcriptTestTurn(
		"turn-cost-issue-order",
		"source-cost-issue-order",
		session.SessionKey,
		0,
		session.StartedAt,
		transcript.RoleUser,
		transcript.Payload{Text: "order", JSONLByteOffset: 1},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}

	highUSD := 9.0
	equalUSD := 4.0
	lowUSD := 1.0
	issues := []issueintel.Issue{
		costIssueTestIssue(
			"issue-unknown",
			issueintel.DetectorFileThrash,
			"unknown",
			project,
			session,
			20,
			10000,
			nil,
			"unknown",
		),
		costIssueTestIssue(
			"issue-low",
			issueintel.DetectorRecurringError,
			"low",
			project,
			session,
			2,
			100,
			&lowUSD,
			"low",
		),
		costIssueTestIssue(
			"issue-high",
			issueintel.DetectorRetryLoop,
			"high",
			project,
			session,
			3,
			200,
			&highUSD,
			"high",
		),
		costIssueTestIssue(
			"issue-equal-more-sessions",
			issueintel.DetectorPermissionChurn,
			"equal-more",
			project,
			session,
			4,
			300,
			&equalUSD,
			"equal more",
		),
		costIssueTestIssue(
			"issue-equal-fewer-sessions",
			issueintel.DetectorColdStartCost,
			"equal-fewer",
			project,
			session,
			4,
			300,
			&equalUSD,
			"equal fewer",
		),
	}
	issues[3].Sessions = append(
		issues[3].Sessions,
		issueintel.SessionRef{
			SessionKey: "ses-second",
			Agent:      "codex",
			StartedAt:  session.StartedAt,
			EndedAt:    session.EndedAt,
		},
	)
	issues[3].SessionCount = 2
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		project,
		1,
		issueintel.Analysis{Issues: issues},
	); err != nil {
		t.Fatal(err)
	}

	got, err := store.QueryCostIssues(ctx, CostIssueQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"issue-high",
		"issue-equal-more-sessions",
		"issue-equal-fewer-sessions",
		"issue-low",
		"issue-unknown",
	}
	if len(got) != len(want) {
		t.Fatalf("cost issue count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].IssueID != want[index] {
			t.Fatalf("cost issue order[%d] = %q, want %q", index, got[index].IssueID, want[index])
		}
	}
}

func TestTranscriptProjectDirtyGenerationReplayAnalysisAndProjectMove(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	firstProject := issueintel.Project{Identity: "project-dirty-a", Path: "/dirty/a"}
	session := costIssueTestSession(
		"ses_cost_issue_dirty",
		firstProject,
		time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
	)
	firstTurn := transcriptTestTurn(
		"turn-cost-issue-dirty-a",
		"source-cost-issue-dirty-a",
		session.SessionKey,
		0,
		session.StartedAt,
		transcript.RoleUser,
		transcript.Payload{Text: "first", JSONLByteOffset: 1},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{firstTurn},
	); err != nil {
		t.Fatal(err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 1, 0)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{firstTurn},
	); err != nil {
		t.Fatal(err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 1, 0)

	session.Coverage = transcript.CoverageLive
	if _, err := store.AppendTranscriptBatch(ctx, session, nil); err != nil {
		t.Fatal(err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 2, 0)

	secondTurn := transcriptTestTurn(
		"turn-cost-issue-dirty-b",
		"source-cost-issue-dirty-b",
		session.SessionKey,
		1,
		session.StartedAt.Add(time.Minute),
		transcript.RoleAssistant,
		transcript.Payload{Text: "second", JSONLByteOffset: 2},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{secondTurn},
	); err != nil {
		t.Fatal(err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 3, 0)

	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		firstProject,
		2,
		issueintel.Analysis{},
	); !errors.Is(err, ErrTranscriptProjectGenerationChanged) {
		t.Fatalf("stale analysis replacement error = %v", err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 3, 0)

	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		firstProject,
		3,
		issueintel.Analysis{},
	); err != nil {
		t.Fatal(err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 3, 3)
	dirty, err := store.ListDirtyTranscriptProjects(ctx)
	if err != nil || len(dirty) != 0 {
		t.Fatalf("clean project list = %+v/%v", dirty, err)
	}

	secondProject := issueintel.Project{Identity: "project-dirty-b", Path: "/dirty/b"}
	session.ProjectIdentity = secondProject.Identity
	session.ProjectPath = secondProject.Path
	if _, err := store.AppendTranscriptBatch(ctx, session, nil); err != nil {
		t.Fatal(err)
	}
	assertDirtyProjectGenerations(t, store, firstProject.Identity, 4, 3)
	assertDirtyProjectGenerations(t, store, secondProject.Identity, 1, 0)
	dirty, err = store.ListDirtyTranscriptProjects(ctx, 10)
	if err != nil || len(dirty) != 2 {
		t.Fatalf("moved project dirty list = %+v/%v", dirty, err)
	}
}

func TestLoadTranscriptProjectDataDecryptsWithDeterministicOrder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	store, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		OpenOptions{
			KeyProvider: newMemoryKeyProvider(),
			Clock:       func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	project := issueintel.Project{
		Identity: "git@example.test:team/load.git",
		Path:     "/work/load",
	}
	later := costIssueTestSession(
		"ses-project-load-later",
		project,
		time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC),
	)
	earlier := costIssueTestSession(
		"ses-project-load-earlier",
		project,
		time.Date(2026, 9, 8, 13, 0, 0, 0, time.UTC),
	)
	laterTurns := []transcript.Turn{
		transcriptTestTurn(
			"turn-load-later-b",
			"source-load-later-b",
			later.SessionKey,
			7,
			later.StartedAt.Add(time.Minute),
			transcript.RoleAssistant,
			transcript.Payload{Text: "later-b", JSONLByteOffset: 2},
		),
		transcriptTestTurn(
			"turn-load-later-a",
			"source-load-later-a",
			later.SessionKey,
			8,
			later.StartedAt,
			transcript.RoleUser,
			transcript.Payload{Text: "later-a", JSONLByteOffset: 1},
		),
	}
	earlierTurn := transcriptTestTurn(
		"turn-load-earlier",
		"source-load-earlier",
		earlier.SessionKey,
		0,
		earlier.StartedAt,
		transcript.RoleUser,
		transcript.Payload{Text: "earlier", JSONLByteOffset: 1},
	)
	if _, err := store.AppendTranscriptBatch(ctx, later, laterTurns); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTranscriptBatch(
		ctx,
		earlier,
		[]transcript.Turn{earlierTurn},
	); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadTranscriptProjectData(ctx, project.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != project || !got.Now.Equal(now) || len(got.Sessions) != 2 {
		t.Fatalf("loaded transcript project = %+v", got)
	}
	if got.Sessions[0].Metadata.SessionKey != earlier.SessionKey ||
		got.Sessions[0].Turns[0].Payload.Text != "earlier" ||
		got.Sessions[1].Metadata.SessionKey != later.SessionKey ||
		len(got.Sessions[1].Turns) != 2 ||
		got.Sessions[1].Turns[0].Payload.Text != "later-a" ||
		got.Sessions[1].Turns[1].Payload.Text != "later-b" {
		t.Fatalf("loaded transcript order = %+v", got.Sessions)
	}
}

func TestLoadTranscriptProjectDataRejectsSessionSafetyOverflow(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	projectIdentity := "project-session-overflow"
	now := formatProjectionTime(time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC))
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxTranscriptProjectSessions; index++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcript_sessions (
				session_key, agent, native_session_id, project_path,
				git_remote_url, project_identity, coverage,
				created_at, updated_at
			) VALUES (?, 'codex', ?, '/overflow', '', ?, 'complete', ?, ?)`,
			"ses-overflow-"+formatTestIndex(index),
			"native-overflow-"+formatTestIndex(index),
			projectIdentity,
			now,
			now,
		); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTranscriptProjectData(
		ctx,
		projectIdentity,
	); err == nil {
		t.Fatal("project session safety overflow returned partial data")
	}
}

func TestCostIssuePayloadsParticipateInRetention(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	project := issueintel.Project{Identity: "project-retention", Path: "/retention"}
	session := costIssueTestSession("ses-cost-retention", project, base)
	turn := transcriptTestTurn(
		"turn-cost-retention",
		"source-cost-retention",
		session.SessionKey,
		0,
		base,
		transcript.RoleUser,
		transcript.Payload{Text: "retained", JSONLByteOffset: 1},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}
	issue := costIssueTestIssue(
		"issue-cost-retention",
		issueintel.DetectorRepeatedCorrection,
		"retention",
		project,
		session,
		1,
		10,
		nil,
		"retention issue",
	)
	candidate := issueintel.CorrectionCandidate{
		CandidateID: "candidate-cost-retention",
		Project:     project,
		Citation: issueintel.Citation{
			SessionKey:      session.SessionKey,
			TurnIndex:       0,
			OccurredAt:      base,
			JSONLByteOffset: 1,
		},
		Text:       "retention candidate",
		ShortTurn:  true,
		OccurredAt: base,
	}
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		project,
		1,
		issueintel.Analysis{
			Issues:               []issueintel.Issue{issue},
			CorrectionCandidates: []issueintel.CorrectionCandidate{candidate},
		},
	); err != nil {
		t.Fatal(err)
	}
	diagnostics, err := store.RetentionDiagnostics(
		ctx,
		RetentionPolicy{MaxAge: 24 * time.Hour},
		base.Add(7*24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.CurrentPayloadBytes <= diagnostics.CurrentTranscriptPayloadBytes {
		t.Fatal("retention bytes omitted cost issue intelligence payloads")
	}
	result, err := store.Prune(
		ctx,
		RetentionPolicy{MaxAge: 24 * time.Hour},
		base.Add(7*24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedPayloadBytes <= result.PrunedTranscriptPayloadBytes {
		t.Fatal("retention prune omitted cost issue intelligence payloads")
	}
	issues, err := store.QueryCostIssues(ctx, CostIssueQuery{})
	if err != nil || len(issues) != 0 {
		t.Fatalf("retained cost issues = %+v/%v", issues, err)
	}
	var candidates int
	if err := store.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM correction_candidates",
	).Scan(&candidates); err != nil || candidates != 0 {
		t.Fatalf("retained correction candidates = %d/%v", candidates, err)
	}
}

func costIssueTestSession(
	sessionKey string,
	project issueintel.Project,
	startedAt time.Time,
) transcript.Session {
	session := transcriptTestSession(sessionKey, transcript.CoverageComplete)
	session.ProjectIdentity = project.Identity
	session.ProjectPath = project.Path
	session.StartedAt = startedAt
	session.EndedAt = startedAt
	return session
}

func costIssueTestIssue(
	issueID string,
	detectorID string,
	fingerprint string,
	project issueintel.Project,
	session transcript.Session,
	wastedMinutes float64,
	wastedTokens int64,
	wastedUSD *float64,
	headline string,
) issueintel.Issue {
	occurredAt := session.StartedAt
	return issueintel.Issue{
		IssueID:     issueID,
		DetectorID:  detectorID,
		Fingerprint: fingerprint,
		Headline:    headline,
		Cost: issueintel.Cost{
			WastedMinutes: wastedMinutes,
			WastedTokens:  wastedTokens,
			WastedUSD:     wastedUSD,
			LowerBound:    true,
		},
		SessionCount: 1,
		Sessions: []issueintel.SessionRef{{
			SessionKey: session.SessionKey,
			Agent:      session.Agent,
			StartedAt:  occurredAt,
			EndedAt:    occurredAt,
		}},
		FirstSeen: occurredAt,
		LastSeen:  occurredAt,
		Trend: []issueintel.TrendWeek{{
			WeekStart: occurredAt,
			Count:     1,
		}},
		Excerpts: []issueintel.Excerpt{
			{
				Citation: issueintel.Citation{
					SessionKey:      session.SessionKey,
					TurnIndex:       0,
					OccurredAt:      occurredAt,
					SourceFileID:    "source-file",
					JSONLByteOffset: 1,
				},
				Role: transcript.RoleAssistant,
				Text: "preceding behavior",
			},
			{
				Citation: issueintel.Citation{
					SessionKey:      session.SessionKey,
					TurnIndex:       1,
					OccurredAt:      occurredAt,
					SourceFileID:    "source-file",
					JSONLByteOffset: 2,
				},
				Role: transcript.RoleUser,
				Text: headline,
			},
		},
		Project: project,
		SuggestedFix: issueintel.SuggestedFix{
			Kind:       "instruction",
			TargetFile: "AGENTS.md",
			Rationale:  headline,
		},
	}
}

func assertDirtyProjectGenerations(
	t *testing.T,
	store *Store,
	projectIdentity string,
	wantTranscript int64,
	wantAnalyzed int64,
) {
	t.Helper()
	var transcriptGeneration, analyzedGeneration int64
	if err := store.db.QueryRow(`
		SELECT transcript_generation, analyzed_generation
		FROM transcript_project_analysis_state
		WHERE project_identity = ?`,
		projectIdentity,
	).Scan(&transcriptGeneration, &analyzedGeneration); err != nil {
		t.Fatal(err)
	}
	if transcriptGeneration != wantTranscript || analyzedGeneration != wantAnalyzed {
		t.Fatalf(
			"project %q generations = %d/%d, want %d/%d",
			projectIdentity,
			transcriptGeneration,
			analyzedGeneration,
			wantTranscript,
			wantAnalyzed,
		)
	}
}

func formatTestIndex(index int) string {
	const digits = "0123456789"
	if index == 0 {
		return "0"
	}
	var buffer [20]byte
	offset := len(buffer)
	for index > 0 {
		offset--
		buffer[offset] = digits[index%10]
		index /= 10
	}
	return string(buffer[offset:])
}
