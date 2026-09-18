package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxToolInputBytes  = 16 << 10
	maxToolResultBytes = 16 << 10
)

func (p *parser) scrub(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value, _ = p.options.Boundary.ScrubSecrets(value)
	return value
}

func (p *parser) scrubJSON(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		value = map[string]any{"value": p.scrub(string(raw))}
	} else {
		value = p.scrubJSONValue(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	if len(encoded) <= maxToolInputBytes {
		return encoded
	}
	return truncatedJSON(encoded)
}

func (p *parser) scrubJSONValue(value any) any {
	switch typed := value.(type) {
	case string:
		return p.scrub(typed)
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = p.scrubJSONValue(typed[index])
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[p.scrub(key)] = p.scrubJSONValue(child)
		}
		return result
	default:
		return value
	}
}

func truncatedJSON(encoded []byte) json.RawMessage {
	const marker = "[belay truncated tool_input]"
	headBytes := maxToolInputBytes / 3
	tailBytes := maxToolInputBytes / 3
	for headBytes > 0 && tailBytes > 0 {
		value := map[string]any{
			"_belay_truncated": true,
			"marker":           marker,
			"head":             validHead(string(encoded), headBytes),
			"tail":             validTail(string(encoded), tailBytes),
		}
		result, err := json.Marshal(value)
		if err == nil && len(result) <= maxToolInputBytes {
			return result
		}
		headBytes -= 128
		tailBytes -= 128
	}
	return json.RawMessage(`{"_belay_truncated":true,"marker":"[belay truncated tool_input]"}`)
}

func (p *parser) truncateToolResult(value string) string {
	value = p.scrub(value)
	if len(value) <= maxToolResultBytes {
		return value
	}
	marker := fmt.Sprintf(
		"\n...[belay truncated %d bytes; head and tail retained]...\n",
		len(value)-maxToolResultBytes,
	)
	remaining := maxToolResultBytes - len(marker)
	head := remaining / 2
	tail := remaining - head
	return validHead(value, head) + marker + validTail(value, tail)
}

func validHead(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func validTail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[len(value)-limit:]
	for !utf8.ValidString(value) {
		_, size := utf8.DecodeRuneInString(value)
		value = value[size:]
	}
	return value
}

func (p *parser) compactJSON(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, raw); err != nil {
		return p.scrub(string(raw))
	}
	return p.scrub(buffer.String())
}
