package errors

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type ErrorClassifier struct {
	mu              sync.RWMutex
	transientPats   []*regexp.Regexp
	permanentPats   []*regexp.Regexp
	userFixablePats []*regexp.Regexp
	rateLimitCodes  map[int]struct{}
	degradingCodes  map[int]struct{}
}

func NewErrorClassifier() *ErrorClassifier {
	return &ErrorClassifier{
		transientPats:   make([]*regexp.Regexp, 0),
		permanentPats:   make([]*regexp.Regexp, 0),
		userFixablePats: make([]*regexp.Regexp, 0),
		rateLimitCodes: map[int]struct{}{
			http.StatusTooManyRequests: {},
		},
		degradingCodes: map[int]struct{}{
			http.StatusInternalServerError: {},
			http.StatusBadGateway:          {},
			http.StatusServiceUnavailable:  {},
			http.StatusGatewayTimeout:      {},
		},
	}
}

func NewErrorClassifierFromConfig(cfg *ErrorClassifierConfig) (*ErrorClassifier, error) {
	c := NewErrorClassifier()
	if err := c.loadAllPatterns(cfg); err != nil {
		return nil, err
	}
	c.loadStatusCodes(cfg)
	return c, nil
}

type patternLoadSpec struct {
	patterns []string
	target   *[]*regexp.Regexp
	name     string
}

func (c *ErrorClassifier) loadAllPatterns(cfg *ErrorClassifierConfig) error {
	specs := []patternLoadSpec{
		{cfg.TransientPatterns, &c.transientPats, "transient"},
		{cfg.PermanentPatterns, &c.permanentPats, "permanent"},
		{cfg.UserFixablePatterns, &c.userFixablePats, "user-fixable"},
	}
	for _, spec := range specs {
		if err := c.loadPatterns(spec.patterns, spec.target); err != nil {
			return WrapWithTier(TierPermanent, "invalid "+spec.name+" pattern", err)
		}
	}
	return nil
}

func (c *ErrorClassifier) loadStatusCodes(cfg *ErrorClassifierConfig) {
	if len(cfg.RateLimitStatuses) > 0 {
		c.rateLimitCodes = intSliceToSet(cfg.RateLimitStatuses)
	}
	if len(cfg.DegradingStatuses) > 0 {
		c.degradingCodes = intSliceToSet(cfg.DegradingStatuses)
	}
}

func intSliceToSet(codes []int) map[int]struct{} {
	set := make(map[int]struct{}, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	return set
}

func (c *ErrorClassifier) loadPatterns(patterns []string, target *[]*regexp.Regexp) error {
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return err
		}
		*target = append(*target, re)
	}
	return nil
}

func (c *ErrorClassifier) Classify(err error) ErrorTier {
	if err == nil {
		return TierPermanent
	}

	if tier, ok := c.extractExistingTier(err); ok {
		return tier
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.classifyByContent(err.Error())
}

func (c *ErrorClassifier) extractExistingTier(err error) (ErrorTier, bool) {
	var te *TieredError
	if errors.As(err, &te) {
		return te.Tier, true
	}
	return TierPermanent, false
}

func (c *ErrorClassifier) classifyByContent(errStr string) ErrorTier {
	if tier, ok := c.classifyByHTTPStatus(errStr); ok {
		return tier
	}
	if tier, ok := c.classifyByPatterns(errStr); ok {
		return tier
	}
	return TierPermanent
}

func (c *ErrorClassifier) classifyByHTTPStatus(errStr string) (ErrorTier, bool) {
	if c.isRateLimitError(errStr) {
		return TierExternalRateLimit, true
	}
	if c.isDegradingError(errStr) {
		return TierExternalDegrading, true
	}
	return TierPermanent, false
}

type patternTierPair struct {
	patterns *[]*regexp.Regexp
	tier     ErrorTier
}

func (c *ErrorClassifier) classifyByPatterns(errStr string) (ErrorTier, bool) {
	pairs := []patternTierPair{
		{&c.transientPats, TierTransient},
		{&c.userFixablePats, TierUserFixable},
		{&c.permanentPats, TierPermanent},
	}
	for _, pair := range pairs {
		if c.matchesPatterns(errStr, *pair.patterns) {
			return pair.tier, true
		}
	}
	if c.matchesTransientKeywords(errStr) {
		return TierTransient, true
	}
	return TierPermanent, false
}

func (c *ErrorClassifier) isRateLimitError(errStr string) bool {
	lower := strings.ToLower(errStr)
	if strings.Contains(lower, "rate limit") {
		return true
	}
	if strings.Contains(lower, "too many requests") {
		return true
	}
	return c.containsAnyStatusCode(errStr, c.rateLimitCodes)
}

func (c *ErrorClassifier) isDegradingError(errStr string) bool {
	return c.containsAnyStatusCode(errStr, c.degradingCodes)
}

func (c *ErrorClassifier) containsAnyStatusCode(errStr string, codes map[int]struct{}) bool {
	for code := range codes {
		if strings.Contains(errStr, strconv.Itoa(code)) {
			return true
		}
	}
	return false
}

func (c *ErrorClassifier) matchesPatterns(errStr string, patterns []*regexp.Regexp) bool {
	for _, p := range patterns {
		if p.MatchString(errStr) {
			return true
		}
	}
	return false
}

var transientKeywords = []string{
	"timeout",
	"temporary",
	"connection reset",
	"connection refused",
	"eof",
	"broken pipe",
	"network unreachable",
	"no route to host",
}

func (c *ErrorClassifier) matchesTransientKeywords(errStr string) bool {
	lower := strings.ToLower(errStr)
	for _, kw := range transientKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func (c *ErrorClassifier) AddTransientPattern(pattern string) error {
	return c.addPattern(pattern, &c.transientPats)
}

func (c *ErrorClassifier) AddPermanentPattern(pattern string) error {
	return c.addPattern(pattern, &c.permanentPats)
}

func (c *ErrorClassifier) AddUserFixablePattern(pattern string) error {
	return c.addPattern(pattern, &c.userFixablePats)
}

func (c *ErrorClassifier) addPattern(pattern string, target *[]*regexp.Regexp) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	c.mu.Lock()
	*target = append(*target, re)
	c.mu.Unlock()
	return nil
}
