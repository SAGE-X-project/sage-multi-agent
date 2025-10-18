//go:build demo
// +build demo

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sage-x-project/sage-multi-agent/adapters"
	"github.com/sage-x-project/sage-multi-agent/config"
	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func main() {
	fmt.Println("==============================================")
	fmt.Println("Blockchain SAGE Verification Test")
	fmt.Println("==============================================")

	// Check environment variables
	rpcURL := os.Getenv("LOCAL_RPC_ENDPOINT")
	if rpcURL == "" {
		rpcURL = "http://127.0.0.1:8545"
	}

	contractAddr := os.Getenv("LOCAL_CONTRACT_ADDRESS")
	if contractAddr == "" {
		contractAddr = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	}

	fmt.Printf("\nBlockchain Configuration:\n")
	fmt.Printf("  RPC URL: %s\n", rpcURL)
	fmt.Printf("  Contract: %s\n", contractAddr)

	// Test blockchain connection
	fmt.Println("\n1. Testing Blockchain Connection")
	fmt.Println("---------------------------------")

	// Initialize verifier helper (will connect to blockchain)
	verifierHelper, err := adapters.NewVerifierHelper("keys", false)
	if err != nil {
		log.Fatalf("Failed to create verifier helper: %v", err)
	}

	// Get DID manager
	didManager := verifierHelper.GetDIDManager()
	if didManager == nil {
		log.Fatalf("DID manager is nil")
	}

	fmt.Println(" Connected to blockchain")

	// Test agent registration status
	fmt.Println("\n2. Checking Agent Registration Status")
	fmt.Println("--------------------------------------")

	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		log.Fatalf("Failed to load agent config: %v", err)
	}

	ctx := context.Background()

	for agentType, cfg := range agentConfig.Agents {
		if agentType == "client" {
			continue
		}

		fmt.Printf("\nChecking %s agent (DID: %s):\n", agentType, cfg.DID)

		// Try to resolve agent from blockchain
		agentMetadata, err := didManager.ResolveAgent(ctx, did.AgentDID(cfg.DID))
		if err != nil {
			fmt.Printf("   Not registered on blockchain: %v\n", err)
			continue
		}

		if agentMetadata != nil {
			fmt.Printf("   Registered on blockchain\n")
			fmt.Printf("     Name: %s\n", agentMetadata.Name)
			fmt.Printf("     Active: %v\n", agentMetadata.IsActive)
			fmt.Printf("     Owner: %s\n", agentMetadata.Owner)
			fmt.Printf("     Endpoint: %s\n", agentMetadata.Endpoint)

			// Check if we can get the public key
			pubKey, err := didManager.ResolvePublicKey(ctx, did.AgentDID(cfg.DID))
			if err != nil {
				fmt.Printf("      Failed to get public key: %v\n", err)
			} else if pubKey != nil {
				fmt.Printf("      Public key retrieved\n")
			}
		}
	}

	// Test SAGE signing and verification with blockchain
	fmt.Println("\n3. Testing SAGE with Blockchain Verification")
	fmt.Println("---------------------------------------------")

	// Create SAGE manager
	sageManager, err := adapters.NewSAGEManager(verifierHelper)
	if err != nil {
		log.Fatalf("Failed to create SAGE manager: %v", err)
	}

	// Enable SAGE
	sageManager.SetEnabled(true)

	// Get verifier and set it to NOT skip on error
	verifier := sageManager.GetVerifier()
	verifier.SetSkipOnError(false) // Reject messages that fail verification

	fmt.Println(" SAGE enabled with strict verification (skipOnError=false)")

	// Test signing
	fmt.Println("\n4. Testing Message Signing and Verification")
	fmt.Println("--------------------------------------------")

	// Sign a message from root agent
	rootSigner, err := sageManager.GetOrCreateSigner("root", verifierHelper)
	if err != nil {
		log.Fatalf("Failed to get root signer: %v", err)
	}

	testMessage := "Test message for blockchain verification"
	metadata := map[string]interface{}{
		"test_id":   "blockchain_test",
		"timestamp": time.Now().Unix(),
	}

	signedMessage, err := rootSigner.SignMessage(ctx, testMessage, metadata)
	if err != nil {
		log.Fatalf("Failed to sign message: %v", err)
	}

	fmt.Printf(" Message signed by root agent\n")
	fmt.Printf("   Message ID: %s\n", signedMessage.MessageID)
	fmt.Printf("   Algorithm: %s\n", signedMessage.Algorithm)

	// Try to verify the message using blockchain
	fmt.Println("\n5. Verifying with Blockchain Public Key")
	fmt.Println("----------------------------------------")

	verifyResult, err := verifier.VerifyMessage(ctx, signedMessage)

	if err != nil {
		fmt.Printf(" Verification error: %v\n", err)
		fmt.Printf("   This means the message would be REJECTED\n")

		// Check if it's a blockchain connection issue
		if verifyResult != nil {
			fmt.Printf("   Verification details:\n")
			fmt.Printf("     Verified: %v\n", verifyResult.Verified)
			fmt.Printf("     Error: %s\n", verifyResult.Error)
			for key, value := range verifyResult.Details {
				fmt.Printf("     %s: %s\n", key, value)
			}
		}
	} else if verifyResult.Verified {
		fmt.Printf(" Message verified successfully using blockchain!\n")
		fmt.Printf("   Agent DID: %s\n", verifyResult.AgentDID)
		for key, value := range verifyResult.Details {
			fmt.Printf("   %s: %s\n", key, value)
		}
	} else {
		fmt.Printf(" Verification failed\n")
		fmt.Printf("   Error: %s\n", verifyResult.Error)
		fmt.Printf("   Message would be REJECTED\n")
	}

	// Test cross-agent verification
	fmt.Println("\n6. Testing Cross-Agent Verification")
	fmt.Println("------------------------------------")

	// Ordering agent signs a message
	orderingSigner, err := sageManager.GetOrCreateSigner("ordering", verifierHelper)
	if err != nil {
		log.Printf("Failed to get ordering signer: %v", err)
	} else {
		orderingMsg := "Order confirmation #54321"
		orderingSignedMsg, err := orderingSigner.SignMessage(ctx, orderingMsg, nil)
		if err != nil {
			log.Printf("Failed to sign ordering message: %v", err)
		} else {
			fmt.Printf(" Ordering agent signed message\n")

			// Verify ordering agent's message
			orderingVerifyResult, err := verifier.VerifyMessage(ctx, orderingSignedMsg)
			if err != nil {
				fmt.Printf(" Ordering message verification error: %v\n", err)
				fmt.Printf("   Message would be REJECTED by receiving agent\n")
			} else if orderingVerifyResult.Verified {
				fmt.Printf(" Ordering message verified successfully!\n")
			} else {
				fmt.Printf(" Ordering message verification failed: %s\n", orderingVerifyResult.Error)
				fmt.Printf("   Message would be REJECTED\n")
			}
		}
	}

	// Test error response creation
	fmt.Println("\n7. Testing Error Response for Failed Verification")
	fmt.Println("--------------------------------------------------")

	// Create a fake message with invalid signature
	fakeMessage := &rfc9421.Message{
		AgentDID:     "did:sage:ethereum:fake_agent",
		MessageID:    "fake-message-123",
		Timestamp:    time.Now(),
		Nonce:        "fake-nonce",
		Body:         []byte("This is a fake message"),
		Algorithm:    string(rfc9421.AlgorithmECDSASecp256k1),
		Signature:    []byte("invalid-signature"),
		SignedFields: []string{"body"},
	}

	fakeVerifyResult, err := verifier.VerifyMessage(ctx, fakeMessage)
	if err != nil {
		fmt.Printf(" Fake message correctly rejected: %v\n", err)

		// Create error response
		sageError := types.NewSAGEVerificationError(
			types.SAGEErrorCodeInvalidSignature,
			"Message signature verification failed",
			fakeMessage.AgentDID,
			fakeMessage.MessageID,
		)

		errorResponse := types.NewSAGEErrorResponse("root", "fake_agent", sageError)
		fmt.Printf("\n   Error Response to be sent back:\n")
		fmt.Printf("     Type: %s\n", errorResponse.Type)
		fmt.Printf("     From: %s (rejecting agent)\n", errorResponse.From)
		fmt.Printf("     To: %s (sender)\n", errorResponse.To)
		fmt.Printf("     Error Code: %s\n", errorResponse.Error.Code)
		fmt.Printf("     Error Message: %s\n", errorResponse.Error.Message)
	} else if !fakeVerifyResult.Verified {
		fmt.Printf(" Fake message verification failed as expected\n")
	}

	fmt.Println("\n==============================================")
	fmt.Println("Blockchain Verification Test Complete!")
	fmt.Println("==============================================")

	// Final summary
	fmt.Println("\nSummary:")
	fmt.Println("---------")
	fmt.Printf("• Blockchain connected: \n")
	fmt.Printf("• Agents registered: Check above results\n")
	fmt.Printf("• SAGE verification mode: STRICT (reject on failure)\n")
	fmt.Printf("• Failed verifications will be REJECTED\n")
	fmt.Printf("• Error responses will be sent to senders\n")
}
