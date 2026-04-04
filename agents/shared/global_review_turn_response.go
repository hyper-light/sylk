package shared

import (
	"encoding/json"
	"strings"
)

type globalReviewResponseTexter interface {
	ResponseText() string
}

// GlobalReviewTurnResponse carries the human-facing response text plus the
// machine-readable global-review action selected during the turn.
type GlobalReviewTurnResponse struct {
	Result any                     `json:"result,omitempty"`
	Action *GlobalReviewTurnAction `json:"action,omitempty"`
}

func (r *GlobalReviewTurnResponse) ResponseText() string {
	if r == nil || r.Result == nil {
		return ""
	}
	if text, ok := r.Result.(string); ok {
		return strings.TrimSpace(text)
	}
	if rt, ok := r.Result.(globalReviewResponseTexter); ok {
		return strings.TrimSpace(rt.ResponseText())
	}
	return ""
}

func EncodeGlobalReviewTurnResponse(resp *GlobalReviewTurnResponse) any {
	if resp == nil {
		return nil
	}
	return resp
}

func DecodeGlobalReviewTurnResponse(payload any) (*GlobalReviewTurnResponse, error) {
	if payload == nil {
		return nil, nil
	}
	if resp, ok := payload.(*GlobalReviewTurnResponse); ok {
		return resp, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var decoded GlobalReviewTurnResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}
