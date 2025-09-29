package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/sage-x-project/sage-multi-agent/adapters"
	"github.com/sage-x-project/sage-multi-agent/config"
)

// AgentServer simulates an agent server with SAGE capabilities
type AgentServer struct {
	agentType      string
	port           int
	sageManager    *adapters.SAGEManager
	verifierHelper *adapters.VerifierHelper
	messageSigner  *adapters.MessageSigner
}

// Message types for testing
var testMessages = []string{
	"Request for trip planning to Tokyo",
	"Order confirmation for item #12345",
	"Schedule optimization query",
	"Product availability check",
	"Route planning request from A to B",
	"Purchase authorization request",
	"Calendar sync request",
	"Inventory status update",
	"Weather information query",
	"Transportation booking request",
}

// Agent communication paths
var communicationPaths = []struct {
	from string
	to   string
}{
	{"root", "ordering"},
	{"root", "planning"},
	{"ordering", "root"},
	{"planning", "root"},
	{"ordering", "planning"},
	{"planning", "ordering"},
}

func main() {
	fmt.Println("==============================================")
	fmt.Println("Agent-to-Agent SAGE Communication Test")
	fmt.Println("==============================================")
	
	// Initialize verifier helper
	verifierHelper, err := adapters.NewVerifierHelper("keys", false)
	if err != nil {
		log.Fatalf("Failed to create verifier helper: %v", err)
	}

	// Create SAGE manager
	sageManager, err := adapters.NewSAGEManager(verifierHelper)
	if err != nil {
		log.Fatalf("Failed to create SAGE manager: %v", err)
	}

	// Enable SAGE globally
	sageManager.SetEnabled(true)
	fmt.Println("\n SAGE Protocol Enabled")

	// Load agent configuration
	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		log.Fatalf("Failed to load agent config: %v", err)
	}

	// Create agent simulators
	agents := make(map[string]*AgentServer)
	
	for agentType := range agentConfig.Agents {
		if agentType == "client" {
			continue // Skip client agent
		}
		
		signer, err := sageManager.GetOrCreateSigner(agentType, verifierHelper)
		if err != nil {
			log.Printf("Failed to create signer for %s: %v", agentType, err)
			continue
		}
		
		agents[agentType] = &AgentServer{
			agentType:      agentType,
			sageManager:    sageManager,
			verifierHelper: verifierHelper,
			messageSigner:  signer,
		}
		
		fmt.Printf(" Initialized %s agent with SAGE\n", agentType)
	}

	fmt.Println("\n==============================================")
	fmt.Println("Starting Agent Communication Tests")
	fmt.Println("==============================================")

	ctx := context.Background()
	verifier := sageManager.GetVerifier()
	
	// Statistics
	totalMessages := 0
	successfulSigns := 0
	successfulVerifications := 0
	failedVerifications := 0
	
	// Run random message exchanges
	rand.Seed(time.Now().UnixNano())
	numTests := 10
	
	for i := 0; i < numTests; i++ {
		// Random communication path
		path := communicationPaths[rand.Intn(len(communicationPaths))]
		fromAgent := agents[path.from]
		
		// Random message
		message := testMessages[rand.Intn(len(testMessages))]
		
		fmt.Printf("\n[Test %d/%d]\n", i+1, numTests)
		fmt.Printf("📤 %s → %s\n", path.from, path.to)
		fmt.Printf("   Message: %s\n", message)
		
		totalMessages++
		
		// Sign the message
		metadata := map[string]interface{}{
			"from":      path.from,
			"to":        path.to,
			"timestamp": time.Now().Unix(),
			"test_id":   fmt.Sprintf("test_%d", i+1),
		}
		
		signedMessage, err := fromAgent.messageSigner.SignMessage(ctx, message, metadata)
		if err != nil {
			fmt.Printf("    Signing failed: %v\n", err)
			continue
		}
		
		if signedMessage == nil {
			fmt.Printf("    SAGE is disabled\n")
			continue
		}
		
		successfulSigns++
		fmt.Printf("    Message signed (ID: %s)\n", signedMessage.MessageID[:16])
		fmt.Printf("      Algorithm: %s\n", signedMessage.Algorithm)
		fmt.Printf("      Signature: %d bytes\n", len(signedMessage.Signature))
		
		// Simulate network delay
		time.Sleep(100 * time.Millisecond)
		
		// Verify the message (simulating the receiving agent)
		verifyResult, err := verifier.VerifyMessage(ctx, signedMessage)
		if err != nil {
			fmt.Printf("    Verification error: %v\n", err)
			failedVerifications++
			continue
		}
		
		if verifyResult.Verified {
			successfulVerifications++
			fmt.Printf("    Signature verified\n")
			if details := verifyResult.Details; len(details) > 0 {
				fmt.Printf("      Agent: %s\n", details["agent_name"])
			}
		} else {
			failedVerifications++
			fmt.Printf("    Verification failed: %s\n", verifyResult.Error)
			
			// In skip-on-error mode, message would still be processed
			if verifier.IsEnabled() {
				fmt.Printf("    Message would be processed anyway (skip-on-error mode)\n")
			}
		}
		
		// Add some delay between tests
		time.Sleep(200 * time.Millisecond)
	}
	
	// Print statistics
	fmt.Println("\n==============================================")
	fmt.Println("Test Statistics")
	fmt.Println("==============================================")
	fmt.Printf("Total Messages: %d\n", totalMessages)
	fmt.Printf("Successfully Signed: %d (%.1f%%)\n", 
		successfulSigns, float64(successfulSigns)*100/float64(totalMessages))
	fmt.Printf("Successfully Verified: %d (%.1f%%)\n", 
		successfulVerifications, float64(successfulVerifications)*100/float64(successfulSigns))
	fmt.Printf("Verification Failures: %d\n", failedVerifications)
	
	// Test SAGE toggle
	fmt.Println("\n==============================================")
	fmt.Println("Testing SAGE Toggle")
	fmt.Println("==============================================")
	
	// Disable SAGE
	fmt.Println("\n🔴 Disabling SAGE...")
	sageManager.SetEnabled(false)
	
	// Try to sign with SAGE disabled
	testSigner, _ := sageManager.GetOrCreateSigner("root", verifierHelper)
	disabledMsg, err := testSigner.SignMessage(ctx, "Test with SAGE disabled", nil)
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
	} else if disabledMsg == nil {
		fmt.Println("    Signing skipped when SAGE disabled (expected behavior)")
	} else {
		fmt.Println("    Unexpected: message was signed with SAGE disabled")
	}
	
	// Re-enable SAGE
	fmt.Println("\n Re-enabling SAGE...")
	sageManager.SetEnabled(true)
	
	enabledMsg, err := testSigner.SignMessage(ctx, "Test with SAGE enabled", nil)
	if err != nil {
		fmt.Printf("    Error: %v\n", err)
	} else if enabledMsg != nil {
		fmt.Println("    Signing works when SAGE enabled")
		fmt.Printf("   Message ID: %s\n", enabledMsg.MessageID)
	}
	
	// Test HTTP header signing
	fmt.Println("\n==============================================")
	fmt.Println("Testing HTTP Request Signing")
	fmt.Println("==============================================")
	
	for agentType, agent := range agents {
		headers, err := agent.messageSigner.SignRequest(ctx, "POST", "/api/message", 
			[]byte(`{"action": "test", "data": "sample"}`))
		if err != nil {
			fmt.Printf(" %s agent failed to sign request: %v\n", agentType, err)
			continue
		}
		
		fmt.Printf(" %s agent signed HTTP request\n", agentType)
		fmt.Printf("   Headers: %d\n", len(headers))
		fmt.Printf("   X-Agent-DID: %s\n", headers["X-Agent-DID"])
		fmt.Printf("   X-Signature-Algorithm: %s\n", headers["X-Signature-Algorithm"])
		
		// Verify the request headers
		body := []byte(`{"action": "test", "data": "sample"}`)
		verifyResult, err := verifier.VerifyRequestHeaders(ctx, headers, body)
		if err != nil {
			fmt.Printf("    Header verification error: %v\n", err)
		} else if verifyResult.Verified {
			fmt.Printf("    Request signature verified\n")
		} else {
			fmt.Printf("    Request verification failed: %s\n", verifyResult.Error)
		}
	}
	
	fmt.Println("\n==============================================")
	fmt.Println("Agent Communication Test Complete!")
	fmt.Println("==============================================")
	
	// Final status
	status := sageManager.GetStatus()
	fmt.Println("\nFinal SAGE Status:")
	fmt.Printf("  SAGE Enabled: %v\n", status.Enabled)
	fmt.Printf("  Active Agents: %d\n", len(status.AgentSigners))
	for agent, enabled := range status.AgentSigners {
		fmt.Printf("    %s: %v\n", agent, enabled)
	}
}

// Simulate sending a message via HTTP (for more realistic testing)
func simulateHTTPMessage(from, to string, message string, signer *adapters.MessageSigner) error {
	// This would be the actual HTTP call to another agent
	// For now, we just simulate it
	
	ctx := context.Background()
	
	// Create request body
	body := map[string]interface{}{
		"from":    from,
		"to":      to,
		"message": message,
		"timestamp": time.Now().Unix(),
	}
	
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	
	// Sign the request
	headers, err := signer.SignRequest(ctx, "POST", fmt.Sprintf("/agent/%s/message", to), bodyBytes)
	if err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}
	
	// Create HTTP request (simulation)
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:808X/message"), bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}
	
	// Add signed headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	
	// In real scenario, this would send the request
	// client := &http.Client{}
	// resp, err := client.Do(req)
	
	return nil
}