package session

import (
	"context"
	"time"
)

//go:generate mockery --name=SessionManagerService --output=./mocks --outpkg=mocks
//go:generate mockery --name=SessionContextService --output=./mocks --outpkg=mocks
//go:generate mockery --name=PersisterService --output=./mocks --outpkg=mocks

type SessionManagerService interface {
	Create(ctx context.Context, cfg Config) (*Session, error)
	Get(id string) (*Session, bool)
	GetActive() (*Session, bool)
	List() []*Session
	Switch(id string) error
	Pause(id string) error
	Resume(id string) error
	Suspend(id string) error
	Restore(id string) error
	Close(id string) error
	CloseAll() error
	Shutdown() error
	Stats() ManagerStats
	LoadSessions() error
	SetPersister(p Persister)
	Subscribe(handler EventHandler) func()
	Count() int
}

type SessionContextService interface {
	Session() *Session
	ID() string
	State() State
	Context() context.Context
	Done() <-chan struct{}
	Cancel()
	WithTimeout(d time.Duration) (context.Context, context.CancelFunc)
	SetService(name string, service any)
	GetService(name string) (any, bool)
	MustGetService(name string) any
	CacheRoute(key string, value any)
	GetCachedRoute(key string) (any, bool)
	ClearRouteCache()
	AddPendingRequest(id string, request any)
	GetPendingRequest(id string) (any, bool)
	RemovePendingRequest(id string)
	PendingRequestCount() int
	GetMetadata(key string) (any, bool)
	SetMetadata(key string, value any)
	Serialize() ([]byte, error)
	Restore(data []byte) error
	ToContext(ctx context.Context) context.Context
}

type PersisterService interface {
	Save(session *Session) error
	Load(id string) (*Session, error)
	LoadAll() ([]*Session, error)
	Delete(id string) error
	List() ([]string, error)
}
