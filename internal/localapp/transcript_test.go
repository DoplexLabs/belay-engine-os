package localapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acquisition "github.com/DoplexLabs/belay-engine/internal/acquisition/transcript"
	"github.com/DoplexLabs/belay-engine/internal/canonical/numbatmap"
	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

type transcriptStoreCall struct {
	session belaytranscript.Session
	turns   []belaytranscript.Turn
}

type recordingTranscriptStore struct {
	calls       []transcriptStoreCall
	err         error
	afterAppend func()
}

func (store *recordingTranscriptStore) AppendTranscriptBatch(
	_ context.Context,
	session belaytranscript.Session,
	turns []belaytranscript.Turn,
) (int, error) {
	if store.err != nil {
		return 0, store.err
	}
	copied := append([]belaytranscript.Turn(nil), turns...)
	store.calls = append(store.calls, transcriptStoreCall{
		session: session,
		turns:   copied,
	})
	if store.afterAppend != nil {
		afterAppend := store.afterAppend
		store.afterAppend = nil
		afterAppend()
	}
	return len(turns), nil
}

func TestScanTranscriptsGroupsClaudeOrdersTurnsAndSanitizesRemote(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	sessionID := "11111111-1111-4111-8111-111111111111"
	projectRoot := filepath.Join(claudeRoot, "projects", "-synthetic-project")
	parent := filepath.Join(projectRoot, sessionID+".jsonl")
	subagent := filepath.Join(
		projectRoot,
		sessionID,
		"subagents",
		"agent-synthetic.jsonl",
	)
	parentBody := readTranscriptFixture(t, "claude", "main.jsonl")
	parentBody = []byte(strings.Replace(
		string(parentBody),
		"https://example.invalid/synthetic.git",
		"https://user:password@example.invalid/synthetic.git?token=private#fragment",
		1,
	))
	writeTranscriptTestFile(t, parent, parentBody)
	writeTranscriptTestFile(
		t,
		subagent,
		readTranscriptFixture(t, "claude", "subagent.jsonl"),
	)
	old := time.Now().Add(-10 * time.Minute)
	for _, path := range []string{parent, subagent} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := ResolvePaths(filepath.Join(root, "belay"))
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingTranscriptStore{}
	if err := ScanTranscripts(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("store calls = %d, want 1", len(store.calls))
	}
	call := store.calls[0]
	if call.session.SessionKey != numbatmap.SessionKey("claude-code", sessionID, "") ||
		call.session.Coverage != belaytranscript.CoverageComplete ||
		call.session.GitRemoteURL != "https://example.invalid/synthetic.git" ||
		call.session.ProjectIdentity != "https://example.invalid/synthetic.git" {
		t.Fatalf("session = %+v", call.session)
	}
	if len(call.turns) != 7 {
		t.Fatalf("turns = %d, want 7", len(call.turns))
	}
	for index, turn := range call.turns {
		if turn.TurnIndex != int64(index) {
			t.Fatalf("turn %d index = %d", index, turn.TurnIndex)
		}
		if index > 0 && turn.OccurredAt.Before(call.turns[index-1].OccurredAt) {
			t.Fatalf("turns are not timestamp ordered: %+v", call.turns)
		}
	}
	foundSubagent := false
	sourceFiles := make(map[string]bool)
	for _, turn := range call.turns {
		sourceFiles[turn.Payload.SourceFileID] = true
		if turn.Payload.Text == "Subagent evidence" {
			foundSubagent = true
			if turn.Payload.ParentToolUseID != "tool-parent-1" {
				t.Fatalf("subagent parent tool = %q", turn.Payload.ParentToolUseID)
			}
		}
	}
	if !foundSubagent {
		t.Fatal("subagent turn missing")
	}
	if len(sourceFiles) != 2 {
		t.Fatalf("source citation identities = %v, want parent and subagent", sourceFiles)
	}
}

func TestScanTranscriptsCoveragePartialAndRecentLive(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		age      time.Duration
		coverage belaytranscript.SessionCoverage
	}{
		{
			name: "malformed and incomplete is partial",
			body: claudeUserLine(
				"66666666-6666-4666-8666-666666666666",
				"2026-08-01T14:00:00Z",
				"first",
			) + "\n{bad}\n" + claudeUserLine(
				"66666666-6666-4666-8666-666666666666",
				"2026-08-01T14:00:01Z",
				"incomplete",
			),
			age:      10 * time.Minute,
			coverage: belaytranscript.CoveragePartial,
		},
		{
			name: "recent complete file is live",
			body: claudeUserLine(
				"77777777-7777-4777-8777-777777777777",
				"2026-08-01T14:00:00Z",
				"recent",
			) + "\n",
			coverage: belaytranscript.CoverageLive,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			claudeRoot := filepath.Join(root, "claude")
			t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
			t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
			t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
			sessionID := strings.Split(test.body, `"`)[7]
			path := filepath.Join(
				claudeRoot,
				"projects",
				"-project",
				sessionID+".jsonl",
			)
			writeTranscriptTestFile(t, path, []byte(test.body))
			if test.age > 0 {
				modified := time.Now().Add(-test.age)
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			paths, _ := ResolvePaths(filepath.Join(root, "belay"))
			store := &recordingTranscriptStore{}
			if err := ScanTranscripts(context.Background(), paths, store); err != nil {
				t.Fatal(err)
			}
			if len(store.calls) != 1 ||
				store.calls[0].session.Coverage != test.coverage {
				t.Fatalf("calls = %+v", store.calls)
			}
		})
	}
}

func TestScanHistoricalTranscriptsBackfillsNewestFirst(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	now := time.Date(2026, time.September, 9, 18, 0, 0, 0, time.UTC)
	sessions := []struct {
		id       string
		project  string
		modified time.Time
	}{
		{
			id:       "10000000-0000-4000-8000-000000000001",
			project:  "-oldest",
			modified: now.Add(-30 * time.Minute),
		},
		{
			id:       "20000000-0000-4000-8000-000000000002",
			project:  "-newest",
			modified: now.Add(-10 * time.Minute),
		},
		{
			id:       "30000000-0000-4000-8000-000000000003",
			project:  "-middle",
			modified: now.Add(-20 * time.Minute),
		},
	}
	for _, session := range sessions {
		path := filepath.Join(
			claudeRoot,
			"projects",
			session.project,
			session.id+".jsonl",
		)
		writeTranscriptTestFile(
			t,
			path,
			[]byte(claudeUserLine(
				session.id,
				"2026-09-09T17:00:00Z",
				session.project,
			)+"\n"),
		)
		if err := os.Chtimes(path, session.modified, session.modified); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := ResolvePaths(filepath.Join(root, "belay"))
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingTranscriptStore{}
	if err := scanHistoricalTranscriptsAt(
		context.Background(),
		paths,
		store,
		now,
	); err != nil {
		t.Fatal(err)
	}
	want := []string{sessions[1].id, sessions[2].id, sessions[0].id}
	if len(store.calls) != len(want) {
		t.Fatalf("backfill calls = %d, want %d", len(store.calls), len(want))
	}
	for index, nativeID := range want {
		if got := store.calls[index].session.NativeSessionID; got != nativeID {
			t.Fatalf("backfill call %d session = %q, want %q", index, got, nativeID)
		}
	}

	store.calls = nil
	if err := scanHistoricalTranscriptsAt(
		context.Background(),
		paths,
		store,
		now.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("completed backfill was reimported: %+v", store.calls)
	}
}

func TestTranscriptGroupPlanPartitionsRecentHistoricalAndOwned(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolvePaths(filepath.Join(root, "belay"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 9, 18, 0, 0, 0, time.UTC)
	makeSource := func(name string, modified time.Time) acquisition.Source {
		path := filepath.Join(root, name+".jsonl")
		writeTranscriptTestFile(t, path, []byte("{}\n"))
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		return acquisition.Source{
			Agent:           acquisition.AgentClaude,
			Path:            path,
			GroupKey:        name,
			NativeSessionID: name,
			Primary:         true,
		}
	}
	recent := makeSource("recent", now.Add(-time.Minute))
	historical := makeSource("historical", now.Add(-time.Hour))
	tailOwned := makeSource("tail-owned", now.Add(-time.Hour))
	complete := makeSource("complete", now.Add(-time.Hour))
	if err := saveTranscriptCursor(
		transcriptCursorPath(paths, tailOwned.Path),
		transcriptCursor{Owner: transcriptOwnerTail},
	); err != nil {
		t.Fatal(err)
	}
	if err := saveTranscriptCursor(
		transcriptCursorPath(paths, complete.Path),
		transcriptCursor{
			Owner:             transcriptOwnerBackfill,
			InactiveFinalized: true,
			Offset:            int64(len("{}\n")),
		},
	); err != nil {
		t.Fatal(err)
	}

	plans, err := planTranscriptGroups(
		paths,
		[]acquisition.Source{historical, complete, recent, tailOwned},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]transcriptGroupPlan, len(plans))
	for _, plan := range plans {
		byKey[plan.key] = plan
	}
	if !byKey["recent"].recent ||
		byKey["recent"].tailOwned ||
		byKey["recent"].complete {
		t.Fatalf("recent plan = %+v", byKey["recent"])
	}
	if byKey["historical"].recent ||
		byKey["historical"].tailOwned ||
		byKey["historical"].complete {
		t.Fatalf("historical plan = %+v", byKey["historical"])
	}
	if byKey["tail-owned"].recent ||
		!byKey["tail-owned"].tailOwned ||
		byKey["tail-owned"].complete {
		t.Fatalf("tail-owned plan = %+v", byKey["tail-owned"])
	}
	if byKey["complete"].recent ||
		byKey["complete"].tailOwned ||
		!byKey["complete"].complete {
		t.Fatalf("complete plan = %+v", byKey["complete"])
	}
}

func TestImportTranscriptsIncompleteReplayRotationAndStoreBeforeCursor(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	sessionID := "88888888-8888-4888-8888-888888888888"
	path := filepath.Join(
		claudeRoot,
		"projects",
		"-project",
		sessionID+".jsonl",
	)
	first := strings.Replace(
		claudeUserLine(sessionID, "2026-08-01T15:00:00Z", "first"),
		`"gitBranch":"main"`,
		`"gitBranch":"main","gitRemoteURL":"https://user:password@example.invalid/repo.git?token=private#fragment"`,
		1,
	)
	second := claudeUserLine(sessionID, "2026-08-01T15:00:01Z", "second")
	writeTranscriptTestFile(t, path, []byte(first+"\n"+second))
	paths, _ := ResolvePaths(filepath.Join(root, "belay"))
	store := &recordingTranscriptStore{err: errors.New("store unavailable")}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err == nil {
		t.Fatal("ImportTranscriptsOnce() error = nil, want store failure")
	}
	cursorFiles, _ := filepath.Glob(filepath.Join(paths.TranscriptCursors, "*.json"))
	if len(cursorFiles) != 0 {
		t.Fatalf("cursor advanced before store commit: %v", cursorFiles)
	}

	store.err = nil
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 ||
		len(store.calls[0].turns) != 1 ||
		store.calls[0].session.Coverage != belaytranscript.CoveragePartial {
		t.Fatalf("first live call = %+v", store.calls)
	}
	cursorFiles, _ = filepath.Glob(filepath.Join(paths.TranscriptCursors, "*.json"))
	if len(cursorFiles) != 1 {
		t.Fatalf("cursor files = %v, want one", cursorFiles)
	}
	cursor, err := loadTranscriptCursor(cursorFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Offset != int64(len(first)+1) {
		t.Fatalf("partial cursor offset = %d, want %d", cursor.Offset, len(first)+1)
	}
	if cursor.State.GitRemoteURL != "https://example.invalid/repo.git" {
		t.Fatalf("cursor git remote = %q", cursor.State.GitRemoteURL)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 2 ||
		len(store.calls[1].turns) != 1 ||
		store.calls[1].turns[0].TurnIndex != 0 {
		t.Fatalf("completed replay calls = %+v", store.calls)
	}

	replacement := claudeUserLine(
		sessionID,
		"2026-08-01T15:00:02Z",
		"replacement",
	) + "\n"
	if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 3 ||
		len(store.calls[2].turns) != 1 ||
		store.calls[2].turns[0].Payload.Text != "replacement" ||
		store.calls[2].turns[0].TurnIndex != 0 {
		t.Fatalf("rotation calls = %+v", store.calls)
	}
}

func TestImportTranscriptsAgesUnchangedLiveSessionToComplete(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	sessionID := "99999999-9999-4999-8999-999999999999"
	path := filepath.Join(
		claudeRoot,
		"projects",
		"-project",
		sessionID+".jsonl",
	)
	writeTranscriptTestFile(
		t,
		path,
		[]byte(claudeUserLine(
			sessionID,
			"2026-08-01T16:00:00Z",
			"active",
		)+"\n"),
	)
	paths, _ := ResolvePaths(filepath.Join(root, "belay"))
	store := &recordingTranscriptStore{}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 ||
		store.calls[0].session.Coverage != belaytranscript.CoverageLive {
		t.Fatalf("initial calls = %+v", store.calls)
	}
	old := time.Now().Add(-transcriptActiveWindow - time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 2 ||
		store.calls[1].session.Coverage != belaytranscript.CoverageComplete ||
		len(store.calls[1].turns) != 0 {
		t.Fatalf("aged calls = %+v", store.calls)
	}
}

func TestImportTranscriptsFinalizesPendingCodexUsageOnInactivity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "missing-claude"))
	codexRoot := filepath.Join(root, "codex")
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	sessionID := "aaaaaaaa-1111-4111-8111-111111111111"
	path := filepath.Join(
		codexRoot,
		"sessions",
		"2026",
		"09",
		"09",
		"rollout-2026-09-09T10-00-00-"+sessionID+".jsonl",
	)
	body := strings.Join([]string{
		`{"timestamp":"2026-09-09T17:00:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-09-09T17:00:00.100Z","type":"turn_context","payload":{"turn_id":"turn-live","model":"openai.gpt-5.6-luna"}}`,
		`{"timestamp":"2026-09-09T17:00:00.200Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":10,"cached_input_tokens":20,"cache_write_input_tokens":5}}}}`,
		"",
	}, "\n")
	writeTranscriptTestFile(t, path, []byte(body))
	paths, _ := ResolvePaths(filepath.Join(root, "belay"))
	store := &recordingTranscriptStore{}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 ||
		store.calls[0].session.Coverage != belaytranscript.CoverageLive ||
		len(store.calls[0].turns) != 0 {
		t.Fatalf("initial Codex import = %+v", store.calls)
	}
	old := time.Now().Add(-transcriptActiveWindow - time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := ImportTranscriptsOnce(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 2 ||
		store.calls[1].session.Coverage != belaytranscript.CoverageComplete ||
		len(store.calls[1].turns) != 1 ||
		store.calls[1].turns[0].InputTokens == nil ||
		*store.calls[1].turns[0].InputTokens != 75 {
		t.Fatalf("finalized Codex import = %+v", store.calls)
	}
}

func TestTranscriptGroupFinalizationEligibility(t *testing.T) {
	now := time.Date(2026, time.September, 9, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		item *openTranscriptSource
		want bool
	}{
		{
			name: "recent",
			item: &openTranscriptSource{
				modified: now.Add(-time.Minute),
			},
		},
		{
			name: "newly inactive",
			item: &openTranscriptSource{
				modified: now.Add(-transcriptActiveWindow),
			},
			want: true,
		},
		{
			name: "already finalized",
			item: &openTranscriptSource{
				modified: now.Add(-time.Hour),
				cursor: transcriptCursor{
					InactiveFinalized: true,
				},
			},
		},
		{
			name: "pending usage overrides finalized marker",
			item: &openTranscriptSource{
				modified: now.Add(-time.Hour),
				cursor: transcriptCursor{
					InactiveFinalized: true,
					State: acquisition.State{
						PendingCodexUsage: map[string]acquisition.PendingCodexUsage{
							"turn": {TurnID: "turn"},
						},
					},
				},
			},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := transcriptGroupFinalizationEligible(
				[]*openTranscriptSource{test.item},
				now,
			); got != test.want {
				t.Fatalf("eligibility = %t, want %t", got, test.want)
			}
		})
	}
}

func TestTranscriptChunkEndAlignsToCompleteJSONLRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chunk.jsonl")
	body := []byte("12345abc\nrest\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	end, err := transcriptChunkEnd(file, 0, int64(len(body)), 5)
	if err != nil {
		t.Fatal(err)
	}
	if end != int64(len("12345abc\n")) {
		t.Fatalf("chunk end = %d", end)
	}
}

func TestImportRecentTranscriptsRotatesAcrossRecentGroups(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	now := time.Date(2026, time.September, 9, 18, 0, 0, 0, time.UTC)
	sessionIDs := []string{
		"41000000-0000-4000-8000-000000000001",
		"42000000-0000-4000-8000-000000000002",
		"43000000-0000-4000-8000-000000000003",
	}
	for index, sessionID := range sessionIDs {
		path := filepath.Join(
			claudeRoot,
			"projects",
			"-project",
			sessionID+".jsonl",
		)
		writeTranscriptTestFile(
			t,
			path,
			[]byte(claudeUserLine(
				sessionID,
				"2026-09-09T17:00:00Z",
				sessionID,
			)+"\n"),
		)
		modified := now.Add(-time.Duration(index+1) * time.Minute)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	paths, _ := ResolvePaths(filepath.Join(root, "belay"))
	store := &recordingTranscriptStore{}
	for poll := 0; poll < len(sessionIDs); poll++ {
		if err := importRecentTranscriptsAt(
			context.Background(),
			paths,
			store,
			now.Add(time.Duration(poll)*time.Second),
		); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.calls) != len(sessionIDs) {
		t.Fatalf("recent calls = %d", len(store.calls))
	}
	seen := make(map[string]bool)
	for _, call := range store.calls {
		seen[call.session.NativeSessionID] = true
	}
	if len(seen) != len(sessionIDs) {
		t.Fatalf("recent scheduler repeated a group: %+v", store.calls)
	}
}

func TestDrainScanTranscriptsPrioritizesSmallPendingEligibleGroups(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	now := time.Now().UTC()
	sessions := []struct {
		id        string
		project   string
		modified  time.Time
		tailOwned bool
		eligible  bool
		large     bool
	}{
		{
			id:        "53000000-0000-4000-8000-000000000003",
			project:   "-small-tail-owned",
			modified:  now.Add(-time.Hour),
			tailOwned: true,
			eligible:  true,
		},
		{
			id:       "51000000-0000-4000-8000-000000000001",
			project:  "-newer-large",
			modified: now.Add(-time.Minute),
			eligible: true,
			large:    true,
		},
		{
			id:       "54000000-0000-4000-8000-000000000004",
			project:  "-historical",
			modified: now.Add(-time.Hour),
		},
	}
	paths, err := ResolvePaths(filepath.Join(root, "belay"))
	if err != nil {
		t.Fatal(err)
	}
	sourcePaths := make(map[string]string, len(sessions))
	for _, session := range sessions {
		path := filepath.Join(
			claudeRoot,
			"projects",
			session.project,
			session.id+".jsonl",
		)
		text := session.project
		if session.large {
			text = strings.Repeat("x", transcriptFastStartMax+1)
		}
		writeTranscriptTestFile(
			t,
			path,
			[]byte(claudeUserLine(
				session.id,
				"2026-09-10T17:00:00Z",
				text,
			)+"\n"),
		)
		if err := os.Chtimes(path, session.modified, session.modified); err != nil {
			t.Fatal(err)
		}
		sourcePaths[session.id] = path
		if session.tailOwned {
			if err := saveTranscriptCursor(
				transcriptCursorPath(paths, path),
				transcriptCursor{Owner: transcriptOwnerTail},
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	store := &recordingTranscriptStore{}
	if err := DrainScanTranscripts(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	largeInfo, err := os.Stat(sourcePaths[sessions[1].id])
	if err != nil {
		t.Fatal(err)
	}
	if largeInfo.Size() <= transcriptFastStartMax {
		t.Fatalf(
			"large transcript size = %d, want over %d",
			largeInfo.Size(),
			transcriptFastStartMax,
		)
	}
	wantOrder := []string{sessions[0].id, sessions[1].id}
	if len(store.calls) != len(wantOrder) {
		t.Fatalf("drain calls = %d, want %d", len(store.calls), len(wantOrder))
	}
	for index, want := range wantOrder {
		if got := store.calls[index].session.NativeSessionID; got != want {
			t.Fatalf("drain call %d session = %q, want %q", index, got, want)
		}
	}
	for _, session := range sessions {
		cursor, err := loadTranscriptCursor(
			transcriptCursorPath(paths, sourcePaths[session.id]),
		)
		if !session.eligible {
			if err != nil {
				t.Fatal(err)
			}
			if cursor.Offset != 0 {
				t.Fatalf("historical cursor offset = %d, want 0", cursor.Offset)
			}
			continue
		}
		info, err := os.Stat(sourcePaths[session.id])
		if err != nil {
			t.Fatal(err)
		}
		if cursor.Offset != info.Size() {
			t.Fatalf(
				"session %s cursor offset = %d, want %d",
				session.id,
				cursor.Offset,
				info.Size(),
			)
		}
	}
}

func TestDrainScanTranscriptsFullyDrainsLargeSnapshotAndDefersAppend(
	t *testing.T,
) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	sessionID := "55000000-0000-4000-8000-000000000005"
	path := filepath.Join(
		claudeRoot,
		"projects",
		"-project",
		sessionID+".jsonl",
	)
	initial := claudeUserLine(
		sessionID,
		"2026-09-10T17:00:00Z",
		"initial",
	) + "\n"
	writeTranscriptTestFile(t, path, []byte(initial))
	paths, _ := ResolvePaths(filepath.Join(root, "belay"))
	store := &recordingTranscriptStore{}
	if err := ImportRecentTranscriptsOnce(
		context.Background(),
		paths,
		store,
	); err != nil {
		t.Fatal(err)
	}

	line := claudeUserLine(
		sessionID,
		"2026-09-10T17:00:01Z",
		strings.Repeat("x", 1024),
	) + "\n"
	var growth strings.Builder
	for growth.Len() <= 2*transcriptTailChunk {
		growth.WriteString(line)
	}
	snapshotBody := initial + growth.String()
	writeTranscriptTestFile(t, path, []byte(snapshotBody))
	snapshotSize := int64(len(snapshotBody))
	if pendingBytes := snapshotSize - int64(len(initial)); pendingBytes <= transcriptTailChunk {
		t.Fatalf(
			"pending snapshot bytes = %d, want more than tail chunk %d",
			pendingBytes,
			transcriptTailChunk,
		)
	}
	lateAppend := claudeUserLine(
		sessionID,
		"2026-09-10T17:00:02Z",
		"arrived during drain",
	) + "\n"
	store.afterAppend = func() {
		writeTranscriptTestFile(
			t,
			path,
			[]byte(snapshotBody+lateAppend),
		)
	}
	callsBeforeDrain := len(store.calls)
	if err := DrainScanTranscripts(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	cursor, err := loadTranscriptCursor(transcriptCursorPath(paths, path))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Offset != snapshotSize {
		t.Fatalf(
			"first drain cursor offset = %d, want snapshot %d",
			cursor.Offset,
			snapshotSize,
		)
	}
	if calls := len(store.calls) - callsBeforeDrain; calls < 2 {
		t.Fatalf("large snapshot drain calls = %d, want multiple", calls)
	}

	if err := DrainScanTranscripts(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	cursor, err = loadTranscriptCursor(transcriptCursorPath(paths, path))
	if err != nil {
		t.Fatal(err)
	}
	finalSize := int64(len(snapshotBody + lateAppend))
	if cursor.Offset != finalSize {
		t.Fatalf(
			"second drain cursor offset = %d, want %d",
			cursor.Offset,
			finalSize,
		)
	}
}

func TestHistoricalBackfillCommitsLargeSessionInPartialChunks(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	sessionID := "44000000-0000-4000-8000-000000000004"
	path := filepath.Join(
		claudeRoot,
		"projects",
		"-project",
		sessionID+".jsonl",
	)
	line := claudeUserLine(
		sessionID,
		"2026-09-09T17:00:00Z",
		strings.Repeat("x", 1024),
	) + "\n"
	var body strings.Builder
	for body.Len() < transcriptBackfillChunk+len(line) {
		body.WriteString(line)
	}
	writeTranscriptTestFile(t, path, []byte(body.String()))
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	paths, _ := ResolvePaths(filepath.Join(root, "belay"))
	store := &recordingTranscriptStore{}
	if err := importTranscriptGroupAt(
		context.Background(),
		paths,
		store,
		[]acquisition.Source{{
			Agent:           acquisition.AgentClaude,
			Path:            path,
			GroupKey:        "large",
			NativeSessionID: sessionID,
			Primary:         true,
		}},
		transcriptOwnerBackfill,
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 ||
		store.calls[0].session.Coverage != belaytranscript.CoveragePartial ||
		len(store.calls[0].turns) == 0 {
		t.Fatalf("partial large-session import = %+v", store.calls)
	}
	cursor, err := loadTranscriptCursor(transcriptCursorPath(paths, path))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Offset <= 0 || cursor.Offset >= int64(body.Len()) {
		t.Fatalf("partial cursor offset = %d of %d", cursor.Offset, body.Len())
	}
}

func readTranscriptFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	all := append([]string{"..", "..", "testdata", "transcript"}, parts...)
	body, err := os.ReadFile(filepath.Join(all...))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeTranscriptTestFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeUserLine(sessionID, timestamp, text string) string {
	return `{"type":"user","sessionId":"` + sessionID +
		`","timestamp":"` + timestamp +
		`","cwd":"/synthetic/project","gitBranch":"main","message":{"role":"user","content":"` +
		text + `"}}`
}

// Cursor transcripts import like the other harnesses: the top-level thread and
// its nested subagent thread land in one session, ordered by timestamp, and the
// canaries planted in the fixtures are scrubbed before anything reaches the
// store. The Claude-only parent-tool pre-pass must not touch this group.
func TestScanTranscriptsImportsCursorThreadsAndScrubsBeforeStorage(t *testing.T) {
	root := t.TempDir()
	cursorRoot := filepath.Join(root, "cursor")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", cursorRoot)

	conversation := "cur-11111111-1111-4111-8111-111111111111"
	transcripts := filepath.Join(
		cursorRoot,
		"projects",
		"7f3a9c2b5e1d",
		"agent-transcripts",
	)
	thread := filepath.Join(transcripts, conversation+".jsonl")
	subagent := filepath.Join(transcripts, conversation, "agent-synthetic.jsonl")
	writeTranscriptTestFile(t, thread, readTranscriptFixture(t, "cursor", "main.jsonl"))
	writeTranscriptTestFile(
		t,
		subagent,
		readTranscriptFixture(t, "cursor", "subagent.jsonl"),
	)
	old := time.Now().Add(-10 * time.Minute)
	for _, path := range []string{thread, subagent} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := ResolvePaths(filepath.Join(root, "belay"))
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingTranscriptStore{}
	if err := ScanTranscripts(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("store calls = %d, want 1 (both threads are one session)", len(store.calls))
	}
	call := store.calls[0]
	if call.session.Agent != acquisition.AgentCursor ||
		call.session.SessionKey != numbatmap.SessionKey(
			acquisition.AgentCursor,
			conversation,
			"",
		) ||
		call.session.NativeSessionID != conversation ||
		call.session.ProjectPath != "/synthetic/cursor-project" ||
		call.session.ProjectIdentity != "/synthetic/cursor-project" {
		t.Fatalf("session = %+v", call.session)
	}
	// The fixture carries one malformed line, so coverage degrades honestly.
	if call.session.Coverage != belaytranscript.CoveragePartial {
		t.Fatalf("coverage = %q, want partial", call.session.Coverage)
	}
	if len(call.turns) != 11 {
		t.Fatalf("turns = %d, want 11", len(call.turns))
	}
	sourceFiles := make(map[string]bool)
	foundSubagent := false
	for index, turn := range call.turns {
		sourceFiles[turn.Payload.SourceFileID] = true
		if turn.TurnIndex != int64(index) {
			t.Fatalf("turn %d index = %d", index, turn.TurnIndex)
		}
		if index > 0 && turn.OccurredAt.Before(call.turns[index-1].OccurredAt) {
			t.Fatalf("turns are not timestamp ordered: %+v", call.turns)
		}
		if turn.Payload.Text == "Cursor subagent evidence" {
			foundSubagent = true
			if turn.Payload.ParentToolUseID != "cursor-subagent-thread:agent-synthetic" {
				t.Fatalf("subagent parent linkage = %q", turn.Payload.ParentToolUseID)
			}
		}
	}
	if !foundSubagent {
		t.Fatal("Cursor subagent turn missing")
	}
	if len(sourceFiles) != 2 {
		t.Fatalf("source citation identities = %v, want thread and subagent", sourceFiles)
	}
	for _, turn := range call.turns {
		for _, forbidden := range []string{
			"cursorsupersecret",
			"cursoranothersecret",
			"cursor-private-result",
			"cursor-private-read",
			"PRIVATE_CURSOR_REASONING_CANARY",
		} {
			if strings.Contains(turn.Payload.Text, forbidden) ||
				strings.Contains(turn.Payload.ToolResult, forbidden) ||
				strings.Contains(turn.Payload.RawCommand, forbidden) ||
				strings.Contains(string(turn.Payload.ToolInput), forbidden) {
				t.Fatalf("stored Cursor turn contains %q: %+v", forbidden, turn.Payload)
			}
		}
	}
}
