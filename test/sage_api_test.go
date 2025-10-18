package main

import (
	"fmt"
	"log"
	"os"

	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/crypto/storage"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func main() {
	fmt.Println("=== SAGE API Test ===")
	fmt.Println()

	// Test 1: Key generation
	fmt.Println("Test 1: Key Generation")
	fmt.Println("----------------------")

	// Test Ed25519
	ed25519KeyPair, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		log.Printf(" Ed25519 generation failed: %v", err)
	} else {
		fmt.Printf(" Ed25519 key generated\n")
		fmt.Printf("   Type: %s\n", ed25519KeyPair.Type())
		fmt.Printf("   ID: %s\n", ed25519KeyPair.ID())
	}

	// Test Secp256k1
	secp256k1KeyPair, err := keys.GenerateSecp256k1KeyPair()
	if err != nil {
		log.Printf(" Secp256k1 generation failed: %v", err)
	} else {
		fmt.Printf(" Secp256k1 key generated\n")
		fmt.Printf("   Type: %s\n", secp256k1KeyPair.Type())
		fmt.Printf("   ID: %s\n", secp256k1KeyPair.ID())
	}
	fmt.Println()

	// Test 2: Storage
	fmt.Println("Test 2: Key Storage")
	fmt.Println("-------------------")

	testDir := "/tmp/sage_test_keys"
	os.RemoveAll(testDir) // Clean up any previous test

	// Create storage
	fileStorage, err := storage.NewFileKeyStorage(testDir)
	if err != nil {
		log.Fatalf(" Failed to create storage: %v", err)
	}
	fmt.Printf(" File storage created at: %s\n", testDir)

	// Store key
	testKeyID := "test-agent-key"
	err = fileStorage.Store(testKeyID, secp256k1KeyPair)
	if err != nil {
		log.Printf(" Failed to store key: %v", err)
	} else {
		fmt.Printf(" Key stored with ID: %s\n", testKeyID)
	}

	// Check if key exists
	exists := fileStorage.Exists(testKeyID)
	fmt.Printf("   Key exists: %v\n", exists)

	// Load key back
	loadedKey, err := fileStorage.Load(testKeyID)
	if err != nil {
		log.Printf(" Failed to load key: %v", err)
	} else {
		fmt.Printf(" Key loaded successfully\n")
		fmt.Printf("   Loaded Type: %s\n", loadedKey.Type())
		fmt.Printf("   Loaded ID: %s\n", loadedKey.ID())
	}

	// List keys
	keyList, err := fileStorage.List()
	if err != nil {
		log.Printf(" Failed to list keys: %v", err)
	} else {
		fmt.Printf(" Keys in storage: %v\n", keyList)
	}
	fmt.Println()

	// Test 3: DID types
	fmt.Println("Test 3: DID Types")
	fmt.Println("-----------------")

	// Check AgentMetadata structure
	metadata := &did.AgentMetadata{}
	fmt.Printf("AgentMetadata fields:\n")
	fmt.Printf("   DID: %T\n", metadata.DID)
	fmt.Printf("   Name: %T\n", metadata.Name)
	fmt.Printf("   IsActive: %T\n", metadata.IsActive)
	fmt.Printf("   CreatedAt: %T\n", metadata.CreatedAt)
	fmt.Printf("   UpdatedAt: %T\n", metadata.UpdatedAt)
	fmt.Println()

	// Test 4: Crypto Manager
	fmt.Println("Test 4: Crypto Manager")
	fmt.Println("----------------------")

	manager := sagecrypto.NewManager()
	manager.SetStorage(fileStorage)

	// Generate through manager
	managerKey, err := manager.GenerateKeyPair(sagecrypto.KeyTypeSecp256k1)
	if err != nil {
		log.Printf(" Manager generation failed: %v", err)
	} else {
		fmt.Printf(" Key generated via manager\n")
		fmt.Printf("   Type: %s\n", managerKey.Type())
	}

	// Store through manager
	err = manager.StoreKeyPair(managerKey)
	if err != nil {
		log.Printf(" Manager store failed: %v", err)
	} else {
		fmt.Printf(" Key stored via manager\n")
	}

	// List through manager
	managerList, err := manager.ListKeyPairs()
	if err != nil {
		log.Printf(" Manager list failed: %v", err)
	} else {
		fmt.Printf(" Keys via manager: %v\n", managerList)
	}

	// Clean up
	os.RemoveAll(testDir)
	fmt.Println()
	fmt.Println("=== Test Complete ===")
}
