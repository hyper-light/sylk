package violations

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
)

type Artifact struct {
	Data     []byte
	DataType string
}

type Validation struct {
	Status        string
	StatusHistory []string
}

type HandlerDeterminism string

type Bus interface {
	SubscribeDelta(pattern string, handler any) (any, error)
}

func NewParticipantRegistration(category string, routeKey string, scope map[string]string, queueCapacity int, concurrencyBudget int, timeout time.Duration, determinism HandlerDeterminism, actions []string) {
}

func Violations(ctx context.Context, bus Bus, session *versioning.SessionVFS, manager versioning.VFSManager) {
	go func() {}()                                                                                                           // want "raw go statement is forbidden"
	_ = make(chan int)                                                                                                       // want "literal unbounded work channel is forbidden"
	_, _ = bus.SubscribeDelta("*", nil)                                                                                      // want "broad claims subscription is forbidden"
	NewParticipantRegistration("service", "vfs", map[string]string{"session": "s"}, 1, 1, time.Second, "", []string{"task"}) // want "participant registration must declare HandlerDeterminism"
	_ = Artifact{Data: []byte("payload")}                                                                                    // want "claims Artifact with Data must set DataType"
	_, _ = session.BeginPipeline(versioning.BeginPipelineConfig{})                                                           // want "direct VFS mutation bypasses claims evidence"
	_, _ = session.CommitPipeline(ctx, "pipe")                                                                               // want "direct VFS mutation bypasses claims evidence"
	_, _ = session.MergePipelineIntoGreen(ctx, "pipe", versioning.PipelineInspectorCertificate{})                            // want "direct VFS mutation bypasses claims evidence"
	_, _ = session.RollbackPipelineIfTracked("pipe")                                                                         // want "direct VFS mutation bypasses claims evidence"
	_, _ = manager.CreatePipelineVFS(versioning.VFSConfig{})                                                                 // want "direct VFS mutation bypasses claims evidence"
	validation := &Validation{}
	validation.Status = "failed"                                          // want "direct claims lifecycle/status mutation is forbidden"
	validation.StatusHistory = append(validation.StatusHistory, "failed") // want "direct claims lifecycle/status mutation is forbidden"
}
