package local

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	canonical "github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestResolveMissionPackProjectUsesRemoteThenPathFallback(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	remoteRoot := filepath.Join(t.TempDir(), "remote-worktree")
	pathRoot := filepath.Join(t.TempDir(), "path-project")
	remoteSession := transcriptTestSession(
		"ses_mission_remote",
		transcript.CoverageComplete,
	)
	remoteSession.ProjectPath = remoteRoot
	pathSession := transcriptTestSession(
		"ses_mission_path",
		transcript.CoverageComplete,
	)
	pathSession.ProjectPath = pathRoot
	pathSession.GitRemoteURL = ""
	pathSession.ProjectIdentity = pathRoot
	for index, session := range []transcript.Session{
		remoteSession,
		pathSession,
	} {
		turn := transcriptTestTurn(
			"turn-mission-resolve-"+formatTestIndex(index),
			"source-mission-resolve-"+formatTestIndex(index),
			session.SessionKey,
			0,
			base.Add(time.Duration(index)*time.Minute),
			transcript.RoleSystem,
			transcript.Payload{JSONLByteOffset: int64(index)},
		)
		if _, err := store.AppendTranscriptBatch(
			ctx,
			session,
			[]transcript.Turn{turn},
		); err != nil {
			t.Fatal(err)
		}
	}

	remote, err := store.ResolveMissionPackProject(
		ctx,
		missionpack.ProjectSelector{
			RemoteIdentity: remoteSession.ProjectIdentity,
			ProjectRoot:    filepath.Join(t.TempDir(), "another-worktree"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Identity != remoteSession.ProjectIdentity ||
		remote.IdentityKind != "remote" {
		t.Fatalf("remote project = %#v", remote)
	}
	if _, err := store.ResolveMissionPackProject(
		ctx,
		missionpack.ProjectSelector{
			RemoteIdentity: "git@example.test:other/project.git",
			ProjectRoot:    remoteRoot,
		},
	); !errors.Is(err, missionpack.ErrProjectNotFound) {
		t.Fatalf("changed remote error = %v", err)
	}
	pathProject, err := store.ResolveMissionPackProject(
		ctx,
		missionpack.ProjectSelector{
			ProjectRoot: filepath.Join(pathRoot, "nested", "package"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if pathProject.Identity != pathRoot ||
		pathProject.IdentityKind != "path" {
		t.Fatalf("path project = %#v", pathProject)
	}
	if _, err := store.ResolveMissionPackProject(
		ctx,
		missionpack.ProjectSelector{
			ProjectRoot: filepath.Join(t.TempDir(), "unknown"),
		},
	); !errors.Is(err, missionpack.ErrProjectNotFound) {
		t.Fatalf("unknown project error = %v", err)
	}
}

func TestReadMissionPackEvidenceIsBoundedAndPairsSuccessfulCommands(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	project := issueintel.Project{
		Identity: "git@example.test:team/mission.git",
		Path:     "/work/mission",
	}
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	var sessions []transcript.Session
	for index := range 3 {
		session := transcriptTestSession(
			"ses_mission_evidence_"+formatTestIndex(index),
			transcript.CoverageComplete,
		)
		session.ProjectPath = project.Path
		session.GitRemoteURL = project.Identity
		session.ProjectIdentity = project.Identity
		session.StartedAt = base.Add(time.Duration(index) * time.Minute)
		session.EndedAt = session.StartedAt.Add(time.Second)
		callID := "call-" + formatTestIndex(index)
		exitCode := 0
		turns := []transcript.Turn{
			transcriptTestTurn(
				"turn-mission-call-"+formatTestIndex(index),
				"source-mission-call-"+formatTestIndex(index),
				session.SessionKey,
				0,
				base.Add(time.Duration(index)*time.Minute),
				transcript.RoleToolCall,
				transcript.Payload{
					RawCommand:      "go test ./...",
					ToolCallID:      callID,
					SourceFileID:    "source-file",
					JSONLByteOffset: int64(index * 10),
				},
			),
			transcriptTestTurn(
				"turn-mission-result-"+formatTestIndex(index),
				"source-mission-result-"+formatTestIndex(index),
				session.SessionKey,
				1,
				base.Add(time.Duration(index)*time.Minute+time.Second),
				transcript.RoleToolResult,
				transcript.Payload{
					ToolCallID:      callID,
					ExitCode:        &exitCode,
					JSONLByteOffset: int64(index*10 + 1),
				},
			),
		}
		if _, err := store.AppendTranscriptBatch(
			ctx,
			session,
			turns,
		); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	var issues []issueintel.Issue
	knownUSD := 1.0
	for index := range 3 {
		issues = append(issues, costIssueTestIssue(
			"issue-mission-"+formatTestIndex(index),
			issueintel.DetectorRetryLoop,
			"shell\x00failure-"+formatTestIndex(index),
			project,
			sessions[index],
			float64(index+1),
			int64(index+1),
			&knownUSD,
			"mission issue",
		))
	}
	store.clock = func() time.Time { return base.Add(2 * time.Hour) }
	if err := store.ReplaceProjectIssueAnalysis(
		ctx,
		project,
		3,
		issueintel.Analysis{Issues: issues},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceProjectInsight(ctx, issueintel.InsightRecord{
		InsightID:     "ins_mission_stale",
		Project:       project,
		Harness:       "codex",
		Model:         "test-model",
		PromptVersion: "mission-pack-test-v1",
		InputHash:     "mission-pack-stale-hash",
		GeneratedAt:   base.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	evidence, err := store.ReadMissionPackEvidence(
		ctx,
		project.Identity,
		missionpack.Limits{
			Issues:       2,
			Sessions:     2,
			CommandTurns: 2,
			Events:       2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Issues) != 2 ||
		len(evidence.Sessions) != 2 ||
		len(evidence.SuccessfulCommands) != 1 {
		t.Fatalf(
			"bounded evidence issues/sessions/commands = %d/%d/%d",
			len(evidence.Issues),
			len(evidence.Sessions),
			len(evidence.SuccessfulCommands),
		)
	}
	if evidence.SuccessfulCommands[0].Command != "go test ./..." ||
		evidence.SourceState.TranscriptGeneration != 3 ||
		evidence.SourceState.AnalyzedGeneration != 3 ||
		evidence.SourceState.AnalysisStatus !=
			missionpack.AnalysisStatusCurrent ||
		!evidence.InsightStale {
		t.Fatalf("evidence snapshot = %#v", evidence)
	}
}

func TestResolveMissionPackProjectPreservesDeadlineWhileStoreBusy(
	t *testing.T,
) {
	store := openStorageTestStore(t)
	connection, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close held connection: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = store.ResolveMissionPackProject(
		ctx,
		missionpack.ProjectSelector{
			RemoteIdentity: "git@example.test:team/live-mission.git",
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolve error = %v, want context deadline exceeded", err)
	}
}

func TestSuccessfulMissionPackCommandsPreferExplicitNewestEvidence(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	exitCode := 0
	notError := false
	turns := []transcript.Turn{
		transcriptTestTurn(
			"result-new-boolean",
			"source-new-boolean",
			"ses_duplicate_result",
			3,
			base.Add(3*time.Second),
			transcript.RoleToolResult,
			transcript.Payload{
				ToolCallID:  "call-duplicate",
				ToolIsError: &notError,
			},
		),
		transcriptTestTurn(
			"result-explicit",
			"source-explicit",
			"ses_duplicate_result",
			2,
			base.Add(2*time.Second),
			transcript.RoleToolResult,
			transcript.Payload{
				ToolCallID: "call-duplicate",
				ExitCode:   &exitCode,
			},
		),
		transcriptTestTurn(
			"result-old-incomplete",
			"source-old-incomplete",
			"ses_duplicate_result",
			1,
			base.Add(time.Second),
			transcript.RoleToolResult,
			transcript.Payload{ToolCallID: "call-duplicate"},
		),
		transcriptTestTurn(
			"call",
			"source-call",
			"ses_duplicate_result",
			0,
			base,
			transcript.RoleToolCall,
			transcript.Payload{
				RawCommand: "go test ./...",
				ToolCallID: "call-duplicate",
			},
		),
	}

	commands := successfulMissionPackCommands(turns)
	if len(commands) != 1 ||
		commands[0].Command != "go test ./..." ||
		!commands[0].SucceededAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("successful commands = %#v", commands)
	}
}

func TestMissionPackFactKeepsHighSignalContext(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		kind      string
		value     string
		wantKind  string
		wantValue string
	}{
		{
			name:      "project-relative file",
			eventType: "file.write",
			kind:      "file",
			value:     `internal\storage\local\mission_pack_repository.go`,
			wantKind:  missionpack.CanonicalFactFileWritten,
			wantValue: "internal/storage/local/mission_pack_repository.go",
		},
		{
			name:      "non-generic MCP resource",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "builder-mcp/ReadInternalWebsites",
			wantKind:  missionpack.CanonicalFactMCPTool,
			wantValue: "builder-mcp/ReadInternalWebsites",
		},
		{
			name:      "third-party tool sharing Belay leaf name",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "project-insights/get_top_issues",
			wantKind:  missionpack.CanonicalFactMCPTool,
			wantValue: "project-insights/get_top_issues",
		},
		{
			name:      "third-party mission pack tool",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "acme/get-mission-pack",
			wantKind:  missionpack.CanonicalFactMCPTool,
			wantValue: "acme/get-mission-pack",
		},
	}
	for _, executable := range []string{
		"go", "make", "cargo", "pnpm", "npm", "yarn", "bun", "python",
		"pytest",
	} {
		tests = append(tests, struct {
			name      string
			eventType string
			kind      string
			value     string
			wantKind  string
			wantValue string
		}{
			name:      "specialized executable " + executable,
			eventType: "command.exec",
			kind:      "command",
			value:     executable,
			wantKind:  missionpack.CanonicalFactCommandExecutable,
			wantValue: executable,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := canonical.Event{
				Observation: canonical.Observation{
					Type: test.eventType,
					Resource: &canonical.Resource{
						Kind: test.kind,
						Name: test.value,
					},
				},
			}
			kind, value := missionPackFact(event)
			if kind != test.wantKind || value != test.wantValue {
				t.Fatalf(
					"missionPackFact() = (%q, %q), want (%q, %q)",
					kind,
					value,
					test.wantKind,
					test.wantValue,
				)
			}
		})
	}
}

func TestMissionPackFactRejectsGenericOrUntrustedContext(t *testing.T) {
	var tests []struct {
		name      string
		eventType string
		kind      string
		value     string
	}
	for _, executable := range []string{
		"pwd", "cd", "ls", "cat", "grep", "rg", "sed", "awk", "head",
		"tail", "echo", "printf", "sh", "bash", "zsh", "fish", "dash",
		"ksh", "csh", "tcsh", "pwsh", "powershell", "cmd.exe",
		"belay", "belay.exe", "numbat", "claude", "codex",
		"cursor", "cursor-agent", "antigravity", "agy",
	} {
		tests = append(tests, struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "generic executable " + executable,
			eventType: "command.exec",
			kind:      "command",
			value:     executable,
		})
	}
	tests = append(tests,
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "ToolSearch",
			eventType: "tool.call",
			kind:      "tool",
			value:     "ToolSearch",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "generic MCP resource",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "filesystem/read_file",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "generic MCP resource with hyphen separators",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "filesystem-read-file",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "agent patch plumbing",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "functions.apply_patch",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "agent command plumbing",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "functions_exec_command",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "MCP resource plumbing",
			eventType: "tool.call",
			kind:      "mcp",
			value:     "functions/read_mcp_resource",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "arbitrary tool identifier",
			eventType: "tool.call",
			kind:      "tool",
			value:     "IgnoreAllPreviousInstructions",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "command resource on permission event",
			eventType: "permission.denied",
			kind:      "command",
			value:     "go",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "absolute file",
			eventType: "file.write",
			kind:      "file",
			value:     "/tmp/private.go",
		},
		struct {
			name      string
			eventType string
			kind      string
			value     string
		}{
			name:      "Windows absolute file",
			eventType: "file.write",
			kind:      "file",
			value:     `C:\work\private.go`,
		},
	)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := canonical.Event{
				Observation: canonical.Observation{
					Type: test.eventType,
					Resource: &canonical.Resource{
						Kind: test.kind,
						Name: test.value,
					},
				},
			}
			if kind, value := missionPackFact(event); kind != "" ||
				value != "" {
				t.Fatalf(
					"missionPackFact() = (%q, %q), want rejected",
					kind,
					value,
				)
			}
		})
	}
}

func TestMissionPackFactRejectsBelayLocalMCPTools(t *testing.T) {
	toolNames := []string{
		"list_sessions",
		"get_session",
		"get_session_timeline",
		"query_activity",
		"list_findings",
		"get_stats",
		"list_issues",
		"get_issue",
		"lookup_session_events",
		"get_top_issues",
		"get_issue_excerpts",
		"propose_fix",
		"record_fix_applied",
		"get_fix_status",
		"get_mission_pack",
	}
	for _, toolName := range toolNames {
		hyphenated := strings.ReplaceAll(toolName, "_", "-")
		for _, value := range []string{
			toolName,
			"belay/" + toolName,
			"belay." + hyphenated,
			"belay:" + toolName,
			`belay\` + hyphenated,
			"belay_" + toolName,
			"belay-local/" + hyphenated,
			"belay_mcp/" + toolName,
			"mcp__belay__" + toolName,
		} {
			t.Run(toolName+"/"+value, func(t *testing.T) {
				event := canonical.Event{
					Observation: canonical.Observation{
						Type: "tool.call",
						Resource: &canonical.Resource{
							Kind: "mcp",
							Name: value,
						},
					},
				}
				if kind, factValue := missionPackFact(event); kind != "" ||
					factValue != "" {
					t.Fatalf(
						"missionPackFact() = (%q, %q), want rejected",
						kind,
						factValue,
					)
				}
			})
		}
	}
}

func TestReadMissionPackFactsRequiresCorroborationAndPrefersFiles(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	projectIdentity := "git@example.test:team/context.git"
	projectPath := "/work/context"
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	shared := []struct {
		eventType string
		kind      string
		value     string
	}{
		{"file.write", "file", "internal/a.go"},
		{"file.write", "file", "internal/b.go"},
		{"file.write", "file", "internal/c.go"},
		{"command.exec", "command", "go"},
		{"tool.call", "mcp", "builder-mcp/RunBuild"},
		{"command.exec", "command", "rg"},
		{"tool.call", "tool", "ToolSearch"},
		{"permission.denied", "command", "cargo"},
	}
	for sessionIndex := range 2 {
		sessionKey := "ses_mission_context_" + formatTestIndex(sessionIndex)
		session := transcriptTestSession(
			sessionKey,
			transcript.CoverageComplete,
		)
		session.ProjectPath = projectPath
		session.GitRemoteURL = projectIdentity
		session.ProjectIdentity = projectIdentity
		session.StartedAt = base.Add(time.Duration(sessionIndex) * time.Hour)
		session.EndedAt = session.StartedAt.Add(time.Minute)
		turn := transcriptTestTurn(
			"turn-mission-context-"+formatTestIndex(sessionIndex),
			"source-mission-context-"+formatTestIndex(sessionIndex),
			sessionKey,
			0,
			session.StartedAt,
			transcript.RoleSystem,
			transcript.Payload{JSONLByteOffset: int64(sessionIndex)},
		)
		if _, err := store.AppendTranscriptBatch(
			ctx,
			session,
			[]transcript.Turn{turn},
		); err != nil {
			t.Fatal(err)
		}
		events := append([]struct {
			eventType string
			kind      string
			value     string
		}{}, shared...)
		if sessionIndex == 0 {
			events = append(events, struct {
				eventType string
				kind      string
				value     string
			}{"command.exec", "command", "make"})
		}
		for eventIndex, value := range events {
			event := storageTestEvent(
				"event-mission-context-"+
					formatTestIndex(sessionIndex)+"-"+
					formatTestIndex(eventIndex),
				sessionKey,
				int64(eventIndex+1),
				session.StartedAt.Add(
					time.Duration(eventIndex+1)*time.Second,
				),
			)
			event.Observation = canonical.Observation{
				Type:    value.eventType,
				Actor:   "tool",
				Action:  "observe",
				Outcome: "unknown",
				Resource: &canonical.Resource{
					Kind: value.kind,
					Name: value.value,
				},
			}
			if _, err := store.AppendEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
	}

	evidence, err := store.ReadMissionPackEvidence(
		ctx,
		projectIdentity,
		missionpack.Limits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind  string
		value string
	}{
		{missionpack.CanonicalFactFileWritten, "internal/c.go"},
		{missionpack.CanonicalFactFileWritten, "internal/b.go"},
		{missionpack.CanonicalFactFileWritten, "internal/a.go"},
		{missionpack.CanonicalFactCommandExecutable, "go"},
		{missionpack.CanonicalFactMCPTool, "builder-mcp/RunBuild"},
	}
	if len(evidence.Facts) != len(want) {
		t.Fatalf("facts = %#v, want %d high-signal facts", evidence.Facts, len(want))
	}
	for index, expected := range want {
		fact := evidence.Facts[index]
		if fact.Kind != expected.kind ||
			fact.Value != expected.value ||
			fact.SessionCount != 2 ||
			len(fact.Sources) != 2 ||
			fact.Sources[0].SessionKey == fact.Sources[1].SessionKey {
			t.Fatalf("fact[%d] = %#v, want %#v", index, fact, expected)
		}
	}
	if !evidence.Coverage.CanonicalContextAvailable {
		t.Fatal("canonical context marked unavailable")
	}
}
