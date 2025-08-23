package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ethereum/go-ethereum/crypto"
)

// AgentKey holds the key information for an agent
type AgentKey struct {
	DID        string `json:"did"`
	Name       string `json:"name"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	Address    string `json:"address"`
}

// KeyStore holds all agent keys
type KeyStore struct {
	Agents []AgentKey `json:"agents"`
}

func main() {
	// Parse flags
	outputDir := flag.String("output", "keys", "Output directory for keys")
	demoFile := flag.String("demo", "../sage-fe/demo-agents-metadata.json", "Path to demo metadata file")
	flag.Parse()

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Load demo metadata to get agent names and DIDs
	demoData, err := ioutil.ReadFile(*demoFile)
	if err != nil {
		log.Fatalf("Failed to read demo file: %v", err)
	}

	var demo struct {
		Agents []struct {
			Name string `json:"name"`
			DID  string `json:"did"`
		} `json:"agents"`
	}

	if err := json.Unmarshal(demoData, &demo); err != nil {
		log.Fatalf("Failed to parse demo file: %v", err)
	}

	keyStore := KeyStore{
		Agents: []AgentKey{},
	}

	fmt.Println("🔑 Generating secp256k1 keys for agents...")
	fmt.Println(strings.Repeat("=", 51))

	for _, agent := range demo.Agents {
		// Generate secp256k1 key pair
		privateKey, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			log.Printf("Failed to generate key for %s: %v", agent.Name, err)
			continue
		}

		// Get public key
		publicKey := privateKey.PubKey()

		// Convert to Ethereum format
		ecdsaPubKey := publicKey.ToECDSA()

		// Get Ethereum address
		address := crypto.PubkeyToAddress(*ecdsaPubKey)

		// Serialize keys
		privateKeyHex := hex.EncodeToString(privateKey.Serialize())
		publicKeyHex := hex.EncodeToString(publicKey.SerializeCompressed())

		agentKey := AgentKey{
			DID:        agent.DID,
			Name:       agent.Name,
			PrivateKey: privateKeyHex,
			PublicKey:  publicKeyHex,
			Address:    address.Hex(),
		}

		keyStore.Agents = append(keyStore.Agents, agentKey)

		// Save individual key file
		keyFile := filepath.Join(*outputDir, fmt.Sprintf("%s.key", agent.Name))
		keyData := map[string]string{
			"did":        agent.DID,
			"privateKey": privateKeyHex,
			"publicKey":  publicKeyHex,
			"address":    address.Hex(),
			"type":       "secp256k1",
		}

		keyJSON, err := json.MarshalIndent(keyData, "", "  ")
		if err != nil {
			log.Printf("Failed to marshal key for %s: %v", agent.Name, err)
			continue
		}

		if err := ioutil.WriteFile(keyFile, keyJSON, 0600); err != nil {
			log.Printf("Failed to save key for %s: %v", agent.Name, err)
			continue
		}

		fmt.Printf("✅ Generated key for %s\n", agent.Name)
		fmt.Printf("   Address: %s\n", address.Hex())
		fmt.Printf("   Public Key: %s\n", publicKeyHex)
		fmt.Printf("   Saved to: %s\n", keyFile)
		fmt.Println()
	}

	// Save all keys to a single file
	allKeysFile := filepath.Join(*outputDir, "all_keys.json")
	allKeysJSON, err := json.MarshalIndent(keyStore, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal all keys: %v", err)
	}

	if err := ioutil.WriteFile(allKeysFile, allKeysJSON, 0600); err != nil {
		log.Fatalf("Failed to save all keys: %v", err)
	}

	fmt.Println(strings.Repeat("=", 51))
	fmt.Printf("🎉 Key generation complete!\n")
	fmt.Printf("📁 Keys saved to: %s\n", *outputDir)
	fmt.Printf("📋 All keys: %s\n", allKeysFile)
}