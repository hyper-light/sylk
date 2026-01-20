package errors

import (
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type patternSet struct {
	transient   []*regexp.Regexp
	permanent   []*regexp.Regexp
	userFixable []*regexp.Regexp
}

// emptyPatternSet is the default pattern set used when none is configured.
var emptyPatternSet = &patternSet{
	transient:   make([]*regexp.Regexp, 0),
	permanent:   make([]*regexp.Regexp, 0),
	userFixable: make([]*regexp.Regexp, 0),
}

// ErrorClassifier classifies errors by tier using pattern matching and HTTP status codes.
type ErrorClassifier struct {
	patterns       atomic.Value // stores *patternSet
	mu             sync.Mutex
	rateLimitCodes []int
	degradingCodes []int
}

// NewErrorClassifier creates a new ErrorClassifier with default configuration.
func NewErrorClassifier() *ErrorClassifier {
	ps := &patternSet{
		transient:   make([]*regexp.Regexp, 0),
		permanent:   make([]*regexp.Regexp, 0),
		userFixable: make([]*regexp.Regexp, 0),
	}

	c := &ErrorClassifier{
		rateLimitCodes: []int{http.StatusTooManyRequests},
		degradingCodes: []int{
			http.StatusBadGateway,
			http.StatusGatewayTimeout,
			http.StatusInternalServerError,
			http.StatusServiceUnavailable,
		},
	}
	c.patterns.Store(ps)

	sort.Ints(c.rateLimitCodes)
	sort.Ints(c.degradingCodes)

	return c
}

func NewErrorClassifierFromConfig(cfg *ErrorClassifierConfig) (*ErrorClassifier, error) {
	c := NewErrorClassifier()
	if err := c.loadAllPatterns(cfg); err != nil {
		return nil, err
	}
	c.loadStatusCodes(cfg)
	return c, nil
}

func (c *ErrorClassifier) loadAllPatterns(cfg *ErrorClassifierConfig) error {
	ps := c.loadPatterns()

	transient, err := compilePatterns(cfg.TransientPatterns)
	if err != nil {
		return WrapWithTier(TierPermanent, "invalid transient pattern", err)
	}
	permanent, err := compilePatterns(cfg.PermanentPatterns)
	if err != nil {
		return WrapWithTier(TierPermanent, "invalid permanent pattern", err)
	}
	userFixable, err := compilePatterns(cfg.UserFixablePatterns)
	if err != nil {
		return WrapWithTier(TierPermanent, "invalid user-fixable pattern", err)
	}

	newPS := &patternSet{
		transient:   append(ps.transient, transient...),
		permanent:   append(ps.permanent, permanent...),
		userFixable: append(ps.userFixable, userFixable...),
	}
	c.patterns.Store(newPS)
	return nil
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		result = append(result, re)
	}
	return result, nil
}

func (c *ErrorClassifier) loadStatusCodes(cfg *ErrorClassifierConfig) {
	if len(cfg.RateLimitStatuses) > 0 {
		c.rateLimitCodes = sortedCopy(cfg.RateLimitStatuses)
	}
	if len(cfg.DegradingStatuses) > 0 {
		c.degradingCodes = sortedCopy(cfg.DegradingStatuses)
	}
}

func sortedCopy(codes []int) []int {
	result := make([]int, len(codes))
	copy(result, codes)
	sort.Ints(result)
	return result
}

// loadPatterns safely loads the current pattern set.
// Returns emptyPatternSet if no patterns have been configured.
func (c *ErrorClassifier) loadPatterns() *patternSet {
	if ps, ok := c.patterns.Load().(*patternSet); ok && ps != nil {
		return ps
	}
	return emptyPatternSet
}

func (c *ErrorClassifier) Classify(err error) ErrorTier {
	if err == nil {
		return TierPermanent
	}

	if tier, ok := c.extractExistingTier(err); ok {
		return tier
	}

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

func (c *ErrorClassifier) classifyByPatterns(errStr string) (ErrorTier, bool) {
	ps := c.loadPatterns()

	if matchesPatterns(errStr, ps.transient) {
		return TierTransient, true
	}
	if matchesPatterns(errStr, ps.userFixable) {
		return TierUserFixable, true
	}
	if matchesPatterns(errStr, ps.permanent) {
		return TierPermanent, true
	}
	if matchesTransientKeywords(errStr) {
		return TierTransient, true
	}
	return TierPermanent, false
}

func (c *ErrorClassifier) isRateLimitError(errStr string) bool {
	lower := strings.ToLower(errStr)
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") {
		return true
	}
	return containsAnyStatusCode(errStr, c.rateLimitCodes)
}

func (c *ErrorClassifier) isDegradingError(errStr string) bool {
	return containsAnyStatusCode(errStr, c.degradingCodes)
}

func containsAnyStatusCode(errStr string, codes []int) bool {
	for _, code := range codes {
		if strings.Contains(errStr, strconv.Itoa(code)) {
			return true
		}
	}
	return false
}

func matchesPatterns(errStr string, patterns []*regexp.Regexp) bool {
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

func matchesTransientKeywords(errStr string) bool {
	lower := strings.ToLower(errStr)
	for _, kw := range transientKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func (c *ErrorClassifier) AddTransientPattern(pattern string) error {
	return c.addPatternTo(pattern, "transient")
}

func (c *ErrorClassifier) AddPermanentPattern(pattern string) error {
	return c.addPatternTo(pattern, "permanent")
}

func (c *ErrorClassifier) AddUserFixablePattern(pattern string) error {
	return c.addPatternTo(pattern, "userFixable")
}

func (c *ErrorClassifier) addPatternTo(pattern string, target string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	ps := c.loadPatterns()
	newPS := &patternSet{
		transient:   ps.transient,
		permanent:   ps.permanent,
		userFixable: ps.userFixable,
	}

	switch target {
	case "transient":
		newPS.transient = append(newPS.transient[:len(newPS.transient):len(newPS.transient)], re)
	case "permanent":
		newPS.permanent = append(newPS.permanent[:len(newPS.permanent):len(newPS.permanent)], re)
	case "userFixable":
		newPS.userFixable = append(newPS.userFixable[:len(newPS.userFixable):len(newPS.userFixable)], re)
	}

	c.patterns.Store(newPS)
	return nil
}
