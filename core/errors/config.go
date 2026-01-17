package errors

type ErrorClassifierConfig struct {
	TransientPatterns   []string `yaml:"transient_patterns"`
	PermanentPatterns   []string `yaml:"permanent_patterns"`
	UserFixablePatterns []string `yaml:"user_fixable_patterns"`
	RateLimitStatuses   []int    `yaml:"rate_limit_statuses"`
	DegradingStatuses   []int    `yaml:"degrading_statuses"`
}

func DefaultErrorClassifierConfig() *ErrorClassifierConfig {
	return &ErrorClassifierConfig{
		TransientPatterns: []string{
			`(?i)timeout`,
			`(?i)temporary`,
			`(?i)connection reset`,
			`(?i)connection refused`,
			`(?i)eof`,
			`(?i)broken pipe`,
		},
		PermanentPatterns: []string{
			`(?i)not found`,
			`(?i)invalid`,
			`(?i)malformed`,
			`(?i)unsupported`,
		},
		UserFixablePatterns: []string{
			`(?i)missing.*config`,
			`(?i)invalid.*credential`,
			`(?i)api.*key.*required`,
			`(?i)authentication.*failed`,
		},
		RateLimitStatuses: []int{429},
		DegradingStatuses: []int{500, 502, 503, 504},
	}
}
