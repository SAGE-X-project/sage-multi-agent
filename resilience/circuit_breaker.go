package resilience

import (
	"errors"
	"sync"
	"time"
)

// State represents the state of CircuitBreaker
type State int

const (
	// StateClosed is the normal state of the circuit breaker
	StateClosed State = iota
	// StateOpen is when the circuit breaker is open due to failures
	StateOpen
	// StateHalfOpen is when the circuit breaker is testing if the service recovered
	StateHalfOpen
)

// String returns the string representation of the state
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker prevents cascading failures
type CircuitBreaker struct {
	mu sync.RWMutex

	// Configuration
	maxFailures      int           // Maximum failures before opening
	resetTimeout     time.Duration // Time before attempting reset
	halfOpenRequests int           // Max requests in half-open state

	// State
	state            State
	failures         int
	lastFailureTime  time.Time
	halfOpenAttempts int

	// Callbacks
	onStateChange func(from, to State)
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:      maxFailures,
		resetTimeout:     resetTimeout,
		halfOpenRequests: 1,
		state:            StateClosed,
	}
}

// SetOnStateChange sets the callback for state changes
func (cb *CircuitBreaker) SetOnStateChange(fn func(from, to State)) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.onStateChange = fn
}

// SetHalfOpenRequests sets the number of requests allowed in half-open state
func (cb *CircuitBreaker) SetHalfOpenRequests(n int) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.halfOpenRequests = n
}

// Execute runs the given function if the circuit breaker allows it
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if err := cb.beforeCall(); err != nil {
		return err
	}

	err := fn()
	cb.afterCall(err)
	return err
}

// beforeCall checks if the call is allowed
func (cb *CircuitBreaker) beforeCall() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastFailureTime) > cb.resetTimeout {
			cb.changeState(StateHalfOpen)
			cb.halfOpenAttempts = 0
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		if cb.halfOpenAttempts >= cb.halfOpenRequests {
			return ErrTooManyRequests
		}
		cb.halfOpenAttempts++
		return nil

	default:
		return ErrUnknownState
	}
}

// afterCall records the result of the call
func (cb *CircuitBreaker) afterCall(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		if err != nil {
			cb.failures++
			cb.lastFailureTime = time.Now()
			if cb.failures >= cb.maxFailures {
				cb.changeState(StateOpen)
			}
		} else {
			cb.failures = 0
		}

	case StateHalfOpen:
		if err != nil {
			cb.changeState(StateOpen)
			cb.failures = 1
			cb.lastFailureTime = time.Now()
		} else {
			cb.changeState(StateClosed)
			cb.failures = 0
		}
	}
}

// changeState changes the state and calls the callback
func (cb *CircuitBreaker) changeState(to State) {
	if cb.state == to {
		return
	}

	from := cb.state
	cb.state = to

	if cb.onStateChange != nil {
		// Call callback in goroutine to avoid blocking
		go cb.onStateChange(from, to)
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetFailures returns the current failure count
func (cb *CircuitBreaker) GetFailures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// Reset manually resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.changeState(StateClosed)
	cb.failures = 0
	cb.halfOpenAttempts = 0
}

// Trip manually trips the circuit breaker
func (cb *CircuitBreaker) Trip() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.changeState(StateOpen)
	cb.lastFailureTime = time.Now()
}

// Errors
var (
	// ErrCircuitOpen is returned when the circuit is open
	ErrCircuitOpen = errors.New("circuit breaker: circuit is open")

	// ErrTooManyRequests is returned when too many requests in half-open state
	ErrTooManyRequests = errors.New("circuit breaker: too many requests in half-open state")

	// ErrUnknownState is returned when the circuit is in an unknown state
	ErrUnknownState = errors.New("circuit breaker: unknown state")
)