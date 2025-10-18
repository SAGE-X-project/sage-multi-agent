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
	"strconv"
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

	// Added: two modes only ('local' default, and 'self-signed')
	mode := flag.String("mode", "local", "Registration mode: 'local' (default) or 'self-signed'")

	// Added: defaults chosen to satisfy SageRegistryV2 without any flags
	sigMode := flag.String("sigmode", "bytes32", "Signature mode: 'bytes' (len-prefixed), 'bytes32' (\\n32), or 'raw' (no prefix)")
	senderInDigest := flag.String("sender-in-digest", "auto", "Sender in digest: 'auto' (default), 'registrar', or 'agent'")
	didInDigest := flag.String("did-in-digest", "none", "Include DID in digest: 'none' (default), 'string', or 'hash'")
	keyHashMode := flag.String("keyhash-mode", "raw", "Key hash mode: 'raw'(keccak(publicKey as sent)), 'xy'(keccak(X||Y)), 'compressed'(keccak(compressed))")

	// Added: cooldown/backoff so fast loops won’t hit cooldown reverts
	cooldownWaitSec := flag.Int("cooldown-wait-sec", 65, "Seconds to wait when cooldown is active")
	cooldownRetries := flag.Int("cooldown-retries", 5, "Max retries when cooldown is active")

	// Added: optional hook disable (owner only)
	disableHooks := flag.Bool("disable-hooks", false, "If true, attempt to disable before/after hooks (owner only)")

	// Added: self-signed funding
	fundingKeyHex := flag.String("funding-key", "", "Private key (without 0x) to fund agents for self-signed mode")
	fundingAmountWei := flag.String("funding-amount-wei", "10000000000000000", "ETH amount in wei to fund each agent (default 0.01 ETH)")

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

	// Added: optionally disable hooks
	if *disableHooks {
		if err := manager.DisableHooks(); err != nil {
			fmt.Printf("   Warning: Failed to disable hooks: %v\n", err)
		} else {
			fmt.Println("   Hooks disabled (before/after)")
		}
	}

	// Added: parse funding key for self-signed if provided
	// NOTE: Also allow optional pre-funding in local mode if --funding-key is supplied.
	var fundingKey *ecdsa.PrivateKey
	if *fundingKeyHex != "" {
		fundingKey, err = crypto.HexToECDSA(strings.TrimPrefix(*fundingKeyHex, "0x"))
		if err != nil {
			log.Fatalf("Failed to parse funding key: %v", err)
		}
	}

	fmt.Println(" Starting Agent Registration on Local Blockchain")
	fmt.Println("================================================")
	fmt.Printf(" Contract: %s\n", *contractAddr)
	fmt.Printf(" RPC: %s\n", *rpcURL)
	fmt.Printf(" Registrar: %s\n", manager.fromAddress.Hex())
	fmt.Printf(" Mode: %s | SigMode: %s | SenderInDigest: %s | DidInDigest: %s | KeyHashMode: %s\n", *mode, *sigMode, *senderInDigest, *didInDigest, *keyHashMode)
	fmt.Println("================================================\n")

	// --- Funding Step (optional) ---
	// NOTE: If --funding-key is provided, pre-fund each agent address from the keys file (both in local and self-signed modes).
	if fundingKey != nil {
		fmt.Println(" Step 1: Funding agents with ETH...")
		amt, ok := new(big.Int).SetString(*fundingAmountWei, 10)
		if !ok {
			log.Fatalf("invalid funding-amount-wei: %s", *fundingAmountWei)
		}
		funderAddr := crypto.PubkeyToAddress(fundingKey.PublicKey)
		fmt.Printf(" Funding from: %s\n", funderAddr.Hex())

		for _, agent := range demoData.Agents {
			// find matching key by name
			var akey *AgentKeyData
			for i := range agentKeys {
				if agentKeys[i].Name == agent.Name {
					akey = &agentKeys[i]
					break
				}
			}
			if akey == nil {
				fmt.Printf("   %s: no key found in keys file, skipping funding\n", agent.Name)
				continue
			}
			addr := common.HexToAddress(akey.Address)

			// Optional: skip if already has sufficient balance
			bal, _ := manager.client.BalanceAt(context.Background(), addr, nil)
			if bal != nil && bal.Cmp(amt) >= 0 {
				fmt.Printf("   %s: %s already has >= %s wei, skipping\n", agent.Name, addr.Hex(), amt.String())
				continue
			}

			if err := manager.fundAddress(fundingKey, addr, amt); err != nil {
				fmt.Printf("   %s: funding failed: %v\n", agent.Name, err)
				continue
			}
			fmt.Printf("   %s: funded %s wei -> %s\n", agent.Name, amt.String(), addr.Hex())
			time.Sleep(1 * time.Second)
		}
		fmt.Println()
	}

	// --- Registration Step ---
	stepLabel := "Step 2"
	if fundingKey == nil {
		stepLabel = "Step 1"
	}
	fmt.Printf(" %s: Agent registration...\n", stepLabel)

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

		// Added: align metadata public key with keyfile at runtime to avoid mismatches
		if strings.TrimPrefix(agent.Metadata.PublicKey, "0x") != strings.TrimPrefix(agentKey.PublicKey, "0x") {
			fmt.Printf("   Notice: metadata publicKey != keyfile publicKey for %s; replacing with keyfile publicKey\n", agent.Name)
			agent.Metadata.PublicKey = agentKey.PublicKey
		}

		switch strings.ToLower(*mode) {
		case "self-signed":
			if err := manager.SelfRegisterAgent(agent, *agentKey, *sigMode, *senderInDigest, *didInDigest, *keyHashMode, *cooldownWaitSec, *cooldownRetries, fundingKey, *fundingAmountWei); err != nil {
				log.Printf(" Failed to register %s: %v", agent.Name, err)
				continue
			}
		default: // local
			if err := manager.RegisterAgentWithKey(agent, agentKey, *sigMode, *senderInDigest, *didInDigest, *keyHashMode, *cooldownWaitSec, *cooldownRetries); err != nil {
				log.Printf(" Failed to register %s: %v", agent.Name, err)
				continue
			}
		}

		fmt.Printf(" Successfully registered %s\n", agent.Name)
		time.Sleep(2 * time.Second) // small gap
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

// Added: Disable before/after hooks via onlyOwner (best-effort; ignores error if not owner)
func (rm *RegistrationManager) DisableHooks() error {
	ctx := context.Background()

	data1, err := rm.contractABI.Pack("setBeforeRegisterHook", common.Address{})
	if err != nil {
		return fmt.Errorf("pack setBeforeRegisterHook: %w", err)
	}
	nonce, err := rm.client.PendingNonceAt(ctx, rm.fromAddress)
	if err != nil {
		return err
	}
	gasPrice, err := rm.client.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}
	tx1 := types.NewTransaction(nonce, rm.contractAddress, big.NewInt(0), 200000, gasPrice, data1)
	signed1, err := types.SignTx(tx1, types.NewEIP155Signer(rm.chainID), rm.privateKey)
	if err != nil {
		return err
	}
	if err := rm.client.SendTransaction(ctx, signed1); err != nil {
		return err
	}
	if _, err := bind.WaitMined(ctx, rm.client, signed1); err != nil {
		return err
	}

	data2, err := rm.contractABI.Pack("setAfterRegisterHook", common.Address{})
	if err != nil {
		return fmt.Errorf("pack setAfterRegisterHook: %w", err)
	}
	nonce2 := nonce + 1
	tx2 := types.NewTransaction(nonce2, rm.contractAddress, big.NewInt(0), 200000, gasPrice, data2)
	signed2, err := types.SignTx(tx2, types.NewEIP155Signer(rm.chainID), rm.privateKey)
	if err != nil {
		return err
	}
	if err := rm.client.SendTransaction(ctx, signed2); err != nil {
		return err
	}
	if _, err := bind.WaitMined(ctx, rm.client, signed2); err != nil {
		return err
	}
	return nil
}

func (rm *RegistrationManager) RegisterAgentWithKey(agent DemoAgent, agentKey *AgentKeyData, sigMode, senderInDigest, didInDigest, keyHashMode string, cooldownWaitSec, cooldownRetries int) error {
	fmt.Printf("\n Registering %s...\n", agent.Name)
	fmt.Printf("   DID: %s\n", agent.DID)
	fmt.Printf("   Type: %s\n", agent.Metadata.Type)
	fmt.Printf("   Endpoint: %s\n", agent.Metadata.Endpoint)

	// Convert capabilities to JSON
	capabilitiesJSON, err := json.Marshal(agent.Metadata.Capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	// Decode/normalize public key to uncompressed 65 bytes
	publicKeyBytes, err := ensureUncompressedSecp256k1(agent.Metadata.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to normalize public key: %w", err)
	}

	// Debug
	fmt.Printf("   Public key length: %d bytes\n", len(publicKeyBytes))
	fmt.Printf("   Public key prefix: 0x%02x\n", publicKeyBytes[0])
	if len(publicKeyBytes) == 65 && publicKeyBytes[0] == 0x04 {
		fmt.Printf("   Key format: Uncompressed secp256k1\n")
	} else {
		fmt.Printf("   Key format: Unknown\n")
	}

	// Agent key
	agentPrivateKey, err := crypto.HexToECDSA(strings.TrimPrefix(agentKey.PrivateKey, "0x"))
	if err != nil {
		return fmt.Errorf("failed to parse agent private key: %w", err)
	}
	agentAddr := crypto.PubkeyToAddress(agentPrivateKey.PublicKey)

	// Choose sender used in digest (must equal tx msg.sender)
	digestSender := rm.fromAddress // local mode → registrar sends tx
	if strings.ToLower(senderInDigest) == "agent" {
		digestSender = agentAddr
	}
	// If "auto", keep registrar here for local mode.

	// keyHash (default raw = keccak(publicKey as sent), which matches on-chain)
	keyHash := computeKeyHashFromPub(publicKeyBytes, strings.ToLower(keyHashMode))
	fmt.Printf("   Digest params -> chainID:%s, contract:%s, sender:%s, didMode:%s\n", rm.chainID.String(), rm.contractAddress.Hex(), digestSender.Hex(), strings.ToLower(didInDigest))
	fmt.Printf("   keyHash(%s): 0x%x\n", strings.ToLower(keyHashMode), keyHash)

	// Digest
	var digest common.Hash
	switch strings.ToLower(sigMode) {
	case "bytes":
		digest = makeOwnershipDigestBytesKH(rm.chainID, rm.contractAddress, digestSender, keyHash, strings.ToLower(didInDigest), agent.DID)
	case "raw":
		digest = makeOwnershipDigestRawKH(rm.chainID, rm.contractAddress, digestSender, keyHash, strings.ToLower(didInDigest), agent.DID)
	default:
		digest = makeOwnershipDigest32KH(rm.chainID, rm.contractAddress, digestSender, keyHash, strings.ToLower(didInDigest), agent.DID)
	}
	fmt.Printf("   SigMode: %s\n", strings.ToLower(sigMode))
	fmt.Printf("   Digest: 0x%x\n", digest.Bytes())

	// Sign with agent key (V2 requires key owner’s signature)
	signature, err := crypto.Sign(digest.Bytes(), agentPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to create signature: %w", err)
	}
	if signature[64] < 27 {
		signature[64] += 27
	}

	// Debug verify
	sigCopy := append([]byte(nil), signature...)
	if sigCopy[64] >= 27 {
		sigCopy[64] -= 27
	}
	if pub, err := crypto.SigToPub(digest.Bytes(), sigCopy); err == nil {
		rec := crypto.PubkeyToAddress(*pub)
		var derived common.Address
		if len(publicKeyBytes) == 65 && publicKeyBytes[0] == 0x04 {
			h := crypto.Keccak256(publicKeyBytes[1:])
			copy(derived[:], h[12:])
		}
		fmt.Printf("   Agent address (from private key): %s\n", agentAddr.Hex())
		fmt.Printf("   Recovered address (from signature): %s\n", rec.Hex())
		fmt.Printf("   Derived address (from public key bytes): %s\n", derived.Hex())
		fmt.Printf("   All match: %v\n", rec == agentAddr && agentAddr == derived)
	}
	fmt.Printf("   Signature created with agent's key (v=%d)\n", signature[64])

	// Preflight (eth_call with msg.sender = registrar)
	if err := rm.preflightUntilOk(agent, publicKeyBytes, string(capabilitiesJSON), signature, rm.fromAddress, cooldownWaitSec, cooldownRetries); err != nil {
		return err
	}

	// Send tx from registrar
	nonce, err := rm.client.PendingNonceAt(context.Background(), rm.fromAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}
	gasPrice, err := rm.client.SuggestGasPrice(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get gas price: %w", err)
	}
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
	tx := types.NewTransaction(nonce, rm.contractAddress, big.NewInt(0), uint64(3000000), gasPrice, data)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(rm.chainID), rm.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}
	if err := rm.client.SendTransaction(context.Background(), signedTx); err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}
	fmt.Printf("   TX Hash: %s\n", signedTx.Hash().Hex())
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

// Added: Self-signed mode (agent sends the transaction)
func (rm *RegistrationManager) SelfRegisterAgent(agent DemoAgent, agentKey AgentKeyData, sigMode, senderInDigest, didInDigest, keyHashMode string, cooldownWaitSec, cooldownRetries int, fundingKey *ecdsa.PrivateKey, fundingAmountWeiStr string) error {
	fmt.Printf("\n Registering %s (self-signed)...\n", agent.Name)
	fmt.Printf("   DID: %s\n", agent.DID)
	fmt.Printf("   Type: %s\n", agent.Metadata.Type)

	publicKeyBytes, err := ensureUncompressedSecp256k1(agent.Metadata.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to normalize public key: %w", err)
	}
	fmt.Printf("   Public key length: %d bytes\n", len(publicKeyBytes))
	fmt.Printf("   Public key prefix: 0x%02x\n", publicKeyBytes[0])

	agentPriv, err := crypto.HexToECDSA(strings.TrimPrefix(agentKey.PrivateKey, "0x"))
	if err != nil {
		return fmt.Errorf("failed to parse agent private key: %w", err)
	}
	agentAddr := crypto.PubkeyToAddress(agentPriv.PublicKey)
	fmt.Printf("   Agent Address: %s\n", agentAddr.Hex())

	// Optional funding
	if fundingKey != nil {
		amt, ok := new(big.Int).SetString(fundingAmountWeiStr, 10)
		if !ok {
			return fmt.Errorf("invalid funding-amount-wei: %s", fundingAmountWeiStr)
		}
		if err := rm.fundAddress(fundingKey, agentAddr, amt); err != nil {
			return fmt.Errorf("funding failed: %w", err)
		}
		fmt.Printf("   Funded %s with %s wei\n", agentAddr.Hex(), amt.String())
	}

	// Self-signed: msg.sender will be the agent
	digestSender := agentAddr
	if strings.ToLower(senderInDigest) == "registrar" {
		digestSender = rm.fromAddress
	}

	keyHash := computeKeyHashFromPub(publicKeyBytes, strings.ToLower(keyHashMode))
	fmt.Printf("   keyHash(%s): 0x%x\n", strings.ToLower(keyHashMode), keyHash)

	var digest common.Hash
	switch strings.ToLower(sigMode) {
	case "bytes":
		digest = makeOwnershipDigestBytesKH(rm.chainID, rm.contractAddress, digestSender, keyHash, strings.ToLower(didInDigest), agent.DID)
	case "raw":
		digest = makeOwnershipDigestRawKH(rm.chainID, rm.contractAddress, digestSender, keyHash, strings.ToLower(didInDigest), agent.DID)
	default:
		digest = makeOwnershipDigest32KH(rm.chainID, rm.contractAddress, digestSender, keyHash, strings.ToLower(didInDigest), agent.DID)
	}

	signature, err := crypto.Sign(digest.Bytes(), agentPriv)
	if err != nil {
		return fmt.Errorf("failed to sign: %w", err)
	}
	if signature[64] < 27 {
		signature[64] += 27
	}

	// Preflight (eth_call with msg.sender = agent)
	if err := rm.preflightUntilOk(agent, publicKeyBytes, string(mustJSON(agent.Metadata.Capabilities)), signature, agentAddr, cooldownWaitSec, cooldownRetries); err != nil {
		return err
	}

	// Build tx data
	caps := string(mustJSON(agent.Metadata.Capabilities))
	data, err := rm.contractABI.Pack(
		"registerAgent",
		agent.DID,
		agent.Metadata.Name,
		agent.Metadata.Description,
		agent.Metadata.Endpoint,
		publicKeyBytes,
		caps,
		signature,
	)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}

	nonce, err := rm.client.PendingNonceAt(context.Background(), agentAddr)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := rm.client.SuggestGasPrice(context.Background())
	if err != nil {
		return fmt.Errorf("gas price: %w", err)
	}
	tx := types.NewTransaction(nonce, rm.contractAddress, big.NewInt(0), 3000000, gasPrice, data)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(rm.chainID), agentPriv)
	if err != nil {
		return fmt.Errorf("sign tx: %w", err)
	}
	if err := rm.client.SendTransaction(context.Background(), signedTx); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	fmt.Printf("   TX Hash: %s\n", signedTx.Hash().Hex())
	receipt, err := bind.WaitMined(context.Background(), rm.client, signedTx)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
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

// Added: preflight (eth_call) with custom From (tx sender) to avoid cooldown reverts
func (rm *RegistrationManager) preflightUntilOk(agent DemoAgent, publicKeyBytes []byte, capabilities string, signature []byte, from common.Address, waitSec, maxRetries int) error {
	ctx := context.Background()
	for attempt := 0; attempt <= maxRetries; attempt++ {
		data, err := rm.contractABI.Pack(
			"registerAgent",
			agent.DID,
			agent.Metadata.Name,
			agent.Metadata.Description,
			agent.Metadata.Endpoint,
			publicKeyBytes,
			capabilities,
			signature,
		)
		if err != nil {
			return fmt.Errorf("preflight pack: %w", err)
		}

		msg := ethereum.CallMsg{
			From: from, // must equal tx sender so msg.sender aligns in _validatePublicKey
			To:   &rm.contractAddress,
			Data: data,
		}

		_, callErr := rm.client.CallContract(ctx, msg, nil)
		if callErr == nil {
			return nil
		}
		errStr := strings.ToLower(callErr.Error())
		if strings.Contains(errStr, "registration cooldown active") {
			if attempt == maxRetries {
				return fmt.Errorf("cooldown still active after %d retries", maxRetries)
			}
			fmt.Printf("   Preflight: cooldown active; waiting %ds then retrying (%d/%d)\n", waitSec, attempt+1, maxRetries)
			time.Sleep(time.Duration(waitSec) * time.Second)
			continue
		}
		return fmt.Errorf("preflight revert: %s", callErr.Error())
	}
	return nil
}

// Added: fund address utility for self-signed mode
func (rm *RegistrationManager) fundAddress(funder *ecdsa.PrivateKey, to common.Address, amount *big.Int) error {
	ctx := context.Background()
	from := crypto.PubkeyToAddress(funder.PublicKey)
	nonce, err := rm.client.PendingNonceAt(ctx, from)
	if err != nil {
		return err
	}
	gasPrice, err := rm.client.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}
	tx := types.NewTransaction(nonce, to, amount, 21000, gasPrice, nil)
	signed, err := types.SignTx(tx, types.NewEIP155Signer(rm.chainID), funder)
	if err != nil {
		return err
	}
	if err := rm.client.SendTransaction(ctx, signed); err != nil {
		return err
	}
	_, err = bind.WaitMined(ctx, rm.client, signed)
	return err
}

// Added: JSON helper
func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// Added: Normalize public key to 65-byte uncompressed form (0x04 + X + Y).
func ensureUncompressedSecp256k1(pubHex string) ([]byte, error) {
	h := strings.TrimPrefix(pubHex, "0x")
	raw, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key hex: %w", err)
	}
	if len(raw) == 65 && raw[0] == 0x04 {
		return raw, nil
	}
	if len(raw) == 33 && (raw[0] == 0x02 || raw[0] == 0x03) {
		pub, err := crypto.DecompressPubkey(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress public key: %w", err)
		}
		return crypto.FromECDSAPub(pub), nil
	}
	if len(raw) == 64 {
		return append([]byte{0x04}, raw...), nil
	}
	return nil, fmt.Errorf("unexpected public key length: %d", len(raw))
}

// Added: computeKeyHashFromPub supports multiple on-chain variants (raw/xy/compressed)
func computeKeyHashFromPub(uncompressed []byte, mode string) []byte {
	switch mode {
	case "raw":
		// keccak(uncompressed publicKey exactly as sent to the contract)
		return crypto.Keccak256(uncompressed)
	case "compressed":
		// keccak(compressed 33B)
		if len(uncompressed) != 65 || uncompressed[0] != 0x04 {
			return crypto.Keccak256(uncompressed)
		}
		x := uncompressed[1:33]
		y := uncompressed[33:65]
		yOdd := (y[len(y)-1] & 1) == 1
		prefix := byte(0x02)
		if yOdd {
			prefix = 0x03
		}
		comp := make([]byte, 33)
		comp[0] = prefix
		copy(comp[1:], x)
		return crypto.Keccak256(comp)
	default: // "xy"
		// keccak(X||Y)
		if len(uncompressed) == 65 && uncompressed[0] == 0x04 {
			return crypto.Keccak256(uncompressed[1:])
		}
		return crypto.Keccak256(uncompressed)
	}
}

// Added: Build digest using ECDSA.toEthSignedMessageHash(bytes32) path ("\n32") with precomputed keyHash and optional DID
func makeOwnershipDigest32KH(chainID *big.Int, contractAddr, sender common.Address, keyHash []byte, didMode, did string) common.Hash {
	chainID32 := make([]byte, 32)
	chainID.FillBytes(chainID32)

	var buf bytes.Buffer
	buf.Write([]byte("SAGE Key Registration:"))
	buf.Write(chainID32)
	buf.Write(contractAddr.Bytes())
	buf.Write(sender.Bytes())
	buf.Write(keyHash)

	switch didMode {
	case "string":
		buf.Write([]byte(did))
	case "hash":
		buf.Write(crypto.Keccak256([]byte(did)))
	}

	challenge := crypto.Keccak256(buf.Bytes())
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	return crypto.Keccak256Hash(append(prefix, challenge...))
}

// Added: Build digest using ECDSA.toEthSignedMessageHash(bytes) path (length-prefixed), with precomputed keyHash and optional DID
func makeOwnershipDigestBytesKH(chainID *big.Int, contractAddr, sender common.Address, keyHash []byte, didMode, did string) common.Hash {
	chainID32 := make([]byte, 32)
	chainID.FillBytes(chainID32)

	var payload bytes.Buffer
	payload.Write([]byte("SAGE Key Registration:"))
	payload.Write(chainID32)
	payload.Write(contractAddr.Bytes())
	payload.Write(sender.Bytes())
	payload.Write(keyHash)

	switch didMode {
	case "string":
		payload.Write([]byte(did))
	case "hash":
		payload.Write(crypto.Keccak256([]byte(did)))
	}

	lenDec := []byte(strconv.Itoa(payload.Len()))
	var pref bytes.Buffer
	pref.Write([]byte("\x19Ethereum Signed Message:\n"))
	pref.Write(lenDec)

	full := append(pref.Bytes(), payload.Bytes()...)
	return crypto.Keccak256Hash(full)
}

// Added: Build "raw" digest (no eth-sign prefix), with precomputed keyHash and optional DID
func makeOwnershipDigestRawKH(chainID *big.Int, contractAddr, sender common.Address, keyHash []byte, didMode, did string) common.Hash {
	chainID32 := make([]byte, 32)
	chainID.FillBytes(chainID32)

	var payload bytes.Buffer
	payload.Write([]byte("SAGE Key Registration:"))
	payload.Write(chainID32)
	payload.Write(contractAddr.Bytes())
	payload.Write(sender.Bytes())
	payload.Write(keyHash)

	switch didMode {
	case "string":
		payload.Write([]byte(did))
	case "hash":
		payload.Write(crypto.Keccak256([]byte(did)))
	}

	return crypto.Keccak256Hash(payload.Bytes())
}
