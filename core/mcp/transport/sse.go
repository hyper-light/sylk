package transport

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// sseEvent is a parsed Server-Sent Event record. Only the data payload is
// carried (id/event/retry are tracked but not exposed; the MCP spec does not
// rely on retry semantics for transport-layer correctness).
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// sseParser is a minimal RFC-compliant SSE parser. We don't depend on a
// third-party SSE library because the MCP profile uses a tiny subset (data
// only, plus optional id/event), and rolling it ourselves means one less
// dependency to audit and lets us bound buffer growth.
type sseParser struct {
	scanner *bufio.Scanner
}

const sseMaxLineBytes = 1 << 20

func newSSEParser(r io.Reader) *sseParser {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), sseMaxLineBytes)
	return &sseParser{scanner: scanner}
}

// Next parses one event from the stream. Returns io.EOF when the stream ends.
// A returned nil event with nil error means a heartbeat / comment line — the
// caller should keep reading.
func (p *sseParser) Next() (*sseEvent, error) {
	event := &sseEvent{}
	hasData := false
	for p.scanner.Scan() {
		line := p.scanner.Text()
		if line == "" {
			if hasData {
				return event, nil
			}
			continue
		}
		consumed := applySSELine(event, line)
		hasData = hasData || consumed
	}
	if err := p.scanner.Err(); err != nil {
		return nil, err
	}
	if hasData {
		return event, nil
	}
	return nil, io.EOF
}

// applySSELine merges one SSE field into the in-flight event. Returns true
// if the line carried data (used to decide if a stream-end-mid-event is a
// real event or just trailing whitespace).
func applySSELine(event *sseEvent, line string) bool {
	if strings.HasPrefix(line, ":") {
		return false
	}
	field, value := splitSSEField(line)
	switch field {
	case "data":
		appendData(event, value)
		return true
	case "event":
		event.Event = value
	case "id":
		event.ID = value
	}
	return false
}

func splitSSEField(line string) (string, string) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line, ""
	}
	field := line[:idx]
	value := line[idx+1:]
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value
}

func appendData(event *sseEvent, value string) {
	if event.Data == "" {
		event.Data = value
		return
	}
	event.Data = event.Data + "\n" + value
}

// errSSEMalformed reports a fatal stream error. Reserved for future use when
// streaming codepaths need to distinguish framing errors from upstream EOF.
var errSSEMalformed = errors.New("mcp sse: malformed event stream")
