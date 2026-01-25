package embedder

import (
	"context"
	"time"
)

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimension() int
}

type Config struct {
	Model          string
	Dimension      int
	BatchSize      int
	Timeout        time.Duration
	TokensPerMin   int
	RequestsPerMin int
}

func DefaultConfig() Config {
	return Config{
		Model:          string(VoyageCode3),
		Dimension:      voyageCode3Dim,
		BatchSize:      voyageDefaultBatchSize,
		Timeout:        voyageDefaultTimeout,
		TokensPerMin:   voyageTokensPerMin,
		RequestsPerMin: voyageRequestsPerMin,
	}
}

type RateLimiter struct {
	tokensPerMin   int
	requestsPerMin int
	tokenBucket    chan struct{}
	requestBucket  chan struct{}
	done           chan struct{}
}

func NewRateLimiter(cfg Config) *RateLimiter {
	rl := &RateLimiter{
		tokensPerMin:   cfg.TokensPerMin,
		requestsPerMin: cfg.RequestsPerMin,
		tokenBucket:    make(chan struct{}, cfg.TokensPerMin),
		requestBucket:  make(chan struct{}, cfg.RequestsPerMin),
		done:           make(chan struct{}),
	}

	for range cfg.TokensPerMin {
		rl.tokenBucket <- struct{}{}
	}
	for range cfg.RequestsPerMin {
		rl.requestBucket <- struct{}{}
	}

	go rl.refillLoop()
	return rl
}

func (rl *RateLimiter) refillLoop() {
	tokenTicker := time.NewTicker(time.Minute / time.Duration(rl.tokensPerMin))
	requestTicker := time.NewTicker(time.Minute / time.Duration(rl.requestsPerMin))
	defer tokenTicker.Stop()
	defer requestTicker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-tokenTicker.C:
			select {
			case rl.tokenBucket <- struct{}{}:
			default:
			}
		case <-requestTicker.C:
			select {
			case rl.requestBucket <- struct{}{}:
			default:
			}
		}
	}
}

func (rl *RateLimiter) Acquire(ctx context.Context, tokens int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rl.requestBucket:
	}

	for range tokens {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-rl.tokenBucket:
		}
	}
	return nil
}

func (rl *RateLimiter) Close() {
	close(rl.done)
}
