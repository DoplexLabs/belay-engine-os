package localmcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxStdioMessageBytes  = 256 << 10
	frameReaderBufferSize = 32 << 10
)

var errStdioMessageTooLarge = errors.New("MCP stdio message exceeds limit")

// BoundedStdioTransport preserves the SDK's newline-delimited JSON transport
// while ensuring a complete inbound message is bounded before the SDK decodes
// it. A zero value uses stdin and stdout.
type BoundedStdioTransport struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
}

func (t *BoundedStdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	reader := t.Reader
	if reader == nil {
		reader = os.Stdin
	}
	writer := t.Writer
	if writer == nil {
		writer = noCloseWriter{Writer: os.Stdout}
	}
	return (&mcp.IOTransport{
		Reader: newBoundedNDJSONReader(reader, maxStdioMessageBytes),
		Writer: writer,
	}).Connect(ctx)
}

type noCloseWriter struct {
	io.Writer
}

func (noCloseWriter) Close() error { return nil }

type boundedNDJSONReader struct {
	source  io.ReadCloser
	reader  *bufio.Reader
	limit   int
	pending []byte
}

func newBoundedNDJSONReader(source io.ReadCloser, limit int) *boundedNDJSONReader {
	return &boundedNDJSONReader{
		source: source,
		reader: bufio.NewReaderSize(source, frameReaderBufferSize),
		limit:  limit,
	}
}

func (r *boundedNDJSONReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 {
		frame, err := r.readFrame()
		if err != nil {
			return 0, err
		}
		r.pending = frame
	}
	count := copy(destination, r.pending)
	r.pending = r.pending[count:]
	return count, nil
}

func (r *boundedNDJSONReader) Close() error {
	return r.source.Close()
}

func (r *boundedNDJSONReader) readFrame() ([]byte, error) {
	frame := make([]byte, 0, r.limit+2)
	for {
		fragment, err := r.reader.ReadSlice('\n')
		frame = append(frame, fragment...)
		if len(frame) > r.limit+2 {
			return nil, errStdioMessageTooLarge
		}
		switch {
		case err == nil:
			return r.validateFrame(frame)
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(frame) > 0:
			return r.validateFrame(frame)
		case errors.Is(err, io.EOF):
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}

func (r *boundedNDJSONReader) validateFrame(frame []byte) ([]byte, error) {
	payloadLength := len(frame)
	if payloadLength > 0 && frame[payloadLength-1] == '\n' {
		payloadLength--
		if payloadLength > 0 && frame[payloadLength-1] == '\r' {
			payloadLength--
		}
	}
	if payloadLength > r.limit {
		return nil, errStdioMessageTooLarge
	}
	return frame, nil
}
