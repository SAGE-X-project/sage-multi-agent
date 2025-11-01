package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

// AgentKeyInfo represents the stored key information (SAGE format)
type AgentKeyInfo struct {
	Type       string `json:"type"`
	Address    string `json:"address"`
	DID        string `json:"did"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

func main() {
	// Parse command line flags
	var (
		amountEther = flag.String("amount", "0.1", "Amount of ETH to send to each agent")
		rpcURL      = flag.String("rpc", "http://localhost:8545", "RPC endpoint")
		privateKey  = flag.String("key", "", "Private key of the funding account (without 0x)")
		keysDir     = flag.String("keys", "keys", "Directory containing agent keys")
		envFile     = flag.String("env", ".env", "Path to .env file")
		dryRun      = flag.Bool("dry-run", false, "Dry run - don't actually send transactions")
	)
	flag.Parse()

	// Load .env file
	if err := godotenv.Load(*envFile); err != nil {
		fmt.Printf("Warning: Could not load .env file: %v\n", err)
	}

	// Get private key from env if not provided
	if *privateKey == "" {
		*privateKey = os.Getenv("REGISTRATION_PRIVATE_KEY")
		if *privateKey == "" {
			// Use Hardhat default account #0
			*privateKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
			fmt.Println("Using default Hardhat account #0 for funding")
		}
	}

	// Remove 0x prefix if present
	*privateKey = strings.TrimPrefix(*privateKey, "0x")

	// Connect to Ethereum client
	client, err := ethclient.Dial(*rpcURL)
	if err != nil {
		fmt.Printf("Failed to connect to Ethereum client: %v\n", err)
		os.Exit(1)
	}

	// Load funding account private key
	fundingPrivateKey, err := crypto.HexToECDSA(*privateKey)
	if err != nil {
		fmt.Printf("Failed to parse private key: %v\n", err)
		os.Exit(1)
	}

	// Get funding account address
	publicKey := fundingPrivateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		fmt.Printf("Error casting public key to ECDSA\n")
		os.Exit(1)
	}
	fundingAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	// Get funding account balance
	balance, err := client.BalanceAt(context.Background(), fundingAddress, nil)
	if err != nil {
		fmt.Printf("Failed to get balance: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("========================================\n")
	fmt.Printf(" Agent Funding Tool\n")
	fmt.Printf("========================================\n")
	fmt.Printf("Funding Account: %s\n", fundingAddress.Hex())
	fmt.Printf("Balance: %s ETH\n", weiToEther(balance))
	fmt.Printf("Amount per agent: %s ETH\n", *amountEther)
	fmt.Printf("========================================\n\n")

	// Parse amount to send
	amountFloat := new(big.Float)
	amountFloat.SetString(*amountEther)
	amountWei := new(big.Int)
	amountFloat.Mul(amountFloat, big.NewFloat(1e18))
	amountFloat.Int(amountWei)

	// Find all agent key files
	agents := []string{"root", "medical", "planning", "payment"}
	totalFunded := 0

	for _, agentType := range agents {
		// Read key file directly (SAGE stores keys as JSON)
		keyPath := fmt.Sprintf("%s/%s.key", *keysDir, agentType)
		keyData, err := ioutil.ReadFile(keyPath)
		if err != nil {
			// Try with _agent suffix
			keyPath = fmt.Sprintf("%s/%s_agent.key", *keysDir, agentType)
			keyData, err = ioutil.ReadFile(keyPath)
			if err != nil {
				fmt.Printf("  Could not read key for %s: %v\n", agentType, err)
				continue
			}
		}

		var keyInfo AgentKeyInfo
		if err := json.Unmarshal(keyData, &keyInfo); err != nil {
			fmt.Printf("  Could not parse key for %s: %v\n", agentType, err)
			continue
		}

		// Skip if no address
		if keyInfo.Address == "" {
			fmt.Printf("  No address found for %s agent\n", agentType)
			continue
		}

		agentAddress := common.HexToAddress(keyInfo.Address)

		// Check agent's current balance
		agentBalance, err := client.BalanceAt(context.Background(), agentAddress, nil)
		if err != nil {
			fmt.Printf(" Failed to get balance for %s: %v\n", agentType, err)
			continue
		}

		fmt.Printf(" %s Agent:\n", strings.Title(agentType))
		fmt.Printf("   Address: %s\n", agentAddress.Hex())
		fmt.Printf("   Current Balance: %s ETH\n", weiToEther(agentBalance))

		// Skip if already has sufficient balance
		if agentBalance.Cmp(amountWei) >= 0 {
			fmt.Printf("    Already has sufficient balance\n\n")
			continue
		}

		if *dryRun {
			fmt.Printf("    [DRY RUN] Would send %s ETH\n\n", *amountEther)
			totalFunded++
			continue
		}

		// Get nonce
		nonce, err := client.PendingNonceAt(context.Background(), fundingAddress)
		if err != nil {
			fmt.Printf("    Failed to get nonce: %v\n\n", err)
			continue
		}

		// Get gas price
		gasPrice, err := client.SuggestGasPrice(context.Background())
		if err != nil {
			fmt.Printf("    Failed to get gas price: %v\n\n", err)
			continue
		}

		// Get chain ID
		chainID, err := client.NetworkID(context.Background())
		if err != nil {
			fmt.Printf("    Failed to get chain ID: %v\n\n", err)
			continue
		}

		// Create transaction
		tx := types.NewTransaction(
			nonce,
			agentAddress,
			amountWei,
			uint64(21000), // Standard gas limit for ETH transfer
			gasPrice,
			nil, // No data for simple ETH transfer
		)

		// Sign transaction
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), fundingPrivateKey)
		if err != nil {
			fmt.Printf("    Failed to sign transaction: %v\n\n", err)
			continue
		}

		// Send transaction
		err = client.SendTransaction(context.Background(), signedTx)
		if err != nil {
			fmt.Printf("    Failed to send transaction: %v\n\n", err)
			continue
		}

		fmt.Printf("    Sent %s ETH\n", *amountEther)
		fmt.Printf("   TX Hash: %s\n", signedTx.Hash().Hex())

		// Wait for confirmation
		receipt, err := bind.WaitMined(context.Background(), client, signedTx)
		if err != nil {
			fmt.Printf("     Failed to wait for confirmation: %v\n\n", err)
			continue
		}

		if receipt.Status == 0 {
			fmt.Printf("    Transaction failed\n\n")
			continue
		}

		fmt.Printf("    Confirmed in block %d\n\n", receipt.BlockNumber.Uint64())
		totalFunded++
	}

	fmt.Printf("========================================\n")
	if *dryRun {
		fmt.Printf(" DRY RUN COMPLETE - Would fund %d agents\n", totalFunded)
	} else {
		fmt.Printf(" Successfully funded %d agents\n", totalFunded)
	}
	fmt.Printf("========================================\n")
}

// weiToEther converts wei to ether string
func weiToEther(wei *big.Int) string {
	ether := new(big.Float).SetInt(wei)
	ether.Quo(ether, big.NewFloat(1e18))
	return fmt.Sprintf("%.6f", ether)
}
