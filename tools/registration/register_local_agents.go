//go:build tools && registration_local
// +build tools,registration_local

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// AgentMetadata from demo file
type DemoAgent struct {
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
	} `json:"metadata"`
}

// DemoData structure
type DemoData struct {
	Agents []DemoAgent `json:"agents"`
}

// AgentKeyData structure for loading generated keys
type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	Address    string `json:"address"`
}

// RegistrationManager handles agent registration
type RegistrationManager struct {
	client          *ethclient.Client
	contractAddress common.Address
	contractABI     abi.ABI
	privateKey      *ecdsa.PrivateKey
	chainID         *big.Int
	fromAddress     common.Address
}

func main() {
	// Parse flags
	demoFile := flag.String("demo", "../sage-fe/demo-agents-metadata.json", "Path to demo metadata file")
	contractAddr := flag.String("contract", "0x5FbDB2315678afecb367f032d93F642f64180aa3", "Contract address")
	rpcURL := flag.String("rpc", "http://localhost:8545", "RPC URL")
	privateKeyHex := flag.String("key", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", "Private key (without 0x)")
	abiPath := flag.String("abi", "../sage/contracts/ethereum/artifacts/contracts/SageRegistryV2.sol/SageRegistryV2.json", "ABI file path")
	keysFile := flag.String("keys", "generated_agent_keys.json", "Path to generated agent keys file")
	flag.Parse()

	// Load demo data
	demoData, err := loadDemoData(*demoFile)
	if err != nil {
		log.Fatalf("Failed to load demo data: %v", err)
	}

	// Load agent keys
	agentKeys, err := loadAgentKeys(*keysFile)
	if err != nil {
		log.Fatalf("Failed to load agent keys: %v", err)
	}

	// Create registration manager
	manager, err := NewRegistrationManager(*rpcURL, *contractAddr, *privateKeyHex, *abiPath)
	if err != nil {
		log.Fatalf("Failed to create registration manager: %v", err)
	}

	// Register each agent
	fmt.Println(" Starting Agent Registration on Local Blockchain")
	fmt.Println("================================================")
	fmt.Printf(" Contract: %s\n", *contractAddr)
	fmt.Printf(" RPC: %s\n", *rpcURL)
	fmt.Printf(" Registrar: %s\n", manager.fromAddress.Hex())
	fmt.Println("================================================\n")

	for _, agent := range demoData.Agents {
		// Find the corresponding agent key
		var agentKey *AgentKeyData
		for _, key := range agentKeys {
			if key.Name == agent.Name {
				agentKey = &key
				break
			}
		}
		if agentKey == nil {
			log.Printf(" No key found for agent %s", agent.Name)
			continue
		}

		if err := manager.RegisterAgentWithKey(agent, agentKey); err != nil {
			log.Printf(" Failed to register %s: %v", agent.Name, err)
			continue
		}
		fmt.Printf(" Successfully registered %s\n", agent.Name)
		time.Sleep(2 * time.Second) // Wait between registrations
	}

	fmt.Println("\n================================================")
	fmt.Println(" Agent Registration Complete!")
	fmt.Println("================================================")

	// Verify registrations
	fmt.Println("\n Verifying Registrations:")
	for _, agent := range demoData.Agents {
		if registered, err := manager.VerifyRegistration(agent.DID); err != nil {
			fmt.Printf("   %s: Error checking - %v\n", agent.Name, err)
		} else if registered {
			fmt.Printf("   %s: Registered\n", agent.Name)
		} else {
			fmt.Printf("   %s: Not found\n", agent.Name)
		}
	}
}

func loadDemoData(filepath string) (*DemoData, error) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var demo DemoData
	if err := json.Unmarshal(data, &demo); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &demo, nil
}

func loadAgentKeys(filepath string) ([]AgentKeyData, error) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read keys file: %w", err)
	}

	var keys []AgentKeyData
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("failed to parse keys JSON: %w", err)
	}

	return keys, nil
}

func NewRegistrationManager(rpcURL, contractAddr, privateKeyHex, abiPath string) (*RegistrationManager, error) {
	// Connect to client
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to client: %w", err)
	}

	// Load private key
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Get from address
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	// Get chain ID
	chainID, err := client.NetworkID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get network ID: %w", err)
	}

	// Load ABI
	abiJSON, err := ioutil.ReadFile(abiPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ABI file: %w", err)
	}

	var artifact struct {
		ABI json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(abiJSON, &artifact); err != nil {
		return nil, fmt.Errorf("failed to parse ABI artifact: %w", err)
	}

	contractABI, err := abi.JSON(strings.NewReader(string(artifact.ABI)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract ABI: %w", err)
	}

	return &RegistrationManager{
		client:          client,
		contractAddress: common.HexToAddress(contractAddr),
		contractABI:     contractABI,
		privateKey:      privateKey,
		chainID:         chainID,
		fromAddress:     fromAddress,
	}, nil
}

func (rm *RegistrationManager) RegisterAgentWithKey(agent DemoAgent, agentKey *AgentKeyData) error {
	fmt.Printf("\n Registering %s...\n", agent.Name)
	fmt.Printf("   DID: %s\n", agent.DID)
	fmt.Printf("   Type: %s\n", agent.Metadata.Type)
	fmt.Printf("   Endpoint: %s\n", agent.Metadata.Endpoint)

	// Convert capabilities to JSON
	capabilitiesJSON, err := json.Marshal(agent.Metadata.Capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	// Decode public key (remove 0x prefix if present)
	publicKeyHex := strings.TrimPrefix(agent.Metadata.PublicKey, "0x")
	publicKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}
	
	// Debug: Check public key format
	fmt.Printf("   Public key length: %d bytes\n", len(publicKeyBytes))
	fmt.Printf("   Public key prefix: 0x%02x\n", publicKeyBytes[0])
	if len(publicKeyBytes) == 65 && publicKeyBytes[0] == 0x04 {
		fmt.Printf("   Key format: Uncompressed secp256k1\n")
	} else if len(publicKeyBytes) == 33 && (publicKeyBytes[0] == 0x02 || publicKeyBytes[0] == 0x03) {
		fmt.Printf("   Key format: Compressed secp256k1\n")
	} else {
		fmt.Printf("   Key format: Unknown\n")
	}

	// Load agent's private key to prove ownership
	agentPrivateKey, err := crypto.HexToECDSA(agentKey.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to parse agent private key: %w", err)
	}

	// Create the message that proves key ownership
	// The contract expects a specific message format to verify ownership
	// Important: This must match EXACTLY what the contract creates
	keyHash := crypto.Keccak256(publicKeyBytes)
	
	// Build the challenge message exactly as Solidity does
	// abi.encodePacked concatenates without padding
	var buf bytes.Buffer
	buf.Write([]byte("SAGE Key Registration:"))
	// Chain ID as uint256 (32 bytes, big-endian)
	chainIDBytes := make([]byte, 32)
	rm.chainID.FillBytes(chainIDBytes)
	buf.Write(chainIDBytes)
	// Contract address (20 bytes)
	buf.Write(rm.contractAddress.Bytes())
	// msg.sender (20 bytes)
	buf.Write(rm.fromAddress.Bytes())
	// keyHash (32 bytes)
	buf.Write(keyHash)
	
	challenge := crypto.Keccak256(buf.Bytes())

	// Add Ethereum message prefix (the contract adds this too)
	// "\x19Ethereum Signed Message:\n32" + challenge
	ethMessagePrefix := []byte("\x19Ethereum Signed Message:\n32")
	messageHash := crypto.Keccak256Hash(append(ethMessagePrefix, challenge...))

	// Sign with the agent's private key to prove ownership
	signature, err := crypto.Sign(messageHash.Bytes(), agentPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to create signature: %w", err)
	}
	
	// Ethereum requires v to be 27 or 28 (not 0 or 1)
	// go-ethereum's crypto.Sign returns v as 0 or 1, so we need to add 27
	if signature[64] < 27 {
		signature[64] += 27
	}

	// Debug: Verify the signature locally
	// Create a copy of signature for local verification (with v = 0 or 1)
	sigCopy := make([]byte, len(signature))
	copy(sigCopy, signature)
	if sigCopy[64] >= 27 {
		sigCopy[64] -= 27
	}
	
	recoveredPubKey, err := crypto.SigToPub(messageHash.Bytes(), sigCopy)
	if err != nil {
		fmt.Printf("   Warning: Failed to recover public key: %v\n", err)
	} else {
		recoveredAddr := crypto.PubkeyToAddress(*recoveredPubKey)
		agentAddr := crypto.PubkeyToAddress(agentPrivateKey.PublicKey)
		
		// Also derive address from publicKeyBytes like the contract does
		var derivedAddr common.Address
		if len(publicKeyBytes) == 65 && publicKeyBytes[0] == 0x04 {
			// Remove 0x04 prefix and hash the remaining 64 bytes
			keyWithoutPrefix := publicKeyBytes[1:]
			hash := crypto.Keccak256(keyWithoutPrefix)
			// Take last 20 bytes
			copy(derivedAddr[:], hash[12:])
		}
		
		fmt.Printf("   Agent address (from private key): %s\n", agentAddr.Hex())
		fmt.Printf("   Recovered address (from signature): %s\n", recoveredAddr.Hex())
		fmt.Printf("   Derived address (from public key bytes): %s\n", derivedAddr.Hex())
		fmt.Printf("   All match: %v\n", recoveredAddr == agentAddr && agentAddr == derivedAddr)
	}

	fmt.Printf("   Signature created with agent's key\n")

	// Prepare transaction
	nonce, err := rm.client.PendingNonceAt(context.Background(), rm.fromAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := rm.client.SuggestGasPrice(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get gas price: %w", err)
	}

	// Pack the function call
	data, err := rm.contractABI.Pack(
		"registerAgent",
		agent.DID,
		agent.Metadata.Name,
		agent.Metadata.Description,
		agent.Metadata.Endpoint,
		publicKeyBytes,
		string(capabilitiesJSON),
		signature,
	)
	if err != nil {
		return fmt.Errorf("failed to pack contract call: %w", err)
	}

	// Create transaction
	tx := types.NewTransaction(
		nonce,
		rm.contractAddress,
		big.NewInt(0),
		uint64(3000000), // Increased gas limit for complex validation
		gasPrice,
		data,
	)

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(rm.chainID), rm.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	err = rm.client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	fmt.Printf("   TX Hash: %s\n", signedTx.Hash().Hex())

	// Wait for confirmation
	receipt, err := bind.WaitMined(context.Background(), rm.client, signedTx)
	if err != nil {
		return fmt.Errorf("failed to wait for transaction: %w", err)
	}

	if receipt.Status == 0 {
		return fmt.Errorf("transaction failed")
	}

	fmt.Printf("   Block: %d\n", receipt.BlockNumber.Uint64())
	fmt.Printf("   Gas Used: %d\n", receipt.GasUsed)

	return nil
}

func (rm *RegistrationManager) VerifyRegistration(did string) (bool, error) {
	// Pack the getAgentByDID function call
	data, err := rm.contractABI.Pack("getAgentByDID", did)
	if err != nil {
		return false, fmt.Errorf("failed to pack contract call: %w", err)
	}

	// Call the contract
	msg := ethereum.CallMsg{
		To:   &rm.contractAddress,
		Data: data,
	}

	result, err := rm.client.CallContract(context.Background(), msg, nil)
	if err != nil {
		// If error contains "Agent not found", the agent is not registered
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}

	// If we got a result, the agent exists
	return len(result) > 0, nil
}
