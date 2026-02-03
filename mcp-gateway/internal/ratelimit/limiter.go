package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter implements a token bucket rate limiter
type Limiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// New creates a new rate limiter with the specified rate (requests per second)
// and burst capacity (maximum tokens that can accumulate)
func New(ratePerSecond float64, burstCapacity int) *Limiter {
	return &Limiter{
		tokens:     float64(burstCapacity),
		maxTokens:  float64(burstCapacity),
		refillRate: ratePerSecond,
		lastRefill: time.Now(),
	}
}

// refill adds tokens based on elapsed time since last refill
func (l *Limiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	l.tokens += elapsed * l.refillRate
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
	l.lastRefill = now
}

// Allow checks if a request can proceed immediately
// Returns true and consumes a token if available, false otherwise
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

// Wait blocks until a token is available or the context is cancelled
// Returns nil if a token was acquired, or the context error if cancelled
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		l.refill()

		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}

		// Calculate time until next token
		tokensNeeded := 1 - l.tokens
		waitTime := time.Duration(tokensNeeded/l.refillRate*1000) * time.Millisecond
		if waitTime < time.Millisecond {
			waitTime = time.Millisecond
		}
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Continue loop to try again
		}
	}
}

// Tokens returns the current number of available tokens (approximate)
func (l *Limiter) Tokens() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()
	return l.tokens
}

// SetRate updates the refill rate (tokens per second)
func (l *Limiter) SetRate(ratePerSecond float64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refillRate = ratePerSecond
}

// SetBurst updates the maximum burst capacity
func (l *Limiter) SetBurst(burstCapacity int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.maxTokens = float64(burstCapacity)
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
}
