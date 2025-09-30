package test

import (
	"context"
	"testing"
	"time"

	"github.com/sage-x-project/sage-multi-agent/protocol"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// TestE2EHotelBookingScenario tests end-to-end hotel booking scenario
func TestE2EHotelBookingScenario(t *testing.T) {
	router := protocol.NewMessageRouter()

	// Register planning agent
	router.RegisterRoute("planning", &protocol.Route{
		AgentID:   "planning-agent",
		AgentType: "planning",
		Endpoint:  "http://localhost:8001",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Simulate hotel booking request
	bookingRequest := &types.AgentMessage{
		ID:      "booking_001",
		From:    "client",
		To:      "root",
		Content: "Book a hotel in Myeongdong for 2 nights",
		Type:    "request",
	}

	response, err := router.RouteMessage(ctx, bookingRequest)
	if err != nil {
		t.Errorf("Hotel booking failed: %v", err)
		return
	}

	if response == nil {
		t.Errorf("Expected response from planning agent")
		return
	}

	if response.From != "planning-agent" {
		t.Errorf("Expected response from planning-agent, got %s", response.From)
	}

	t.Logf("Hotel booking response: %s", response.Content)
}

// TestE2EOrderingScenario tests end-to-end product ordering scenario
func TestE2EOrderingScenario(t *testing.T) {
	router := protocol.NewMessageRouter()

	// Register ordering agent
	router.RegisterRoute("ordering", &protocol.Route{
		AgentID:   "ordering-agent",
		AgentType: "ordering",
		Endpoint:  "http://localhost:8002",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Simulate product ordering request
	orderRequest := &types.AgentMessage{
		ID:      "order_001",
		From:    "client",
		To:      "root",
		Content: "Order sunglasses with UV protection",
		Type:    "request",
	}

	response, err := router.RouteMessage(ctx, orderRequest)
	if err != nil {
		t.Errorf("Product ordering failed: %v", err)
		return
	}

	if response == nil {
		t.Errorf("Expected response from ordering agent")
		return
	}

	if response.From != "ordering-agent" {
		t.Errorf("Expected response from ordering-agent, got %s", response.From)
	}

	t.Logf("Order response: %s", response.Content)
}

// TestE2EPaymentScenario tests end-to-end payment scenario
func TestE2EPaymentScenario(t *testing.T) {
	router := protocol.NewMessageRouter()

	// Register payment agent
	router.RegisterRoute("payment", &protocol.Route{
		AgentID:   "payment-agent",
		AgentType: "payment",
		Endpoint:  "http://localhost:8003",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Simulate payment request
	paymentRequest := &types.AgentMessage{
		ID:      "payment_001",
		From:    "client",
		To:      "root",
		Content: "Transfer 100 USDC to wallet",
		Type:    "request",
	}

	response, err := router.RouteMessage(ctx, paymentRequest)
	if err != nil {
		t.Errorf("Payment processing failed: %v", err)
		return
	}

	if response == nil {
		t.Errorf("Expected response from payment agent")
		return
	}

	if response.From != "payment-agent" {
		t.Errorf("Expected response from payment-agent, got %s", response.From)
	}

	t.Logf("Payment response: %s", response.Content)
}

// TestE2EMultiAgentWorkflow tests complete workflow with multiple agents
func TestE2EMultiAgentWorkflow(t *testing.T) {
	router := protocol.NewMessageRouter()

	// Register all agents
	router.RegisterRoute("planning", &protocol.Route{
		AgentID:   "planning-agent",
		AgentType: "planning",
		Endpoint:  "http://localhost:8001",
		Priority:  1,
		Active:    true,
	})

	router.RegisterRoute("ordering", &protocol.Route{
		AgentID:   "ordering-agent",
		AgentType: "ordering",
		Endpoint:  "http://localhost:8002",
		Priority:  1,
		Active:    true,
	})

	router.RegisterRoute("payment", &protocol.Route{
		AgentID:   "payment-agent",
		AgentType: "payment",
		Endpoint:  "http://localhost:8003",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Test workflow: Plan trip -> Order items -> Make payment
	steps := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "Step 1: Plan trip",
			content:  "Find hotel in Seoul",
			expected: "planning-agent",
		},
		{
			name:     "Step 2: Order travel items",
			content:  "Order travel backpack",
			expected: "ordering-agent",
		},
		{
			name:     "Step 3: Make payment",
			content:  "Transfer 500 USDC payment",
			expected: "payment-agent",
		},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			msg := &types.AgentMessage{
				ID:      "workflow_msg",
				From:    "client",
				To:      "root",
				Content: step.content,
				Type:    "request",
			}

			response, err := router.RouteMessage(ctx, msg)
			if err != nil {
				t.Errorf("%s failed: %v", step.name, err)
				return
			}

			if response.From != step.expected {
				t.Errorf("Expected response from %s, got %s", step.expected, response.From)
			}

			t.Logf("%s completed: %s", step.name, response.Content)
		})
	}

	// Verify metrics
	metrics := router.GetMetrics()
	if metrics.TotalRequests != 3 {
		t.Errorf("Expected 3 total requests, got %d", metrics.TotalRequests)
	}
	if metrics.SuccessRequests != 3 {
		t.Errorf("Expected 3 successful requests, got %d", metrics.SuccessRequests)
	}
}

// TestE2EMessageTampering tests message tampering detection
func TestE2EMessageTampering(t *testing.T) {
	// This test simulates SAGE ON scenario where tampering should be detected

	router := protocol.NewMessageRouter()

	router.RegisterRoute("ordering", &protocol.Route{
		AgentID:   "ordering-agent",
		AgentType: "ordering",
		Endpoint:  "http://localhost:8002",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Original message
	originalMsg := &types.AgentMessage{
		ID:      "tamper_test_001",
		From:    "client",
		To:      "root",
		Content: "Order to address: 0x123456",
		Type:    "request",
	}

	response, err := router.RouteMessage(ctx, originalMsg)
	if err != nil {
		t.Errorf("Original message failed: %v", err)
		return
	}

	// In a real scenario with SAGE enabled, tampering would be detected
	// This test validates the routing works correctly
	if response == nil {
		t.Errorf("Expected response from ordering agent")
	}

	t.Logf("Message processed successfully (tampering detection would be at SAGE layer)")
}

// TestE2EPerformance tests end-to-end performance
func TestE2EPerformance(t *testing.T) {
	router := protocol.NewMessageRouter()

	router.RegisterRoute("planning", &protocol.Route{
		AgentID:   "planning-agent",
		AgentType: "planning",
		Endpoint:  "http://localhost:8001",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Send multiple messages and measure performance
	numMessages := 10
	start := time.Now()

	for i := 0; i < numMessages; i++ {
		msg := &types.AgentMessage{
			ID:      "perf_test",
			From:    "client",
			To:      "root",
			Content: "Book hotel",
			Type:    "request",
		}

		_, err := router.RouteMessage(ctx, msg)
		if err != nil {
			t.Errorf("Message %d failed: %v", i, err)
		}
	}

	duration := time.Since(start)
	avgLatency := duration / time.Duration(numMessages)

	t.Logf("Processed %d messages in %v (avg: %v per message)", numMessages, duration, avgLatency)

	// Verify metrics
	metrics := router.GetMetrics()
	if metrics.TotalRequests != int64(numMessages) {
		t.Errorf("Expected %d total requests, got %d", numMessages, metrics.TotalRequests)
	}
}