package localmcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBoundedNDJSONReaderAcceptsBoundaryAndMultipleFrames(t *testing.T) {
	firstPayload := strings.Repeat("a", maxStdioMessageBytes)
	input := firstPayload + "\r\n" + `{"jsonrpc":"2.0"}` + "\n"
	reader := newBoundedNDJSONReader(
		io.NopCloser(strings.NewReader(input)),
		maxStdioMessageBytes,
	)
	defer reader.Close()

	first, err := reader.readFrame()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != firstPayload+"\r\n" {
		t.Fatalf("first frame length = %d, want %d", len(first), len(firstPayload)+2)
	}
	second, err := reader.readFrame()
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != `{"jsonrpc":"2.0"}`+"\n" {
		t.Fatalf("second frame = %q", second)
	}
	if _, err := reader.readFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal error = %v, want EOF", err)
	}
}

func TestBoundedNDJSONReaderAcceptsFinalFrameWithoutNewline(t *testing.T) {
	reader := newBoundedNDJSONReader(
		io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0"}`)),
		maxStdioMessageBytes,
	)
	defer reader.Close()

	frame, err := reader.readFrame()
	if err != nil {
		t.Fatal(err)
	}
	if string(frame) != `{"jsonrpc":"2.0"}` {
		t.Fatalf("frame = %q", frame)
	}
}

func TestBoundedNDJSONReaderRejectsOversizeBeforeExposure(t *testing.T) {
	reader := newBoundedNDJSONReader(
		io.NopCloser(strings.NewReader(
			strings.Repeat("x", maxStdioMessageBytes+1)+"\n",
		)),
		maxStdioMessageBytes,
	)
	defer reader.Close()

	destination := make([]byte, 4096)
	count, err := reader.Read(destination)
	if count != 0 {
		t.Fatalf("read exposed %d bytes before rejecting oversized frame", count)
	}
	if !errors.Is(err, errStdioMessageTooLarge) {
		t.Fatalf("read error = %v, want oversized-frame error", err)
	}
}

func TestBoundedStdioTransportClosesOversizedSessionWithoutResponse(t *testing.T) {
	var output bytes.Buffer
	protocolServer := mcp.NewServer(
		&mcp.Implementation{Name: "bounded-transport-test", Version: "1"},
		nil,
	)
	transport := &BoundedStdioTransport{
		Reader: io.NopCloser(strings.NewReader(
			strings.Repeat("x", maxStdioMessageBytes+1) + "\n",
		)),
		Writer: noCloseWriter{Writer: &output},
	}

	err := protocolServer.Run(context.Background(), transport)
	if !errors.Is(err, errStdioMessageTooLarge) {
		t.Fatalf("Run error = %v, want oversized-frame error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized pre-decode frame produced %d response bytes", output.Len())
	}
}
