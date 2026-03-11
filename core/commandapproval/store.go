package commandapproval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type RuleStore struct {
	mu    sync.RWMutex
	path  string
	rules map[string]Rule
}

type ruleFile struct {
	Rules []Rule `yaml:"rules"`
}

func NewRuleStore(path string) *RuleStore {
	store := &RuleStore{
		path:  stringsTrimSpace(path),
		rules: make(map[string]Rule),
	}
	store.load()
	return store
}

func (s *RuleStore) Lookup(matchKey string) (Rule, bool) {
	if s == nil {
		return Rule{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rule, ok := s.rules[stringsTrimSpace(matchKey)]
	return rule, ok
}

func (s *RuleStore) Record(rule Rule) error {
	if s == nil {
		return nil
	}
	key := stringsTrimSpace(rule.MatchKey)
	if key == "" {
		return fmt.Errorf("command approval rule key is required")
	}
	rule.MatchKey = key
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.rules[key] = rule
	s.mu.Unlock()
	return s.save()
}

func (s *RuleStore) Rules() []Rule {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rules := make([]Rule, 0, len(s.rules))
	for _, rule := range s.rules {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].MatchKey < rules[j].MatchKey
	})
	return rules
}

func (s *RuleStore) load() {
	if s == nil || s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil || len(data) == 0 {
		return
	}
	var raw ruleFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range raw.Rules {
		key := stringsTrimSpace(rule.MatchKey)
		if key == "" {
			continue
		}
		rule.MatchKey = key
		s.rules[key] = rule
	}
}

func (s *RuleStore) save() error {
	if s == nil || s.path == "" {
		return nil
	}
	rules := s.Rules()
	data, err := yaml.Marshal(&ruleFile{Rules: rules})
	if err != nil {
		return fmt.Errorf("marshal command approval rules: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return fmt.Errorf("create command approval dir: %w", err)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(s.path), ".command-approvals-*.yaml")
	if err != nil {
		return fmt.Errorf("create command approval temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write command approval temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close command approval temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace command approval rules: %w", err)
	}
	return nil
}

func stringsTrimSpace(value string) string {
	return strings.TrimSpace(value)
}
