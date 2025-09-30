package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// AgentKey represents an agent's cryptographic key information
type AgentKey struct {
	AgentName   string `json:"agent_name"`
	DID         string `json:"did"`
	Address     string `json:"address"`
	PublicKey   string `json:"public_key"`
	PrivateKey  string `json:"private_key"`
}

func generateSecp256k1Keys() (*ecdsa.PrivateKey, error) {
	return crypto.GenerateKey()
}

func main() {
	var (
		agentName  = flag.String("agent", "", "Agent name (root, ordering, planning, client)")
		outputPath = flag.String("output", "", "Output file path")
		keysDir    = flag.String("dir", "keys", "Keys directory")
	)
	flag.Parse()

	// Validate agent name
	validAgents := map[string]bool{
		"root":     true,
		"ordering": true,
		"planning": true,
		"client":   true,
	}

	if *agentName == "" {
		log.Fatal("Agent name is required. Use --agent=<name>")
	}

	if !validAgents[*agentName] {
		log.Fatalf("Invalid agent name: %s. Valid names: root, ordering, planning, client", *agentName)
	}

	// Generate secp256k1 keys
	privateKey, err := generateSecp256k1Keys()
	if err != nil {
		log.Fatalf("Failed to generate keys: %v", err)
	}

	// Get public key
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Fatal("Failed to cast public key to ECDSA")
	}

	// Get address
	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	// Get public key bytes (uncompressed, 65 bytes with 0x04 prefix)
	publicKeyBytes := crypto.FromECDSAPub(publicKeyECDSA)

	// Get private key bytes
	privateKeyBytes := crypto.FromECDSA(privateKey)

	// Create DID
	did := fmt.Sprintf("did:sage:ethereum:%s", address.Hex())

	// Create key info
	keyInfo := AgentKey{
		AgentName:  *agentName,
		DID:        did,
		Address:    address.Hex(),
		PublicKey:  "0x" + hex.EncodeToString(publicKeyBytes),
		PrivateKey: "0x" + hex.EncodeToString(privateKeyBytes),
	}

	// Determine output path
	outputFile := *outputPath
	if outputFile == "" {
		// Ensure keys directory exists
		if err := os.MkdirAll(*keysDir, 0755); err != nil {
			log.Fatalf("Failed to create keys directory: %v", err)
		}
		outputFile = filepath.Join(*keysDir, fmt.Sprintf("%s_key.json", *agentName))
	}

	// Write to file
	jsonData, err := json.MarshalIndent(keyInfo, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal key info: %v", err)
	}

	if err := os.WriteFile(outputFile, jsonData, 0600); err != nil {
		log.Fatalf("Failed to write key file: %v", err)
	}

	// Print summary
	fmt.Printf("Generated keys for %s agent:\n", *agentName)
	fmt.Printf("  DID:        %s\n", did)
	fmt.Printf("  Address:    %s\n", address.Hex())
	fmt.Printf("  Public Key: %s...%s (65 bytes)\n", 
		keyInfo.PublicKey[:10], 
		keyInfo.PublicKey[len(keyInfo.PublicKey)-8:])
	fmt.Printf("  Saved to:   %s\n", outputFile)

	// Also create a simplified private key file for Hardhat compatibility
	hardhatKeyFile := filepath.Join(*keysDir, fmt.Sprintf("%s_private.txt", *agentName))
	if err := os.WriteFile(hardhatKeyFile, []byte(keyInfo.PrivateKey), 0600); err != nil {
		log.Printf("Warning: Failed to write Hardhat key file: %v", err)
	} else {
		fmt.Printf("  Private key also saved to: %s (for Hardhat)\n", hardhatKeyFile)
	}
}