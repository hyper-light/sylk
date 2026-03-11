package versioning

import (
	"context"
	"errors"
	"strings"
)

type PromotionMode string

const (
	PromotionModeExplicit      PromotionMode = "explicit"
	PromotionModeDeterministic PromotionMode = "deterministic"
)

var ErrInvalidPromotionMode = errors.New("invalid promotion mode")

type PromotionRequest struct {
	Mode   PromotionMode
	Reason string
}

func PromoteSessionDraft(ctx context.Context, session *SessionVFS, req PromotionRequest) (*FlushResult, error) {
	if !validPromotionMode(req.Mode) {
		return nil, ErrInvalidPromotionMode
	}
	if session == nil || session.DiskFlusher() == nil {
		return nil, ErrDiskExportDisabled
	}
	_ = strings.TrimSpace(req.Reason)
	return session.DiskFlusher().Flush(ctx)
}

func validPromotionMode(mode PromotionMode) bool {
	return mode == PromotionModeExplicit || mode == PromotionModeDeterministic
}
