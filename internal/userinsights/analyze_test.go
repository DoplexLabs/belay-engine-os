package userinsights

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

var testStart = time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)

type turnSpec struct {
	role    transcript.Role
	text    string
	tool    string
	command string
	path    string
	callID  string
	exit    *int
	minute  int
	cost    float64
	parent  string
}

func intPointer(value int) *int { return &value }

func buildTurns(t *testing.T, specs []turnSpec) []transcript.Turn {
	t.Helper()
	turns := make([]transcript.Turn, 0, len(specs))
	for index, spec := range specs {
		turn := transcript.Turn{
			TurnID:     "turn-" + string(rune('a'+index)),
			SessionKey: "ses_test",
			TurnIndex:  int64(index),
			OccurredAt: testStart.Add(time.Duration(spec.minute) * time.Minute),
			Role:       spec.role,
			ToolName:   spec.tool,
		}
		if spec.cost > 0 {
			cost := spec.cost
			turn.CostUSD = &cost
		}
		turn.Payload.Text = spec.text
		turn.Payload.RawCommand = spec.command
		turn.Payload.ToolCallID = spec.callID
		turn.Payload.ExitCode = spec.exit
		turn.Payload.ParentToolUseID = spec.parent
		if spec.path != "" {
			input, err := json.Marshal(map[string]string{"file_path": spec.path})
			if err != nil {
				t.Fatal(err)
			}
			turn.Payload.ToolInput = input
		}
		turns = append(turns, turn)
	}
	return turns
}

func testSession(minutes int, userTurns int) transcript.Session {
	return transcript.Session{
		SessionKey:      "ses_test",
		Agent:           "claude-code",
		ProjectPath:     "/Users/private/project",
		ProjectIdentity: "/Users/private/project",
		StartedAt:       testStart,
		EndedAt:         testStart.Add(time.Duration(minutes) * time.Minute),
		Coverage:        transcript.CoverageComplete,
		UserTurnCount:   userTurns,
	}
}

func findingKinds(findings []Finding) []string {
	kinds := make([]string, 0, len(findings))
	for _, finding := range findings {
		kinds = append(kinds, finding.Kind)
	}
	return kinds
}

func hasKind(findings []Finding, kind string) bool {
	for _, finding := range findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}

func TestAnalyzeFlagsLateVerificationAfterCorrection(t *testing.T) {
	turns := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "Refactor billing into a service layer.", minute: 0, cost: 0.2},
		{role: transcript.RoleAssistant, text: "Starting.", minute: 1, cost: 0.4},
		{role: transcript.RoleToolCall, tool: "Edit", path: "internal/billing/service.go", minute: 2},
		{role: transcript.RoleToolCall, tool: "Write", path: "internal/billing/proration.go", minute: 10},
		{role: transcript.RoleAssistant, text: "Done, the service layer is in place.", minute: 20},
		{role: transcript.RoleUser, text: "No, you haven't tested any of this.", minute: 22},
		{role: transcript.RoleToolCall, tool: "Bash", command: "go test ./internal/billing/...", callID: "call-1", minute: 30},
		{role: transcript.RoleToolResult, callID: "call-1", exit: intPointer(0), minute: 31},
	})
	session := testSession(40, 2)
	measurements := Analyze(session, turns, issueintel.ProjectConfig{})

	if measurements.UserTurns != 2 || measurements.Corrections != 1 {
		t.Fatalf("unexpected user or correction counts: %+v", measurements)
	}
	if measurements.FileChangeTurns != 2 || measurements.FirstChangeAt == nil {
		t.Fatalf("file changes were not measured: %+v", measurements)
	}
	if measurements.VerificationRuns != 1 || measurements.VerificationPasses != 1 ||
		measurements.CorrectionsBeforeFirstVerification != 1 {
		t.Fatalf("verification was not measured: %+v", measurements)
	}
	if measurements.ChangesBeforeFirstVerificationMS != (28 * time.Minute).Milliseconds() {
		t.Fatalf("unexpected change-to-check gap: %d", measurements.ChangesBeforeFirstVerificationMS)
	}
	if measurements.PlanningDurationMS != (2*time.Minute).Milliseconds() ||
		measurements.PlanningCostUSD == nil || math.Abs(*measurements.PlanningCostUSD-0.6) > 1e-9 {
		t.Fatalf("planning window was not measured: %+v", measurements)
	}
	if measurements.FirstPromptNamesChecks {
		t.Fatal("first prompt should not be read as naming checks")
	}

	retro := BuildRetro(session, measurements, nil)
	if retro.Outcome != OutcomeVerifiedPass {
		t.Fatalf("unexpected outcome %q", retro.Outcome)
	}
	if !hasKind(retro.Findings, FindingLateVerification) {
		t.Fatalf("expected late verification finding, got %v", findingKinds(retro.Findings))
	}
	if hasKind(retro.Findings, FindingChecksNamedUpFront) {
		t.Fatal("a corrected session must not be praised for naming checks")
	}
	if !strings.HasPrefix(retro.Verdict, "Finished with passing checks.") ||
		!strings.Contains(retro.Verdict, "needs a few more sessions") {
		t.Fatalf("unexpected verdict %q", retro.Verdict)
	}
	if !strings.Contains(retro.Opener, "list the checks that must pass") {
		t.Fatalf("opener should carry the verification line: %q", retro.Opener)
	}
	for _, finding := range retro.Findings {
		for _, evidence := range finding.Evidence {
			if strings.Contains(evidence, "go test") || strings.Contains(evidence, "/Users/") {
				t.Fatalf("evidence leaked raw command or path: %q", evidence)
			}
		}
	}
}

func TestAnalyzeRecognizesChecksNamedUpFront(t *testing.T) {
	turns := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "Add proration. Run the tests after each change.", minute: 0},
		{role: transcript.RoleToolCall, tool: "Edit", path: "billing.go", minute: 1},
		{role: transcript.RoleToolCall, tool: "Bash", command: "go test ./...", callID: "c1", minute: 2},
		{role: transcript.RoleToolResult, callID: "c1", exit: intPointer(0), minute: 3},
		{role: transcript.RoleToolCall, tool: "Edit", path: "billing_test.go", minute: 4},
		{role: transcript.RoleToolCall, tool: "Bash", command: "go test ./...", callID: "c2", minute: 5},
		{role: transcript.RoleToolResult, callID: "c2", exit: intPointer(0), minute: 6},
	})
	session := testSession(8, 1)
	retro := BuildRetro(session, Analyze(session, turns, issueintel.ProjectConfig{}), nil)
	if len(retro.Findings) != 1 || retro.Findings[0].Kind != FindingChecksNamedUpFront ||
		retro.Findings[0].Tone != ToneKeep {
		t.Fatalf("expected only the positive finding, got %v", findingKinds(retro.Findings))
	}
	if retro.Opener != "" {
		t.Fatalf("a session with nothing to improve must not get an opener: %q", retro.Opener)
	}
	if !strings.Contains(retro.Verdict, "Nothing to change here.") {
		t.Fatalf("unexpected verdict %q", retro.Verdict)
	}
}

func TestAnalyzeFlagsNoVerificationAndFailingLastCheck(t *testing.T) {
	unverified := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "Rename the module.", minute: 0},
		{role: transcript.RoleToolCall, tool: "Edit", path: "a.go", minute: 1},
		{role: transcript.RoleToolCall, tool: "Edit", path: "b.go", minute: 2},
		{role: transcript.RoleToolCall, tool: "Edit", path: "c.go", minute: 3},
		{role: transcript.RoleAssistant, text: "Renamed.", minute: 4},
	})
	session := testSession(5, 1)
	retro := BuildRetro(session, Analyze(session, unverified, issueintel.ProjectConfig{}), nil)
	if retro.Outcome != OutcomeUnverifiedChanges || !hasKind(retro.Findings, FindingNoVerification) {
		t.Fatalf("expected unverified outcome and finding, got %q %v", retro.Outcome, findingKinds(retro.Findings))
	}

	failing := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "Fix the build.", minute: 0},
		{role: transcript.RoleToolCall, tool: "Edit", path: "a.go", minute: 1},
		{role: transcript.RoleToolCall, tool: "Bash", command: "npm test", callID: "c1", minute: 2},
		{role: transcript.RoleToolResult, callID: "c1", exit: intPointer(1), minute: 3},
	})
	retro = BuildRetro(session, Analyze(session, failing, issueintel.ProjectConfig{}), nil)
	if retro.Outcome != OutcomeVerifiedFail || !strings.HasPrefix(retro.Verdict, "Ended with a failing check") {
		t.Fatalf("expected failing outcome, got %q %q", retro.Outcome, retro.Verdict)
	}
}

func TestAnalyzeFlagsLongPlanningAsJudgment(t *testing.T) {
	specs := []turnSpec{}
	for minute := 0; minute < 20; minute += 4 {
		specs = append(specs,
			turnSpec{role: transcript.RoleUser, text: "What about approach " + string(rune('A'+minute/4)) + "?", minute: minute, cost: 0.5},
			turnSpec{role: transcript.RoleAssistant, text: "Considering.", minute: minute + 2, cost: 1.0},
		)
	}
	specs = append(specs,
		turnSpec{role: transcript.RoleToolCall, tool: "Edit", path: "a.go", minute: 20},
		turnSpec{role: transcript.RoleToolCall, tool: "Bash", command: "go test ./...", callID: "c1", minute: 25},
		turnSpec{role: transcript.RoleToolResult, callID: "c1", exit: intPointer(0), minute: 26},
	)
	turns := buildTurns(t, specs)
	session := testSession(30, 5)
	measurements := Analyze(session, turns, issueintel.ProjectConfig{})
	if measurements.UserTurnsBeforeFirstChange != 5 || measurements.PlanningShare == nil {
		t.Fatalf("planning was not measured: %+v", measurements)
	}
	retro := BuildRetro(session, measurements, nil)
	var planning *Finding
	for index := range retro.Findings {
		if retro.Findings[index].Kind == FindingLongPlanning {
			planning = &retro.Findings[index]
		}
	}
	if planning == nil {
		t.Fatalf("expected long planning finding, got %v", findingKinds(retro.Findings))
	}
	if planning.EvidenceClass != EvidenceJudgment || planning.TimeCostMS == nil ||
		*planning.TimeCostMS != (20*time.Minute).Milliseconds() ||
		planning.DollarCost == nil || math.Abs(*planning.DollarCost-7.5) > 1e-9 {
		t.Fatalf("planning finding is missing its evidence framing: %+v", planning)
	}
	if !strings.Contains(retro.Opener, "Push back only") {
		t.Fatalf("opener should carry the planning line: %q", retro.Opener)
	}
}

func TestAnalyzeIgnoresMachineEnvelopesAndDelegatedTurns(t *testing.T) {
	turns := buildTurns(t, []turnSpec{
		{role: transcript.RoleUser, text: "<system-reminder>ignore</system-reminder>", minute: 0},
		{role: transcript.RoleUser, text: "No, stop.", parent: "tool-9", minute: 1},
		{role: transcript.RoleUser, text: "Write the report.", minute: 2},
	})
	measurements := Analyze(testSession(3, 1), turns, issueintel.ProjectConfig{})
	if measurements.UserTurns != 1 || measurements.Corrections != 0 ||
		measurements.FirstPromptChars != len("Write the report.") {
		t.Fatalf("envelopes or delegated turns were counted: %+v", measurements)
	}
}

func TestComputeBaselineComparesOnlySameProjectCompleteSessions(t *testing.T) {
	session := testSession(60, 4)
	measurements := Measurements{DurationMS: (60 * time.Minute).Milliseconds()}
	cost := 4.0
	others := []transcript.Session{
		{SessionKey: "a", ProjectIdentity: session.ProjectIdentity, Coverage: transcript.CoverageComplete, UserTurnCount: 5, WallDurationMS: (20 * time.Minute).Milliseconds(), TotalCostUSD: &cost},
		{SessionKey: "b", ProjectIdentity: session.ProjectIdentity, Coverage: transcript.CoverageComplete, UserTurnCount: 5, WallDurationMS: (30 * time.Minute).Milliseconds(), TotalCostUSD: &cost},
		{SessionKey: "c", ProjectIdentity: session.ProjectIdentity, Coverage: transcript.CoverageComplete, UserTurnCount: 5, WallDurationMS: (40 * time.Minute).Milliseconds(), TotalCostUSD: &cost},
		{SessionKey: "other-project", ProjectIdentity: "/elsewhere", Coverage: transcript.CoverageComplete, UserTurnCount: 5, WallDurationMS: (5 * time.Minute).Milliseconds()},
		{SessionKey: "live", ProjectIdentity: session.ProjectIdentity, Coverage: transcript.CoverageLive, UserTurnCount: 5, WallDurationMS: (5 * time.Minute).Milliseconds()},
		{SessionKey: "tiny", ProjectIdentity: session.ProjectIdentity, Coverage: transcript.CoverageComplete, UserTurnCount: 1, WallDurationMS: (5 * time.Minute).Milliseconds()},
		{SessionKey: session.SessionKey, ProjectIdentity: session.ProjectIdentity, Coverage: transcript.CoverageComplete, UserTurnCount: 5, WallDurationMS: (5 * time.Minute).Milliseconds()},
	}
	baseline := ComputeBaseline(session, measurements, others)
	if baseline == nil || baseline.SessionCount != 3 {
		t.Fatalf("baseline should use exactly the three comparable sessions: %+v", baseline)
	}
	if baseline.MedianDurationMS != (30*time.Minute).Milliseconds() ||
		baseline.MedianCostUSD == nil || *baseline.MedianCostUSD != 4.0 {
		t.Fatalf("unexpected medians: %+v", baseline)
	}
	if baseline.Comparison != ComparisonLonger || baseline.DurationRatio == nil || *baseline.DurationRatio != 2 {
		t.Fatalf("expected a longer comparison: %+v", baseline)
	}
	retro := BuildRetro(session, measurements, baseline)
	if !strings.Contains(retro.Verdict, "took about twice as long as your usual session") {
		t.Fatalf("verdict should describe the comparison: %q", retro.Verdict)
	}
	if ComputeBaseline(session, measurements, others[:2]) != nil {
		t.Fatal("baseline must be nil below the minimum sample")
	}
}

func TestBuildRetroCapsFindingsAndOrdersImprovementsFirst(t *testing.T) {
	planning := (15 * time.Minute).Milliseconds()
	share := 0.5
	now := testStart
	measurements := Measurements{
		FileChangeTurns:                    4,
		Corrections:                        4,
		CorrectionsBeforeFirstVerification: 2,
		VerificationRuns:                   1,
		Compactions:                        3,
		FirstChangeAt:                      &now,
		UserTurnsBeforeFirstChange:         6,
		PlanningDurationMS:                 planning,
		PlanningShare:                      &share,
		DurationMS:                         (30 * time.Minute).Milliseconds(),
		FirstPromptNamesChecks:             true,
	}
	retro := BuildRetro(testSession(30, 6), measurements, nil)
	if len(retro.Findings) != maxFindingsPerSession {
		t.Fatalf("expected %d findings, got %v", maxFindingsPerSession, findingKinds(retro.Findings))
	}
	for _, finding := range retro.Findings {
		if finding.Tone != ToneImprove {
			t.Fatalf("improvements must fill the cap before praise: %v", findingKinds(retro.Findings))
		}
	}
	if retro.Findings[0].Kind != FindingLongPlanning {
		t.Fatalf("the finding with the largest time cost should lead: %v", findingKinds(retro.Findings))
	}
}
