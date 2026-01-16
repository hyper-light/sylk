package pool

import "time"

//go:generate mockery --name=PriorityPoolService --output=./mocks --outpkg=mocks
//go:generate mockery --name=SessionPoolService --output=./mocks --outpkg=mocks
//go:generate mockery --name=FairnessControllerService --output=./mocks --outpkg=mocks

type PriorityPoolService interface {
	Start()
	Stop()
	Close() error
	Submit(job *PriorityJob) bool
	SubmitBlocking(job *PriorityJob) bool
	SubmitWithTimeout(job *PriorityJob, timeout time.Duration) bool
	Stats() PriorityPoolStats
}

type SessionPoolService interface {
	Start()
	Stop()
	Close() error
	Submit(job *SessionJob) bool
	SubmitBlocking(job *SessionJob) bool
	Stats() SessionPoolStats
	RemoveSession(sessionID string)
	SessionCount() int
	SessionIDs() []string
}

type FairnessControllerService interface {
	Acquire(entityID string) (bool, error)
	Release(entityID string) error
	SelectFairest(candidates []string) string
	SetWeight(entityID string, weight float64) error
	GetWeight(entityID string) float64
	EntityStats(entityID string) (FairnessEntityStats, bool)
	Stats() FairnessStats
	Close() error
	RemoveEntity(entityID string)
}
