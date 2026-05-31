package boot

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

func TestPipelineReportsSetupProgressBeforeSetupLockFailure(t *testing.T) {
	root := t.TempDir()
	sd := sylkdir.New(root)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.Mkdir(sd.LockPath(), 0o755); err != nil {
		t.Fatalf("mkdir lock path: %v", err)
	}

	var progress []string
	pipeline := &Pipeline{config: &PipelineConfig{
		ProjectRoot: root,
		OnProgress: func(phase string, current, total int64) {
			progress = append(progress, phase)
			if phase != "setup" || current != 0 || total != 6 {
				t.Fatalf("progress = %s %d/%d, want setup 0/6", phase, current, total)
			}
		},
	}}

	_, err := pipeline.Run(context.Background())
	if err == nil {
		t.Fatal("Run error = nil, want setup lock failure")
	}
	if !strings.Contains(err.Error(), "setup: lock sylkdir") {
		t.Fatalf("Run error = %v, want setup lock failure", err)
	}
	if len(progress) != 1 {
		t.Fatalf("progress events = %v, want exactly setup start", progress)
	}
}
