package userinsights

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestBuildPacketCondensesAndScrubs(t *testing.T) {
	specs := []turnSpec{
		{role: transcript.RoleUser, text: "Refactor billing in /Users/me/work/app/billing.go. Token sk-live-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456", minute: 0},
		{role: transcript.RoleAssistant, text: "Reading the module.", minute: 1, cost: 0.3},
	}
	for i := 0; i < 5; i++ {
		specs = append(specs, turnSpec{role: transcript.RoleToolCall, tool: "Read", path: "/Users/me/work/app/file" + string(rune('a'+i)) + ".go", minute: 2 + i})
	}
	specs = append(specs,
		turnSpec{role: transcript.RoleToolCall, tool: "Edit", path: "/Users/me/work/app/billing.go", minute: 8},
		turnSpec{role: transcript.RoleToolCall, tool: "Bash", command: "go test ./...", callID: "c1", minute: 9},
		turnSpec{role: transcript.RoleToolResult, callID: "c1", exit: intPointer(1), text: "", minute: 10},
		turnSpec{role: transcript.RoleUser, text: "<system-reminder>ignore</system-reminder>", minute: 11},
		turnSpec{role: transcript.RoleUser, text: "No, fix the failing test first.", minute: 12},
	)
	turns := buildTurns(t, specs)
	turns[len(turns)-3].Payload.ToolResult = "FAIL: TestX at /Users/me/work/app/billing_test.go:12"
	session := testSession(15, 2)
	packet := BuildPacket(session, turns, nil, "app", issueintel.ProjectConfig{})

	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, forbidden := range []string{"/Users/me", "sk-live-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456", "system-reminder"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("packet leaked %q: %s", forbidden, body)
		}
	}
	if packet.Session.ProjectLabel != "app" || packet.Session.UserMessages != 2 ||
		packet.Session.FileEdits != 1 || packet.Session.VerificationRuns != 1 ||
		packet.Session.Corrections != 1 || packet.Session.ToolCalls != 7 {
		t.Fatalf("unexpected packet session: %+v", packet.Session)
	}
	var collapsed *PacketTurn
	var failure *PacketTurn
	for index := range packet.Turns {
		turn := &packet.Turns[index]
		if turn.Class == "read" && turn.Count == 5 {
			collapsed = turn
		}
		if turn.Class == "test" && turn.OK != nil && !*turn.OK {
			failure = turn
		}
	}
	if collapsed == nil || len(collapsed.Targets) != 5 || collapsed.Targets[0] != "filea.go" {
		t.Fatalf("read run was not collapsed: %+v", packet.Turns)
	}
	if failure == nil || !strings.Contains(failure.Text, "FAIL: TestX") || strings.Contains(failure.Text, "/Users") {
		t.Fatalf("failing check was not retained safely: %+v", failure)
	}
	if !strings.Contains(body, `"target":"billing.go"`) {
		t.Fatalf("edit target should be a basename: %s", body)
	}
}

func TestAnalyzeSessionCountsCodexPatchesAndCanonicalWrites(t *testing.T) {
	turns := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "Rename the handler.", minute: 0},
		{role: transcript.RoleToolCall, tool: "shell", command: "apply_patch <<'EOF'\n*** Begin Patch\n*** Update File: internal/handler.go\n*** End Patch\nEOF", minute: 1},
	})
	measurements := Analyze(testSession(5, 1), turns, issueintel.ProjectConfig{})
	if measurements.FileChangeTurns != 1 {
		t.Fatalf("apply_patch should count as a file change: %+v", measurements)
	}
	events := []model.Event{{
		EventID:     "evt-1",
		OccurredAt:  testStart.Add(3 * time.Minute),
		Observation: model.Observation{Type: "file.write"},
	}}
	plain := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "Write the file.", minute: 0},
		{role: transcript.RoleToolCall, tool: "Bash", command: "cat > out.txt <<EOF\nhi\nEOF", minute: 3},
	})
	withEvents := AnalyzeSession(testSession(5, 1), plain, events, issueintel.ProjectConfig{})
	if withEvents.FileChangeTurns != 1 || withEvents.FirstChangeAt == nil {
		t.Fatalf("canonical file.write should count as a change: %+v", withEvents)
	}
}

func TestDecodeDebriefSanitizesAgainstPacket(t *testing.T) {
	packet := Packet{Turns: []PacketTurn{{Index: 0}, {Index: 4, Count: 3}, {Index: 9}}}
	raw := map[string]any{
		"headline":     "You asked for tests too late, and it cost about 20 minutes.",
		"task_summary": "Refactor the billing module.",
		"phases": []map[string]any{
			{"label": "Planning", "from_minute": 0, "to_minute": 5, "what_happened": "Discussion.", "verdict": "productive"},
			{"label": "Bad", "from_minute": 9, "to_minute": 2, "what_happened": "x", "verdict": "wasted"},
			{"label": "Odd", "from_minute": 5, "to_minute": 9, "what_happened": "y", "verdict": "sideways"},
		},
		"insights": []map[string]any{
			{"kind": "verification", "title": "Name the checks up front", "what_you_did": "You said `done?` at turn 9 [link](http://x.y).",
				"what_it_cost": "About 20 minutes.", "ideal_path": "Ask for go test first.", "say_this_instead": "Run go test ./... after each change and show me the output.",
				"evidence_turns": []int{9, 42, 5}, "confidence": 0.8},
			{"kind": "prompting", "title": "Unsupported", "what_you_did": "x", "what_it_cost": "y", "ideal_path": "z", "say_this_instead": "w",
				"evidence_turns": []int{77}, "confidence": 0.5},
		},
		"keep_doing":          []map[string]any{{"title": "Clear goal", "why": "Turn 0 named the goal.", "evidence_turns": []int{0}}},
		"prompt_length_read":  map[string]any{"verdict": "under_specified", "explanation": "No checks named.", "rewrite": "Refactor billing; run go test after each phase."},
		"next_session_opener": "Refactor billing into a service layer. Run go test ./... after each phase and report failures verbatim.",
	}
	body, _ := json.Marshal(raw)
	debrief, notes, err := DecodeDebrief(body, packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(debrief.Phases) != 2 || debrief.Phases[1].Verdict != PhaseUnclear {
		t.Fatalf("phases were not sanitized: %+v", debrief.Phases)
	}
	if len(debrief.Insights) != 1 || len(debrief.Insights[0].EvidenceTurns) != 2 ||
		debrief.Insights[0].EvidenceTurns[0] != 5 || debrief.Insights[0].EvidenceTurns[1] != 9 {
		t.Fatalf("insight evidence was not filtered to known turns: %+v", debrief.Insights)
	}
	if strings.Contains(debrief.Insights[0].WhatYouDid, "`") || strings.Contains(debrief.Insights[0].WhatYouDid, "http") {
		t.Fatalf("markup was not stripped: %q", debrief.Insights[0].WhatYouDid)
	}
	if len(notes) == 0 {
		t.Fatal("sanitization notes should record the dropped material")
	}
	if _, _, err := DecodeDebrief([]byte(`{"headline":"x","insights":[]}`), packet); err == nil {
		t.Fatal("a debrief without usable insights must be rejected")
	}
	if !json.Valid(OutputSchema()) {
		t.Fatal("output schema must be valid JSON")
	}
	if InputHash([]byte("a"), []byte("b"), "claude") == InputHash([]byte("a"), []byte("b"), "codex") {
		t.Fatal("input hash must bind the harness")
	}
}
