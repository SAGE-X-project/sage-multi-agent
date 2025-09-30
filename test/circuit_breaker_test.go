package test

import (
	"errors"
	"testing"
	"time"

	"github.com/sage-x-project/sage-multi-agent/resilience"
)

// TestCircuitBreakerStates tests circuit breaker state transitions
func TestCircuitBreakerStates(t *testing.T) {
	cb := resilience.NewCircuitBreaker(3, 1*time.Second)

	// Initially should be closed
	if cb.GetState() != resilience.StateClosed {
		t.Errorf("Expected initial state to be closed, got %s", cb.GetState())
	}

	// Execute successful operations
	for i := 0; i < 2; i++ {
		err := cb.Execute(func() error {
			return nil
		})
		if err != nil {
			t.Errorf("Expected successful execution, got error: %v", err)
		}
	}

	// State should still be closed
	if cb.GetState() != resilience.StateClosed {
		t.Errorf("Expected state to remain closed after successful calls")
	}
}

// TestCircuitBreakerOpening tests circuit breaker opening after failures
func TestCircuitBreakerOpening(t *testing.T) {
	cb := resilience.NewCircuitBreaker(3, 1*time.Second)

	testError := errors.New("test failure")

	// Execute failing operations
	for i := 0; i < 3; i++ {
		cb.Execute(func() error {
			return testError
		})
	}

	// Circuit should be open now
	if cb.GetState() != resilience.StateOpen {
		t.Errorf("Expected circuit to be open after 3 failures, got %s", cb.GetState())
	}

	// Further calls should fail immediately with ErrCircuitOpen
	err := cb.Execute(func() error {
		return nil
	})
	if !errors.Is(err, resilience.ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

// TestCircuitBreakerHalfOpen tests half-open state and recovery
func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := resilience.NewCircuitBreaker(2, 100*time.Millisecond)

	testError := errors.New("test failure")

	// Trigger circuit opening
	for i := 0; i < 2; i++ {
		cb.Execute(func() error {
			return testError
		})
	}

	if cb.GetState() != resilience.StateOpen {
		t.Errorf("Expected circuit to be open")
	}

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Next call should transition to half-open
	err := cb.Execute(func() error {
		return nil
	})

	// After successful call in half-open, should transition to closed
	if cb.GetState() != resilience.StateClosed {
		t.Errorf("Expected circuit to be closed after successful half-open call, got %s", cb.GetState())
	}

	if err != nil {
		t.Errorf("Expected successful execution, got error: %v", err)
	}
}

// TestCircuitBreakerFailureCount tests failure counting
func TestCircuitBreakerFailureCount(t *testing.T) {
	cb := resilience.NewCircuitBreaker(5, 1*time.Second)

	testError := errors.New("test failure")

	// Execute some failures
	for i := 0; i < 3; i++ {
		cb.Execute(func() error {
			return testError
		})
	}

	if cb.GetFailures() != 3 {
		t.Errorf("Expected 3 failures, got %d", cb.GetFailures())
	}

	// Execute a success
	cb.Execute(func() error {
		return nil
	})

	// Failures should reset
	if cb.GetFailures() != 0 {
		t.Errorf("Expected failures to reset after success, got %d", cb.GetFailures())
	}
}

// TestCircuitBreakerStateChangeCallback tests state change callbacks
func TestCircuitBreakerStateChangeCallback(t *testing.T) {
	cb := resilience.NewCircuitBreaker(2, 100*time.Millisecond)

	stateChanges := make([]string, 0)
	cb.SetOnStateChange(func(from, to resilience.State) {
		stateChanges = append(stateChanges, from.String()+"->"+to.String())
	})

	testError := errors.New("test failure")

	// Trigger opening
	for i := 0; i < 2; i++ {
		cb.Execute(func() error {
			return testError
		})
	}

	// Wait for callback
	time.Sleep(50 * time.Millisecond)

	if len(stateChanges) == 0 {
		t.Errorf("Expected state change callback to be called")
	}
}

// TestCircuitBreakerReset tests manual reset
func TestCircuitBreakerReset(t *testing.T) {
	cb := resilience.NewCircuitBreaker(2, 1*time.Second)

	testError := errors.New("test failure")

	// Trigger opening
	for i := 0; i < 2; i++ {
		cb.Execute(func() error {
			return testError
		})
	}

	if cb.GetState() != resilience.StateOpen {
		t.Errorf("Expected circuit to be open")
	}

	// Manual reset
	cb.Reset()

	if cb.GetState() != resilience.StateClosed {
		t.Errorf("Expected circuit to be closed after reset, got %s", cb.GetState())
	}

	if cb.GetFailures() != 0 {
		t.Errorf("Expected failures to be reset, got %d", cb.GetFailures())
	}
}

// TestCircuitBreakerTrip tests manual trip
func TestCircuitBreakerTrip(t *testing.T) {
	cb := resilience.NewCircuitBreaker(5, 1*time.Second)

	if cb.GetState() != resilience.StateClosed {
		t.Errorf("Expected initial state to be closed")
	}

	// Manual trip
	cb.Trip()

	if cb.GetState() != resilience.StateOpen {
		t.Errorf("Expected circuit to be open after trip, got %s", cb.GetState())
	}

	// Should fail immediately
	err := cb.Execute(func() error {
		return nil
	})
	if !errors.Is(err, resilience.ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

// TestCircuitBreakerHalfOpenLimits tests half-open request limits
func TestCircuitBreakerHalfOpenLimits(t *testing.T) {
	cb := resilience.NewCircuitBreaker(2, 100*time.Millisecond)
	cb.SetHalfOpenRequests(1)

	testError := errors.New("test failure")

	// Trigger opening
	for i := 0; i < 2; i++ {
		cb.Execute(func() error {
			return testError
		})
	}

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// First call in half-open should be allowed
	cb.Execute(func() error {
		return testError
	})

	// Circuit should be open again
	if cb.GetState() != resilience.StateOpen {
		t.Errorf("Expected circuit to be open after failed half-open attempt")
	}
}