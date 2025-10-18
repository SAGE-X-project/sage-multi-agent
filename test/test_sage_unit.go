//go:build demo
// +build demo

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sage-x-project/sage-multi-agent/adapters"
	"github.com/sage-x-project/sage-multi-agent/config"
)

func main() {
	fmt.Println("Unit Testing SAGE Message Signing and Verification")
	fmt.Println("==================================================")

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

	// Test 1: Create signers for different agents
	fmt.Println("\n1. Creating signers for agents...")
	agents := []string{"root", "ordering", "planning"}
	for _, agent := range agents {
		_, err := sageManager.GetOrCreateSigner(agent, verifierHelper)
		if err != nil {
			log.Printf("    Failed to create signer for %s: %v", agent, err)
			continue
		}
		fmt.Printf("    Created signer for %s agent\n", agent)
		
		// Get agent DID info
		if agentConfig, _ := config.LoadAgentConfig(""); agentConfig != nil {
			if cfg, exists := agentConfig.Agents[agent]; exists {
				fmt.Printf("      DID: %s\n", cfg.DID)
			}
		}
	}

	// Test 2: Sign a message
	fmt.Println("\n2. Testing message signing...")
	ctx := context.Background()
	testMessage := "Hello, this is a test message for SAGE protocol verification"
	
	rootSigner, err := sageManager.GetOrCreateSigner("root", verifierHelper)
	if err != nil {
		log.Fatalf("Failed to get root signer: %v", err)
	}

	metadata := map[string]interface{}{
		"test":      true,
		"timestamp": time.Now().Unix(),
		"purpose":   "unit_test",
	}

	signedMessage, err := rootSigner.SignMessage(ctx, testMessage, metadata)
	if err != nil {
		log.Fatalf("Failed to sign message: %v", err)
	}

	if signedMessage != nil {
		fmt.Printf("    Message signed successfully\n")
		fmt.Printf("      Message ID: %s\n", signedMessage.MessageID)
		fmt.Printf("      Agent DID: %s\n", signedMessage.AgentDID)
		fmt.Printf("      Algorithm: %s\n", signedMessage.Algorithm)
		fmt.Printf("      Signature length: %d bytes\n", len(signedMessage.Signature))
	}

	// Test 3: Verify the signed message
	fmt.Println("\n3. Testing message verification...")
	verifier := sageManager.GetVerifier()
	
	verifyResult, err := verifier.VerifyMessage(ctx, signedMessage)
	if err != nil {
		log.Printf("    Verification error: %v", err)
	} else {
		fmt.Printf("   Verification result:\n")
		fmt.Printf("      Verified: %v\n", verifyResult.Verified)
		fmt.Printf("      Signature Valid: %v\n", verifyResult.SignatureValid)
		if verifyResult.Error != "" {
			fmt.Printf("      Error: %s\n", verifyResult.Error)
		}
		if verifyResult.Verified {
			fmt.Println("    Message verification successful!")
		} else {
			fmt.Println("    Message verification failed!")
		}
	}

	// Test 4: Test SAGE enable/disable
	fmt.Println("\n4. Testing SAGE enable/disable...")
	
	// Disable SAGE
	sageManager.SetEnabled(false)
	fmt.Printf("   SAGE disabled, current status: %v\n", sageManager.IsEnabled())
	
	// Try signing with SAGE disabled
	disabledSignedMsg, err := rootSigner.SignMessage(ctx, "Test with SAGE disabled", nil)
	if err != nil {
		log.Printf("   Error signing with SAGE disabled: %v", err)
	} else if disabledSignedMsg == nil {
		fmt.Println("    Signing skipped when SAGE disabled (as expected)")
	}

	// Re-enable SAGE
	sageManager.SetEnabled(true)
	fmt.Printf("   SAGE enabled, current status: %v\n", sageManager.IsEnabled())

	// Test 5: Cross-agent verification
	fmt.Println("\n5. Testing cross-agent message verification...")
	
	// Ordering agent signs a message
	orderingSigner, err := sageManager.GetOrCreateSigner("ordering", verifierHelper)
	if err != nil {
		log.Printf("   Failed to get ordering signer: %v", err)
	} else {
		orderingMessage, err := orderingSigner.SignMessage(ctx, "Order confirmation #12345", map[string]interface{}{
			"order_id": "12345",
			"status":   "confirmed",
		})
		if err != nil {
			log.Printf("   Failed to sign ordering message: %v", err)
		} else if orderingMessage != nil {
			fmt.Printf("    Ordering agent signed message\n")
			
			// Verify ordering agent's message
			orderingVerifyResult, err := verifier.VerifyMessage(ctx, orderingMessage)
			if err != nil {
				log.Printf("    Failed to verify ordering message: %v", err)
			} else {
				fmt.Printf("   Ordering message verification: %v\n", orderingVerifyResult.Verified)
				if orderingVerifyResult.Verified {
					fmt.Println("    Cross-agent verification successful!")
				}
			}
		}
	}

	// Test 6: Test request header signing
	fmt.Println("\n6. Testing HTTP request header signing...")
	
	headers, err := rootSigner.SignRequest(ctx, "POST", "/api/test", []byte("test body"))
	if err != nil {
		log.Printf("    Failed to sign request: %v", err)
	} else if headers != nil {
		fmt.Println("    Request headers signed:")
		for key, value := range headers {
			if key == "X-Signature" {
				fmt.Printf("      %s: [%d bytes]\n", key, len(value)/2) // hex string
			} else {
				fmt.Printf("      %s: %s\n", key, value)
			}
		}
	}

	fmt.Println("\n==================================================")
	fmt.Println("SAGE Unit Testing Complete!")
	
	// Summary
	status := sageManager.GetStatus()
	fmt.Println("\nFinal SAGE Status:")
	fmt.Printf("  System Enabled: %v\n", status.Enabled)
	fmt.Printf("  Verifier Enabled: %v\n", status.VerifierEnabled)
	fmt.Printf("  Active Signers: %d\n", len(status.AgentSigners))
}
