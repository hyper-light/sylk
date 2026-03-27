package guide

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ArchivalistStoreRouteInput returns a deterministic direct-communication DSL
// route for archivalist store/history requests.
func ArchivalistStoreRouteInput(content string) (string, error) {
	payload := map[string]any{}
	if trimmed := strings.TrimSpace(content); trimmed != "" {
		payload["content"] = trimmed
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode archivalist store payload: %w", err)
	}
	return "@archivalist:store:history" + string(encoded), nil
}
