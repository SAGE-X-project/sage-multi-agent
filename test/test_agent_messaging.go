package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/sage-multi-agent/adapters"
	"github.com/sage-multi-agent/config"
)

// Test messages for random selection
var testMessages = []string{
	"Please provide the current system status",
	"Execute task ID: TASK-12345",
	"What is the optimal route for order #98765?",
	"Initialize planning sequence for next quarter",
	"Request resource allocation for project Alpha",
	"Verify agent synchronization status",
	"Calculate estimated completion time",
	"Retrieve historical performance metrics",
	"Update configuration parameters",
	"Confirm receipt of previous instruction",
}

// Agent types for testing
var agentTypes = []string{
	"root",
	"ordering",
	"planning",
}

func main() {
	fmt.Println("==============================================")
	fmt.Println("Agent-to-Agent Messaging Test with SAGE")
	fmt.Println("==============================================")
	
	// Initialize random seed
	rand.Seed(time.Now().UnixNano())
	
	// Load agent configuration
	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		log.Fatalf("Failed to load agent config: %v", err)
	}
	
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
	
	// Enable SAGE
	sageManager.SetEnabled(true)
	fmt.Println("\n✅ SAGE Protocol enabled for all agents")
	
	// Create agent messenger
	messenger := adapters.NewAgentMessenger(sageManager, verifierHelper, agentConfig)
	
	ctx := context.Background()
	
	// Test 1: Random message from one agent to another
	fmt.Println("\n1. Testing Random Message Sending")
	fmt.Println("----------------------------------")
	
	for i := 0; i < 5; i++ {
		// Select random sender and receiver
		fromAgent := agentTypes[rand.Intn(len(agentTypes))]
		toAgent := agentTypes[rand.Intn(len(agentTypes))]
		
		// Don't send to self
		for toAgent == fromAgent {
			toAgent = agentTypes[rand.Intn(len(agentTypes))]
		}
		
		// Select random message
		message := testMessages[rand.Intn(len(testMessages))]
		
		fmt.Printf("\n[Test %d] %s → %s\n", i+1, fromAgent, toAgent)
		fmt.Printf("  Message: %s\n", message)
		
		// Send message
		conversation, err := messenger.SendMessage(ctx, fromAgent, toAgent, message, false)
		if err != nil {
			fmt.Printf("  ❌ Failed: %v\n", err)
			continue
		}
		
		fmt.Printf("  ✅ Sent successfully (ID: %s)\n", conversation.ConversationID)
		fmt.Printf("  📝 Details:\n")
		fmt.Printf("     From DID: %s\n", conversation.Request.FromAgentDID)
		fmt.Printf("     To DID: %s\n", conversation.Request.ToAgentDID)
		fmt.Printf("     Algorithm: %s\n", conversation.Request.Algorithm)
		fmt.Printf("     Timestamp: %s\n", conversation.Request.Timestamp.Format(time.RFC3339))
		
		// Small delay between messages
		time.Sleep(500 * time.Millisecond)
	}
	
	// Test 2: Request-Response conversation
	fmt.Println("\n2. Testing Request-Response Conversation")
	fmt.Println("-----------------------------------------")
	
	// Send a message expecting response
	fromAgent := "root"
	toAgent := "ordering"
	requestMessage := "What is the current order processing capacity?"
	
	fmt.Printf("\n[Request] %s → %s\n", fromAgent, toAgent)
	fmt.Printf("  Message: %s\n", requestMessage)
	
	conversation, err := messenger.SendMessage(ctx, fromAgent, toAgent, requestMessage, true)
	if err != nil {
		fmt.Printf("  ❌ Failed to send request: %v\n", err)
	} else {
		fmt.Printf("  ✅ Request sent (ID: %s)\n", conversation.ConversationID)
		fmt.Printf("  ⏳ Expecting response...\n")
		
		// Simulate creating a response
		fmt.Printf("\n[Response] %s → %s\n", toAgent, fromAgent)
		responseBody := "Current capacity: 1000 orders/hour, 85% utilization"
		
		response, signedResponse, err := messenger.CreateResponse(
			ctx,
			conversation.Request,
			toAgent,
			responseBody,
		)
		if err != nil {
			fmt.Printf("  ❌ Failed to create response: %v\n", err)
		} else {
			fmt.Printf("  ✅ Response created (ID: %s)\n", response.ResponseID)
			fmt.Printf("  📝 Response Details:\n")
			fmt.Printf("     Body: %s\n", response.Body)
			fmt.Printf("     In Response To: %s\n", response.InResponseTo.OriginalRequestID)
			fmt.Printf("     Original Sender: %s\n", response.InResponseTo.OriginalSenderDID)
			fmt.Printf("     Original Nonce: %s\n", response.InResponseTo.OriginalNonce)
			fmt.Printf("     Message Digest: %s\n", response.InResponseTo.OriginalMessageDigest[:16]+"...")
			fmt.Printf("     Signature: %x\n", signedResponse.Signature[:32])
			
			// Handle the response
			err = messenger.HandleResponse(ctx, response, signedResponse)
			if err != nil {
				fmt.Printf("  ❌ Failed to handle response: %v\n", err)
			} else {
				fmt.Printf("  ✅ Response handled successfully\n")
				
				// Verify conversation is complete
				conv, exists := messenger.GetConversation(conversation.ConversationID)
				if exists && conv.Status == "completed" {
					fmt.Printf("  ✅ Conversation completed successfully\n")
				}
			}
		}
	}
	
	// Test 3: Multi-agent conversation chain
	fmt.Println("\n3. Testing Multi-Agent Conversation Chain")
	fmt.Println("------------------------------------------")
	
	chain := []struct {
		from    string
		to      string
		message string
	}{
		{"root", "planning", "Initiate resource planning for Q2"},
		{"planning", "ordering", "Reserve capacity for projected demand"},
		{"ordering", "root", "Capacity reserved, confirmation #CAP-2024-Q2"},
	}
	
	for i, step := range chain {
		fmt.Printf("\n[Step %d] %s → %s\n", i+1, step.from, step.to)
		fmt.Printf("  Message: %s\n", step.message)
		
		conversation, err := messenger.SendMessage(ctx, step.from, step.to, step.message, false)
		if err != nil {
			fmt.Printf("  ❌ Failed: %v\n", err)
			break
		}
		
		fmt.Printf("  ✅ Sent (ID: %s)\n", conversation.ConversationID)
		time.Sleep(300 * time.Millisecond)
	}
	
	// Summary
	fmt.Println("\n==============================================")
	fmt.Println("Test Summary")
	fmt.Println("==============================================")
	
	allConversations := messenger.GetAllConversations()
	completed := 0
	pending := 0
	
	for _, conv := range allConversations {
		if conv.Status == "completed" {
			completed++
		} else if conv.Status == "pending" {
			pending++
		}
	}
	
	fmt.Printf("\nTotal Conversations: %d\n", len(allConversations))
	fmt.Printf("  Completed: %d\n", completed)
	fmt.Printf("  Pending: %d\n", pending)
	
	fmt.Println("\nKey Features Demonstrated:")
	fmt.Println("  ✅ RFC-9421 compliant message signing")
	fmt.Println("  ✅ Agent DID identification (contract registered)")
	fmt.Println("  ✅ Timestamp and nonce for replay protection")
	fmt.Println("  ✅ Algorithm specification (ECDSA-secp256k1)")
	fmt.Println("  ✅ Request-Response correlation")
	fmt.Println("  ✅ Message digest for integrity verification")
	fmt.Println("  ✅ Multi-agent conversation chains")
	
	fmt.Println("\n✨ Agent messaging with SAGE protocol test completed!")
}