package toolruntime

import (
	"sort"
	"strings"
	"sync"
)

type State struct {
	mu     sync.RWMutex
	active map[string]struct{}
}

func NewState() *State {
	return &State{
		active: make(map[string]struct{}),
	}
}

func (s *State) Activate(names ...string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	activated := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := s.active[name]; ok {
			continue
		}
		s.active[name] = struct{}{}
		activated = append(activated, name)
	}
	sort.Strings(activated)
	return activated
}

func (s *State) IsActive(name string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.active[strings.TrimSpace(name)]
	return ok
}

func (s *State) Snapshot() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.active))
	for name := range s.active {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
