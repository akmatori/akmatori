package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	l := New(10, 20)

	if l == nil {
		t.Fatal("Expected limiter to not be nil")
	}
	if l.refillRate != 10 {
		t.Errorf("Expected refillRate 10, got %f", l.refillRate)
	}
	if l.maxTokens != 20 {
		t.Errorf("Expected maxTokens 20, got %f", l.maxTokens)
	}
	if l.tokens != 20 {
		t.Errorf("Expected initial tokens 20, got %f", l.tokens)
	}
}

func TestLimiter_Allow(t *testing.T) {
	l := New(10, 5)

	// Should be able to use all burst tokens
	for i := 0; i < 5; i++ {
		if !l.Allow() {
			t.Errorf("Expected Allow() to return true on attempt %d", i)
		}
	}

	// Next attempt should fail (no tokens left)
	if l.Allow() {
		t.Error("Expected Allow() to return false when no tokens left")
	}
}

func TestLimiter_AllowWithRefill(t *testing.T) {
	l := New(100, 1) // 100 tokens/second, burst of 1

	// Use the token
	if !l.Allow() {
		t.Error("Expected first Allow() to succeed")
	}

	// Should fail immediately
	if l.Allow() {
		t.Error("Expected second Allow() to fail")
	}

	// Wait for refill (10ms should add 1 token at 100/sec)
	time.Sleep(15 * time.Millisecond)

	// Should succeed after refill
	if !l.Allow() {
		t.Error("Expected Allow() to succeed after refill")
	}
}

func TestLimiter_Wait(t *testing.T) {
	l := New(100, 1) // 100 tokens/second

	ctx := context.Background()

	// First wait should succeed immediately
	start := time.Now()
	if err := l.Wait(ctx); err != nil {
		t.Errorf("Expected Wait() to succeed: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 10*time.Millisecond {
		t.Errorf("First Wait() took too long: %v", elapsed)
	}

	// Second wait should block briefly
	start = time.Now()
	if err := l.Wait(ctx); err != nil {
		t.Errorf("Expected Wait() to succeed: %v", err)
	}
	elapsed = time.Since(start)
	// Should wait ~10ms for 1 token at 100/sec
	if elapsed < 5*time.Millisecond || elapsed > 50*time.Millisecond {
		t.Errorf("Second Wait() had unexpected duration: %v", elapsed)
	}
}

func TestLimiter_WaitContextCancelled(t *testing.T) {
	l := New(1, 1) // Very slow refill

	// Use the token
	l.Allow()

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Wait should return immediately with context error
	start := time.Now()
	err := l.Wait(ctx)
	elapsed := time.Since(start)

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got: %v", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("Wait with cancelled context took too long: %v", elapsed)
	}
}

func TestLimiter_WaitContextTimeout(t *testing.T) {
	l := New(0.1, 1) // Very slow refill (0.1 tokens/sec = 10 sec per token)

	// Use the token
	l.Allow()

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Wait should timeout
	start := time.Now()
	err := l.Wait(ctx)
	elapsed := time.Since(start)

	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded error, got: %v", err)
	}
	if elapsed < 40*time.Millisecond || elapsed > 100*time.Millisecond {
		t.Errorf("Wait timeout had unexpected duration: %v", elapsed)
	}
}

func TestLimiter_Tokens(t *testing.T) {
	l := New(10, 5)

	tokens := l.Tokens()
	if tokens != 5 {
		t.Errorf("Expected 5 tokens, got %f", tokens)
	}

	l.Allow()
	tokens = l.Tokens()
	// Allow for small refill between calls
	if tokens < 3.9 || tokens > 4.1 {
		t.Errorf("Expected ~4 tokens after Allow(), got %f", tokens)
	}
}

func TestLimiter_SetRate(t *testing.T) {
	l := New(10, 5)

	l.SetRate(100)

	if l.refillRate != 100 {
		t.Errorf("Expected refillRate 100, got %f", l.refillRate)
	}
}

func TestLimiter_SetBurst(t *testing.T) {
	l := New(10, 20)

	// Should have 20 tokens initially
	if l.tokens != 20 {
		t.Errorf("Expected 20 tokens, got %f", l.tokens)
	}

	// Reduce burst
	l.SetBurst(5)

	if l.maxTokens != 5 {
		t.Errorf("Expected maxTokens 5, got %f", l.maxTokens)
	}
	// Tokens should be capped
	if l.tokens != 5 {
		t.Errorf("Expected tokens capped to 5, got %f", l.tokens)
	}
}

func TestLimiter_ConcurrentAccess(t *testing.T) {
	l := New(1000, 100)

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				l.Allow()
				l.Tokens()
			}
		}()
	}

	wg.Wait()
	// If we get here without deadlock or panic, concurrent access is safe
}

func TestLimiter_BurstCapacity(t *testing.T) {
	l := New(10, 10) // 10 req/sec, burst of 10

	// Should be able to make burst requests quickly
	start := time.Now()
	for i := 0; i < 10; i++ {
		if !l.Allow() {
			t.Errorf("Expected burst request %d to succeed", i)
		}
	}
	elapsed := time.Since(start)

	// All burst requests should be nearly instant
	if elapsed > 10*time.Millisecond {
		t.Errorf("Burst requests took too long: %v", elapsed)
	}

	// 11th request should fail
	if l.Allow() {
		t.Error("Expected 11th request to fail")
	}
}

func TestLimiter_TokensDoNotExceedMax(t *testing.T) {
	l := New(1000, 10) // High refill rate, low max

	// Wait for potential over-refill
	time.Sleep(50 * time.Millisecond)

	tokens := l.Tokens()
	if tokens > 10 {
		t.Errorf("Tokens exceeded max: %f > 10", tokens)
	}
}

func TestLimiter_ZeroRateDoesNotPanic(t *testing.T) {
	l := New(0, 5)

	// Should work but never refill
	for i := 0; i < 5; i++ {
		if !l.Allow() {
			t.Errorf("Expected Allow() to succeed with burst tokens, attempt %d", i)
		}
	}

	// Should fail and not panic
	if l.Allow() {
		t.Error("Expected Allow() to fail with zero rate and no tokens")
	}
}

func TestLimiter_WaitMultipleRequests(t *testing.T) {
	l := New(100, 2) // 100/sec with burst of 2

	ctx := context.Background()
	var wg sync.WaitGroup

	// Start 5 requests concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := l.Wait(ctx)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()
	// All requests should complete (some after waiting)
}

func TestLimiter_RefillAccuracy(t *testing.T) {
	l := New(10, 5) // Start with 5 tokens, 10/sec refill

	// Consume all tokens
	for i := 0; i < 5; i++ {
		l.Allow()
	}

	// Verify tokens are exhausted
	if l.Allow() {
		t.Error("Expected tokens to be exhausted")
	}

	// Wait 100ms (should add ~1 token at 10/sec)
	time.Sleep(100 * time.Millisecond)

	tokens := l.Tokens()
	// Allow some tolerance for timing
	if tokens < 0.8 || tokens > 1.5 {
		t.Errorf("Expected ~1 token after 100ms at 10/sec, got %f", tokens)
	}
}
