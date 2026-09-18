package localmcp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testMissionPackService struct {
	request missionpack.Request
	pack    missionpack.Pack
	err     error
}

func (s *testMissionPackService) Generate(
	_ context.Context,
	request missionpack.Request,
) (missionpack.Pack, error) {
	s.request = request
	return s.pack, s.err
}

type testMissionPackAcceptanceService struct {
	result  localapp.MissionPackAcceptanceResult
	err     error
	packIDs []string
}

func (s *testMissionPackAcceptanceService) Accept(
	_ context.Context,
	packID string,
) (localapp.MissionPackAcceptanceResult, error) {
	s.packIDs = append(s.packIDs, packID)
	return s.result, s.err
}

type testMissionPackStatusService struct {
	result     localapp.MissionPackStatusResult
	err        error
	receiptIDs []string
}

func (s *testMissionPackStatusService) Get(
	_ context.Context,
	receiptID string,
) (localapp.MissionPackStatusResult, error) {
	s.receiptIDs = append(s.receiptIDs, receiptID)
	return s.result, s.err
}

type testMissionPackError string

func (err testMissionPackError) Error() string {
	return "private Mission Pack detail"
}

func (err testMissionPackError) MissionPackErrorKind() string {
	return string(err)
}

func TestMissionPackToolRegistersWithClosedSchemaAndTrustEnvelope(
	t *testing.T,
) {
	service := &testMissionPackService{pack: testMissionPack()}
	session := newMissionPackTestClient(t, service)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var found *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "get_mission_pack" {
			found = tool
			break
		}
	}
	if found == nil {
		t.Fatal("get_mission_pack was not registered")
	}
	if found.Annotations == nil || !found.Annotations.ReadOnlyHint {
		t.Fatalf("Mission Pack annotations = %#v", found.Annotations)
	}
	schemas, err := missionPackSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if schemas.input.AdditionalProperties == nil ||
		schemas.output.AdditionalProperties == nil {
		t.Fatal("Mission Pack schemas are not closed")
	}

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if result.IsError {
		t.Fatalf("get_mission_pack failed: %v", result.Content)
	}
	if service.request.Intent != missionpack.IntentGeneral {
		t.Fatalf("default intent = %q", service.request.Intent)
	}
	structured := asObject(t, result.StructuredContent)
	trust := asObject(t, structured["trust"])
	if trust["instruction_authority"] != "none" ||
		trust["must_not_authorize_actions"] != true {
		t.Fatalf("strict trust = %#v", trust)
	}
	pack := asObject(t, structured["readmodel"])
	packTrust := asObject(t, pack["trust"])
	if packTrust["instruction_authority"] != "none" ||
		packTrust["activation_required"] != true {
		t.Fatalf("pack trust = %#v", packTrust)
	}
	verification, ok := pack["verification"].([]any)
	if !ok || len(verification) != 1 {
		t.Fatalf("verification = %#v", pack["verification"])
	}
	command := asObject(t, verification[0])
	if _, exists := command["last_success"]; exists {
		t.Fatalf("configured-only command has last_success: %#v", command)
	}
	sources, ok := command["sources"].([]any)
	if !ok || len(sources) != 1 ||
		asObject(t, sources[0])["source_sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("command sources = %#v", command["sources"])
	}
}

func TestMissionPackAcceptanceToolRegistersOnlyWhenConfigured(t *testing.T) {
	withoutAcceptance := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: testMissionPack()},
	)
	tools, err := withoutAcceptance.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "record_mission_pack_accepted" {
			t.Fatal("acceptance tool registered without acceptance service")
		}
	}

	acceptance := &testMissionPackAcceptanceService{}
	withAcceptance := newMissionPackTestClientWithServerOptions(
		t,
		WithMissionPackService(
			&testMissionPackService{pack: testMissionPack()},
		),
		WithMissionPackAcceptanceService(acceptance),
	)
	tools, err = withAcceptance.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "record_mission_pack_accepted" {
			continue
		}
		if tool.Annotations == nil ||
			tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil ||
			*tool.Annotations.DestructiveHint ||
			tool.Annotations.OpenWorldHint == nil ||
			*tool.Annotations.OpenWorldHint {
			t.Fatalf("acceptance annotations = %#v", tool.Annotations)
		}
		schemas, err := missionPackAcceptanceSchemas()
		if err != nil {
			t.Fatal(err)
		}
		if schemas.input.AdditionalProperties == nil ||
			schemas.output.AdditionalProperties == nil {
			t.Fatal("acceptance schemas are not closed")
		}
		if len(schemas.input.Properties) != 1 ||
			schemas.input.Properties["pack_id"] == nil {
			t.Fatalf(
				"acceptance input properties = %#v",
				schemas.input.Properties,
			)
		}
		return
	}
	t.Fatal("record_mission_pack_accepted was not registered")
}

func TestMissionPackStatusToolRegistersOnlyWhenConfiguredWithClosedSchema(
	t *testing.T,
) {
	withoutStatus := newMissionPackTestClientWithServerOptions(t)
	tools, err := withoutStatus.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "get_mission_pack_status" {
			t.Fatal("status tool registered without status service")
		}
	}

	status := &testMissionPackStatusService{
		result: testMissionPackStatusResult(),
	}
	withStatus := newMissionPackTestClientWithServerOptions(
		t,
		WithMissionPackStatusService(status),
	)
	tools, err = withStatus.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var found *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "get_mission_pack_status" {
			found = tool
			break
		}
	}
	if found == nil {
		t.Fatal("get_mission_pack_status was not registered")
	}
	if found.Annotations == nil || !found.Annotations.ReadOnlyHint {
		t.Fatalf("status annotations = %#v", found.Annotations)
	}
	schemas, err := missionPackStatusSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if schemas.input.AdditionalProperties == nil ||
		schemas.output.AdditionalProperties == nil ||
		len(schemas.input.Properties) != 1 ||
		schemas.input.Properties["receipt_id"] == nil {
		t.Fatalf("status schemas are not strict: %#v", schemas.input)
	}

	receiptID := "mpr_" + strings.Repeat("a", 64)
	result := callTool(
		t,
		withStatus,
		"get_mission_pack_status",
		map[string]any{"receipt_id": receiptID},
	)
	if result.IsError {
		t.Fatalf("status call failed: %#v", result.Content)
	}
	if !reflect.DeepEqual(status.receiptIDs, []string{receiptID}) {
		t.Fatalf("status receipt IDs = %#v", status.receiptIDs)
	}
	structured := asObject(t, result.StructuredContent)
	if structured["untrusted_observations"] != true {
		t.Fatalf("status trust wrapper = %#v", structured)
	}
	readModel := asObject(t, structured["readmodel"])
	if readModel["receipt_state"] != "bound" ||
		readModel["destination_harness"] != "codex" {
		t.Fatalf("status readmodel = %#v", readModel)
	}
	item := asObject(t, readModel["items"].([]any)[0])
	if item["status"] != "evaluated" ||
		item["verifier_state"] != "satisfied" ||
		item["task_outcome_state"] != "unknown" {
		t.Fatalf("status item = %#v", item)
	}
	for _, forbidden := range []string{
		"application_id",
		"evaluation_id",
		"project_identity",
		"generation",
		"derivation_version",
	} {
		if _, exists := item[forbidden]; exists {
			t.Fatalf("status item exposed %q: %#v", forbidden, item)
		}
	}
}

func TestMissionPackStatusToolMapsInputNotFoundAndMismatchSafely(
	t *testing.T,
) {
	receiptID := "mpr_" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		args map[string]any
		err  error
		want strictToolErrorCode
	}{
		{
			name: "invalid",
			args: map[string]any{"receipt_id": "mpr_invalid"},
			want: strictInvalidInput,
		},
		{
			name: "unknown field",
			args: map[string]any{
				"receipt_id": receiptID,
				"latest":     true,
			},
			want: strictInvalidInput,
		},
		{
			name: "not found",
			args: map[string]any{"receipt_id": receiptID},
			err:  localapp.ErrMissionPackStatusNotFound,
			want: strictIssueNotFound,
		},
		{
			name: "mismatch",
			args: map[string]any{"receipt_id": receiptID},
			err:  localapp.ErrMissionPackStatusMismatch,
			want: strictReadFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := newMissionPackTestClientWithServerOptions(
				t,
				WithMissionPackStatusService(
					&testMissionPackStatusService{err: test.err},
				),
			)
			result := callTool(
				t,
				session,
				"get_mission_pack_status",
				test.args,
			)
			if got := missionPackResultErrorCode(result); got !=
				string(test.want) {
				t.Fatalf("error code = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMissionPackAcceptanceToolReturnsCrispIdempotentReceipt(t *testing.T) {
	packID := testMissionPack().PackID
	expiresAt := time.Date(
		2026,
		time.September,
		10,
		16,
		5,
		0,
		0,
		time.UTC,
	)
	acceptance := &testMissionPackAcceptanceService{
		result: localapp.MissionPackAcceptanceResult{
			ReceiptID: "mpr_example",
			State:     "pending",
			ExpiresAt: expiresAt,
		},
	}
	session := newMissionPackTestClientWithServerOptions(
		t,
		WithMissionPackService(
			&testMissionPackService{pack: testMissionPack()},
		),
		WithMissionPackAcceptanceService(acceptance),
	)
	for iteration := 0; iteration < 2; iteration++ {
		result := callTool(
			t,
			session,
			"record_mission_pack_accepted",
			map[string]any{"pack_id": packID},
		)
		if result.IsError {
			t.Fatalf("acceptance failed: %#v", result.Content)
		}
		readModel := asObject(
			t,
			asObject(t, result.StructuredContent)["readmodel"],
		)
		if readModel["receipt_id"] != "mpr_example" ||
			readModel["state"] != "pending" ||
			readModel["expires_at"] != expiresAt.Format(time.RFC3339) {
			t.Fatalf("acceptance result = %#v", readModel)
		}
	}
	if !reflect.DeepEqual(acceptance.packIDs, []string{packID, packID}) {
		t.Fatalf("accepted pack IDs = %#v", acceptance.packIDs)
	}
}

func TestGetMissionPackDoesNotAcceptPreview(t *testing.T) {
	acceptance := &testMissionPackAcceptanceService{}
	session := newMissionPackTestClientWithServerOptions(
		t,
		WithMissionPackService(
			&testMissionPackService{pack: testExperienceMissionPack()},
		),
		WithMissionPackAcceptanceService(acceptance),
	)
	result := callTool(
		t,
		session,
		"get_mission_pack",
		map[string]any{"cwd": "/tmp/example"},
	)
	if result.IsError {
		t.Fatalf("get_mission_pack failed: %#v", result.Content)
	}
	if len(acceptance.packIDs) != 0 {
		t.Fatalf("get_mission_pack accepted packs: %#v", acceptance.packIDs)
	}
}

func TestMissionPackAcceptanceToolMapsFailuresSafely(t *testing.T) {
	packID := testMissionPack().PackID
	tests := []struct {
		name string
		err  error
		args map[string]any
		want strictToolErrorCode
	}{
		{
			name: "invalid",
			args: map[string]any{"pack_id": "mpk_invalid"},
			want: strictInvalidInput,
		},
		{
			name: "missing",
			err:  localapp.ErrMissionPackPreviewNotFound,
			args: map[string]any{"pack_id": packID},
			want: strictIssueNotFound,
		},
		{
			name: "expired",
			err:  localapp.ErrMissionPackPreviewExpired,
			args: map[string]any{"pack_id": packID},
			want: strictReadFailed,
		},
		{
			name: "conflict",
			err:  localapp.ErrMissionPackPreviewConflict,
			args: map[string]any{"pack_id": packID},
			want: strictReadFailed,
		},
		{
			name: "unknown field",
			args: map[string]any{
				"pack_id": packID,
				"approve": true,
			},
			want: strictInvalidInput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			acceptance := &testMissionPackAcceptanceService{err: test.err}
			session := newMissionPackTestClientWithServerOptions(
				t,
				WithMissionPackAcceptanceService(acceptance),
			)
			result := callTool(
				t,
				session,
				"record_mission_pack_accepted",
				test.args,
			)
			if got := missionPackResultErrorCode(result); got !=
				string(test.want) {
				t.Fatalf("error code = %q, want %q", got, test.want)
			}
			if strings.Contains(
				missionPackResultErrorCode(result),
				"preview",
			) {
				t.Fatalf("private acceptance detail leaked: %#v", result)
			}
		})
	}
}

func TestMissionPackToolPassesHarnessAndExposesIt(t *testing.T) {
	pack := testMissionPack()
	pack.Harness = missionpack.HarnessClaude
	service := &testMissionPackService{pack: pack}
	session := newMissionPackTestClient(t, service)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd":     "/tmp/example",
		"harness": "claude",
	})
	if result.IsError {
		t.Fatalf("get_mission_pack failed: %v", result.Content)
	}
	if service.request.Harness != missionpack.HarnessClaude {
		t.Fatalf("request harness = %q", service.request.Harness)
	}
	readModel := asObject(
		t,
		asObject(t, result.StructuredContent)["readmodel"],
	)
	if readModel["harness"] != "claude" {
		t.Fatalf("pack harness = %#v", readModel["harness"])
	}
}

func TestMissionPackToolReturnsApprovedExperiences(t *testing.T) {
	pack := testExperienceMissionPack()
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: pack},
	)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if result.IsError {
		t.Fatalf("experience pack failed: %v", result.Content)
	}
	readModel := asObject(
		t,
		asObject(t, result.StructuredContent)["readmodel"],
	)
	if readModel["experience_generation"] != float64(7) {
		t.Fatalf(
			"experience_generation = %#v",
			readModel["experience_generation"],
		)
	}
	experiences, ok := readModel["experiences"].([]any)
	if !ok || len(experiences) != 1 {
		t.Fatalf("experiences = %#v", readModel["experiences"])
	}
	experience := asObject(t, experiences[0])
	if experience["experience_id"] != "exp_verify_after_edit" ||
		experience["version"] != float64(2) ||
		experience["type"] != "procedure" ||
		experience["guidance"] !=
			"Run project verification after the final edit." ||
		experience["applicability"] !=
			"Use this rule when the task changes project files." ||
		experience["rationale"] !=
			"Prior sessions regressed after unverified edits." ||
		experience["authority"] != "user_approved" {
		t.Fatalf("experience = %#v", experience)
	}
	exceptions, ok := experience["exceptions"].([]any)
	if !ok || !reflect.DeepEqual(
		exceptions,
		[]any{"Do not apply it to read-only review tasks."},
	) {
		t.Fatalf("experience exceptions = %#v", experience["exceptions"])
	}
	verifier := asObject(t, experience["verifier"])
	if verifier["kind"] != "command_succeeded" ||
		verifier["summary"] !=
			"Observe a successful verification after the last edit." {
		t.Fatalf("verifier = %#v", verifier)
	}
	sources, ok := experience["sources"].([]any)
	if !ok || len(sources) != 1 ||
		asObject(t, sources[0])["session_key"] != "ses_example" {
		t.Fatalf("sources = %#v", experience["sources"])
	}
}

func TestMissionPackToolPreservesLegacyPackWithoutExperienceFields(
	t *testing.T,
) {
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: testMissionPack()},
	)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if result.IsError {
		t.Fatalf("legacy pack failed: %v", result.Content)
	}
	readModel := asObject(
		t,
		asObject(t, result.StructuredContent)["readmodel"],
	)
	if _, exists := readModel["experience_generation"]; exists {
		t.Fatalf(
			"legacy pack exposed experience_generation: %#v",
			readModel,
		)
	}
	if _, exists := readModel["experiences"]; exists {
		t.Fatalf("legacy pack exposed experiences: %#v", readModel)
	}
}

func TestMissionPackToolAcceptsEmptyNonActivatablePack(t *testing.T) {
	pack := testMissionPack()
	pack.Status = "empty"
	pack.Trust.GuidanceState = "unavailable"
	pack.Trust.ActivationRequired = false
	pack.KnownTraps = []missionpack.GuidanceItem{}
	pack.OperatingRules = []missionpack.GuidanceItem{}
	pack.Verification = []missionpack.CommandItem{}
	pack.Completion = []missionpack.ChecklistItem{}
	pack.Context.Facts = []missionpack.ContextFact{{
		ID:      "fact_project_file",
		Kind:    missionpack.CanonicalFactFileWritten,
		Summary: "Frequently edited file: internal/example.go",
		Sources: []missionpack.SourceRef{},
	}}
	pack.RenderedMarkdown = "# Mission Pack\n\nNo actionable guidance.\n"
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: pack},
	)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if result.IsError {
		t.Fatalf("empty pack failed: %v", result.Content)
	}
	readModel := asObject(
		t,
		asObject(t, result.StructuredContent)["readmodel"],
	)
	trust := asObject(t, readModel["trust"])
	if readModel["status"] != "empty" ||
		trust["guidance_state"] != "unavailable" ||
		trust["activation_required"] != false {
		t.Fatalf("empty pack/trust = %#v / %#v", readModel, trust)
	}
}

func TestMissionPackToolRejectsInvalidExperienceTrust(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*missionpack.Pack)
	}{
		{
			name: "zero generation",
			mutate: func(pack *missionpack.Pack) {
				pack.ExperienceGeneration = 0
			},
		},
		{
			name: "generation without experiences",
			mutate: func(pack *missionpack.Pack) {
				pack.Experiences = nil
			},
		},
		{
			name: "wrong authority",
			mutate: func(pack *missionpack.Pack) {
				pack.Experiences[0].Authority = "agent_generated"
			},
		},
		{
			name: "invalid version",
			mutate: func(pack *missionpack.Pack) {
				pack.Experiences[0].Version = 0
			},
		},
		{
			name: "too many experiences",
			mutate: func(pack *missionpack.Pack) {
				item := pack.Experiences[0]
				pack.Experiences = []missionpack.ExperienceItem{
					item,
					item,
					item,
					item,
				}
			},
		},
		{
			name: "experience in empty pack",
			mutate: func(pack *missionpack.Pack) {
				pack.Status = "empty"
				pack.Trust.GuidanceState = "unavailable"
				pack.Trust.ActivationRequired = false
				pack.KnownTraps = nil
				pack.OperatingRules = nil
				pack.Verification = nil
			},
		},
		{
			name: "experience guidance unavailable",
			mutate: func(pack *missionpack.Pack) {
				pack.Trust.GuidanceState = "unavailable"
			},
		},
		{
			name: "experience activation not required",
			mutate: func(pack *missionpack.Pack) {
				pack.Trust.ActivationRequired = false
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pack := testExperienceMissionPack()
			test.mutate(&pack)
			session := newMissionPackTestClient(
				t,
				&testMissionPackService{pack: pack},
			)

			result := callTool(
				t,
				session,
				"get_mission_pack",
				map[string]any{"cwd": "/tmp/example"},
			)
			if !result.IsError ||
				missionPackResultErrorCode(result) !=
					string(strictReadFailed) {
				t.Fatalf(
					"result = %#v, want %s",
					result,
					strictReadFailed,
				)
			}
		})
	}
}

func TestMissionPackToolRejectsInconsistentTrustStates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*missionpack.Pack)
	}{
		{
			name: "empty proposal",
			mutate: func(pack *missionpack.Pack) {
				pack.Status = "empty"
				pack.Trust.GuidanceState = "proposal"
				pack.Trust.ActivationRequired = false
			},
		},
		{
			name: "empty activatable",
			mutate: func(pack *missionpack.Pack) {
				pack.Status = "empty"
				pack.Trust.GuidanceState = "unavailable"
				pack.Trust.ActivationRequired = true
			},
		},
		{
			name: "empty with actionable content",
			mutate: func(pack *missionpack.Pack) {
				pack.Status = "empty"
				pack.Trust.GuidanceState = "unavailable"
				pack.Trust.ActivationRequired = false
			},
		},
		{
			name: "partial unavailable",
			mutate: func(pack *missionpack.Pack) {
				pack.Status = "partial"
				pack.Trust.GuidanceState = "unavailable"
				pack.Trust.ActivationRequired = false
			},
		},
		{
			name: "wrong authority",
			mutate: func(pack *missionpack.Pack) {
				pack.Trust.InstructionAuthority = "agent"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pack := testMissionPack()
			test.mutate(&pack)
			session := newMissionPackTestClient(
				t,
				&testMissionPackService{pack: pack},
			)
			result := callTool(
				t,
				session,
				"get_mission_pack",
				map[string]any{"cwd": "/tmp/example"},
			)
			if !result.IsError ||
				missionPackResultErrorCode(result) !=
					string(strictReadFailed) {
				t.Fatalf(
					"result = %#v, want %s",
					result,
					strictReadFailed,
				)
			}
		})
	}
}

func TestMissionPackToolAcceptsEvidenceOnlyKnownTrap(t *testing.T) {
	pack := testMissionPack()
	pack.KnownTraps = []missionpack.GuidanceItem{{
		ID:               "trap_retry_loop",
		Kind:             "retry_loop",
		Title:            "Repeated command failure loop",
		SessionCount:     2,
		RequiresApproval: true,
		Sources: []missionpack.SourceRef{{
			Kind:       "issue",
			IssueID:    "iss_test",
			SessionKey: "ses_test",
		}},
	}}
	pack.OperatingRules = []missionpack.GuidanceItem{}
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: pack},
	)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if result.IsError {
		t.Fatalf("get_mission_pack failed: %v", result.Content)
	}
	readModel := asObject(
		t,
		asObject(t, result.StructuredContent)["readmodel"],
	)
	traps, ok := readModel["known_traps"].([]any)
	if !ok || len(traps) != 1 {
		t.Fatalf("known_traps = %#v", readModel["known_traps"])
	}
	if _, exists := asObject(t, traps[0])["guidance"]; exists {
		t.Fatalf("evidence-only trap serialized guidance: %#v", traps[0])
	}
	rules, ok := readModel["operating_rules"].([]any)
	if !ok || len(rules) != 0 {
		t.Fatalf("operating_rules = %#v", readModel["operating_rules"])
	}
}

func TestMissionPackToolKeepsExperienceResponseByteBound(t *testing.T) {
	pack := testExperienceMissionPack()
	item := pack.Experiences[0]
	item.Rationale = strings.Repeat("r", 8*1024)
	pack.Experiences = []missionpack.ExperienceItem{item, item, item}
	pack.RenderedMarkdown = strings.Repeat(
		"approved guidance ",
		missionpack.MaxRenderedMarkdown/len("approved guidance "),
	)
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: pack},
	)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if !result.IsError ||
		missionPackResultErrorCode(result) !=
			string(strictResultTooLarge) {
		t.Fatalf(
			"result = %#v, want %s",
			result,
			strictResultTooLarge,
		)
	}
}

func TestMissionPackToolRejectsOperatingRuleWithoutGuidance(t *testing.T) {
	pack := testMissionPack()
	pack.OperatingRules = []missionpack.GuidanceItem{{
		ID:               "rule_missing_guidance",
		Kind:             "semantic_rule",
		Title:            "Project operating rule",
		RequiresApproval: true,
		Sources:          []missionpack.SourceRef{},
	}}
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: pack},
	)

	result := callTool(t, session, "get_mission_pack", map[string]any{
		"cwd": "/tmp/example",
	})
	if !result.IsError ||
		missionPackResultErrorCode(result) != string(strictReadFailed) {
		t.Fatalf("result = %#v, want %s", result, strictReadFailed)
	}
}

func TestMissionPackToolRejectsInvalidInputs(t *testing.T) {
	session := newMissionPackTestClient(
		t,
		&testMissionPackService{pack: testMissionPack()},
	)
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "empty selector", args: map[string]any{}},
		{
			name: "relative cwd",
			args: map[string]any{"cwd": "relative/project"},
		},
		{
			name: "invalid intent",
			args: map[string]any{
				"cwd":    "/tmp/example",
				"intent": "deploy",
			},
		},
		{
			name: "invalid harness",
			args: map[string]any{
				"cwd":     "/tmp/example",
				"harness": "cursor",
			},
		},
		{
			name: "oversized cwd",
			args: map[string]any{
				"cwd": "/" + strings.Repeat("x", maxMissionPackCWDBytes),
			},
		},
		{
			name: "oversized issue",
			args: map[string]any{
				"issue_id": strings.Repeat(
					"x",
					maxMissionPackIssueIDBytes+1,
				),
			},
		},
		{
			name: "oversized task hint",
			args: map[string]any{
				"cwd": "/tmp/example",
				"task_hint": strings.Repeat(
					"x",
					maxMissionPackTaskHintRunes+1,
				),
			},
		},
		{
			name: "unknown field",
			args: map[string]any{
				"cwd":       "/tmp/example",
				"authorize": true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := callTool(
				t,
				session,
				"get_mission_pack",
				test.args,
			)
			if !result.IsError ||
				missionPackResultErrorCode(result) !=
					string(strictInvalidInput) {
				t.Fatalf(
					"result = %#v, want %s",
					result,
					strictInvalidInput,
				)
			}
		})
	}
}

func TestMissionPackToolMapsFailuresToFixedCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want strictToolErrorCode
	}{
		{
			name: "validation",
			err:  testMissionPackError("invalid_request"),
			want: strictInvalidInput,
		},
		{
			name: "project not found",
			err:  missionpack.ErrProjectNotFound,
			want: strictIssueNotFound,
		},
		{
			name: "issue not found",
			err:  testMissionPackError("issue_not_found"),
			want: strictIssueNotFound,
		},
		{
			name: "project mismatch",
			err:  missionpack.ErrProjectMismatch,
			want: strictInvalidInput,
		},
		{
			name: "deadline",
			err:  context.DeadlineExceeded,
			want: strictReadTimeout,
		},
		{
			name: "local failure",
			err:  errors.New("private path /Users/example/secret"),
			want: strictReadFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := newMissionPackTestClient(
				t,
				&testMissionPackService{err: test.err},
			)
			result := callTool(
				t,
				session,
				"get_mission_pack",
				map[string]any{"issue_id": "csi_test"},
			)
			if got := missionPackResultErrorCode(result); got !=
				string(test.want) {
				t.Fatalf("error code = %q, want %q", got, test.want)
			}
			if strings.Contains(
				missionPackResultErrorCode(result),
				"private",
			) {
				t.Fatal("private service detail leaked")
			}
		})
	}
}

func newMissionPackTestClient(
	t *testing.T,
	service MissionPackService,
) *mcp.ClientSession {
	t.Helper()
	return newMissionPackTestClientWithServerOptions(
		t,
		WithMissionPackService(service),
	)
}

func newMissionPackTestClientWithServerOptions(
	t *testing.T,
	options ...Option,
) *mcp.ClientSession {
	t.Helper()
	repository := &testRepository{}
	server, err := New(
		readmodel.New(
			repository,
			readmodel.WithIssueRepository(repository),
			readmodel.WithIssueCursorCodec(testIssueCursorCodec{}),
			readmodel.WithCostIssueRepository(repository),
			readmodel.WithClock(testTime),
		),
		options...,
	)
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcp.Connect(
		context.Background(),
		serverTransport,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "mission-pack-test", Version: "1"},
		nil,
	)
	clientSession, err := client.Connect(
		context.Background(),
		clientTransport,
		nil,
	)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func testMissionPack() missionpack.Pack {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	return missionpack.Pack{
		SchemaVersion:    missionpack.SchemaVersion,
		GeneratorVersion: missionpack.GeneratorVersion,
		PackID: "mpk_" +
			strings.Repeat("a", 52),
		GeneratedAt: now,
		Project: missionpack.Project{
			Label:        "example",
			IdentityKind: "remote",
			Branch:       "main",
			Worktree:     "example",
		},
		Intent: missionpack.IntentGeneral,
		Status: "ready",
		Trust: missionpack.Trust{
			InstructionAuthority: "none",
			GuidanceState:        "proposal",
			EvidenceState:        "untrusted",
			ActivationRequired:   true,
		},
		SourceState: missionpack.SourceState{
			TranscriptGeneration: 2,
			AnalyzedGeneration:   2,
			AnalysisStatus:       missionpack.AnalysisStatusCurrent,
			DataThrough:          now,
		},
		Context: missionpack.Context{
			Harnesses: []string{"codex"},
			Facts:     []missionpack.ContextFact{},
		},
		KnownTraps:     []missionpack.GuidanceItem{},
		OperatingRules: []missionpack.GuidanceItem{},
		Verification: []missionpack.CommandItem{{
			ID:               "cmd_test",
			Command:          "go test ./...",
			Class:            "test",
			Configured:       true,
			RequiresApproval: true,
			Sources: []missionpack.SourceRef{{
				Kind:         "project_config",
				ProjectFile:  "Makefile",
				SourceSHA256: strings.Repeat("a", 64),
			}},
		}},
		Completion:       []missionpack.ChecklistItem{},
		Warnings:         []missionpack.Warning{},
		EstimatedTokens:  4,
		RenderedMarkdown: "# Mission Pack\n",
	}
}

func testMissionPackStatusResult() localapp.MissionPackStatusResult {
	acceptedAt := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	deliveredAt := acceptedAt
	evaluatedAt := acceptedAt.Add(time.Minute)
	turn := int64(4)
	return localapp.MissionPackStatusResult{
		ReceiptState:       "bound",
		DestinationHarness: "codex",
		BoundSession:       "ses_status",
		AcceptedAt:         acceptedAt,
		ExpiresAt:          acceptedAt.Add(5 * time.Minute),
		Items: []localapp.MissionPackStatusItem{{
			Instruction:        "Run the focused verifier.",
			Version:            1,
			Status:             localapp.MissionPackItemEvaluated,
			DeliveredAt:        &deliveredAt,
			EvaluatedAt:        &evaluatedAt,
			OpportunityState:   "observed",
			ApplicabilityState: "applicable",
			VerifierState:      "satisfied",
			TaskOutcomeState:   "unknown",
			CoverageGaps:       []experience.CoverageRequirement{},
			Evidence: []localapp.MissionPackStatusEvidence{{
				Kind:      "transcript_turn",
				TurnIndex: &turn,
				Excerpt:   "focused verifier passed",
			}},
			ObservedAfter: &localapp.MissionPackObservedImpact{
				ObservedAt:      evaluatedAt.Add(time.Minute),
				ComparisonState: "matched",
				MatchedSessions: 4,
				MatchedOn:       []string{"project", "harness"},
				Corrections: localapp.MissionPackImpactMetric{
					Current: 1,
				},
				FailedAttempts: localapp.MissionPackImpactMetric{
					Current: 1,
				},
				VerificationAfterLastEdit: "observed",
				TaskOutcomeState:          "unknown",
				TranscriptCoverage:        "complete",
				OutcomeCoverageComplete:   false,
				EvidenceStartTurn:         3,
				EvidenceEndTurn:           9,
			},
		}},
	}
}

func testExperienceMissionPack() missionpack.Pack {
	pack := testMissionPack()
	pack.ExperienceGeneration = 7
	pack.Experiences = []missionpack.ExperienceItem{{
		ExperienceID:  "exp_verify_after_edit",
		Version:       2,
		Type:          "procedure",
		Guidance:      "Run project verification after the final edit.",
		Applicability: "Use this rule when the task changes project files.",
		Exceptions: []string{
			"Do not apply it to read-only review tasks.",
		},
		Rationale: "Prior sessions regressed after unverified edits.",
		Verifier: missionpack.VerifierSummary{
			Kind:    "command_succeeded",
			Summary: "Observe a successful verification after the last edit.",
		},
		Authority: "user_approved",
		Sources: []missionpack.SourceRef{{
			Kind:       "transcript_turn",
			SessionKey: "ses_example",
			TurnIndex:  int64Pointer(12),
		}},
	}}
	return pack
}

func int64Pointer(value int64) *int64 {
	return &value
}

func missionPackResultErrorCode(result *mcp.CallToolResult) string {
	if result == nil || !result.IsError || len(result.Content) != 1 {
		return ""
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return text.Text
}
