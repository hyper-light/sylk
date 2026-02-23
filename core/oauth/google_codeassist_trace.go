package oauth

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	codeAssistTraceEnvEnable  = "SYLK_GOOGLE_TRACE"
	codeAssistTraceEnvFile    = "SYLK_GOOGLE_TRACE_FILE"
	codeAssistTraceDefaultLog = "/tmp/sylk_google_trace.log"
)

var codeAssistTraceMu sync.Mutex

type codeAssistTraceRecord struct {
	Timestamp string         `json:"timestamp"`
	Component string         `json:"component"`
	Event     string         `json:"event"`
	Fields    map[string]any `json:"fields,omitempty"`
}

func codeAssistTrace(component string, event string, fields map[string]any) {
	path := codeAssistTraceFilePath()
	if path == "" {
		return
	}
	record := codeAssistTraceRecord{
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

	codeAssistTraceMu.Lock()
	defer codeAssistTraceMu.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(payload)
}

func codeAssistTraceFilePath() string {
	if explicit := strings.TrimSpace(os.Getenv(codeAssistTraceEnvFile)); explicit != "" {
		return explicit
	}
	if codeAssistTraceEnvTruthy(os.Getenv(codeAssistTraceEnvEnable)) {
		return codeAssistTraceDefaultLog
	}
	return ""
}

func codeAssistTraceEnvTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
