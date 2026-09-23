package localmcp

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	maxMissionPackCWDBytes      = 4096
	maxMissionPackIssueIDBytes  = 512
	maxMissionPackTaskHintRunes = 280
	maxMissionPackTaskHintBytes = 1120
	maxMissionPackResponseBytes = 32 << 10
	maxMissionPackIDBytes       = 56
)

type MissionPackService interface {
	Generate(
		context.Context,
		missionpack.Request,
	) (missionpack.Pack, error)
}

type MissionPackAcceptanceService interface {
	Accept(
		context.Context,
		string,
	) (localapp.MissionPackAcceptanceResult, error)
}

type getMissionPackInput struct {
	CWD      string              `json:"cwd,omitempty"`
	IssueID  string              `json:"issue_id,omitempty"`
	Intent   missionpack.Intent  `json:"intent,omitempty"`
	Harness  missionpack.Harness `json:"harness,omitempty"`
	TaskHint string              `json:"task_hint,omitempty"`
}

type recordMissionPackAcceptedInput struct {
	PackID string `json:"pack_id"`
}

type missionPackErrorKind interface {
	MissionPackErrorKind() string
}

func WithMissionPackService(service MissionPackService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("Mission Pack service is required")
		}
		server.missionPacks = service
		return nil
	}
}

func WithMissionPackAcceptanceService(
	service MissionPackAcceptanceService,
) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("Mission Pack acceptance service is required")
		}
		server.missionPackAcceptance = service
		return nil
	}
}

func (s *Server) registerMissionPackTool() error {
	schemas, err := missionPackSchemas()
	if err != nil {
		return errors.New("initialize local MCP Mission Pack schemas")
	}
	tool, err := newStrictReadOnlyTool(
		"get_mission_pack",
		"Prepare a bounded evidence-backed Mission Pack for one Claude Code, Codex, Cursor, or Antigravity project. Actionable guidance is inactive until the user explicitly approves it; empty packs cannot be activated and evidence remains untrusted.",
		schemas,
	)
	if err != nil {
		return errors.New("register local MCP Mission Pack tool")
	}
	s.mcp.AddTool(
		tool,
		bindStrictTool(s.strict, schemas, s.getMissionPack),
	)
	return nil
}

func (s *Server) registerMissionPackAcceptanceTool() error {
	schemas, err := missionPackAcceptanceSchemas()
	if err != nil {
		return errors.New("initialize local MCP Mission Pack acceptance schemas")
	}
	tool := additiveTool(
		"record_mission_pack_accepted",
		"Record the user's explicit approval of one prepared experience-bearing Mission Pack. This creates or replays a local pending receipt and does not edit project files.",
	)
	tool.InputSchema = schemas.input
	tool.OutputSchema = schemas.output
	s.mcp.AddTool(
		tool,
		bindStrictTool(
			s.strict,
			schemas,
			s.recordMissionPackAccepted,
		),
	)
	return nil
}

func (s *Server) getMissionPack(
	ctx context.Context,
	input getMissionPackInput,
) (missionpack.Pack, error) {
	input.CWD = strings.TrimSpace(input.CWD)
	input.IssueID = strings.TrimSpace(input.IssueID)
	if input.CWD == "" && input.IssueID == "" {
		return missionpack.Pack{}, newStrictToolFailure(strictInvalidInput)
	}
	if len(input.CWD) > maxMissionPackCWDBytes ||
		(input.CWD != "" && !absoluteInputPath(input.CWD)) {
		return missionpack.Pack{}, newStrictToolFailure(strictInvalidInput)
	}
	if len(input.IssueID) > maxMissionPackIssueIDBytes {
		return missionpack.Pack{}, newStrictToolFailure(strictInvalidInput)
	}
	if input.Intent == "" {
		input.Intent = missionpack.IntentGeneral
	}
	if !validMissionPackIntent(input.Intent) {
		return missionpack.Pack{}, newStrictToolFailure(strictInvalidInput)
	}
	if !validMissionPackHarness(input.Harness) {
		return missionpack.Pack{}, newStrictToolFailure(strictInvalidInput)
	}
	if !utf8.ValidString(input.TaskHint) ||
		len(input.TaskHint) > maxMissionPackTaskHintBytes ||
		utf8.RuneCountInString(input.TaskHint) > maxMissionPackTaskHintRunes {
		return missionpack.Pack{}, newStrictToolFailure(strictInvalidInput)
	}

	pack, err := s.missionPacks.Generate(ctx, missionpack.Request{
		CWD:      input.CWD,
		IssueID:  input.IssueID,
		Intent:   input.Intent,
		Harness:  input.Harness,
		TaskHint: input.TaskHint,
	})
	if err != nil {
		return missionpack.Pack{}, missionPackToolError(err)
	}
	if pack.Harness != input.Harness ||
		!validMissionPackTrust(pack) {
		return missionpack.Pack{}, newStrictToolFailure(strictReadFailed)
	}
	if !missionPackResponseFits(pack) {
		return missionpack.Pack{}, newStrictToolFailure(strictResultTooLarge)
	}
	return pack, nil
}

func (s *Server) recordMissionPackAccepted(
	ctx context.Context,
	input recordMissionPackAcceptedInput,
) (localapp.MissionPackAcceptanceResult, error) {
	input.PackID = strings.TrimSpace(input.PackID)
	if len(input.PackID) != maxMissionPackIDBytes {
		return localapp.MissionPackAcceptanceResult{},
			newStrictToolFailure(strictInvalidInput)
	}
	result, err := s.missionPackAcceptance.Accept(ctx, input.PackID)
	if err != nil {
		switch {
		case errors.Is(err, localapp.ErrMissionPackPreviewNotFound):
			return localapp.MissionPackAcceptanceResult{},
				newStrictToolFailure(strictIssueNotFound)
		case errors.Is(err, localapp.ErrMissionPackPreviewExpired),
			errors.Is(err, localapp.ErrMissionPackPreviewConflict):
			return localapp.MissionPackAcceptanceResult{},
				newStrictToolFailure(strictReadFailed)
		case errors.Is(err, context.DeadlineExceeded):
			return localapp.MissionPackAcceptanceResult{},
				newStrictToolFailure(strictReadTimeout)
		case errors.Is(err, context.Canceled):
			return localapp.MissionPackAcceptanceResult{},
				newStrictToolFailure(strictCancelled)
		default:
			return localapp.MissionPackAcceptanceResult{},
				newStrictToolFailure(strictReadFailed)
		}
	}
	return result, nil
}

func validMissionPackHarness(harness missionpack.Harness) bool {
	switch harness {
	case "", missionpack.HarnessClaude, missionpack.HarnessCodex,
		missionpack.HarnessCursor, missionpack.HarnessAntigravity:
		return true
	default:
		return false
	}
}

func validMissionPackTrust(pack missionpack.Pack) bool {
	if pack.Trust.InstructionAuthority != "none" ||
		pack.Trust.EvidenceState != "untrusted" {
		return false
	}
	if !validMissionPackExperiences(pack) {
		return false
	}
	actionable := len(pack.KnownTraps) > 0 ||
		len(pack.OperatingRules) > 0 ||
		len(pack.Verification) > 0 ||
		len(pack.Experiences) > 0
	if pack.Status == "empty" {
		return !actionable &&
			pack.Trust.GuidanceState == "unavailable" &&
			!pack.Trust.ActivationRequired
	}
	return actionable &&
		(pack.Status == "ready" || pack.Status == "partial") &&
		pack.Trust.GuidanceState == "proposal" &&
		pack.Trust.ActivationRequired
}

func validMissionPackExperiences(pack missionpack.Pack) bool {
	if len(pack.Experiences) == 0 {
		return pack.ExperienceGeneration == 0
	}
	if pack.ExperienceGeneration <= 0 || len(pack.Experiences) > 3 {
		return false
	}
	for _, item := range pack.Experiences {
		if strings.TrimSpace(item.ExperienceID) == "" ||
			item.Version <= 0 ||
			strings.TrimSpace(item.Type) == "" ||
			strings.TrimSpace(item.Guidance) == "" ||
			strings.TrimSpace(item.Rationale) == "" ||
			strings.TrimSpace(item.Verifier.Kind) == "" ||
			strings.TrimSpace(item.Verifier.Summary) == "" ||
			item.Authority != "user_approved" ||
			len(item.Sources) > missionpack.MaxSourcesPerItem {
			return false
		}
	}
	return true
}

func validMissionPackIntent(intent missionpack.Intent) bool {
	switch intent {
	case missionpack.IntentGeneral,
		missionpack.IntentDebug,
		missionpack.IntentImplement,
		missionpack.IntentRefactor,
		missionpack.IntentReview,
		missionpack.IntentRelease:
		return true
	default:
		return false
	}
}

func missionPackToolError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return newStrictToolFailure(strictReadTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return newStrictToolFailure(strictCancelled)
	}
	if errors.Is(err, missionpack.ErrProjectNotFound) {
		return newStrictToolFailure(strictIssueNotFound)
	}
	if errors.Is(err, missionpack.ErrProjectMismatch) {
		return newStrictToolFailure(strictInvalidInput)
	}
	var classified missionPackErrorKind
	if !errors.As(err, &classified) {
		return newStrictToolFailure(strictReadFailed)
	}
	switch classified.MissionPackErrorKind() {
	case "invalid_request", "invalid_selector":
		return newStrictToolFailure(strictInvalidInput)
	case "project_not_found":
		return newStrictToolFailure(strictIssueNotFound)
	case "issue_not_found":
		return newStrictToolFailure(strictIssueNotFound)
	case "project_mismatch":
		return newStrictToolFailure(strictInvalidInput)
	default:
		return newStrictToolFailure(strictReadFailed)
	}
}

func missionPackResponseFits(pack missionpack.Pack) bool {
	_, err := encodeBoundedJSON(strictToolOutput{
		UntrustedObservations: true,
		Trust: strictTrust{
			Classification:          "untrusted_observations",
			InstructionAuthority:    "none",
			MustNotAuthorizeActions: true,
		},
		ReadModel: pack,
	}, maxMissionPackResponseBytes)
	return err == nil
}

func missionPackSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(map[string]*jsonschema.Schema{
		"cwd": boundedTextSchema(1, maxMissionPackCWDBytes),
		"issue_id": boundedTextSchema(
			1,
			maxMissionPackIssueIDBytes,
		),
		"intent": enumStringSchema(
			string(missionpack.IntentGeneral),
			string(missionpack.IntentDebug),
			string(missionpack.IntentImplement),
			string(missionpack.IntentRefactor),
			string(missionpack.IntentReview),
			string(missionpack.IntentRelease),
		),
		"harness": enumStringSchema(
			string(missionpack.HarnessClaude),
			string(missionpack.HarnessCodex),
			string(missionpack.HarnessCursor),
			string(missionpack.HarnessAntigravity),
		),
		"task_hint": boundedTextSchema(
			1,
			maxMissionPackTaskHintRunes,
		),
	})
	return newStrictToolSchemas(
		input,
		strictSuccessSchema(missionPackSchema()),
	)
}

func missionPackAcceptanceSchemas() (*strictToolSchemas, error) {
	input := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"pack_id": patternStringSchema(
				`^mpk_[a-z2-7]{52}$`,
				maxMissionPackIDBytes,
				maxMissionPackIDBytes,
			),
		},
		"pack_id",
	)
	output := strictSuccessSchema(
		closedObjectSchema(
			map[string]*jsonschema.Schema{
				"receipt_id": boundedTextSchema(1, 256),
				"state": enumStringSchema(
					"pending",
					"bound",
					"ambiguous",
					"expired",
					"cancelled",
				),
				"expires_at": timestampSchema(),
			},
			"receipt_id",
			"state",
			"expires_at",
		),
	)
	return newStrictToolSchemas(input, output)
}

func missionPackSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"schema_version": constSchema(
				"string",
				missionpack.SchemaVersion,
			),
			"generator_version": constSchema(
				"string",
				missionpack.GeneratorVersion,
			),
			"pack_id": patternStringSchema(
				`^mpk_[a-z2-7]{52}$`,
				56,
				56,
			),
			"generated_at": timestampSchema(),
			"project": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"label": boundedTextSchema(1, 120),
					"identity_kind": boundedTextSchema(
						1,
						32,
					),
					"branch":   boundedTextSchema(1, 160),
					"worktree": boundedTextSchema(1, 160),
				},
				"label",
				"identity_kind",
			),
			"intent": enumStringSchema(
				string(missionpack.IntentGeneral),
				string(missionpack.IntentDebug),
				string(missionpack.IntentImplement),
				string(missionpack.IntentRefactor),
				string(missionpack.IntentReview),
				string(missionpack.IntentRelease),
			),
			"harness": enumStringSchema(
				string(missionpack.HarnessClaude),
				string(missionpack.HarnessCodex),
				string(missionpack.HarnessCursor),
				string(missionpack.HarnessAntigravity),
			),
			"status": enumStringSchema("ready", "partial", "empty"),
			"trust": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"instruction_authority": constSchema(
						"string",
						"none",
					),
					"guidance_state": enumStringSchema(
						"proposal",
						"unavailable",
					),
					"evidence_state": constSchema(
						"string",
						"untrusted",
					),
					"activation_required": {Type: "boolean"},
				},
				"instruction_authority",
				"guidance_state",
				"evidence_state",
				"activation_required",
			),
			"source_state": missionPackSourceStateSchema(),
			"experience_generation": {
				Type:    "integer",
				Minimum: jsonNumberPointer(1),
			},
			"experiences": arraySchema(
				missionPackExperienceSchema(),
				0,
				3,
			),
			"context": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"harnesses": arraySchema(
						boundedTextSchema(1, 64),
						0,
						8,
					),
					"facts": arraySchema(
						missionPackContextFactSchema(),
						0,
						missionpack.MaxContextFacts,
					),
				},
				"harnesses",
				"facts",
			),
			"known_traps": arraySchema(
				missionPackGuidanceSchema(false),
				0,
				missionpack.MaxKnownTraps,
			),
			"operating_rules": arraySchema(
				missionPackGuidanceSchema(true),
				0,
				missionpack.MaxOperatingRules,
			),
			"verification": arraySchema(
				missionPackCommandSchema(),
				0,
				missionpack.MaxVerificationCommands,
			),
			"completion_checklist": arraySchema(
				closedObjectSchema(
					map[string]*jsonschema.Schema{
						"id":   boundedTextSchema(1, 128),
						"text": boundedTextSchema(1, 512),
					},
					"id",
					"text",
				),
				0,
				missionpack.MaxChecklistItems,
			),
			"warnings": arraySchema(
				closedObjectSchema(
					map[string]*jsonschema.Schema{
						"code": boundedTextSchema(1, 64),
						"message": boundedTextSchema(
							1,
							512,
						),
					},
					"code",
					"message",
				),
				0,
				16,
			),
			"estimated_tokens": integerSchema(
				0,
				missionpack.MaxEstimatedTokens,
			),
			"truncated": {Type: "boolean"},
			"rendered_markdown": boundedTextSchema(
				0,
				missionpack.MaxRenderedMarkdown,
			),
		},
		"schema_version",
		"generator_version",
		"pack_id",
		"generated_at",
		"project",
		"intent",
		"status",
		"trust",
		"source_state",
		"context",
		"known_traps",
		"operating_rules",
		"verification",
		"completion_checklist",
		"warnings",
		"estimated_tokens",
		"truncated",
		"rendered_markdown",
	)
}

func missionPackExperienceSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"experience_id": boundedTextSchema(1, 256),
			"version": {
				Type:    "integer",
				Minimum: jsonNumberPointer(1),
			},
			"type":          boundedTextSchema(1, 64),
			"guidance":      boundedTextSchema(1, 2*1024),
			"applicability": boundedTextSchema(1, 2*1024),
			"exceptions": arraySchema(
				boundedTextSchema(1, 2*1024),
				0,
				8,
			),
			"rationale": boundedTextSchema(1, 8*1024),
			"verifier": closedObjectSchema(
				map[string]*jsonschema.Schema{
					"kind":    boundedTextSchema(1, 64),
					"summary": boundedTextSchema(1, 300),
				},
				"kind",
				"summary",
			),
			"authority": constSchema("string", "user_approved"),
			"sources": arraySchema(
				missionPackSourceRefSchema(),
				0,
				missionpack.MaxSourcesPerItem,
			),
		},
		"experience_id",
		"version",
		"type",
		"guidance",
		"applicability",
		"rationale",
		"verifier",
		"authority",
		"sources",
	)
}

func missionPackSourceStateSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"transcript_generation": nonNegativeIntegerSchema(),
			"analyzed_generation":   nonNegativeIntegerSchema(),
			"analysis_status": enumStringSchema(
				missionpack.AnalysisStatusCurrent,
				missionpack.AnalysisStatusPending,
				missionpack.AnalysisStatusStale,
			),
			"data_through": timestampSchema(),
		},
		"transcript_generation",
		"analyzed_generation",
		"analysis_status",
		"data_through",
	)
}

func missionPackSourceRefSchema() *jsonschema.Schema {
	nonNegativeInt64 := &jsonschema.Schema{
		Type:    "integer",
		Minimum: jsonNumberPointer(0),
	}
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"kind":           boundedTextSchema(1, 64),
			"issue_id":       boundedTextSchema(1, 256),
			"insight_id":     boundedTextSchema(1, 256),
			"candidate_id":   boundedTextSchema(1, 256),
			"session_key":    boundedTextSchema(1, 256),
			"turn_index":     nonNegativeInt64,
			"event_id":       boundedTextSchema(1, 256),
			"project_file":   boundedTextSchema(1, 256),
			"source_sha256":  patternStringSchema(`^[a-f0-9]{64}$`, 64, 64),
			"source_file_id": boundedTextSchema(1, 256),
			"jsonl_byte_offset": {
				Type:    "integer",
				Minimum: jsonNumberPointer(0),
			},
			"observed_at": timestampSchema(),
		},
		"kind",
	)
}

func missionPackGuidanceSchema(requireGuidance bool) *jsonschema.Schema {
	required := []string{
		"id",
		"kind",
		"title",
		"requires_approval",
		"sources",
	}
	if requireGuidance {
		required = append(required, "guidance")
	}
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"id":          boundedTextSchema(1, 128),
			"kind":        boundedTextSchema(1, 64),
			"title":       boundedTextSchema(1, 512),
			"guidance":    boundedTextSchema(1, 1024),
			"target_file": boundedTextSchema(1, 160),
			"confidence":  unitNumberSchema(),
			"session_count": integerSchema(
				1,
				1_000_000_000,
			),
			"wasted_minutes": nonNegativeNumberSchema(),
			"wasted_tokens": {
				Type:    "integer",
				Minimum: jsonNumberPointer(0),
			},
			"wasted_usd":       nonNegativeNumberSchema(),
			"cost_lower_bound": {Type: "boolean"},
			"requires_approval": {
				Type: "boolean",
			},
			"sources": arraySchema(
				missionPackSourceRefSchema(),
				0,
				missionpack.MaxSourcesPerItem,
			),
		},
		required...,
	)
}

func missionPackCommandSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"id":         boundedTextSchema(1, 128),
			"command":    boundedTextSchema(1, 1024),
			"class":      boundedTextSchema(1, 128),
			"configured": {Type: "boolean"},
			"observed":   {Type: "boolean"},
			"success_count": integerSchema(
				1,
				1_000_000_000,
			),
			"last_success": timestampSchema(),
			"requires_approval": {
				Type: "boolean",
			},
			"sources": arraySchema(
				missionPackSourceRefSchema(),
				0,
				missionpack.MaxSourcesPerItem,
			),
		},
		"id",
		"command",
		"configured",
		"observed",
		"requires_approval",
		"sources",
	)
}

func missionPackContextFactSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"id":      boundedTextSchema(1, 128),
			"kind":    boundedTextSchema(1, 64),
			"summary": boundedTextSchema(1, 512),
			"session_count": integerSchema(
				1,
				1_000_000_000,
			),
			"observed_at": timestampSchema(),
			"sources": arraySchema(
				missionPackSourceRefSchema(),
				0,
				missionpack.MaxSourcesPerItem,
			),
		},
		"id",
		"kind",
		"summary",
		"sources",
	)
}

func nonNegativeNumberSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:    "number",
		Minimum: jsonNumberPointer(0),
	}
}

func unitNumberSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:    "number",
		Minimum: jsonNumberPointer(0),
		Maximum: jsonNumberPointer(1),
	}
}

func jsonNumberPointer(value float64) *float64 {
	return &value
}
