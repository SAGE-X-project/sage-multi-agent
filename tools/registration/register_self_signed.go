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

func main() {
	// Parse flags
	demoFile := flag.String("demo", "../sage-fe/demo-agents-metadata.json", "Path to demo metadata file")
	keysDir := flag.String("keys", "keys", "Directory containing agent keys")
	contractAddr := flag.String("contract", "0x5FbDB2315678afecb367f032d93F642f64180aa3", "Contract address")
	rpcURL := flag.String("rpc", "http://localhost:8545", "RPC URL")
	fundingKeyHex := flag.String("funding-key", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", "Private key for funding agents")
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

	// Connect to client
	client, err := ethclient.Dial(*rpcURL)
	if err != nil {
		log.Fatalf("Failed to connect to client: %v", err)
	}

	// Load ABI
	contractABI, err := loadContractABI(*abiPath)
	if err != nil {
		log.Fatalf("Failed to load ABI: %v", err)
	}

	// Get chain ID
	chainID, err := client.NetworkID(context.Background())
	if err != nil {
		log.Fatalf("Failed to get network ID: %v", err)
	}

	// Load funding private key
	fundingKey, err := crypto.HexToECDSA(*fundingKeyHex)
	if err != nil {
		log.Fatalf("Failed to parse funding key: %v", err)
	}
	fundingPubKey := fundingKey.Public().(*ecdsa.PublicKey)
	fundingAddress := crypto.PubkeyToAddress(*fundingPubKey)

	fmt.Println(" Starting Self-Signed Agent Registration")
	fmt.Println("================================================")
	fmt.Printf(" Contract: %s\n", *contractAddr)
	fmt.Printf(" RPC: %s\n", *rpcURL)
	fmt.Printf(" Funding from: %s\n", fundingAddress.Hex())
	fmt.Println("================================================\n")

	// Step 1: Fund agents with ETH for gas
	fmt.Println(" Step 1: Funding agents with ETH...")
	for _, agent := range demoData.Agents {
		agentKey, found := agentKeys[agent.Name]
		if !found {
			log.Printf(" No key found for %s, skipping", agent.Name)
			continue
		}

		if err := fundAgent(client, fundingKey, chainID, agentKey.Address); err != nil {
			log.Printf(" Failed to fund %s: %v", agent.Name, err)
			continue
		}
		fmt.Printf(" Funded %s (%s)\n", agent.Name, agentKey.Address)
	}

	fmt.Println("\n Step 2: Agents self-registering...")
	contractAddress := common.HexToAddress(*contractAddr)

	// Step 2: Each agent registers itself
	for _, agent := range demoData.Agents {
		agentKey, found := agentKeys[agent.Name]
		if !found {
			continue
		}

		// Update the agent's public key with the secp256k1 key
		agent.Metadata.PublicKey = agentKey.PublicKey

		if err := selfRegisterAgent(client, contractABI, contractAddress, chainID, agent, agentKey); err != nil {
			log.Printf(" Failed to register %s: %v", agent.Name, err)
			continue
		}
		fmt.Printf(" Successfully registered %s\n", agent.Name)
		time.Sleep(2 * time.Second)
	}

	fmt.Println("\n================================================")
	fmt.Println(" Agent Registration Complete!")
	fmt.Println("================================================")

	// Verify registrations
	fmt.Println("\n Verifying Registrations:")
	for _, agent := range demoData.Agents {
		if registered, err := verifyRegistration(client, contractABI, contractAddress, agent.DID); err != nil {
			fmt.Printf("   %s: Error checking - %v\n", agent.Name, err)
		} else if registered {
			fmt.Printf("   %s: Registered\n", agent.Name)
		} else {
			fmt.Printf("   %s: Not found\n", agent.Name)
		}
	}
}

func fundAgent(client *ethclient.Client, fundingKey *ecdsa.PrivateKey, chainID *big.Int, agentAddress string) error {
	// Get nonce
	fundingPubKey := fundingKey.Public().(*ecdsa.PublicKey)
	fromAddress := crypto.PubkeyToAddress(*fundingPubKey)
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}

	// Get gas price
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get gas price: %w", err)
	}

	// Send 0.01 ETH for gas
	value := big.NewInt(10000000000000000) // 0.01 ETH
	toAddress := common.HexToAddress(agentAddress)

	// Create transaction
	tx := types.NewTransaction(
		nonce,
		toAddress,
		value,
		uint64(21000), // Standard gas limit for ETH transfer
		gasPrice,
		nil,
	)

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), fundingKey)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	// Wait for confirmation
	_, err = bind.WaitMined(context.Background(), client, signedTx)
	return err
}

func selfRegisterAgent(
	client *ethclient.Client,
	contractABI abi.ABI,
	contractAddress common.Address,
	chainID *big.Int,
	agent DemoAgent,
	agentKey AgentKey,
) error {
	fmt.Printf("\n Registering %s (self-signed)...\n", agent.Name)
	fmt.Printf("   DID: %s\n", agent.DID)
	fmt.Printf("   Address: %s\n", agentKey.Address)

	// Parse agent's private key
	privateKeyBytes, err := hex.DecodeString(agentKey.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to decode private key: %w", err)
	}

	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	agentPubKey := privateKey.Public().(*ecdsa.PublicKey)
	agentAddress := crypto.PubkeyToAddress(*agentPubKey)

	// Convert capabilities to JSON
	capabilitiesJSON, err := json.Marshal(agent.Metadata.Capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	// Decode public key
	publicKeyHex := strings.TrimPrefix(agentKey.PublicKey, "0x")
	publicKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}

	// Create the challenge that the contract expects
	challenge := crypto.Keccak256Hash(
		[]byte("SAGE Key Registration:"),
		chainID.Bytes(),
		contractAddress.Bytes(),
		agentAddress.Bytes(),
		crypto.Keccak256(publicKeyBytes),
	)

	// Sign the challenge directly (no Ethereum signed message prefix)
	// The contract adds the prefix internally
	signature, err := crypto.Sign(challenge.Bytes(), privateKey)
	if err != nil {
		return fmt.Errorf("failed to create signature: %w", err)
	}

	// Adjust v value for Ethereum (add 27)
	if signature[64] < 27 {
		signature[64] += 27
	}

	// Prepare transaction
	nonce, err := client.PendingNonceAt(context.Background(), agentAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get gas price: %w", err)
	}

	// Pack the function call
	data, err := contractABI.Pack(
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
		contractAddress,
		big.NewInt(0),
		uint64(500000), // Gas limit
		gasPrice,
		data,
	)

	// Sign transaction with agent's key
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	fmt.Printf("   TX Hash: %s\n", signedTx.Hash().Hex())

	// Wait for confirmation
	receipt, err := bind.WaitMined(context.Background(), client, signedTx)
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

	keys := make(map[string]AgentKey)
	for _, key := range keyStore.Agents {
		keys[key.Name] = key
	}

	return keys, nil
}

func loadContractABI(abiPath string) (abi.ABI, error) {
	abiJSON, err := ioutil.ReadFile(abiPath)
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to read ABI file: %w", err)
	}

	var artifact struct {
		ABI json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(abiJSON, &artifact); err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse ABI artifact: %w", err)
	}

	contractABI, err := abi.JSON(strings.NewReader(string(artifact.ABI)))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse contract ABI: %w", err)
	}

	return contractABI, nil
}

func verifyRegistration(client *ethclient.Client, contractABI abi.ABI, contractAddress common.Address, did string) (bool, error) {
	data, err := contractABI.Pack("getAgentByDID", did)
	if err != nil {
		return false, fmt.Errorf("failed to pack contract call: %w", err)
	}

	msg := ethereum.CallMsg{
		To:   &contractAddress,
		Data: data,
	}

	result, err := client.CallContract(context.Background(), msg, nil)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}

	return len(result) > 0, nil
}