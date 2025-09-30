package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sage-x-project/sage-multi-agent/resilience"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// MessageRouter handles intelligent message routing between agents
type MessageRouter struct {
	mu sync.RWMutex

	// Routing table
	routes map[string]*Route

	// Message tracking
	pendingRequests map[string]*PendingRequest
	messageTimeout  time.Duration

	// Circuit breakers for each route
	circuitBreakers map[string]*resilience.CircuitBreaker

	// Metrics
	metrics *RouterMetrics
}

// Route defines a message route to an agent
type Route struct {
	AgentID     string
	AgentType   string
	Endpoint    string
	Priority    int
	HealthCheck func() error
	Active      bool
}

// PendingRequest tracks in-flight requests
type PendingRequest struct {
	RequestID   string
	Request     *types.AgentMessage
	ResponseCh  chan *types.AgentMessage
	ErrorCh     chan error
	Timestamp   time.Time
	TimeoutTime time.Time
	Retries     int
}

// RouterMetrics tracks router performance
type RouterMetrics struct {
	mu              sync.RWMutex
	TotalRequests   int64
	SuccessRequests int64
	FailedRequests  int64
	TimeoutRequests int64
	AverageLatency  time.Duration
}

// NewMessageRouter creates a new message router
func NewMessageRouter() *MessageRouter {
	return &MessageRouter{
		routes:          make(map[string]*Route),
		pendingRequests: make(map[string]*PendingRequest),
		circuitBreakers: make(map[string]*resilience.CircuitBreaker),
		messageTimeout:  30 * time.Second,
		metrics:         &RouterMetrics{},
	}
}

// RegisterRoute registers a new route
func (mr *MessageRouter) RegisterRoute(agentType string, route *Route) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	mr.routes[agentType] = route

	// Create circuit breaker for this route
	cb := resilience.NewCircuitBreaker(3, 30*time.Second)
	cb.SetOnStateChange(func(from, to resilience.State) {
		log.Printf("Circuit breaker for %s: %s -> %s", agentType, from, to)
	})
	mr.circuitBreakers[agentType] = cb

	log.Printf("Registered route for %s agent: %s", agentType, route.AgentID)
}

// RouteMessage routes a message to the appropriate agent
func (mr *MessageRouter) RouteMessage(ctx context.Context, msg *types.AgentMessage) (*types.AgentMessage, error) {
	// Determine target agent type
	agentType := mr.determineTargetAgent(msg)

	// Get route
	mr.mu.RLock()
	route, exists := mr.routes[agentType]
	cb := mr.circuitBreakers[agentType]
	mr.mu.RUnlock()

	if !exists || !route.Active {
		return nil, fmt.Errorf("no active route for agent type: %s", agentType)
	}

	// Check circuit breaker
	if cb != nil {
		return mr.executeWithCircuitBreaker(ctx, cb, route, msg)
	}

	// Direct execution without circuit breaker
	return mr.sendToAgent(ctx, route, msg)
}

// executeWithCircuitBreaker executes request with circuit breaker protection
func (mr *MessageRouter) executeWithCircuitBreaker(
	ctx context.Context,
	cb *resilience.CircuitBreaker,
	route *Route,
	msg *types.AgentMessage,
) (*types.AgentMessage, error) {
	var response *types.AgentMessage
	var routeErr error

	err := cb.Execute(func() error {
		resp, err := mr.sendToAgent(ctx, route, msg)
		if err != nil {
			routeErr = err
			return err
		}
		response = resp
		return nil
	})

	if err != nil {
		if errors.Is(err, resilience.ErrCircuitOpen) {
			// Try fallback route if available
			return mr.tryFallbackRoute(ctx, msg)
		}
		return nil, routeErr
	}

	return response, nil
}

// sendToAgent sends a message to a specific agent
func (mr *MessageRouter) sendToAgent(ctx context.Context, route *Route, msg *types.AgentMessage) (*types.AgentMessage, error) {
	// Create pending request
	pending := &PendingRequest{
		RequestID:   msg.ID,
		Request:     msg,
		ResponseCh:  make(chan *types.AgentMessage, 1),
		ErrorCh:     make(chan error, 1),
		Timestamp:   time.Now(),
		TimeoutTime: time.Now().Add(mr.messageTimeout),
	}

	// Store pending request
	mr.mu.Lock()
	mr.pendingRequests[msg.ID] = pending
	mr.mu.Unlock()

	// Clean up when done
	defer func() {
		mr.mu.Lock()
		delete(mr.pendingRequests, msg.ID)
		mr.mu.Unlock()
	}()

	// Send message with retry logic
	retryConfig := &resilience.RetryConfig{
		MaxAttempts:     3,
		InitialDelay:    100 * time.Millisecond,
		MaxDelay:        5 * time.Second,
		Multiplier:      2.0,
		RandomizeFactor: 0.1,
	}

	var response *types.AgentMessage
	err := resilience.RetryWithConfig(ctx, retryConfig, func() error {
		// Simulate sending to agent (replace with actual HTTP/gRPC call)
		resp, err := mr.mockSendToAgent(route, msg)
		if err != nil {
			return err
		}
		response = resp
		return nil
	})

	if err != nil {
		mr.recordMetrics(false, time.Since(pending.Timestamp))
		return nil, err
	}

	mr.recordMetrics(true, time.Since(pending.Timestamp))
	return response, nil
}

// mockSendToAgent simulates sending message to agent
func (mr *MessageRouter) mockSendToAgent(route *Route, msg *types.AgentMessage) (*types.AgentMessage, error) {
	// This would be replaced with actual HTTP/gRPC call
	response := &types.AgentMessage{
		ID:        fmt.Sprintf("resp_%s", msg.ID),
		From:      route.AgentID,
		To:        msg.From,
		Content:   fmt.Sprintf("Response to: %s", msg.Content),
		Type:      "response",
		Timestamp: time.Now().Unix(),
	}

	// Simulate network delay
	time.Sleep(50 * time.Millisecond)

	return response, nil
}

// tryFallbackRoute attempts to use a fallback route
func (mr *MessageRouter) tryFallbackRoute(ctx context.Context, msg *types.AgentMessage) (*types.AgentMessage, error) {
	// Look for alternative routes based on priority
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	var fallbackRoute *Route
	for _, route := range mr.routes {
		if route.Active && (fallbackRoute == nil || route.Priority > fallbackRoute.Priority) {
			fallbackRoute = route
		}
	}

	if fallbackRoute == nil {
		return nil, errors.New("no fallback route available")
	}

	log.Printf("Using fallback route: %s", fallbackRoute.AgentID)
	return mr.sendToAgent(ctx, fallbackRoute, msg)
}

// determineTargetAgent determines which agent should handle the message
func (mr *MessageRouter) determineTargetAgent(msg *types.AgentMessage) string {
	content := msg.Content

	// Simple keyword-based routing
	switch {
	case contains(content, "hotel", "accommodation", "booking", "reservation"):
		return "planning"
	case contains(content, "order", "product", "shopping", "purchase"):
		return "ordering"
	case contains(content, "payment", "pay", "transfer", "money", "coin"):
		return "payment"
	default:
		return "planning" // Default fallback
	}
}

// contains checks if any of the keywords exist in the text
func contains(text string, keywords ...string) bool {
	for _, keyword := range keywords {
		if containsIgnoreCase(text, keyword) {
			return true
		}
	}
	return false
}

// containsIgnoreCase checks if keyword exists in text (case-insensitive)
func containsIgnoreCase(text, keyword string) bool {
	return len(text) >= len(keyword) &&
		   containsString(toLower(text), toLower(keyword))
}

// Helper functions for string operations
func toLower(s string) string {
	// Simple lowercase conversion
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func containsString(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// recordMetrics records routing metrics
func (mr *MessageRouter) recordMetrics(success bool, latency time.Duration) {
	mr.metrics.mu.Lock()
	defer mr.metrics.mu.Unlock()

	mr.metrics.TotalRequests++
	if success {
		mr.metrics.SuccessRequests++
	} else {
		mr.metrics.FailedRequests++
	}

	// Update average latency
	currentAvg := mr.metrics.AverageLatency
	mr.metrics.AverageLatency = (currentAvg*time.Duration(mr.metrics.TotalRequests-1) + latency) / time.Duration(mr.metrics.TotalRequests)
}

// GetMetrics returns current router metrics
func (mr *MessageRouter) GetMetrics() RouterMetrics {
	mr.metrics.mu.RLock()
	defer mr.metrics.mu.RUnlock()
	return *mr.metrics
}

// HealthCheck performs health check on all routes
func (mr *MessageRouter) HealthCheck() map[string]bool {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	results := make(map[string]bool)
	for agentType, route := range mr.routes {
		if route.HealthCheck != nil {
			err := route.HealthCheck()
			results[agentType] = err == nil
			route.Active = err == nil
		} else {
			results[agentType] = route.Active
		}
	}

	return results
}

// SetMessageTimeout sets the timeout for messages
func (mr *MessageRouter) SetMessageTimeout(timeout time.Duration) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	mr.messageTimeout = timeout
}

// GetPendingRequests returns count of pending requests
func (mr *MessageRouter) GetPendingRequests() int {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	return len(mr.pendingRequests)
}

// CancelPendingRequest cancels a pending request
func (mr *MessageRouter) CancelPendingRequest(requestID string) error {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	pending, exists := mr.pendingRequests[requestID]
	if !exists {
		return errors.New("request not found")
	}

	// Send cancellation
	select {
	case pending.ErrorCh <- errors.New("request cancelled"):
	default:
	}

	delete(mr.pendingRequests, requestID)
	return nil
}

// MarshalJSON implements json.Marshaler for RouterMetrics
func (rm *RouterMetrics) MarshalJSON() ([]byte, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return json.Marshal(map[string]interface{}{
		"total_requests":   rm.TotalRequests,
		"success_requests": rm.SuccessRequests,
		"failed_requests":  rm.FailedRequests,
		"timeout_requests": rm.TimeoutRequests,
		"average_latency":  rm.AverageLatency.String(),
		"success_rate":     float64(rm.SuccessRequests) / float64(rm.TotalRequests),
	})
}