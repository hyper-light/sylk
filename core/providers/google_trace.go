package providers

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	googleTraceEnvEnable  = "SYLK_GOOGLE_TRACE"
	googleTraceEnvFile    = "SYLK_GOOGLE_TRACE_FILE"
	googleTraceDefaultLog = "/tmp/sylk_google_trace.log"
)

var googleTraceMu sync.Mutex

type googleTraceRecord struct {
	Timestamp string         `json:"timestamp"`
	Component string         `json:"component"`
	Event     string         `json:"event"`
	Fields    map[string]any `json:"fields,omitempty"`
}

func googleTrace(component string, event string, fields map[string]any) {
	path := googleTraceFilePath()
	if path == "" {
		return
	}
	record := googleTraceRecord{
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

	googleTraceMu.Lock()
	defer googleTraceMu.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(payload)
}

func googleTraceFilePath() string {
	if explicit := strings.TrimSpace(os.Getenv(googleTraceEnvFile)); explicit != "" {
		return explicit
	}
	if googleTraceEnvTruthy(os.Getenv(googleTraceEnvEnable)) {
		return googleTraceDefaultLog
	}
	return ""
}

func googleTraceEnvTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func googleTracePreview(raw string, maxRunes int) string {
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
