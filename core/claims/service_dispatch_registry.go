package claims

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrServiceDispatcherRegistryInvalid   = errors.New("service dispatcher registry invalid")
	ErrServiceDispatcherAlreadyRegistered = errors.New("service dispatcher already registered")
)

type ServiceDispatcherRegistry struct {
	mu          sync.RWMutex
	dispatchers map[string]*ServiceDispatcher
}

func NewServiceDispatcherRegistry() *ServiceDispatcherRegistry {
	return &ServiceDispatcherRegistry{dispatchers: make(map[string]*ServiceDispatcher)}
}

func (r *ServiceDispatcherRegistry) Register(sessionID string, dispatcher *ServiceDispatcher) error {
	if r == nil {
		return fmt.Errorf("%w: registry is nil", ErrServiceDispatcherRegistryInvalid)
	}
	key, err := serviceDispatcherRegistryKey(sessionID, dispatcher)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.dispatchers[key]; exists {
		return ErrServiceDispatcherAlreadyRegistered
	}
	r.dispatchers[key] = dispatcher
	return nil
}

func (r *ServiceDispatcherRegistry) ReplaceForReason(sessionID string, dispatcher *ServiceDispatcher, reason string) error {
	if r == nil {
		return fmt.Errorf("%w: registry is nil", ErrServiceDispatcherRegistryInvalid)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: replacement reason is required", ErrServiceDispatcherRegistryInvalid)
	}
	key, err := serviceDispatcherRegistryKey(sessionID, dispatcher)
	if err != nil {
		return err
	}
	old := r.swap(key, dispatcher)
	if old != nil && old != dispatcher {
		return old.Close()
	}
	return nil
}

func (r *ServiceDispatcherRegistry) Lookup(sessionID, participantUID string) (*ServiceDispatcher, bool) {
	if r == nil {
		return nil, false
	}
	key := strings.TrimSpace(sessionID) + "|" + strings.TrimSpace(participantUID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	dispatcher, ok := r.dispatchers[key]
	return dispatcher, ok
}

func (r *ServiceDispatcherRegistry) Remove(sessionID, participantUID string) {
	if r == nil {
		return
	}
	key := strings.TrimSpace(sessionID) + "|" + strings.TrimSpace(participantUID)
	r.mu.Lock()
	delete(r.dispatchers, key)
	r.mu.Unlock()
}

func (r *ServiceDispatcherRegistry) CloseSession(sessionID string) error {
	if r == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	dispatchers := r.removeSession(sessionID)
	var out error
	for _, dispatcher := range dispatchers {
		out = errors.Join(out, dispatcher.Close())
	}
	return out
}

func (r *ServiceDispatcherRegistry) SessionParticipantUIDs(sessionID string) []string {
	if r == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	prefix := sessionID + "|"
	r.mu.RLock()
	defer r.mu.RUnlock()
	uids := make([]string, 0)
	for key := range r.dispatchers {
		uid, ok := strings.CutPrefix(key, prefix)
		if ok {
			uids = append(uids, uid)
		}
	}
	sort.Strings(uids)
	return uids
}

func (r *ServiceDispatcherRegistry) swap(key string, dispatcher *ServiceDispatcher) *ServiceDispatcher {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.dispatchers[key]
	r.dispatchers[key] = dispatcher
	return old
}

func (r *ServiceDispatcherRegistry) removeSession(sessionID string) []*ServiceDispatcher {
	prefix := sessionID + "|"
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*ServiceDispatcher, 0)
	for key, dispatcher := range r.dispatchers {
		if strings.HasPrefix(key, prefix) {
			out = append(out, dispatcher)
			delete(r.dispatchers, key)
		}
	}
	return out
}

func serviceDispatcherRegistryKey(sessionID string, dispatcher *ServiceDispatcher) (string, error) {
	if dispatcher == nil {
		return "", fmt.Errorf("%w: dispatcher is nil", ErrServiceDispatcherRegistryInvalid)
	}
	sessionID = firstNonEmpty(strings.TrimSpace(sessionID), dispatcher.sessionID)
	if sessionID == "" || strings.TrimSpace(dispatcher.participant.UID) == "" {
		return "", fmt.Errorf("%w: session id and participant uid are required", ErrServiceDispatcherRegistryInvalid)
	}
	return sessionID + "|" + strings.TrimSpace(dispatcher.participant.UID), nil
}
