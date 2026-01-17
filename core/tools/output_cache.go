package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type ToolResult struct {
	ExitCode   int
	Stdout     []byte
	Stderr     []byte
	Duration   time.Duration
	Killed     bool
	KillSignal string
	Partial    bool
}

type CachePolicy struct {
	Cacheable    bool
	TTL          time.Duration
	InvalidateOn []string
}

type ToolCacheConfig struct {
	MaxEntries     int
	MaxSize        int64
	TTL            time.Duration
	CacheableTools map[string]CachePolicy
}

func DefaultToolCacheConfig() ToolCacheConfig {
	return ToolCacheConfig{
		MaxEntries: 1000,
		MaxSize:    100 * 1024 * 1024,
		TTL:        5 * time.Minute,
		CacheableTools: map[string]CachePolicy{
			"eslint":   {Cacheable: true, TTL: 5 * time.Minute, InvalidateOn: []string{"*.js", "*.ts"}},
			"prettier": {Cacheable: true, TTL: 5 * time.Minute, InvalidateOn: []string{"*"}},
			"go vet":   {Cacheable: true, TTL: 5 * time.Minute, InvalidateOn: []string{"*.go"}},
		},
	}
}

type CachedToolOutput struct {
	Tool      string
	InputHash string
	Output    *ToolResult
	CachedAt  time.Time
	HitCount  int
	Size      int64
}

type ToolOutputCache struct {
	cache     map[string]*CachedToolOutput
	config    ToolCacheConfig
	mu        sync.RWMutex
	totalSize int64
}

func NewToolOutputCache(config ToolCacheConfig) *ToolOutputCache {
	return &ToolOutputCache{
		cache:  make(map[string]*CachedToolOutput),
		config: config,
	}
}

func (c *ToolOutputCache) Get(tool, inputHash string) (*ToolResult, bool) {
	c.mu.RLock()
	key := tool + ":" + inputHash
	cached, ok := c.cache[key]
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}

	ttl := c.getTTL(tool)
	if time.Since(cached.CachedAt) >= ttl {
		c.mu.RUnlock()
		c.evict(key)
		return nil, false
	}

	cached.HitCount++
	c.mu.RUnlock()
	return cached.Output, true
}

func (c *ToolOutputCache) getTTL(tool string) time.Duration {
	if policy, ok := c.config.CacheableTools[tool]; ok && policy.TTL > 0 {
		return policy.TTL
	}
	return c.config.TTL
}

func (c *ToolOutputCache) Put(tool, inputHash string, result *ToolResult) {
	if !c.isCacheable(tool) {
		return
	}

	size := c.calculateSize(result)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictIfNeeded(size)

	key := tool + ":" + inputHash
	c.cache[key] = &CachedToolOutput{
		Tool:      tool,
		InputHash: inputHash,
		Output:    result,
		CachedAt:  time.Now(),
		Size:      size,
	}
	c.totalSize += size
}

func (c *ToolOutputCache) isCacheable(tool string) bool {
	policy, ok := c.config.CacheableTools[tool]
	return ok && policy.Cacheable
}

func (c *ToolOutputCache) calculateSize(result *ToolResult) int64 {
	if result == nil {
		return 0
	}
	return int64(len(result.Stdout) + len(result.Stderr))
}

func (c *ToolOutputCache) evictIfNeeded(newSize int64) {
	for len(c.cache) >= c.config.MaxEntries || c.totalSize+newSize > c.config.MaxSize {
		if !c.evictOldest() {
			break
		}
	}
}

func (c *ToolOutputCache) findOldestKey() string {
	var oldestKey string
	var oldestTime time.Time

	for key, cached := range c.cache {
		if oldestKey == "" || cached.CachedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = cached.CachedAt
		}
	}
	return oldestKey
}

func (c *ToolOutputCache) evictOldest() bool {
	oldestKey := c.findOldestKey()
	if oldestKey == "" {
		return false
	}

	c.totalSize -= c.cache[oldestKey].Size
	delete(c.cache, oldestKey)
	return true
}

func (c *ToolOutputCache) evict(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.cache[key]; ok {
		c.totalSize -= cached.Size
		delete(c.cache, key)
	}
}

func (c *ToolOutputCache) Invalidate(tool string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, cached := range c.cache {
		if cached.Tool == tool {
			c.totalSize -= cached.Size
			delete(c.cache, key)
		}
	}
}

func (c *ToolOutputCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*CachedToolOutput)
	c.totalSize = 0
}

func (c *ToolOutputCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

func (c *ToolOutputCache) TotalBytes() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalSize
}

type ToolInvocation struct {
	Command    string
	Args       []string
	WorkingDir string
	Env        map[string]string
	Stdin      interface{}
	Timeout    time.Duration
}

func (c *ToolOutputCache) ComputeInputHash(inv ToolInvocation) string {
	h := sha256.New()
	h.Write([]byte(inv.Command))
	for _, arg := range inv.Args {
		h.Write([]byte(arg))
	}
	h.Write([]byte(inv.WorkingDir))
	return hex.EncodeToString(h.Sum(nil))
}
