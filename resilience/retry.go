package resilience

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// RetryConfig defines retry behavior configuration
type RetryConfig struct {
	MaxAttempts     int           // Maximum number of retry attempts
	InitialDelay    time.Duration // Initial delay between retries
	MaxDelay        time.Duration // Maximum delay between retries
	Multiplier      float64       // Multiplier for exponential backoff
	RandomizeFactor float64       // Randomization factor for jitter (0-1)
	RetryIf         func(error) bool // Function to determine if error is retryable
}

// DefaultRetryConfig returns a default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:     3,
		InitialDelay:    100 * time.Millisecond,
		MaxDelay:        10 * time.Second,
		Multiplier:      2.0,
		RandomizeFactor: 0.1,
		RetryIf:         IsRetryable,
	}
}

// RetryWithConfig executes a function with retry logic based on config
func RetryWithConfig(ctx context.Context, config *RetryConfig, fn func() error) error {
	if config == nil {
		config = DefaultRetryConfig()
	}

	var lastErr error
	delay := config.InitialDelay

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Check context cancellation
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Execute the function
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err

			// Check if error is retryable
			if config.RetryIf != nil && !config.RetryIf(err) {
				return err
			}
		}

		// Don't delay after the last attempt
		if attempt < config.MaxAttempts-1 {
			// Apply jitter to delay
			jitteredDelay := applyJitter(delay, config.RandomizeFactor)

			// Wait with context cancellation support
			select {
			case <-time.After(jitteredDelay):
			case <-ctx.Done():
				return ctx.Err()
			}

			// Calculate next delay with exponential backoff
			delay = time.Duration(float64(delay) * config.Multiplier)
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}
		}
	}

	return ErrMaxRetriesExceeded{
		Attempts: config.MaxAttempts,
		LastErr:  lastErr,
	}
}

// Retry executes a function with default retry logic
func Retry(ctx context.Context, fn func() error) error {
	return RetryWithConfig(ctx, DefaultRetryConfig(), fn)
}

// RetryWithBackoff executes a function with exponential backoff
func RetryWithBackoff(ctx context.Context, attempts int, delay time.Duration, fn func() error) error {
	config := &RetryConfig{
		MaxAttempts:     attempts,
		InitialDelay:    delay,
		MaxDelay:        30 * time.Second,
		Multiplier:      2.0,
		RandomizeFactor: 0.1,
		RetryIf:         IsRetryable,
	}
	return RetryWithConfig(ctx, config, fn)
}

// applyJitter adds randomization to the delay
func applyJitter(delay time.Duration, factor float64) time.Duration {
	if factor <= 0 {
		return delay
	}

	// Calculate jitter range
	jitter := float64(delay) * factor
	minDelay := float64(delay) - jitter
	maxDelay := float64(delay) + jitter

	// Generate random delay within range
	return time.Duration(minDelay + rand.Float64()*(maxDelay-minDelay))
}

// IsRetryable determines if an error should trigger a retry
func IsRetryable(err error) bool {
	// Don't retry on context cancellation
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Add more specific error checks here
	// For now, retry all other errors
	return true
}

// ErrMaxRetriesExceeded is returned when max retries are exceeded
type ErrMaxRetriesExceeded struct {
	Attempts int
	LastErr  error
}

func (e ErrMaxRetriesExceeded) Error() string {
	if e.LastErr != nil {
		return "max retries exceeded: " + e.LastErr.Error()
	}
	return "max retries exceeded"
}

func (e ErrMaxRetriesExceeded) Unwrap() error {
	return e.LastErr
}

// RetryPolicy defines an interface for custom retry policies
type RetryPolicy interface {
	NextDelay(attempt int) time.Duration
	ShouldRetry(err error, attempt int) bool
}

// ExponentialBackoffPolicy implements exponential backoff
type ExponentialBackoffPolicy struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	MaxAttempts  int
}

// NextDelay calculates the next delay
func (p *ExponentialBackoffPolicy) NextDelay(attempt int) time.Duration {
	delay := p.InitialDelay * time.Duration(math.Pow(p.Multiplier, float64(attempt)))
	if delay > p.MaxDelay {
		return p.MaxDelay
	}
	return delay
}

// ShouldRetry determines if we should retry
func (p *ExponentialBackoffPolicy) ShouldRetry(err error, attempt int) bool {
	return attempt < p.MaxAttempts && IsRetryable(err)
}

// LinearBackoffPolicy implements linear backoff
type LinearBackoffPolicy struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Increment    time.Duration
	MaxAttempts  int
}

// NextDelay calculates the next delay
func (p *LinearBackoffPolicy) NextDelay(attempt int) time.Duration {
	delay := p.InitialDelay + p.Increment*time.Duration(attempt)
	if delay > p.MaxDelay {
		return p.MaxDelay
	}
	return delay
}

// ShouldRetry determines if we should retry
func (p *LinearBackoffPolicy) ShouldRetry(err error, attempt int) bool {
	return attempt < p.MaxAttempts && IsRetryable(err)
}

// RetryWithPolicy executes a function with a custom retry policy
func RetryWithPolicy(ctx context.Context, policy RetryPolicy, fn func() error) error {
	attempt := 0
	var lastErr error

	for {
		// Check context cancellation
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Execute the function
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err

			// Check if we should retry
			if !policy.ShouldRetry(err, attempt) {
				return err
			}
		}

		// Calculate delay
		delay := policy.NextDelay(attempt)
		attempt++

		// Wait with context cancellation support
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}