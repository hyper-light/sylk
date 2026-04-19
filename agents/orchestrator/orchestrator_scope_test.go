package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

func TestNew_WithSylkDir_ConfiguresLongLivedBackgroundScope(t *testing.T) {
	projectDir := t.TempDir()
	sd := sylkdir.New(projectDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	cfg := DefaultConfig()
	cfg.Factory = newTestFactory(t)
	o, err := New(cfg, nil, nil, sd)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if o.scope != nil {
			_ = o.scope.Shutdown(0, 0)
		}
		if o.bufferRegistry != nil {
			_ = o.bufferRegistry.Close()
		}
		if o.coordination != nil {
			o.coordination.Close()
		}
		if o.journal != nil {
			_ = o.journal.Close()
		}
		if o.store != nil {
			_ = o.store.Close()
		}
		if o.logWAL != nil {
			_ = o.logWAL.Close()
		}
	})

	deadlineCh := make(chan time.Time, 1)
	errCh := make(chan error, 1)
	if err := o.scope.Go("lifetime-probe", 0, func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			errCh <- fmt.Errorf("scope worker missing deadline")
			return nil
		}
		deadlineCh <- deadline
		return nil
	}); err != nil {
		t.Fatalf("scope.Go() error = %v", err)
	}

	select {
	case deadline := <-deadlineCh:
		if remaining := time.Until(deadline); remaining < 23*time.Hour {
			t.Fatalf("scope lifetime remaining = %v, want at least 23h", remaining)
		}
	case err := <-errCh:
		t.Fatalf("scope worker reported error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scope deadline probe")
	}
}
