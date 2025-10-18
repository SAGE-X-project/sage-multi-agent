//go:build tools
// +build tools

package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/keys"
)

type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	Address    string `json:"address"`
}

type UpdatedMetadata struct {
	Agents []struct {
		Name     string `json:"name"`
		DID      string `json:"did"`
		Metadata struct {
			Name         string                 `json:"name"`
			Description  string                 `json:"description"`
			Version      string                 `json:"version"`
			Type         string                 `json:"type"`
			Endpoint     string                 `json:"endpoint"`
			PublicKey    string                 `json:"publicKey"`
			Capabilities map[string]interface{} `json:"capabilities"`
			Sage         map[string]interface{} `json:"sage"`
		} `json:"metadata"`
	} `json:"agents"`
}

func main() {
	fmt.Println(" Generating secp256k1 keys using SAGE library...")
	fmt.Println("================================================")

	// Agent names
	agents := []string{"root", "ordering", "planning", "payment"}

	var generatedKeys []AgentKeyData

	for _, name := range agents {
		fmt.Printf("\nGenerating keys for %s agent...\n", name)

		// Generate secp256k1 key pair using SAGE library
		keyPair, err := keys.GenerateSecp256k1KeyPair()
		if err != nil {
			log.Fatalf("Failed to generate key pair for %s: %v", name, err)
		}

		// Get the private key as ECDSA
		privateKey := keyPair.PrivateKey().(*ecdsa.PrivateKey)

		// Get public key bytes (uncompressed format with 0x04 prefix)
		publicKeyBytes := ethcrypto.FromECDSAPub(&privateKey.PublicKey)
		publicKeyHex := "0x" + hex.EncodeToString(publicKeyBytes)

		// Get private key bytes
		privateKeyBytes := ethcrypto.FromECDSA(privateKey)
		privateKeyHex := hex.EncodeToString(privateKeyBytes)

		// Get Ethereum address from public key
		address := ethcrypto.PubkeyToAddress(privateKey.PublicKey)

		// Create DID
		did := fmt.Sprintf("did:sage:ethereum:%s", address.Hex())

		keyData := AgentKeyData{
			Name:       name,
			DID:        did,
			PublicKey:  publicKeyHex,
			PrivateKey: privateKeyHex,
			Address:    address.Hex(),
		}

		generatedKeys = append(generatedKeys, keyData)

		fmt.Printf("   Generated for %s:\n", name)
		fmt.Printf("     Address: %s\n", address.Hex())
		fmt.Printf("     DID: %s\n", did)
		fmt.Printf("     Public Key Length: %d bytes\n", len(publicKeyBytes))
		fmt.Printf("     Key Format: Uncompressed (0x04 prefix)\n")

		// Save individual key file for the agent
		keyFileName := fmt.Sprintf("../keys/%s.key", name)
		os.MkdirAll("../keys", 0755)
		err = ioutil.WriteFile(keyFileName, privateKeyBytes, 0600)
		if err != nil {
			log.Printf("Warning: Failed to save key file for %s: %v", name, err)
		} else {
			fmt.Printf("     Key saved to: %s\n", keyFileName)
		}
	}

	// Save all keys to JSON
	keysJSON, err := json.MarshalIndent(generatedKeys, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal keys to JSON: %v", err)
	}

	err = ioutil.WriteFile("generated_agent_keys.json", keysJSON, 0644)
	if err != nil {
		log.Fatalf("Failed to save keys JSON: %v", err)
	}

	fmt.Println("\n================================================")
	fmt.Println(" Keys generated successfully!")
	fmt.Println(" Keys saved to: generated_agent_keys.json")
	fmt.Println("\n Next Steps:")
	fmt.Println("1. Copy the generated DIDs and public keys")
	fmt.Println("2. Update sage-fe/demo-agents-metadata.json with the new values")
	fmt.Println("3. Re-run the agent registration script")
	fmt.Println("\nExample update for demo-agents-metadata.json:")
	fmt.Println("------------------------------------------------")

	for _, key := range generatedKeys {
		fmt.Printf("\n%s agent:\n", key.Name)
		fmt.Printf("  \"did\": \"%s\",\n", key.DID)
		fmt.Printf("  \"publicKey\": \"%s\",\n", key.PublicKey)
	}

	fmt.Println("\n  IMPORTANT: These keys are for demo purposes only!")
	fmt.Println("Never use them in production!")
}
