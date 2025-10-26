//go:build reg_kem
// +build reg_kem

// tools/registration/register_kem_agents.go
// SPDX-License-Identifier: MIT
//
// Register agents (self-signed) and include an extra X25519 (HPKE) KEM key
// in the same Register() call.
//  - ECDSA key: from signing-keys JSON (required; self-signed flow)
//  - X25519 key: from kem-keys JSON (32 bytes public key; no signature)
//
// 변경점:
//  - Register에 사용할 DID를 signing-keys가 아니라 **KEM JSON의 DID**로 사용.
//  - Register 전에 해당 DID가 이미 온체인에 있으면, **addKey(X25519)** 로 추가 등록.
//    (owner 제약: 등록에 사용한 같은 ECDSA 개인키로 트랜잭션 전송)
//
// 사용법:
//   go run -tags=reg_kem tools/registration/register_kem_agents.go \
//     -contract 0x... \
//     -rpc http://127.0.0.1:8545 \
//     -signing-keys ./generated_agent_keys.json \
//     -kem-keys ./keys/kem/generated_kem_keys.json \
//     -agents "ordering,planing,payment,external"

package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	// go-ethereum
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	// SAGE packages
	"github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

/********** input & meta types **********/

type signingRow struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`  // secp256k1 hex (65B 0x04... 권장; 33B compressed 가능)
	PrivateKey string `json:"privateKey"` // agent EOA (self-signed tx sender)
	Address    string `json:"address"`
}

type kemAgentRow struct {
	Name          string `json:"name"`
	DID           string `json:"did,omitempty"` // 사용 (Register/AddKey에 쓸 DID)
	X25519Public  string `json:"x25519Public"`  // 32B (hex)
	X25519Private string `json:"x25519Private,omitempty"`
}

type kemFile struct {
	Agents []kemAgentRow `json:"agents"`
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

/********** main **********/

func main() {
	// Flags (register_agents.go 스타일과 동일한 느낌)
	contract := flag.String("contract", "", "SageRegistryV4 (proxy) address (env SAGE_REGISTRY_V4_ADDRESS or default)")
	rpcURL := flag.String("rpc", "", "RPC URL (env ETH_RPC_URL or default)")

	signingPath := flag.String("signing-keys", "generated_agent_keys.json", "Signing summary JSON (array)")
	kemPath := flag.String("kem-keys", "keys/kem/generated_kem_keys.json", "KEM JSON (object with agents[] OR top-level array)")

	agentsFilter := flag.String("agents", "", "Comma-separated agent names to process (default: ALL)")

	// Client config
	confirmationBlocks := flag.Int("confirm", 0, "Blocks to wait for confirmation (default 0)")
	maxRetries := flag.Int("retries", 24, "Max polling attempts while waiting for receipts")
	gasPriceWei := flag.Uint64("gas-price-wei", 0, "Static gas price in wei (0 = node suggestion)")

	flag.Parse()

	// Defaults from env if empty
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

	// Load inputs
	signingRows, err := loadSigning(*signingPath)
	if err != nil {
		fatalf("load signing keys: %v", err)
	}
	kemRows, err := loadKEM(*kemPath)
	if err != nil {
		fatalf("load KEM keys: %v", err)
	}

	// Resolve agent filter
	selected := parseAgentsFilter(*agentsFilter)
	if len(selected) == 0 {
		selected = parseAgentsFilter(os.Getenv("SAGE_AGENTS"))
	}

	// Build agent metas (env 기반 메타 채움)
	agents := buildAgentsFromSigning(signingRows, selected)

	fmt.Println("======================================")
	fmt.Println(" SAGE V4 Agent Registration (ECDSA + X25519 via Register/AddKey, KEM DID)")
	fmt.Println("======================================")
	fmt.Printf(" RPC:      %s\n", *rpcURL)
	fmt.Printf(" Contract: %s\n", *contract)
	fmt.Printf(" Signing:  %s\n", shortPath(*signingPath))
	fmt.Printf(" KEM:      %s\n", shortPath(*kemPath))
	if *agentsFilter != "" {
		fmt.Printf(" Agents:   %s\n", *agentsFilter)
	} else if os.Getenv("SAGE_AGENTS") != "" {
		fmt.Printf(" Agents:   %s (from env SAGE_AGENTS)\n", os.Getenv("SAGE_AGENTS"))
	} else {
		fmt.Println(" Agents:   ALL (from signing-keys)")
	}
	fmt.Println("======================================")

	// View-only client for pre-check & verification
	viewCfg := &did.RegistryConfig{
		RPCEndpoint:     *rpcURL,
		ContractAddress: *contract,
		PrivateKey:      "",
	}
	viewClient, err := dideth.NewEthereumClientV4(viewCfg)
	if err != nil {
		fatalf("init view client: %v", err)
	}

	// Register per agent (self-signed) or add X25519 to existing DID
	for _, a := range agents {
		// 1) find signing row
		sk := findSigning(signingRows, a.Name)
		if sk == nil || strings.TrimSpace(sk.PrivateKey) == "" {
			fmt.Printf(" - %s: missing signing privateKey -> skip\n", a.Name)
			continue
		}

		// 2) normalize ECDSA pubkey (65B uncompressed)
		if normHex(a.Metadata.PublicKey) != normHex(sk.PublicKey) {
			fmt.Printf("   notice: metadata publicKey != keyfile publicKey for %s; replacing\n", a.Name)
			a.Metadata.PublicKey = sk.PublicKey
		}
		pubBytes, err := ensureUncompressed(a.Metadata.PublicKey)
		if err != nil || len(pubBytes) != 65 || pubBytes[0] != 0x04 {
			fmt.Printf("   Invalid ECDSA public key for %s: %v\n", a.Name, err)
			continue
		}

		// 3) find KEM row (required) + DID from KEM JSON
		kr := findKEM(kemRows, a.Name)
		if kr == nil {
			fmt.Printf(" - %s: missing KEM row -> skip\n", a.Name)
			continue
		}
		if strings.TrimSpace(kr.X25519Public) == "" {
			fmt.Printf(" - %s: missing x25519Public -> skip\n", a.Name)
			continue
		}
		targetDID := strings.TrimSpace(kr.DID)
		if targetDID == "" {
			fmt.Printf(" - %s: KEM JSON has empty DID -> skip (set agents[].did)\n", a.Name)
			continue
		}
		xpub, err := hexDecode32(kr.X25519Public)
		if err != nil {
			fmt.Printf(" - %s: invalid x25519Public: %v -> skip\n", a.Name, err)
			continue
		}

		// 4) agent private key (sender = agent EOA / owner)
		agentPriv, err := gethcrypto.HexToECDSA(normHex(sk.PrivateKey))
		if err != nil {
			fmt.Printf("   Agent key parse failed for %s: %v\n", a.Name, err)
			continue
		}

		// 5) per-agent tx client
		perAgentCfg := &did.RegistryConfig{
			RPCEndpoint:        *rpcURL,
			ContractAddress:    *contract,
			PrivateKey:         normHex(sk.PrivateKey),
			GasPrice:           *gasPriceWei,
			MaxRetries:         *maxRetries,
			ConfirmationBlocks: *confirmationBlocks,
		}
		client, err := dideth.NewEthereumClientV4(perAgentCfg)
		if err != nil {
			fmt.Printf("   init client failed for %s: %v\n", a.Name, err)
			continue
		}

		// 6) pre-check: DID already registered?
		if _, err := viewClient.Resolve(context.Background(), did.AgentDID(targetDID)); err == nil {
			// 이미 등록됨 → addKey(X25519)
			agentID, err := computeAgentID(targetDID, pubBytes)
			if err != nil {
				fmt.Printf("   compute agentId failed for %s: %v\n", a.Name, err)
				continue
			}
			if err := addKEMKey(
				context.Background(),
				*rpcURL,
				*contract,
				normHex(sk.PrivateKey),
				uint64(*gasPriceWei),
				agentID,
				xpub,
			); err != nil {
				fmt.Printf("   addKey(X25519) failed for %s: %v\n", a.Name, err)
				continue
			}
			fmt.Printf(" - %s: DID already existed; X25519 key added via addKey\n", a.Name)
			// 약간의 딜레이 (UI/log 가독성)
			time.Sleep(900 * time.Millisecond)
			continue
		}

		// 7) 새 등록: self-signed signature (ECDSA first key)
		sig, err := signSelfRegistrationMessage(agentPriv, targetDID, pubBytes)
		if err != nil {
			fmt.Printf("   Failed to sign message for %s: %v\n", a.Name, err)
			continue
		}

		// 8) Build Register request: Keys[0]=ECDSA(with signature), Keys[1]=X25519(no signature)
		req := &did.RegistrationRequest{
			DID:         did.AgentDID(targetDID),
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
					Signature: sig, // signed by agentPriv (msg.sender = agent)
				},
				{
					Type:    did.KeyTypeX25519,
					KeyData: xpub, // no signature needed
				},
			},
		}

		// 9) Register
		fmt.Printf("\n Registering %s as DID=%s (ECDSA+X25519)...\n", a.Name, targetDID)
		res, err := client.Register(context.Background(), req)
		if err != nil {
			fmt.Printf("   Failed: %v\n", err)
			continue
		}
		fmt.Printf("   TX: %s | Block: %d | GasUsed: %d\n", res.TransactionHash, res.BlockNumber, res.GasUsed)
		time.Sleep(1200 * time.Millisecond)
	}

	// Verify
	fmt.Println("\nVerification:")
	for _, a := range agents {
		kr := findKEM(kemRows, a.Name)
		if kr == nil || strings.TrimSpace(kr.DID) == "" {
			fmt.Printf(" - %s: (no KEM DID) skip verify\n", a.Name)
			continue
		}
		if _, err := viewClient.Resolve(context.Background(), did.AgentDID(strings.TrimSpace(kr.DID))); err == nil {
			fmt.Printf(" - %s: Registered (DID=%s)\n", a.Name, strings.TrimSpace(kr.DID))
		} else {
			fmt.Printf(" - %s: Not found (%v)\n", a.Name, err)
		}
	}
}

/********** helpers (mostly from register_agents.go) **********/

func loadSigning(path string) ([]signingRow, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []signingRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func loadKEM(path string) ([]kemAgentRow, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Primary shape: {"agents":[...]}
	var k kemFile
	if json.Unmarshal(b, &k) == nil && len(k.Agents) > 0 {
		return k.Agents, nil
	}
	// Fallback: top-level array
	var arr []kemAgentRow
	if json.Unmarshal(b, &arr) == nil && len(arr) > 0 {
		return arr, nil
	}
	return nil, fmt.Errorf("unrecognized KEM json (need object with agents[])")
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

func findSigning(all []signingRow, name string) *signingRow {
	for i := range all {
		if all[i].Name == name { // register_agents.go와 동일 (대소문자 구분)
			return &all[i]
		}
	}
	return nil
}

func findKEM(all []kemAgentRow, name string) *kemAgentRow {
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

func hexDecode32(h string) ([]byte, error) {
	b, err := hex.DecodeString(normHex(h))
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	return b, nil
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

	// msg.sender = agent address (tx sender = agent EOA)
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

/********** new helpers for addKey path **********/

// computeAgentID replicates _generateAgentId(did, firstKey) = keccak256(abi.encode(did, firstKey))
func computeAgentID(didStr string, firstKey []byte) ([32]byte, error) {
	stringType, _ := abi.NewType("string", "", nil)
	bytesType, _ := abi.NewType("bytes", "", nil)
	args := abi.Arguments{
		{Type: stringType},
		{Type: bytesType},
	}
	packed, err := args.Pack(didStr, firstKey)
	if err != nil {
		return [32]byte{}, err
	}
	h := gethcrypto.Keccak256Hash(packed)
	return [32]byte(h), nil
}

// addKEMKey calls registry.addKey(agentId, X25519, xpub, emptySig)
// 사용 조건: msg.sender == 해당 agentId의 owner (register 시 사용한 EOA)
func addKEMKey(ctx context.Context, rpcURL, contractAddr, privHex string, gasPriceWei uint64, agentID [32]byte, x25519Pub []byte) error {
	if len(x25519Pub) != 32 {
		return fmt.Errorf("x25519 pub must be 32 bytes")
	}

	// Prepare client & transactor
	cli, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer cli.Close()

	chainID, err := cli.NetworkID(ctx)
	if err != nil {
		return fmt.Errorf("network id: %w", err)
	}

	priv, err := gethcrypto.HexToECDSA(strings.TrimPrefix(privHex, "0x"))
	if err != nil {
		return fmt.Errorf("priv parse: %w", err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(priv, chainID)
	if err != nil {
		return fmt.Errorf("transactor: %w", err)
	}
	if gasPriceWei > 0 {
		auth.GasPrice = new(big.Int).SetUint64(gasPriceWei)
	}

	// Minimal ABI for addKey(bytes32,uint8,bytes,bytes)
	const registryABI = `[{"inputs":[{"internalType":"bytes32","name":"agentId","type":"bytes32"},{"internalType":"uint8","name":"keyType","type":"uint8"},{"internalType":"bytes","name":"keyData","type":"bytes"},{"internalType":"bytes","name":"signature","type":"bytes"}],"name":"addKey","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"nonpayable","type":"function"}]`

	parsed, err := abi.JSON(strings.NewReader(registryABI))
	if err != nil {
		return fmt.Errorf("parse abi: %w", err)
	}
	addr := common.HexToAddress(contractAddr)
	contract := bind.NewBoundContract(addr, parsed, cli, cli, cli)

	// enum KeyType.X25519 == 2 (컨트랙트/SDK 동일 가정). SDK 쓰면 캐스팅해서 사용.
	keyType := uint8(did.KeyTypeX25519) // 보통 2

	// signature는 반드시 빈 바이트
	tx, err := contract.Transact(auth, "addKey", agentID, keyType, x25519Pub, []byte{})
	if err != nil {
		return fmt.Errorf("addKey tx: %w", err)
	}
	fmt.Printf("   addKey(X25519) tx: %s\n", tx.Hash().Hex())
	return nil
}

/********** env/metadata helpers (same semantics as register_agents.go) **********/

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

func buildAgentsFromSigning(keys []signingRow, filter []string) []DemoAgent {
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
		a.DID = k.DID // 메타 채우기용(실제 Register/추가는 KEM JSON의 DID 사용)
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

func shortPath(p string) string { return filepath.Clean(p) }

func fatalf(f string, a ...interface{}) {
	fmt.Printf("Error: "+f+"\n", a...)
	os.Exit(1)
}
