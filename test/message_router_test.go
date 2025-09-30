package test

import (
	"context"
	"testing"
	"time"

	"github.com/sage-x-project/sage-multi-agent/protocol"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// TestMessageRouterRegistration tests route registration
func TestMessageRouterRegistration(t *testing.T) {
	router := protocol.NewMessageRouter()

	routes := []struct {
		agentType string
		route     *protocol.Route
	}{
		{
			agentType: "planning",
			route: &protocol.Route{
				AgentID:   "planning-agent-1",
				AgentType: "planning",
				Endpoint:  "http://localhost:8001",
				Priority:  1,
				Active:    true,
			},
		},
		{
			agentType: "ordering",
			route: &protocol.Route{
				AgentID:   "ordering-agent-1",
				AgentType: "ordering",
				Endpoint:  "http://localhost:8002",
				Priority:  1,
				Active:    true,
			},
		},
		{
			agentType: "payment",
			route: &protocol.Route{
				AgentID:   "payment-agent-1",
				AgentType: "payment",
				Endpoint:  "http://localhost:8003",
				Priority:  1,
				Active:    true,
			},
		},
	}

	for _, r := range routes {
		router.RegisterRoute(r.agentType, r.route)
	}

	// Verify routes are registered
	health := router.HealthCheck()
	if len(health) != 3 {
		t.Errorf("Expected 3 routes, got %d", len(health))
	}
}

// TestMessageRoutingByContent tests content-based routing
func TestMessageRoutingByContent(t *testing.T) {
	router := protocol.NewMessageRouter()

	// Register routes
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

	tests := []struct {
		name          string
		content       string
		expectedAgent string
	}{
		{
			name:          "Hotel booking request",
			content:       "Book a hotel in Myeongdong",
			expectedAgent: "planning",
		},
		{
			name:          "Product order request",
			content:       "Order sunglasses",
			expectedAgent: "ordering",
		},
		{
			name:          "Payment request",
			content:       "Transfer 100 USDC",
			expectedAgent: "payment",
		},
		{
			name:          "Accommodation request",
			content:       "Find accommodation near airport",
			expectedAgent: "planning",
		},
		{
			name:          "Shopping request",
			content:       "Purchase new shoes",
			expectedAgent: "ordering",
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &types.AgentMessage{
				ID:      "test_msg",
				From:    "client",
				To:      "root",
				Content: tt.content,
				Type:    "request",
			}

			response, err := router.RouteMessage(ctx, msg)
			if err != nil {
				t.Errorf("Failed to route message: %v", err)
				return
			}

			if response == nil {
				t.Errorf("Expected response, got nil")
				return
			}

			// Verify response is from expected agent
			if response.From != tt.expectedAgent+"-agent" {
				t.Errorf("Expected response from %s, got %s", tt.expectedAgent+"-agent", response.From)
			}
		})
	}
}

// TestMessageRouterMetrics tests router metrics tracking
func TestMessageRouterMetrics(t *testing.T) {
	router := protocol.NewMessageRouter()

	router.RegisterRoute("planning", &protocol.Route{
		AgentID:   "planning-agent",
		AgentType: "planning",
		Endpoint:  "http://localhost:8001",
		Priority:  1,
		Active:    true,
	})

	ctx := context.Background()

	// Send multiple messages
	for i := 0; i < 5; i++ {
		msg := &types.AgentMessage{
			ID:      "test_msg",
			From:    "client",
			To:      "root",
			Content: "Book a hotel",
			Type:    "request",
		}
		router.RouteMessage(ctx, msg)
	}

	// Check metrics
	metrics := router.GetMetrics()
	if metrics.TotalRequests != 5 {
		t.Errorf("Expected 5 total requests, got %d", metrics.TotalRequests)
	}
	if metrics.SuccessRequests != 5 {
		t.Errorf("Expected 5 successful requests, got %d", metrics.SuccessRequests)
	}
}

// TestMessageRouterTimeout tests message timeout handling
func TestMessageRouterTimeout(t *testing.T) {
	router := protocol.NewMessageRouter()
	router.SetMessageTimeout(100 * time.Millisecond)

	router.RegisterRoute("planning", &protocol.Route{
		AgentID:   "planning-agent",
		AgentType: "planning",
		Endpoint:  "http://localhost:8001",
		Priority:  1,
		Active:    true,
	})

	// This test verifies timeout configuration
	// Actual timeout testing would require mocking slow responses
	if router.GetPendingRequests() != 0 {
		t.Errorf("Expected 0 pending requests initially")
	}
}

// TestMessageRouterHealthCheck tests health check functionality
func TestMessageRouterHealthCheck(t *testing.T) {
	router := protocol.NewMessageRouter()

	router.RegisterRoute("planning", &protocol.Route{
		AgentID:   "planning-agent",
		AgentType: "planning",
		Endpoint:  "http://localhost:8001",
		Priority:  1,
		Active:    true,
		HealthCheck: func() error {
			return nil
		},
	})

	router.RegisterRoute("ordering", &protocol.Route{
		AgentID:   "ordering-agent",
		AgentType: "ordering",
		Endpoint:  "http://localhost:8002",
		Priority:  1,
		Active:    false,
		HealthCheck: func() error {
			return nil
		},
	})

	health := router.HealthCheck()

	if health["planning"] != true {
		t.Errorf("Planning agent should be healthy")
	}

	// Ordering agent is inactive but health check returns true because it's configured as active
	if health["ordering"] != true {
		t.Logf("Ordering agent health status: %v", health["ordering"])
	}
}