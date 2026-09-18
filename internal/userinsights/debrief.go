package userinsights

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/numbatmap"
	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

// DebriefPromptVersion changes whenever the packet shape, the instructions,
// or the output schema change, so stored debriefs are regenerated.
const DebriefPromptVersion = "belay.habits-debrief-prompt.v2"

const (
	DebriefSchemaVersion = "belay.habits-debrief.v1"

	maxPacketBytes            = 160 << 10
	maxUserTextBytes          = 2000
	maxAssistantTextBytes     = 600
	maxTargetBytes            = 160
	maxFailureBytes           = 320
	maxPacketTurns            = 900
	reducedUserTextBytes      = 900
	reducedAssistantTextBytes = 220

	maxTitleBytes       = 60
	maxHeadlineBytes    = 220
	maxSummaryBytes     = 320
	maxShortBytes       = 100
	maxMediumBytes      = 320
	maxLongBytes        = 480
	maxMessageBytes     = 900
	maxOpenerBytes      = 1200
	maxPhases           = 6
	maxInsights         = 5
	maxKeepDoing        = 3
	maxEvidenceTurns    = 6
	maxPhaseTargets     = 5
	collapsibleRunStart = 3

	PhaseProductive   = "productive"
	PhasePartlyWasted = "partly_wasted"
	PhaseWasted       = "wasted"
	PhaseUnclear      = "unclear"

	InsightPrompting    = "prompting"
	InsightScoping      = "scoping"
	InsightVerification = "verification"
	InsightDelegation   = "delegation"
	InsightContext      = "context"
	InsightWorkflow     = "workflow"
	InsightReview       = "review"

	LengthOverSpecified  = "over_specified"
	LengthUnderSpecified = "under_specified"
	LengthAboutRight     = "about_right"
)

var (
	ErrDebriefDecode = errors.New("habits debrief output is invalid")

	localPathPattern = regexp.MustCompile(
		`/(?:Users|home|private|tmp|var/folders)/[^\s"'<>\\]+`,
	)
	markupPattern = regexp.MustCompile(
		`(?i)</?[a-z][^>]*>|\[[^\]]+\]\([^)]+\)|https?://\S+`,
	)
	phaseVerdicts = map[string]bool{
		PhaseProductive: true, PhasePartlyWasted: true,
		PhaseWasted: true, PhaseUnclear: true,
	}
	insightKinds = map[string]bool{
		InsightPrompting: true, InsightScoping: true,
		InsightVerification: true, InsightDelegation: true,
		InsightContext: true, InsightWorkflow: true, InsightReview: true,
	}
	lengthVerdicts = map[string]bool{
		LengthOverSpecified: true, LengthUnderSpecified: true,
		LengthAboutRight: true,
	}
)

// Packet is the bounded, scrubbed evidence the harness reasons over. It is
// built from one session only and never leaves the machine except through
// the user's own harness invocation.
type Packet struct {
	Session      PacketSession `json:"session"`
	Turns        []PacketTurn  `json:"turns"`
	Measurements Measurements  `json:"measurements"`
	Notes        []string      `json:"notes,omitempty"`
}

type PacketSession struct {
	Agent              string   `json:"agent"`
	ProjectLabel       string   `json:"project_label"`
	StartedAt          string   `json:"started_at"`
	DurationMinutes    float64  `json:"duration_minutes"`
	TotalCostUSD       *float64 `json:"total_cost_usd"`
	UserMessages       int      `json:"user_messages"`
	AssistantMessages  int      `json:"assistant_messages"`
	ToolCalls          int      `json:"tool_calls"`
	FileEdits          int      `json:"file_edits"`
	VerificationRuns   int      `json:"verification_runs"`
	Corrections        int      `json:"corrections"`
	ContextCompactions int      `json:"context_compactions"`
	TurnsOmitted       int      `json:"turns_omitted"`
}

// PacketTurn is one condensed transcript turn. Runs of similar read, search,
// or command calls collapse into one entry with a count.
type PacketTurn struct {
	Index   int64    `json:"i"`
	Minute  float64  `json:"m"`
	Role    string   `json:"r"`
	Text    string   `json:"t,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Class   string   `json:"class,omitempty"`
	Target  string   `json:"target,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Count   int      `json:"count,omitempty"`
	OK      *bool    `json:"ok,omitempty"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// Debrief is the harness-authored analysis for the human operator.
type Debrief struct {
	Title             string       `json:"title"`
	Headline          string       `json:"headline"`
	TaskSummary       string       `json:"task_summary"`
	Phases            []Phase      `json:"phases"`
	Insights          []Insight    `json:"insights"`
	KeepDoing         []KeepDoing  `json:"keep_doing"`
	PromptLength      PromptLength `json:"prompt_length_read"`
	NextSessionOpener string       `json:"next_session_opener"`
}

type Phase struct {
	Label        string  `json:"label"`
	FromMinute   float64 `json:"from_minute"`
	ToMinute     float64 `json:"to_minute"`
	WhatHappened string  `json:"what_happened"`
	Verdict      string  `json:"verdict"`
}

type Insight struct {
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	WhatYouDid     string   `json:"what_you_did"`
	WhatItCost     string   `json:"what_it_cost"`
	IdealPath      string   `json:"ideal_path"`
	SayThisInstead string   `json:"say_this_instead"`
	EvidenceTurns  []int64  `json:"evidence_turns"`
	Confidence     float64  `json:"confidence"`
	AtMinute       *float64 `json:"at_minute"`
	MinutesCost    float64  `json:"minutes_cost"`
	DollarsCost    float64  `json:"dollars_cost"`
}

type KeepDoing struct {
	Title         string  `json:"title"`
	Why           string  `json:"why"`
	EvidenceTurns []int64 `json:"evidence_turns"`
}

type PromptLength struct {
	Verdict     string `json:"verdict"`
	Explanation string `json:"explanation"`
	Rewrite     string `json:"rewrite"`
}

// DebriefRecord is the stored, provenance-tagged debrief for one session.
type DebriefRecord struct {
	SchemaVersion   string    `json:"schema_version"`
	SessionKey      string    `json:"session_key"`
	ProjectIdentity string    `json:"project_identity,omitempty"`
	Harness         string    `json:"harness"`
	Model           string    `json:"model,omitempty"`
	PromptVersion   string    `json:"prompt_version"`
	InputHash       string    `json:"input_hash"`
	GeneratedAt     time.Time `json:"generated_at"`
	Debrief         Debrief   `json:"debrief"`
	Sanitization    []string  `json:"sanitization,omitempty"`
}

// BuildPacket condenses one session into the harness evidence packet. Events
// are the session's canonical Numbat events and may be nil.
func BuildPacket(
	session transcript.Session,
	turns []transcript.Turn,
	events []model.Event,
	projectLabel string,
	config issueintel.ProjectConfig,
) Packet {
	measurements := AnalyzeSession(session, turns, events, config)
	packet := Packet{
		Session: PacketSession{
			Agent:              strings.TrimSpace(session.Agent),
			ProjectLabel:       strings.TrimSpace(projectLabel),
			StartedAt:          session.StartedAt.UTC().Format(time.RFC3339),
			DurationMinutes:    roundMinutes(measurements.DurationMS),
			TotalCostUSD:       copyFloat(measurements.CostUSD),
			UserMessages:       measurements.UserTurns,
			AssistantMessages:  measurements.AssistantTurns,
			FileEdits:          measurements.FileChangeTurns,
			VerificationRuns:   measurements.VerificationRuns,
			Corrections:        measurements.Corrections,
			ContextCompactions: measurements.Compactions,
		},
		Measurements: measurements,
	}
	if len(turns) == 0 {
		return packet
	}
	start := turns[0].OccurredAt
	results := make(map[string]transcript.Turn)
	for _, turn := range turns {
		if turn.Role == transcript.RoleToolResult {
			if id := strings.TrimSpace(turn.Payload.ToolCallID); id != "" {
				results[id] = turn
			}
		}
	}
	userBudget, assistantBudget := maxUserTextBytes, maxAssistantTextBytes
	for attempt := 0; attempt < 3; attempt++ {
		packet.Turns, packet.Session.ToolCalls, packet.Session.TurnsOmitted = condenseTurns(
			session,
			turns,
			results,
			start,
			config,
			userBudget,
			assistantBudget,
			attempt >= 2,
		)
		encoded, err := json.Marshal(packet)
		if err == nil && len(encoded) <= maxPacketBytes && len(packet.Turns) <= maxPacketTurns {
			break
		}
		userBudget, assistantBudget = reducedUserTextBytes, reducedAssistantTextBytes
		packet.Notes = appendUnique(
			packet.Notes,
			"Long session: assistant text was shortened and routine tool calls were collapsed to fit the evidence budget.",
		)
	}
	return packet
}

func condenseTurns(
	session transcript.Session,
	turns []transcript.Turn,
	results map[string]transcript.Turn,
	start time.Time,
	config issueintel.ProjectConfig,
	userBudget int,
	assistantBudget int,
	dropRoutine bool,
) ([]PacketTurn, int, int) {
	condensed := make([]PacketTurn, 0, len(turns))
	toolCalls := 0
	omitted := 0
	flushRun := func(run []PacketTurn) {
		if len(run) == 0 {
			return
		}
		if len(run) < collapsibleRunStart {
			condensed = append(condensed, run...)
			return
		}
		targets := make([]string, 0, maxPhaseTargets)
		seen := make(map[string]bool)
		var cost float64
		costKnown := false
		for _, entry := range run {
			if entry.CostUSD != nil {
				cost += *entry.CostUSD
				costKnown = true
			}
			if entry.Target != "" && !seen[entry.Target] && len(targets) < maxPhaseTargets {
				seen[entry.Target] = true
				targets = append(targets, entry.Target)
			}
		}
		collapsed := PacketTurn{
			Index:   run[0].Index,
			Minute:  run[0].Minute,
			Role:    "tool",
			Class:   run[0].Class,
			Count:   len(run),
			Targets: targets,
		}
		if costKnown {
			collapsed.CostUSD = &cost
		}
		condensed = append(condensed, collapsed)
	}
	var run []PacketTurn
	runClass := ""
	for _, turn := range turns {
		minute := roundMinutes(turn.OccurredAt.Sub(start).Milliseconds())
		if minute < 0 {
			minute = 0
		}
		switch turn.Role {
		case transcript.RoleUser:
			flushRun(run)
			run, runClass = nil, ""
			text := strings.TrimSpace(turn.Payload.Text)
			if text == "" || transcriptissues.IsMachineGeneratedEnvelope(text) ||
				strings.TrimSpace(turn.Payload.ParentToolUseID) != "" {
				omitted++
				continue
			}
			condensed = append(condensed, PacketTurn{
				Index:  turn.TurnIndex,
				Minute: minute,
				Role:   "user",
				Text:   clipText(scrubText(text), userBudget),
			})
		case transcript.RoleAssistant:
			flushRun(run)
			run, runClass = nil, ""
			text := strings.TrimSpace(turn.Payload.Text)
			entry := PacketTurn{
				Index:   turn.TurnIndex,
				Minute:  minute,
				Role:    "assistant",
				CostUSD: copyFloat(turn.CostUSD),
			}
			if text != "" {
				entry.Text = clipText(scrubText(text), assistantBudget)
			} else if turn.CostUSD == nil {
				omitted++
				continue
			}
			condensed = append(condensed, entry)
		case transcript.RoleCompactionSummary:
			flushRun(run)
			run, runClass = nil, ""
			condensed = append(condensed, PacketTurn{
				Index:  turn.TurnIndex,
				Minute: minute,
				Role:   "compaction",
				Text:   "The agent compressed its context here and continued from a summary.",
			})
		case transcript.RoleToolCall:
			toolCalls++
			class, target := classifyToolCall(session, turn, config)
			entry := PacketTurn{
				Index:   turn.TurnIndex,
				Minute:  minute,
				Role:    "tool",
				Tool:    clipText(strings.TrimSpace(turn.ToolName), 40),
				Class:   class,
				Target:  target,
				CostUSD: copyFloat(turn.CostUSD),
			}
			if result, ok := results[strings.TrimSpace(turn.Payload.ToolCallID)]; ok {
				if failed, known := transcriptissues.ExplicitToolResultFailed(result); known {
					okValue := !failed
					entry.OK = &okValue
					if failed && class != "read" && class != "search" {
						flushRun(run)
						run, runClass = nil, ""
						entry.Text = clipText(scrubText(strings.TrimSpace(result.Payload.ToolResult)), maxFailureBytes)
						condensed = append(condensed, entry)
						continue
					}
				}
			}
			routine := class == "read" || class == "search" || class == "other"
			if routine && dropRoutine {
				omitted++
				continue
			}
			if routine || class == "command" {
				if runClass != "" && runClass != class {
					flushRun(run)
					run = nil
				}
				runClass = class
				run = append(run, entry)
				continue
			}
			flushRun(run)
			run, runClass = nil, ""
			condensed = append(condensed, entry)
		default:
			omitted++
		}
	}
	flushRun(run)
	return condensed, toolCalls, omitted
}

func classifyToolCall(
	session transcript.Session,
	turn transcript.Turn,
	config issueintel.ProjectConfig,
) (string, string) {
	if files := editedFilesForTurn(turn); len(files) > 0 {
		return "edit", clipText(strings.Join(baseNames(files, maxPhaseTargets), ", "), maxTargetBytes)
	}
	if class, raw, ok := transcriptissues.RetainedVerificationCommand(turn, config); ok {
		_ = class
		return "test", clipText(scrubText(raw), maxTargetBytes)
	}
	name := strings.ToLower(strings.TrimSpace(turn.ToolName))
	raw := strings.TrimSpace(turn.Payload.RawCommand)
	switch {
	case raw != "":
		return "command", clipText(scrubText(raw), maxTargetBytes)
	case strings.Contains(name, "read") || strings.Contains(name, "cat") || strings.Contains(name, "view"):
		return "read", clipText(scrubText(toolInputTarget(turn)), maxTargetBytes)
	case strings.Contains(name, "grep") || strings.Contains(name, "glob") ||
		strings.Contains(name, "search") || strings.Contains(name, "find") ||
		strings.Contains(name, "ls"):
		return "search", clipText(scrubText(toolInputTarget(turn)), maxTargetBytes)
	case strings.Contains(name, "agent") || strings.Contains(name, "task") ||
		strings.Contains(name, "delegate"):
		return "delegate", clipText(scrubText(toolInputTarget(turn)), maxTargetBytes)
	case strings.Contains(name, "web") || strings.Contains(name, "fetch") ||
		strings.Contains(name, "browser"):
		return "web", ""
	default:
		return "other", ""
	}
}

func toolInputTarget(turn transcript.Turn) string {
	if len(turn.Payload.ToolInput) == 0 {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(turn.Payload.ToolInput, &input) != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "pattern", "query", "prompt", "description", "url"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			value = strings.TrimSpace(value)
			if key == "file_path" || key == "path" {
				return path.Base(strings.ReplaceAll(value, "\\", "/"))
			}
			return value
		}
	}
	return ""
}

// editedFilesForTurn extends the shared edit-tool detection with Codex
// apply_patch invocations, which arrive as shell commands.
func editedFilesForTurn(turn transcript.Turn) []string {
	if turn.Role != transcript.RoleToolCall {
		return nil
	}
	if files := transcriptissues.ExtractEditedFiles(turn); len(files) > 0 {
		return files
	}
	raw := turn.Payload.RawCommand
	if raw == "" && len(turn.Payload.ToolInput) > 0 {
		raw = string(turn.Payload.ToolInput)
	}
	if !strings.Contains(raw, "*** Begin Patch") && !strings.Contains(raw, "apply_patch") {
		return nil
	}
	var files []string
	for _, match := range applyPatchFilePattern.FindAllStringSubmatch(raw, -1) {
		if len(match) == 2 {
			files = append(files, strings.TrimSpace(match[1]))
		}
	}
	if len(files) == 0 && strings.Contains(raw, "apply_patch") {
		files = []string{"patch"}
	}
	return files
}

var applyPatchFilePattern = regexp.MustCompile(`\*\*\* (?:Add|Update|Delete) File: ([^\n\\"]+)`)

func baseNames(files []string, limit int) []string {
	result := make([]string, 0, limit)
	seen := make(map[string]bool)
	for _, file := range files {
		base := path.Base(strings.ReplaceAll(strings.TrimSpace(file), "\\", "/"))
		if base == "" || base == "." || seen[base] {
			continue
		}
		seen[base] = true
		result = append(result, base)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func scrubText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value, _ = numbatmap.ScrubSecrets(value)
	value = localPathPattern.ReplaceAllStringFunc(value, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".,;:)]}")
		trailing := raw[len(trimmed):]
		base := path.Base(strings.ReplaceAll(trimmed, "\\", "/"))
		if base != "" && base != "." && base != "/" {
			return base + trailing
		}
		return "LOCAL_PATH" + trailing
	})
	return value
}

func clipText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	cut := maximum - len(" […]")
	for cut > 0 && !utf8Boundary(value, cut) {
		cut--
	}
	return value[:cut] + " […]"
}

func utf8Boundary(value string, index int) bool {
	return index >= len(value) || (value[index]&0xC0) != 0x80
}

func roundMinutes(ms int64) float64 {
	return float64(ms/6000) / 10
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Prompt renders the harness instructions followed by the packet JSON.
func Prompt(packet Packet) ([]byte, error) {
	body, err := json.Marshal(packet)
	if err != nil {
		return nil, errors.New("encode habits packet")
	}
	instructions := []byte(debriefInstructions)
	return append(instructions, body...), nil
}

const debriefInstructions = `You are an expert operator of AI coding agents, debriefing the HUMAN developer who drove the session below. Your reader is that developer, not the agent. Your job is to show them, with specifics from this session, where their own prompting, scoping, verification, delegation, and review habits cost time or money, what an expert would have done at that exact moment, and the exact message they should have sent instead.

Rules:
- Use only the packet. Do not use tools, browse, inspect files, or invent facts. Every claim must be traceable to turn indexes ("i") in the packet, and every evidence_turns value must be an index that appears in the packet.
- Ground time and cost in the packet: minute offsets ("m"), per-turn cost ("cost_usd"), duration_minutes, and total_cost_usd. Say "about 18 minutes" or "roughly $4", never precise numbers you cannot support.
- Be specific and concrete. Name the moment, quote the gist of what the developer said, and say what it led to. Generic advice ("write clearer prompts") is worthless here.
- "ideal_path" is what an expert operator would have done at that point in THIS task. "say_this_instead" is a complete, ready-to-paste message for that moment, written in the developer's voice, concrete to this task and codebase, with no placeholders, no angle brackets, and no meta commentary.
- Judge prompt length honestly: developers often over-specify simple tasks (the agent would have understood a one-line ask) and under-specify complex ones (no success criteria, no checks named, no constraints). Say which happened here and show the rewrite.
- Look for: checks requested late or never; corrections that reveal a rule the agent never had; long discussion before any change when the developer already knew what they wanted; accepting "done" without asking for proof; work that should have been one session split across context resets; reading and searching the developer could have short-circuited by pointing at the file; delegation that was or was not worth it.
- Also name what the developer did well in keep_doing, briefly, with evidence.
- Phases must cover the session in order from minute 0 to the end, with a plain verdict for each.
- "title" is a plain three-to-seven word name for what the session was about, like a commit subject ("Billing service layer refactor"). It names the task, never the verdict.
- "at_minute" is the minute offset, from the packet's "m" values, of the moment each insight is about, so it can be placed on the session's timeline.
- "minutes_cost" and "dollars_cost" are your numeric estimate of what the insight cost, derived from the packet's minute offsets and per-turn cost_usd. Use 0 when you cannot support a number; never guess a precise figure.
- next_session_opener is the concrete opening message for the next similar task in this project, written from what this session shows the developer actually wants: goal, constraints, checks that must pass, and how to report. No placeholders.
- Do not moralize, flatter, or hedge. Plain text only: no Markdown, no bullet characters, no URLs, no backticks, no code fences. Keep every field within its length limit.
- If the session was a short exchange with nothing worth improving, say so in the headline, give one insight of kind "workflow" explaining why it went well, and still fill every field.

Return only schema-valid JSON.

`

// OutputSchema is the JSON schema handed to the harness.
func OutputSchema() []byte {
	str := func(maximum int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": maximum}
	}
	turns := map[string]any{
		"type":     "array",
		"minItems": 1,
		"maxItems": maxEvidenceTurns,
		"items":    map[string]any{"type": "integer", "minimum": 0},
	}
	schema := map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"title", "headline", "task_summary", "phases", "insights", "keep_doing",
			"prompt_length_read", "next_session_opener",
		},
		"properties": map[string]any{
			"title":        str(maxTitleBytes),
			"headline":     str(maxHeadlineBytes),
			"task_summary": str(maxSummaryBytes),
			"phases": map[string]any{
				"type": "array", "minItems": 1, "maxItems": maxPhases,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"label", "from_minute", "to_minute", "what_happened", "verdict"},
					"properties": map[string]any{
						"label":         str(maxShortBytes),
						"from_minute":   map[string]any{"type": "number", "minimum": 0},
						"to_minute":     map[string]any{"type": "number", "minimum": 0},
						"what_happened": str(maxMediumBytes),
						"verdict": map[string]any{"type": "string", "enum": []string{
							PhaseProductive, PhasePartlyWasted, PhaseWasted, PhaseUnclear,
						}},
					},
				},
			},
			"insights": map[string]any{
				"type": "array", "minItems": 1, "maxItems": maxInsights,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{
						"kind", "title", "what_you_did", "what_it_cost", "ideal_path",
						"say_this_instead", "evidence_turns", "confidence", "at_minute",
						"minutes_cost", "dollars_cost",
					},
					"properties": map[string]any{
						"kind": map[string]any{"type": "string", "enum": []string{
							InsightPrompting, InsightScoping, InsightVerification,
							InsightDelegation, InsightContext, InsightWorkflow, InsightReview,
						}},
						"title":            str(maxShortBytes),
						"what_you_did":     str(maxLongBytes),
						"what_it_cost":     str(maxMediumBytes),
						"ideal_path":       str(maxLongBytes),
						"say_this_instead": str(maxMessageBytes),
						"evidence_turns":   turns,
						"confidence":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
						"at_minute":        map[string]any{"type": "number", "minimum": 0},
						"minutes_cost":     map[string]any{"type": "number", "minimum": 0},
						"dollars_cost":     map[string]any{"type": "number", "minimum": 0},
					},
				},
			},
			"keep_doing": map[string]any{
				"type": "array", "minItems": 0, "maxItems": maxKeepDoing,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"title", "why", "evidence_turns"},
					"properties": map[string]any{
						"title":          str(maxShortBytes),
						"why":            str(maxMediumBytes),
						"evidence_turns": turns,
					},
				},
			},
			"prompt_length_read": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"verdict", "explanation", "rewrite"},
				"properties": map[string]any{
					"verdict": map[string]any{"type": "string", "enum": []string{
						LengthOverSpecified, LengthUnderSpecified, LengthAboutRight,
					}},
					"explanation": str(maxMediumBytes),
					"rewrite":     str(maxMessageBytes),
				},
			},
			"next_session_opener": str(maxOpenerBytes),
		},
	}
	encoded, _ := json.Marshal(schema)
	return encoded
}

// InputHash binds a stored debrief to the exact prompt, schema, harness, and
// prompt version that produced it.
func InputHash(prompt, schema []byte, harness string) string {
	digest := sha256.New()
	for _, part := range [][]byte{prompt, schema, []byte(harness), []byte(DebriefPromptVersion)} {
		digest.Write(part)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// DecodeDebrief parses and sanitizes harness output against the packet it
// was generated from. Unknown turn references are dropped, enums are
// enforced, and every string is stripped of markup and clipped.
func DecodeDebrief(body []byte, packet Packet) (Debrief, []string, error) {
	var raw Debrief
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&raw); err != nil {
		return Debrief{}, nil, ErrDebriefDecode
	}
	known := make(map[int64]bool, len(packet.Turns))
	for _, turn := range packet.Turns {
		known[turn.Index] = true
		for offset := 0; offset < turn.Count; offset++ {
			known[turn.Index+int64(offset)] = true
		}
	}
	var notes []string
	note := func(value string) { notes = appendUnique(notes, value) }
	clean := func(value string, maximum int) string {
		cleaned := markupPattern.ReplaceAllString(value, "")
		cleaned = strings.Map(func(r rune) rune {
			if r == '`' || (unicode.IsControl(r) && r != '\n') {
				return -1
			}
			return r
		}, cleaned)
		cleaned = strings.TrimSpace(cleaned)
		if len(cleaned) > maximum {
			note("Some fields were shortened to their length limits.")
		}
		return clipText(cleaned, maximum)
	}
	filterTurns := func(values []int64) []int64 {
		result := make([]int64, 0, len(values))
		seen := make(map[int64]bool)
		for _, value := range values {
			if !known[value] {
				note("A cited turn that is not in the evidence was dropped.")
				continue
			}
			if seen[value] {
				continue
			}
			seen[value] = true
			result = append(result, value)
			if len(result) >= maxEvidenceTurns {
				break
			}
		}
		sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
		return result
	}

	out := Debrief{
		Title:             clean(raw.Title, maxTitleBytes),
		Headline:          clean(raw.Headline, maxHeadlineBytes),
		TaskSummary:       clean(raw.TaskSummary, maxSummaryBytes),
		NextSessionOpener: clean(raw.NextSessionOpener, maxOpenerBytes),
		Phases:            make([]Phase, 0, len(raw.Phases)),
		Insights:          make([]Insight, 0, len(raw.Insights)),
		KeepDoing:         make([]KeepDoing, 0, len(raw.KeepDoing)),
	}
	if out.Headline == "" {
		return Debrief{}, nil, ErrDebriefDecode
	}
	for _, phase := range raw.Phases {
		if len(out.Phases) >= maxPhases {
			break
		}
		verdict := phase.Verdict
		if !phaseVerdicts[verdict] {
			verdict = PhaseUnclear
			note("A phase verdict outside the catalog was reset to unclear.")
		}
		if phase.FromMinute < 0 || phase.ToMinute < phase.FromMinute {
			note("A phase with an impossible time range was dropped.")
			continue
		}
		label := clean(phase.Label, maxShortBytes)
		if label == "" {
			continue
		}
		out.Phases = append(out.Phases, Phase{
			Label:        label,
			FromMinute:   phase.FromMinute,
			ToMinute:     phase.ToMinute,
			WhatHappened: clean(phase.WhatHappened, maxMediumBytes),
			Verdict:      verdict,
		})
	}
	for _, insight := range raw.Insights {
		if len(out.Insights) >= maxInsights {
			break
		}
		kind := insight.Kind
		if !insightKinds[kind] {
			kind = InsightWorkflow
			note("An insight kind outside the catalog was reset to workflow.")
		}
		title := clean(insight.Title, maxShortBytes)
		turns := filterTurns(insight.EvidenceTurns)
		if title == "" || len(turns) == 0 {
			note("An insight without valid evidence was dropped.")
			continue
		}
		confidence := insight.Confidence
		if confidence < 0 || confidence > 1 {
			confidence = 0
		}
		var atMinute *float64
		if insight.AtMinute != nil && *insight.AtMinute >= 0 {
			value := *insight.AtMinute
			atMinute = &value
		}
		minutesCost := insight.MinutesCost
		if minutesCost < 0 || minutesCost > 24*60 {
			minutesCost = 0
		}
		dollarsCost := insight.DollarsCost
		if dollarsCost < 0 || dollarsCost > 10000 {
			dollarsCost = 0
		}
		out.Insights = append(out.Insights, Insight{
			MinutesCost:    minutesCost,
			DollarsCost:    dollarsCost,
			AtMinute:       atMinute,
			Kind:           kind,
			Title:          title,
			WhatYouDid:     clean(insight.WhatYouDid, maxLongBytes),
			WhatItCost:     clean(insight.WhatItCost, maxMediumBytes),
			IdealPath:      clean(insight.IdealPath, maxLongBytes),
			SayThisInstead: clean(insight.SayThisInstead, maxMessageBytes),
			EvidenceTurns:  turns,
			Confidence:     confidence,
		})
	}
	for _, keep := range raw.KeepDoing {
		if len(out.KeepDoing) >= maxKeepDoing {
			break
		}
		title := clean(keep.Title, maxShortBytes)
		turns := filterTurns(keep.EvidenceTurns)
		if title == "" || len(turns) == 0 {
			continue
		}
		out.KeepDoing = append(out.KeepDoing, KeepDoing{
			Title:         title,
			Why:           clean(keep.Why, maxMediumBytes),
			EvidenceTurns: turns,
		})
	}
	verdict := raw.PromptLength.Verdict
	if !lengthVerdicts[verdict] {
		verdict = LengthAboutRight
	}
	out.PromptLength = PromptLength{
		Verdict:     verdict,
		Explanation: clean(raw.PromptLength.Explanation, maxMediumBytes),
		Rewrite:     clean(raw.PromptLength.Rewrite, maxMessageBytes),
	}
	if len(out.Insights) == 0 {
		return Debrief{}, notes, ErrDebriefDecode
	}
	return out, notes, nil
}
