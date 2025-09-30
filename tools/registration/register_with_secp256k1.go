package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"path/filepath"
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

// AgentKey holds the key information for an agent
type AgentKey struct {
	DID        string `json:"did"`
	Name       string `json:"name"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	Address    string `json:"address"`
}

// DemoAgent from demo file
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
	keysDir := flag.String("keys", "keys", "Directory containing agent keys")
	contractAddr := flag.String("contract", "0x5FbDB2315678afecb367f032d93F642f64180aa3", "Contract address")
	rpcURL := flag.String("rpc", "http://localhost:8545", "RPC URL")
	privateKeyHex := flag.String("key", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", "Private key (without 0x)")
	abiPath := flag.String("abi", "../sage/contracts/ethereum/artifacts/contracts/SageRegistryV2.sol/SageRegistryV2.json", "ABI file path")
	flag.Parse()

	// Load demo data
	demoData, err := loadDemoData(*demoFile)
	if err != nil {
		log.Fatalf("Failed to load demo data: %v", err)
	}

	// Load agent keys
	agentKeys, err := loadAgentKeys(*keysDir)
	if err != nil {
		log.Fatalf("Failed to load agent keys: %v", err)
	}

	// Create registration manager
	manager, err := NewRegistrationManager(*rpcURL, *contractAddr, *privateKeyHex, *abiPath)
	if err != nil {
		log.Fatalf("Failed to create registration manager: %v", err)
	}

	// Register each agent
	fmt.Println(" Starting Agent Registration with secp256k1 keys")
	fmt.Println("================================================")
	fmt.Printf(" Contract: %s\n", *contractAddr)
	fmt.Printf(" RPC: %s\n", *rpcURL)
	fmt.Printf(" Registrar: %s\n", manager.fromAddress.Hex())
	fmt.Println("================================================\n")

	for _, agent := range demoData.Agents {
		// Find the corresponding key
		agentKey, found := agentKeys[agent.Name]
		if !found {
			log.Printf(" No key found for %s, skipping", agent.Name)
			continue
		}

		// Update the agent's public key with the secp256k1 key
		agent.Metadata.PublicKey = agentKey.PublicKey

		if err := manager.RegisterAgent(agent, agentKey); err != nil {
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

func loadAgentKeys(keysDir string) (map[string]AgentKey, error) {
	allKeysFile := filepath.Join(keysDir, "all_keys.json")
	data, err := ioutil.ReadFile(allKeysFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read keys file: %w", err)
	}

	var keyStore struct {
		Agents []AgentKey `json:"agents"`
	}

	if err := json.Unmarshal(data, &keyStore); err != nil {
		return nil, fmt.Errorf("failed to parse keys JSON: %w", err)
	}

	// Create map for easy lookup
	keys := make(map[string]AgentKey)
	for _, key := range keyStore.Agents {
		keys[key.Name] = key
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

func (rm *RegistrationManager) RegisterAgent(agent DemoAgent, agentKey AgentKey) error {
	fmt.Printf("\n Registering %s...\n", agent.Name)
	fmt.Printf("   DID: %s\n", agent.DID)
	fmt.Printf("   Type: %s\n", agent.Metadata.Type)
	fmt.Printf("   Endpoint: %s\n", agent.Metadata.Endpoint)
	fmt.Printf("   Address: %s\n", agentKey.Address)

	// Convert capabilities to JSON
	capabilitiesJSON, err := json.Marshal(agent.Metadata.Capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	// Decode public key (remove 0x prefix if present)
	publicKeyHex := strings.TrimPrefix(agentKey.PublicKey, "0x")
	publicKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}

	// Create signature for registration
	// Using the agent's own key to sign the registration
	messageHash := crypto.Keccak256Hash(
		[]byte("SAGE Key Registration:"),
		rm.chainID.Bytes(),
		rm.contractAddress.Bytes(),
		rm.fromAddress.Bytes(),
		crypto.Keccak256(publicKeyBytes),
	)

	// Sign with the registrar's key (not the agent's key)
	signature, err := crypto.Sign(messageHash.Bytes(), rm.privateKey)
	if err != nil {
		return fmt.Errorf("failed to create signature: %w", err)
	}

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
		uint64(500000), // Gas limit
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