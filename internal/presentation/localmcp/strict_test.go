package localmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type strictTestInput struct {
	Value string `json:"value"`
}

type strictTestOutput struct {
	Value string `json:"value"`
}

func TestStrictToolReturnsBoundedTrustedWrapper(t *testing.T) {
	adapter := newStrictToolAdapter(time.Second)
	schemas := strictTestSchemas(t)
	handler := bindStrictTool(
		adapter,
		schemas,
		func(_ context.Context, input strictTestInput) (strictTestOutput, error) {
			return strictTestOutput{Value: input.Value}, nil
		},
	)

	result, err := handler(
		context.Background(),
		strictRequest(`{"value":"structured-only"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("strict tool returned error: %#v", result.Content)
	}
	raw, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("structured content type = %T, want json.RawMessage", result.StructuredContent)
	}
	var output struct {
		UntrustedObservations bool        `json:"untrusted_observations"`
		Trust                 strictTrust `json:"trust"`
		ReadModel             struct {
			Value string `json:"value"`
		} `json:"readmodel"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if !output.UntrustedObservations ||
		output.Trust.Classification != "untrusted_observations" ||
		output.Trust.InstructionAuthority != "none" ||
		!output.Trust.MustNotAuthorizeActions ||
		output.ReadModel.Value != "structured-only" {
		t.Fatalf("strict wrapper = %+v", output)
	}
	if got := toolResultText(t, result); got != strictToolNarrative {
		t.Fatalf("narrative = %q, want fixed narrative", got)
	}
	if strings.Contains(toolResultText(t, result), "structured-only") {
		t.Fatal("structured value leaked into narrative content")
	}
}

func TestStrictToolRejectsRawInvalidInputWithoutInvocation(t *testing.T) {
	var invoked atomic.Int32
	handler := bindStrictTool(
		newStrictToolAdapter(time.Second),
		strictTestSchemas(t),
		func(context.Context, strictTestInput) (strictTestOutput, error) {
			invoked.Add(1)
			return strictTestOutput{}, nil
		},
	)
	oversized := `{"value":"` +
		strings.Repeat("x", maxStrictToolArgumentBytes) +
		`"}`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "malformed", raw: `{"value":`},
		{name: "duplicate", raw: `{"value":"safe","value":"PRIVATE_CANARY"}`},
		{name: "unknown", raw: `{"unknown":"PRIVATE_CANARY"}`},
		{name: "wrong type", raw: `{"value":42}`},
		{name: "multiple values", raw: `{"value":"safe"} {"value":"PRIVATE_CANARY"}`},
		{name: "oversized", raw: oversized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := handler(context.Background(), strictRequest(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			assertStrictToolError(t, result, strictInvalidInput)
			if strings.Contains(toolResultText(t, result), "PRIVATE_CANARY") {
				t.Fatal("invalid input was reflected in tool error")
			}
		})
	}
	if invoked.Load() != 0 {
		t.Fatalf("handler invoked %d times for invalid input", invoked.Load())
	}
}

func TestStrictToolAcceptsExactArgumentLimit(t *testing.T) {
	const prefix = `{"value":"`
	const suffix = `"}`
	value := strings.Repeat(
		"x",
		maxStrictToolArgumentBytes-len(prefix)-len(suffix),
	)
	raw := prefix + value + suffix
	if len(raw) != maxStrictToolArgumentBytes {
		t.Fatalf("fixture length = %d, want %d", len(raw), maxStrictToolArgumentBytes)
	}
	handler := bindStrictTool(
		newStrictToolAdapter(time.Second),
		strictTestSchemas(t),
		func(_ context.Context, input strictTestInput) (strictTestOutput, error) {
			return strictTestOutput{Value: input.Value}, nil
		},
	)
	result, err := handler(context.Background(), strictRequest(raw))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exact-limit arguments returned error: %#v", result.Content)
	}
}

func TestRejectDuplicateJSONKeysRecursively(t *testing.T) {
	if err := rejectDuplicateJSONKeys(
		[]byte(`{"outer":{"value":"one","value":"two"}}`),
	); err == nil {
		t.Fatal("nested duplicate key was accepted")
	}
	if err := rejectDuplicateJSONKeys(
		[]byte(`{"left":{"value":"one"},"right":{"value":"two"}}`),
	); err != nil {
		t.Fatalf("distinct nested keys rejected: %v", err)
	}
}

func TestStrictToolMapsFixedFailuresWithoutPayloads(t *testing.T) {
	codes := []strictToolErrorCode{
		strictInvalidInput,
		strictInvalidCursor,
		strictCursorExpired,
		strictIssueNotFound,
		strictReadBusy,
		strictReadTimeout,
		strictResultTooLarge,
		strictCancelled,
		strictReadFailed,
	}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			handler := bindStrictTool(
				newStrictToolAdapter(time.Second),
				strictTestSchemas(t),
				func(context.Context, strictTestInput) (strictTestOutput, error) {
					return strictTestOutput{}, newStrictToolFailure(code)
				},
			)
			result, err := handler(
				context.Background(),
				strictRequest(`{"value":"PRIVATE_CANARY"}`),
			)
			if err != nil {
				t.Fatal(err)
			}
			assertStrictToolError(t, result, code)
			if strings.Contains(toolResultText(t, result), "PRIVATE_CANARY") {
				t.Fatal("handler input was reflected in tool error")
			}
		})
	}

	handler := bindStrictTool(
		newStrictToolAdapter(time.Second),
		strictTestSchemas(t),
		func(context.Context, strictTestInput) (strictTestOutput, error) {
			return strictTestOutput{}, errors.New("PRIVATE_DATABASE_FAILURE")
		},
	)
	result, err := handler(
		context.Background(),
		strictRequest(`{"value":"safe"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStrictToolError(t, result, strictReadFailed)
	if strings.Contains(toolResultText(t, result), "PRIVATE_DATABASE_FAILURE") {
		t.Fatal("handler error was reflected in tool result")
	}
}

func TestStrictToolRejectsInvalidAndOversizedOutput(t *testing.T) {
	t.Run("schema violation", func(t *testing.T) {
		type invalidOutput struct {
			Other string `json:"other"`
		}
		handler := bindStrictTool(
			newStrictToolAdapter(time.Second),
			strictTestSchemas(t),
			func(context.Context, strictTestInput) (invalidOutput, error) {
				return invalidOutput{Other: "PRIVATE_INVALID_OUTPUT"}, nil
			},
		)
		result, err := handler(
			context.Background(),
			strictRequest(`{"value":"safe"}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		assertStrictToolError(t, result, strictReadFailed)
		if strings.Contains(toolResultText(t, result), "PRIVATE_INVALID_OUTPUT") {
			t.Fatal("invalid server output was reflected in tool result")
		}
	})

	t.Run("result cap", func(t *testing.T) {
		handler := bindStrictTool(
			newStrictToolAdapter(time.Second),
			strictTestSchemas(t),
			func(context.Context, strictTestInput) (strictTestOutput, error) {
				return strictTestOutput{
					Value: strings.Repeat("x", maxStrictToolStructuredBytes),
				}, nil
			},
		)
		result, err := handler(
			context.Background(),
			strictRequest(`{"value":"safe"}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		assertStrictToolError(t, result, strictResultTooLarge)
		if result.StructuredContent != nil {
			t.Fatal("oversized result returned partial structured content")
		}
	})
}

func TestBoundedJSONEncoderAcceptsExactLimitAndRejectsOverflow(t *testing.T) {
	const limit = 10
	exact, err := encodeBoundedJSON(strings.Repeat("x", limit-2), limit)
	if err != nil {
		t.Fatalf("exact-limit encode: %v", err)
	}
	if len(exact) != limit {
		t.Fatalf("exact encoded length = %d, want %d", len(exact), limit)
	}
	if _, err := encodeBoundedJSON(strings.Repeat("x", limit-1), limit); !errors.Is(
		err,
		errJSONLimitExceeded,
	) {
		t.Fatalf("overflow encode error = %v, want size limit", err)
	}
}

func TestStrictToolAdmissionAndDatabaseSerialization(t *testing.T) {
	adapter := newStrictToolAdapter(2 * time.Second)
	schemas := strictTestSchemas(t)
	started := make(chan struct{}, strictToolAdmissionSlots)
	release := make(chan struct{}, strictToolAdmissionSlots)
	var active atomic.Int32
	var maximum atomic.Int32
	handler := bindStrictTool(
		adapter,
		schemas,
		func(ctx context.Context, input strictTestInput) (strictTestOutput, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-release:
				return strictTestOutput{Value: input.Value}, nil
			case <-ctx.Done():
				return strictTestOutput{}, ctx.Err()
			}
		},
	)

	results := make(chan *mcp.CallToolResult, strictToolAdmissionSlots)
	var group sync.WaitGroup
	for index := 0; index < strictToolAdmissionSlots; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			result, err := handler(
				context.Background(),
				strictRequest(fmt.Sprintf(`{"value":"call-%d"}`, index)),
			)
			if err != nil {
				t.Errorf("strict call error: %v", err)
				return
			}
			results <- result
		}(index)
	}
	waitForCondition(t, time.Second, func() bool {
		return len(adapter.admission) == strictToolAdmissionSlots
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("database-active handler did not start")
	}

	fifth, err := handler(
		context.Background(),
		strictRequest(`{"value":"fifth"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStrictToolError(t, fifth, strictReadBusy)

	for index := 0; index < strictToolAdmissionSlots; index++ {
		release <- struct{}{}
		if index+1 < strictToolAdmissionSlots {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("next serialized database handler did not start")
			}
		}
	}
	group.Wait()
	close(results)
	for result := range results {
		if result.IsError {
			t.Fatalf("admitted call returned error: %#v", result.Content)
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum database-active calls = %d, want 1", maximum.Load())
	}
	if len(adapter.admission) != 0 || len(adapter.database) != 0 {
		t.Fatalf(
			"semaphore tokens leaked: admission=%d database=%d",
			len(adapter.admission),
			len(adapter.database),
		)
	}
}

func TestStrictToolDeadlineCancellationAndCleanup(t *testing.T) {
	t.Run("deadline includes database queue", func(t *testing.T) {
		adapter := newStrictToolAdapter(50 * time.Millisecond)
		handler := bindStrictTool(
			adapter,
			strictTestSchemas(t),
			func(ctx context.Context, _ strictTestInput) (strictTestOutput, error) {
				<-ctx.Done()
				return strictTestOutput{}, ctx.Err()
			},
		)
		results := make(chan *mcp.CallToolResult, 2)
		for index := 0; index < 2; index++ {
			go func() {
				result, _ := handler(
					context.Background(),
					strictRequest(`{"value":"safe"}`),
				)
				results <- result
			}()
		}
		for index := 0; index < 2; index++ {
			select {
			case result := <-results:
				assertStrictToolError(t, result, strictReadTimeout)
			case <-time.After(time.Second):
				t.Fatal("timed call did not finish")
			}
		}
		waitForCondition(t, time.Second, func() bool {
			return len(adapter.admission) == 0 && len(adapter.database) == 0
		})
	})

	t.Run("parent cancellation", func(t *testing.T) {
		adapter := newStrictToolAdapter(time.Second)
		started := make(chan struct{})
		handler := bindStrictTool(
			adapter,
			strictTestSchemas(t),
			func(ctx context.Context, _ strictTestInput) (strictTestOutput, error) {
				close(started)
				<-ctx.Done()
				return strictTestOutput{}, ctx.Err()
			},
		)
		ctx, cancel := context.WithCancel(context.Background())
		resultChannel := make(chan *mcp.CallToolResult, 1)
		go func() {
			result, _ := handler(ctx, strictRequest(`{"value":"safe"}`))
			resultChannel <- result
		}()
		<-started
		cancel()
		result := <-resultChannel
		assertStrictToolError(t, result, strictCancelled)
		if len(adapter.admission) != 0 || len(adapter.database) != 0 {
			t.Fatalf(
				"semaphore tokens leaked: admission=%d database=%d",
				len(adapter.admission),
				len(adapter.database),
			)
		}
	})
}

func TestStrictToolRawProtocolRejectsDuplicateKey(t *testing.T) {
	adapter := newStrictToolAdapter(time.Second)
	schemas := strictTestSchemas(t)
	var invoked atomic.Int32
	tool, err := newStrictReadOnlyTool(
		"strict_test",
		"Synthetic strict adapter test tool.",
		schemas,
	)
	if err != nil {
		t.Fatal(err)
	}
	protocolServer := mcp.NewServer(
		&mcp.Implementation{Name: "strict-adapter-test", Version: "1"},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{},
			},
		},
	)
	protocolServer.AddTool(tool, bindStrictTool(
		adapter,
		schemas,
		func(context.Context, strictTestInput) (strictTestOutput, error) {
			invoked.Add(1)
			return strictTestOutput{}, nil
		},
	))

	client, stop := startRawMCPServer(t, protocolServer)
	defer stop()
	reader := bufio.NewReader(client)
	writeRawMCPMessage(t, client, `{
		"jsonrpc":"2.0",
		"id":1,
		"method":"initialize",
		"params":{
			"protocolVersion":"2025-06-18",
			"capabilities":{},
			"clientInfo":{"name":"raw-test","version":"1"}
		}
	}`)
	readRawMCPMessage(t, client, reader)
	writeRawMCPMessage(
		t,
		client,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	writeRawMCPMessage(t, client, `{
		"jsonrpc":"2.0",
		"id":2,
		"method":"tools/call",
		"params":{
			"name":"strict_test",
			"arguments":{"value":"safe","value":"PRIVATE_CANARY"}
		}
	}`)
	response := readRawMCPMessage(t, client, reader)
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("raw response has no result: %#v", response)
	}
	if result["isError"] != true {
		t.Fatalf("raw result isError = %#v", result["isError"])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), string(strictInvalidInput)) {
		t.Fatalf("raw result does not contain fixed invalid-input code: %s", encoded)
	}
	if strings.Contains(string(encoded), "PRIVATE_CANARY") {
		t.Fatal("raw duplicate value was reflected in protocol result")
	}
	if invoked.Load() != 0 {
		t.Fatalf("handler invoked %d times for duplicate-key request", invoked.Load())
	}
}

func strictRequest(arguments string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "strict_test",
			Arguments: json.RawMessage(arguments),
		},
	}
}

func strictTestSchemas(t *testing.T) *strictToolSchemas {
	t.Helper()
	input := closedTestObjectSchema(
		map[string]*jsonschema.Schema{
			"value": {Type: "string"},
		},
		"value",
	)
	output := closedTestObjectSchema(
		map[string]*jsonschema.Schema{
			"untrusted_observations": {Type: "boolean"},
			"trust": closedTestObjectSchema(
				map[string]*jsonschema.Schema{
					"classification":             {Type: "string"},
					"instruction_authority":      {Type: "string"},
					"must_not_authorize_actions": {Type: "boolean"},
				},
				"classification",
				"instruction_authority",
				"must_not_authorize_actions",
			),
			"readmodel": closedTestObjectSchema(
				map[string]*jsonschema.Schema{
					"value": {Type: "string"},
				},
				"value",
			),
		},
		"untrusted_observations",
		"trust",
		"readmodel",
	)
	schemas, err := newStrictToolSchemas(input, output)
	if err != nil {
		t.Fatal(err)
	}
	return schemas
}

func closedTestObjectSchema(
	properties map[string]*jsonschema.Schema,
	required ...string,
) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

func toolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("tool result content = %#v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("tool content type = %T, want text", result.Content[0])
	}
	return text.Text
}

func assertStrictToolError(
	t *testing.T,
	result *mcp.CallToolResult,
	code strictToolErrorCode,
) {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("result = %#v, want tool error %q", result, code)
	}
	if result.StructuredContent != nil {
		t.Fatalf("error %q returned structured content", code)
	}
	if got := toolResultText(t, result); got != string(code) {
		t.Fatalf("tool error text = %q, want %q", got, code)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func startRawMCPServer(
	t *testing.T,
	server *mcp.Server,
) (net.Conn, func()) {
	t.Helper()
	serverConnection, clientConnection := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, &mcp.IOTransport{
			Reader: serverConnection,
			Writer: noCloseWriter{Writer: serverConnection},
		})
	}()
	return clientConnection, func() {
		cancel()
		_ = clientConnection.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("raw MCP server did not stop")
		}
	}
}

func writeRawMCPMessage(t *testing.T, connection net.Conn, message string) {
	t.Helper()
	if err := connection.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(message)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(connection, compact.String()); err != nil {
		t.Fatal(err)
	}
}

func readRawMCPMessage(
	t *testing.T,
	connection net.Conn,
	reader *bufio.Reader,
) map[string]any {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode raw MCP response: %v: %s", err, line)
	}
	return response
}
