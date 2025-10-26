//go:build reg_agent
// +build reg_agent

// tools/registration/register_agents.go
// SPDX-License-Identifier: MIT
//
// This CLI registers agents to a deployed SageRegistryV4 contract using the
// Ethereum V4 client. Flow:
// 1) (optional) Fund agent EOAs with --funding-key
// 2) For each agent:
//    - Build the V4 message (abi.encode(...))
//    - Sign with the agent's own ECDSA private key (personal_sign style, v in {27,28})
//    - Create a per-agent client (tx sender = that agent EOA)
//    - Call Register (self-signed)

package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	// go-ethereum
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	// SAGE packages
	"github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// ---------- Input formats (compatible with existing keys JSON) ----------

type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`  // secp256k1 hex (65B 0x04... recommended; 33B compressed allowed)
	PrivateKey string `json:"privateKey"` // hex (agent EOA; REQUIRED for self-signed tx)
	Address    string `json:"address"`
}

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

func main() {
	// Flags
	contract := flag.String("contract", "", "SageRegistryV4 (proxy) address (env SAGE_REGISTRY_V4_ADDRESS or default)")
	rpcURL := flag.String("rpc", "", "RPC URL (env ETH_RPC_URL or default)")

	// Keys file
	keysFile := flag.String("keys", "generated_agent_keys.json", "Path to generated agent keys file")

	// Agent filter
	agentsFlag := flag.String("agents", "", "Comma-separated agent names to register (overrides env SAGE_AGENTS). Default: all in keys file")

	// Funding
	fundingKeyHex := flag.String("funding-key", "", "Funder private key (hex, with or without 0x) — optional")
	fundingAmountWei := flag.String("funding-amount-wei", "10000000000000000", "Wei to fund per agent (default 0.01 ETH)")

	// Client config
	confirmationBlocks := flag.Int("confirm", 0, "Blocks to wait for confirmation (default 0)")
	maxRetries := flag.Int("retries", 24, "Max polling attempts while waiting for receipts")
	gasPriceWei := flag.Uint64("gas-price-wei", 0, "Static gas price in wei (0 = use node suggestion)")

	flag.Parse()

	// Fill with env/defaults if empty
	if strings.TrimSpace(*contract) == "" {
		if v := strings.TrimSpace(os.Getenv("SAGE_REGISTRY_V4_ADDRESS")); v != "" {
			*contract = v
		} else {
			*contract = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
		}
	}
	if strings.TrimSpace(*rpcURL) == "" {
		if v := strings.TrimSpace(os.Getenv("ETH_RPC_URL")); v != "" {
			*rpcURL = v
		} else {
			*rpcURL = "http://127.0.0.1:8545"
		}
	}

	// Load keys JSON
	keys, err := loadKeys(*keysFile)
	if err != nil {
		log.Fatalf("load keys: %v", err)
	}

	// Resolve agent filter
	selected := parseAgentsFilter(*agentsFlag)
	if len(selected) == 0 {
		selected = parseAgentsFilter(os.Getenv("SAGE_AGENTS"))
	}

	fmt.Println("======================================")
	fmt.Println(" SAGE V4 Agent Registration (self-signed)")
	fmt.Println("======================================")
	fmt.Printf(" RPC:      %s\n", *rpcURL)
	fmt.Printf(" Contract: %s\n", *contract)
	fmt.Printf(" Keys:     %s\n", filepath.Base(*keysFile))
	fmt.Println("======================================")

	// 1) (optional) Fund agent EOAs so they can pay gas
	if fk := strings.TrimSpace(*fundingKeyHex); fk != "" {
		if err := fundAgentsIfNeeded(*rpcURL, fk, keys, *fundingAmountWei); err != nil {
			log.Fatalf("funding error: %v", err)
		}
		fmt.Println("Funding done.")
	} else {
		fmt.Println("No funding key provided; agents must already have gas.")
	}

	// 2) Build agents from keys (+ env overrides)
	agents := buildAgentsFromKeys(keys, selected)

	// 3) View-only client for verification (no private key needed)
	viewCfg := &did.RegistryConfig{
		RPCEndpoint:     *rpcURL,
		ContractAddress: *contract,
		PrivateKey:      "",
	}
	viewClient, err := dideth.NewEthereumClientV4(viewCfg)
	if err != nil {
		log.Fatalf("init view client: %v", err)
	}

	// 4) Register each agent using a per-agent client/private key
	for _, a := range agents {
		k := findKey(keys, a.Name)
		if k == nil {
			fmt.Printf(" - %s: no matching key, skip\n", a.Name)
			continue
		}
		if strings.TrimSpace(k.PrivateKey) == "" {
			fmt.Printf(" - %s: missing privateKey in keys file, skip (required for self-signed)\n", a.Name)
			continue
		}

		// Normalize metadata public key
		if normHex(a.Metadata.PublicKey) != normHex(k.PublicKey) {
			fmt.Printf("   notice: metadata publicKey != keyfile publicKey for %s; replacing\n", a.Name)
			a.Metadata.PublicKey = k.PublicKey
		}

		// Uncompressed 65B pubkey
		pubBytes, err := ensureUncompressed(a.Metadata.PublicKey)
		if err != nil {
			fmt.Printf("   Failed to parse public key for %s: %v\n", a.Name, err)
			continue
		}
		if len(pubBytes) != 65 || pubBytes[0] != 0x04 {
			fmt.Printf("   Invalid public key for %s: must be 65B uncompressed\n", a.Name)
			continue
		}

		// Agent private key
		agentPriv, err := gethcrypto.HexToECDSA(normHex(k.PrivateKey))
		if err != nil {
			fmt.Printf("   Agent key parse failed for %s: %v\n", a.Name, err)
			continue
		}
		agentAddr := gethcrypto.PubkeyToAddress(agentPriv.PublicKey)

		// Build self-signed signature per contract logic
		sig, err := signSelfRegistrationMessage(agentPriv, a.DID, pubBytes)
		if err != nil {
			fmt.Printf("   Failed to sign message for %s: %v\n", a.Name, err)
			continue
		}

		// Per-agent tx client (sender = agent EOA)
		perAgentCfg := &did.RegistryConfig{
			RPCEndpoint:        *rpcURL,
			ContractAddress:    *contract,
			PrivateKey:         normHex(k.PrivateKey),
			GasPrice:           *gasPriceWei,
			MaxRetries:         *maxRetries,
			ConfirmationBlocks: *confirmationBlocks,
		}
		client, err := dideth.NewEthereumClientV4(perAgentCfg)
		if err != nil {
			fmt.Printf("   init client failed for %s: %v\n", a.Name, err)
			continue
		}

		// Build request with pre-signed ECDSA key
		req := &did.RegistrationRequest{
			DID:         did.AgentDID(a.DID),
			Name:        a.Metadata.Name,
			Description: a.Metadata.Description,
			Endpoint:    a.Metadata.Endpoint,
			Capabilities: func(m map[string]interface{}) map[string]interface{} {
				if m == nil {
					return map[string]interface{}{}
				}
				return m
			}(a.Metadata.Capabilities),
			Keys: []did.AgentKey{
				{
					Type:      did.KeyTypeECDSA,
					KeyData:   pubBytes,
					Signature: sig, // signed by agentPriv
				},
			},
		}

		fmt.Printf("\n Registering %s (agent=%s)...\n", a.Name, agentAddr.Hex())
		res, err := client.Register(context.Background(), req)
		if err != nil {
			fmt.Printf("   Failed: %v\n", err)
			continue
		}
		fmt.Printf("   TX: %s | Block: %d | GasUsed: %d\n", res.TransactionHash, res.BlockNumber, res.GasUsed)
		time.Sleep(1200 * time.Millisecond)
	}

	// 5) Verify
	fmt.Println("\nVerification:")
	for _, a := range agents {
		_, err := viewClient.Resolve(context.Background(), did.AgentDID(a.DID))
		if err == nil {
			fmt.Printf(" - %s: Registered\n", a.Name)
		} else {
			fmt.Printf(" - %s: Not found (%v)\n", a.Name, err)
		}
	}
}

// ---------- Helpers (env, keys, crypto) ----------

func loadKeys(p string) ([]AgentKeyData, error) {
	b, err := ioutil.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out []AgentKeyData
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func findKey(all []AgentKeyData, name string) *AgentKeyData {
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}

func normHex(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "0x") }

func ensureUncompressed(pubHex string) ([]byte, error) {
	raw, err := hex.DecodeString(normHex(pubHex))
	if err != nil {
		return nil, err
	}
	// Uncompressed (0x04 + X + Y)
	if len(raw) == 65 && raw[0] == 0x04 {
		return raw, nil
	}
	// Compressed → decompress
	if len(raw) == 33 && (raw[0] == 0x02 || raw[0] == 0x03) {
		pk, err := gethcrypto.DecompressPubkey(raw)
		if err != nil {
			return nil, err
		}
		return gethcrypto.FromECDSAPub(pk), nil
	}
	// Raw 64B (X||Y) → add prefix 0x04
	if len(raw) == 64 {
		return append([]byte{0x04}, raw...), nil
	}
	return nil, fmt.Errorf("unexpected public key length %d", len(raw))
}

func parseAgentsFilter(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		q := strings.TrimSpace(p)
		if q != "" {
			out = append(out, q)
		}
	}
	return out
}

func getEnvPerAgent(name, field, def string) string {
	key := "SAGE_AGENT_" + toEnvKey(name) + "_" + field
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseCapabilitiesEnv(name string) map[string]interface{} {
	key := "SAGE_AGENT_" + toEnvKey(name) + "_CAPABILITIES"
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return map[string]interface{}{}
	}
	dec := json.NewDecoder(strings.NewReader(v))
	dec.UseNumber()
	var m map[string]interface{}
	if err := dec.Decode(&m); err != nil {
		fmt.Printf("   warning: CAPABILITIES for %s is not valid JSON, ignoring\n", name)
		return map[string]interface{}{}
	}
	return m
}

func toEnvKey(name string) string {
	up := strings.ToUpper(name)
	b := make([]rune, 0, len(up))
	for _, r := range up {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		} else {
			b = append(b, '_')
		}
	}
	return string(b)
}

func buildAgentsFromKeys(keys []AgentKeyData, filter []string) []DemoAgent {
	allowAll := len(filter) == 0
	allowed := make(map[string]struct{})
	for _, n := range filter {
		allowed[n] = struct{}{}
	}

	var out []DemoAgent
	for _, k := range keys {
		if !allowAll {
			if _, ok := allowed[k.Name]; !ok {
				continue
			}
		}
		var a DemoAgent
		a.Name = k.Name
		a.DID = k.DID
		a.Metadata.Name = k.Name
		a.Metadata.Description = getEnvPerAgent(k.Name, "DESC", "SAGE Agent "+k.Name)
		a.Metadata.Version = getEnvPerAgent(k.Name, "VERSION", "0.1.0")
		a.Metadata.Type = getEnvPerAgent(k.Name, "TYPE", "")
		a.Metadata.Endpoint = getEnvPerAgent(k.Name, "ENDPOINT", "")
		a.Metadata.PublicKey = k.PublicKey
		a.Metadata.Capabilities = parseCapabilitiesEnv(k.Name)
		out = append(out, a)
	}
	return out
}

// signSelfRegistrationMessage builds the exact Solidity-encoded message the contract verifies
// using the AGENT'S own ECDSA key, and returns an Ethereum-compatible signature (v in {27,28}).
func signSelfRegistrationMessage(agentPriv *ecdsa.PrivateKey, didStr string, firstKey []byte) ([]byte, error) {
	// agentId = keccak256(abi.encode(did, firstKey))
	stringType, _ := abi.NewType("string", "", nil)
	bytesType, _ := abi.NewType("bytes", "", nil)
	args := abi.Arguments{
		{Type: stringType},
		{Type: bytesType},
	}
	agentIdPacked, err := args.Pack(didStr, firstKey)
	if err != nil {
		return nil, err
	}
	agentId := gethcrypto.Keccak256Hash(agentIdPacked)

	// msg.sender = agent address (tx sender)
	sender := gethcrypto.PubkeyToAddress(agentPriv.PublicKey)

	// messageHash = keccak256(abi.encode(agentId, keyBytes, msg.sender, nonce=0))
	bytes32Type, _ := abi.NewType("bytes32", "", nil)
	addressType, _ := abi.NewType("address", "", nil)
	uint256Type, _ := abi.NewType("uint256", "", nil)

	msgArgs := abi.Arguments{
		{Type: bytes32Type},
		{Type: bytesType},
		{Type: addressType},
		{Type: uint256Type},
	}
	msgPacked, err := msgArgs.Pack(agentId, firstKey, sender, big.NewInt(0))
	if err != nil {
		return nil, err
	}
	msgHash := gethcrypto.Keccak256Hash(msgPacked)

	// Ethereum personal sign prefix
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	ethSigned := gethcrypto.Keccak256Hash(append(prefix, msgHash.Bytes()...))

	sig, err := gethcrypto.Sign(ethSigned.Bytes(), agentPriv)
	if err != nil {
		return nil, err
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// fundAgentsIfNeeded sends plain ETH to agent EOAs if below target amount.
func fundAgentsIfNeeded(rpcURL, funderKeyHex string, ks []AgentKeyData, amountWei string) error {
	cli, err := ethclient.Dial(rpcURL)
	if err != nil {
		return err
	}
	defer cli.Close()

	amt, ok := new(big.Int).SetString(amountWei, 10)
	if !ok || amt.Sign() <= 0 {
		return errors.New("invalid funding amount")
	}

	funder, err := gethcrypto.HexToECDSA(strings.TrimPrefix(funderKeyHex, "0x"))
	if err != nil {
		return fmt.Errorf("funding key parse: %w", err)
	}
	from := gethcrypto.PubkeyToAddress(funder.PublicKey)

	chainID, err := cli.NetworkID(context.Background())
	if err != nil {
		return err
	}

	nonce, err := cli.PendingNonceAt(context.Background(), from)
	if err != nil {
		return err
	}
	gasPrice, err := cli.SuggestGasPrice(context.Background())
	if err != nil {
		return err
	}

	for _, k := range ks {
		addr := common.HexToAddress(k.Address)
		bal, _ := cli.BalanceAt(context.Background(), addr, nil)
		if bal != nil && bal.Cmp(amt) >= 0 {
			fmt.Printf("   %s: balance >= %s, skip\n", addr.Hex(), amt.String())
			continue
		}
		tx := gethtypes.NewTransaction(nonce, addr, amt, 21000, gasPrice, nil)
		signed, err := gethtypes.SignTx(tx, gethtypes.NewEIP155Signer(chainID), funder)
		if err != nil {
			return err
		}
		if err := cli.SendTransaction(context.Background(), signed); err != nil {
			return err
		}
		fmt.Printf("   funded %s wei -> %s (tx %s)\n", amt.String(), addr.Hex(), signed.Hash().Hex())
		nonce++
	}
	return nil
}
