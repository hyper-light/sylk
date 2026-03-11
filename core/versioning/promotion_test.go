package versioning

import (
	"context"
	"errors"
	"testing"
)

func TestPromoteSessionDraftRejectsInvalidMode(t *testing.T) {
	_, err := PromoteSessionDraft(context.Background(), &SessionVFS{}, PromotionRequest{})
	if !errors.Is(err, ErrInvalidPromotionMode) {
		t.Fatalf("PromoteSessionDraft error = %v, want %v", err, ErrInvalidPromotionMode)
	}
}
