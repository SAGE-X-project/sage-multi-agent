package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sage-multi-agent/adapters"
	"github.com/sage-multi-agent/config"
	"github.com/sage-x-project/sage/core/rfc9421"
)

func main() {
	fmt.Println("==============================================")
	fmt.Println("Local SAGE Signature Verification Test")
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

	// Enable SAGE
	sageManager.SetEnabled(true)
	
	// Load agent configuration
	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		log.Fatalf("Failed to load agent config: %v", err)
	}

	ctx := context.Background()
	
	// Test 1: Sign and verify with same key
	fmt.Println("\n1. Sign and Verify with Same Key (Root Agent)")
	fmt.Println("------------------------------------------------")
	
	rootSigner, err := sageManager.GetOrCreateSigner("root", verifierHelper)
	if err != nil {
		log.Fatalf("Failed to get root signer: %v", err)
	}
	
	// Sign a message
	testMessage := "This is a test message from root agent"
	metadata := map[string]interface{}{
		"test_id": "local_test_1",
		"purpose": "verification",
	}
	
	signedMessage, err := rootSigner.SignMessage(ctx, testMessage, metadata)
	if err != nil {
		log.Fatalf("Failed to sign message: %v", err)
	}
	
	fmt.Printf("✅ Message signed\n")
	fmt.Printf("   Message ID: %s\n", signedMessage.MessageID)
	fmt.Printf("   Algorithm: %s\n", signedMessage.Algorithm)
	fmt.Printf("   Signature: %x\n", signedMessage.Signature[:16])
	
	// Load the same key for verification
	rootKey, err := verifierHelper.LoadAgentKey("root")
	if err != nil {
		log.Fatalf("Failed to load root key: %v", err)
	}
	
	// Get the public key
	var pubKey *ecdsa.PublicKey
	
	// Try to get ECDSA key directly
	if ecdsaKey, ok := rootKey.PrivateKey().(*ecdsa.PrivateKey); ok {
		pubKey = &ecdsaKey.PublicKey
	} else if privKeyBytes, ok := rootKey.PrivateKey().([]byte); ok {
		privKey, err := crypto.ToECDSA(privKeyBytes)
		if err != nil {
			log.Fatalf("Failed to convert private key: %v", err)
		}
		pubKey = &privKey.PublicKey
	} else {
		log.Fatalf("Unknown private key type: %T", rootKey.PrivateKey())
	}
	
	// Manually verify the signature
	verifier := rfc9421.NewVerifier()
	signatureBase := verifier.ConstructSignatureBase(signedMessage)
	
	fmt.Printf("\n   Signature Base:\n")
	fmt.Printf("   %s\n", signatureBase)
	
	// Hash the message
	hash := crypto.Keccak256Hash([]byte(signatureBase))
	
	// Verify ECDSA signature
	sigValid := crypto.VerifySignature(
		crypto.FromECDSAPub(pubKey),
		hash.Bytes(),
		signedMessage.Signature[:64], // Remove recovery ID if present
	)
	
	if sigValid {
		fmt.Printf("\n✅ Manual signature verification successful!\n")
	} else {
		fmt.Printf("\n❌ Manual signature verification failed\n")
		
		// Try alternative verification
		r, s, err := decodeSignature(signedMessage.Signature)
		if err == nil {
			valid := ecdsa.Verify(pubKey, hash.Bytes(), r, s)
			if valid {
				fmt.Printf("✅ Alternative ECDSA verification successful!\n")
			} else {
				fmt.Printf("❌ Alternative ECDSA verification also failed\n")
			}
		}
	}
	
	// Test 2: Cross-agent signing and verification
	fmt.Println("\n2. Cross-Agent Signing and Verification")
	fmt.Println("------------------------------------------------")
	
	// Ordering agent signs
	orderingSigner, err := sageManager.GetOrCreateSigner("ordering", verifierHelper)
	if err != nil {
		log.Fatalf("Failed to get ordering signer: %v", err)
	}
	
	orderingMessage := "Order #12345 confirmed"
	orderingSignedMsg, err := orderingSigner.SignMessage(ctx, orderingMessage, map[string]interface{}{
		"order_id": "12345",
	})
	if err != nil {
		log.Fatalf("Failed to sign ordering message: %v", err)
	}
	
	fmt.Printf("✅ Ordering agent signed message\n")
	fmt.Printf("   Message: %s\n", orderingMessage)
	fmt.Printf("   Message ID: %s\n", orderingSignedMsg.MessageID[:16])
	
	// Load ordering agent's key for verification
	orderingKey, err := verifierHelper.LoadAgentKey("ordering")
	if err != nil {
		log.Fatalf("Failed to load ordering key: %v", err)
	}
	
	var orderingPubKey *ecdsa.PublicKey
	
	// Try to get ECDSA key directly
	if ecdsaKey, ok := orderingKey.PrivateKey().(*ecdsa.PrivateKey); ok {
		orderingPubKey = &ecdsaKey.PublicKey
	} else if privKeyBytes, ok := orderingKey.PrivateKey().([]byte); ok {
		privKey, err := crypto.ToECDSA(privKeyBytes)
		if err != nil {
			log.Fatalf("Failed to convert ordering private key: %v", err)
		}
		orderingPubKey = &privKey.PublicKey
	} else {
		log.Fatalf("Unknown ordering private key type: %T", orderingKey.PrivateKey())
	}
	
	// Verify ordering agent's signature
	orderingSignatureBase := verifier.ConstructSignatureBase(orderingSignedMsg)
	orderingHash := crypto.Keccak256Hash([]byte(orderingSignatureBase))
	
	orderingSigValid := crypto.VerifySignature(
		crypto.FromECDSAPub(orderingPubKey),
		orderingHash.Bytes(),
		orderingSignedMsg.Signature[:64],
	)
	
	if orderingSigValid {
		fmt.Printf("✅ Cross-agent signature verification successful!\n")
	} else {
		fmt.Printf("❌ Cross-agent signature verification failed\n")
	}
	
	// Test 3: Test all agents can sign
	fmt.Println("\n3. Testing All Agents Can Sign Messages")
	fmt.Println("------------------------------------------------")
	
	agents := []string{"root", "ordering", "planning"}
	for _, agentType := range agents {
		signer, err := sageManager.GetOrCreateSigner(agentType, verifierHelper)
		if err != nil {
			fmt.Printf("❌ %s: Failed to create signer: %v\n", agentType, err)
			continue
		}
		
		msg := fmt.Sprintf("Test message from %s agent", agentType)
		signed, err := signer.SignMessage(ctx, msg, nil)
		if err != nil {
			fmt.Printf("❌ %s: Failed to sign: %v\n", agentType, err)
			continue
		}
		
		if signed != nil {
			fmt.Printf("✅ %s: Successfully signed (ID: %s)\n", agentType, signed.MessageID[:16])
			
			// Get agent DID
			if cfg, exists := agentConfig.Agents[agentType]; exists {
				fmt.Printf("     DID: %s\n", cfg.DID)
			}
		}
	}
	
	// Test 4: Verify HTTP request signing
	fmt.Println("\n4. HTTP Request Signing and Verification")
	fmt.Println("------------------------------------------------")
	
	requestBody := []byte(`{"action": "test", "data": "sample"}`)
	headers, err := rootSigner.SignRequest(ctx, "POST", "/api/test", requestBody)
	if err != nil {
		log.Fatalf("Failed to sign request: %v", err)
	}
	
	fmt.Printf("✅ HTTP Request signed\n")
	for key, value := range headers {
		if key == "X-Signature" {
			fmt.Printf("   %s: %s...%s\n", key, value[:16], value[len(value)-16:])
		} else {
			fmt.Printf("   %s: %s\n", key, value)
		}
	}
	
	// Manually verify the HTTP request signature
	signature, err := hex.DecodeString(headers["X-Signature"])
	if err != nil {
		log.Printf("Failed to decode signature: %v", err)
	} else {
		// Reconstruct the message for verification
		httpMessage := &rfc9421.Message{
			AgentDID:     headers["X-Agent-DID"],
			MessageID:    headers["X-Message-ID"],
			Nonce:        headers["X-Nonce"],
			Body:         requestBody,
			Algorithm:    headers["X-Signature-Algorithm"],
			SignedFields: []string{"agent_did", "message_id", "timestamp", "nonce", "body"},
		}
		
		// Parse timestamp
		if timestamp, err := time.Parse(time.RFC3339, headers["X-Timestamp"]); err == nil {
			httpMessage.Timestamp = timestamp
		}
		
		httpSignatureBase := verifier.ConstructSignatureBase(httpMessage)
		httpHash := crypto.Keccak256Hash([]byte(httpSignatureBase))
		
		httpSigValid := crypto.VerifySignature(
			crypto.FromECDSAPub(pubKey),
			httpHash.Bytes(),
			signature[:64],
		)
		
		if httpSigValid {
			fmt.Printf("\n✅ HTTP request signature verification successful!\n")
		} else {
			fmt.Printf("\n❌ HTTP request signature verification failed\n")
		}
	}
	
	fmt.Println("\n==============================================")
	fmt.Println("Local Verification Test Complete!")
	fmt.Println("==============================================")
	
	// Summary
	status := sageManager.GetStatus()
	fmt.Printf("\nSAGE Status:\n")
	fmt.Printf("  Enabled: %v\n", status.Enabled)
	fmt.Printf("  Active Signers: %d\n", len(status.AgentSigners))
}

// decodeSignature decodes an ECDSA signature into r and s values
func decodeSignature(sig []byte) (r, s *big.Int, err error) {
	if len(sig) != 64 && len(sig) != 65 {
		return nil, nil, fmt.Errorf("invalid signature length: %d", len(sig))
	}
	
	r = new(big.Int).SetBytes(sig[:32])
	s = new(big.Int).SetBytes(sig[32:64])
	
	return r, s, nil
}