package guide

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	geminiTraceEnvEnable  = "SYLK_GEMINI_TRACE"
	geminiTraceEnvFile    = "SYLK_GEMINI_TRACE_FILE"
	geminiTraceDefaultLog = "/tmp/sylk_gemini_trace.log"
)

var geminiTraceMu sync.Mutex

type geminiTraceRecord struct {
	Timestamp string         `json:"timestamp"`
	Component string         `json:"component"`
	Event     string         `json:"event"`
	Fields    map[string]any `json:"fields,omitempty"`
}

func geminiTrace(component string, event string, fields map[string]any) {
	path := geminiTraceFilePath()
	if path == "" {
		return
	}
	record := geminiTraceRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Component: strings.TrimSpace(component),
		Event:     strings.TrimSpace(event),
		Fields:    fields,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	payload = append(payload, '\n')

	geminiTraceMu.Lock()
	defer geminiTraceMu.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(payload)
}

func geminiTraceFilePath() string {
	if explicit := strings.TrimSpace(os.Getenv(geminiTraceEnvFile)); explicit != "" {
		return explicit
	}
	if envTruthy(os.Getenv(geminiTraceEnvEnable)) {
		return geminiTraceDefaultLog
	}
	return ""
}

func envTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func tracePreview(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return string(runes[:maxRunes]) + "..."
}
