package root

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/types"
)

func TestRootRoutesToPaymentAgent(t *testing.T) {
	// Start payment agent on test port
	pa := payment.NewPaymentAgent("PaymentAgent", 18083)
	go func() { _ = pa.Start() }()
	time.Sleep(150 * time.Millisecond)

	ra := NewRootAgent("root", 18080)
	ra.RegisterAgent("payment", "PaymentAgent", "http://localhost:18083")

	req := &types.AgentMessage{From: "client", To: "root", Content: "please transfer 5 USDC", Timestamp: time.Now(), Type: "request"}
	resp, err := ra.RouteRequest(context.Background(), req)

	if err != nil {
		t.Fatalf("RouteRequest error: %v", err)
	}
	if resp == nil {
		t.Fatalf("nil response")
	}
	if !strings.Contains(strings.ToLower(resp.Content), "transfer") {
		t.Fatalf("unexpected response: %q", resp.Content)
	}
}
