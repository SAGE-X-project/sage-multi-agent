// tools/registration/register_agents.go
// SPDX-License-Identifier: MIT

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

// -------- Demo formats --------

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
type DemoData struct {
	Agents []DemoAgent `json:"agents"`
}

type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	Address    string `json:"address"`
}

// -------- ABI tuple for V4 --------
//
//	struct RegistrationParams {
//	  string did;
//	  string name;
//	  string description;
//	  string endpoint;
//	  string capabilities;
//	  KeyType[] keyTypes; // enum -> uint8
//	  bytes[] keyData;
//	  bytes[] signatures;
//	}
type RegistrationParams struct {
	Did          string   `abi:"did"`
	Name         string   `abi:"name"`
	Description  string   `abi:"description"`
	Endpoint     string   `abi:"endpoint"`
	Capabilities string   `abi:"capabilities"`
	KeyTypes     []uint8  `abi:"keyTypes"`
	KeyData      [][]byte `abi:"keyData"`
	Signatures   [][]byte `abi:"signatures"`
}

// -------- Manager --------

type RegistrationManager struct {
	cli             *ethclient.Client
	contractAddress common.Address
	contractABI     abi.ABI
	chainID         *big.Int
}

func main() {
	// Flags (V4 = self-signed flow)
	demoFile := flag.String("demo", "../sage-fe/demo-agents-metadata.json", "Path to demo metadata file")
	abiPath := flag.String("abi", "../sage/contracts/ethereum/artifacts/contracts/SageRegistryV4.sol/SageRegistryV4.json", "ABI artifact (case-sensitive)")
	contract := flag.String("contract", "", "SageRegistryV4 (proxy) address")
	rpcURL := flag.String("rpc", "http://localhost:8545", "RPC URL")

	keysFile := flag.String("keys", "generated_agent_keys.json", "Path to generated agent keys file")

	// Optional funding for agent EOAs
	fundingKeyHex := flag.String("funding-key", "", "Funder private key (without 0x)")
	fundingAmountWei := flag.String("funding-amount-wei", "10000000000000000", "Wei to fund per agent (default 0.01 ETH)")

	flag.Parse()
	if strings.TrimSpace(*contract) == "" {
		log.Fatal("missing --contract (SageRegistryV4 proxy address)")
	}

	// Load inputs
	demo, err := loadDemo(*demoFile)
	if err != nil {
		log.Fatalf("load demo: %v", err)
	}
	keys, err := loadKeys(*keysFile)
	if err != nil {
		log.Fatalf("load keys: %v", err)
	}

	rm, err := NewRegistrationManager(*rpcURL, *contract, *abiPath)
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	fmt.Println("======================================")
	fmt.Println(" SAGE V4 Agent Registration (self-signed)")
	fmt.Println("======================================")
	fmt.Printf(" RPC:      %s\n", *rpcURL)
	fmt.Printf(" Contract: %s\n", *contract)
	fmt.Printf(" ABI:      %s\n", filepath.Base(*abiPath))
	fmt.Println("======================================")

	// Optional funding
	var funderPriv *ecdsa.PrivateKey
	if fk := strings.TrimSpace(*fundingKeyHex); fk != "" {
		p, e := crypto.HexToECDSA(strings.TrimPrefix(fk, "0x"))
		if e != nil {
			log.Fatalf("funding key parse: %v", e)
		}
		funderPriv = p
	}
	if funderPriv != nil {
		fmt.Println("\n[Step 1] Funding agent EOAs...")
		amt, ok := new(big.Int).SetString(*fundingAmountWei, 10)
		if !ok || amt.Sign() <= 0 {
			log.Fatalf("invalid --funding-amount-wei: %s", *fundingAmountWei)
		}
		if err := rm.FundAgentsIfNeeded(funderPriv, keys, amt); err != nil {
			log.Fatalf("funding error: %v", err)
		}
		fmt.Println("Funding done.")
	}

	step := 1
	if funderPriv != nil {
		step = 2
	}
	fmt.Printf("\n[Step %d] Registering agents (self-signed)...\n", step)

	for _, a := range demo.Agents {
		ak := findKey(keys, a.Name)
		if ak == nil {
			fmt.Printf(" - %s: no matching key in keys file, skip\n", a.Name)
			continue
		}
		// Align metadata publicKey with keyfile
		if normHex(a.Metadata.PublicKey) != normHex(ak.PublicKey) {
			fmt.Printf("   notice: metadata publicKey != keyfile publicKey for %s; replacing\n", a.Name)
			a.Metadata.PublicKey = ak.PublicKey
		}
		if err := rm.RegisterSelfSignedV4(a, *ak); err != nil {
			fmt.Printf("   Failed to register %s: %v\n", a.Name, err)
			continue
		}
		fmt.Printf("   Registered %s\n", a.Name)
		time.Sleep(1200 * time.Millisecond)
	}

	// Verify
	fmt.Println("\nVerification:")
	for _, a := range demo.Agents {
		ok, err := rm.ExistsByDID(a.DID)
		if err != nil {
			fmt.Printf(" - %s: error: %v\n", a.Name, err)
			continue
		}
		if ok {
			fmt.Printf(" - %s: Registered\n", a.Name)
		} else {
			fmt.Printf(" - %s: Not found\n", a.Name)
		}
	}
}

func NewRegistrationManager(rpcURL, contractAddr, abiPath string) (*RegistrationManager, error) {
	cli, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("ethclient dial: %w", err)
	}

	abiJSON, err := ioutil.ReadFile(abiPath)
	if err != nil {
		return nil, fmt.Errorf("read ABI: %w", err)
	}
	var artifact struct {
		ABI json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(abiJSON, &artifact); err != nil {
		return nil, fmt.Errorf("parse ABI artifact: %w", err)
	}
	cABI, err := abi.JSON(strings.NewReader(string(artifact.ABI)))
	if err != nil {
		return nil, fmt.Errorf("parse ABI: %w", err)
	}

	chainID, err := cli.NetworkID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("network id: %w", err)
	}

	return &RegistrationManager{
		cli:             cli,
		contractAddress: common.HexToAddress(contractAddr),
		contractABI:     cABI,
		chainID:         chainID,
	}, nil
}

// RegisterSelfSignedV4 builds RegistrationParams and sends tx from the agent EOA.
func (rm *RegistrationManager) RegisterSelfSignedV4(a DemoAgent, k AgentKeyData) error {
	fmt.Printf("\n Registering %s...\n", a.Name)
	fmt.Printf("   DID: %s\n", a.DID)
	fmt.Printf("   Endpoint: %s\n", a.Metadata.Endpoint)

	// 1) Normalize public key to 65B uncompressed
	pubBytes, err := ensureUncompressed(a.Metadata.PublicKey)
	if err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	if len(pubBytes) != 65 || pubBytes[0] != 0x04 {
		return errors.New("public key must be uncompressed 65B")
	}

	// 2) Agent EOA
	agentPriv, err := crypto.HexToECDSA(normHex(k.PrivateKey))
	if err != nil {
		return fmt.Errorf("agent key parse: %w", err)
	}
	agentAddr := crypto.PubkeyToAddress(agentPriv.PublicKey)
	fmt.Printf("   Agent: %s\n", agentAddr.Hex())

	// 3) agentId = keccak256(abi.encode(did, firstKey))
	agentID, err := solidityKeccakAgentID(a.DID, pubBytes)
	if err != nil {
		return fmt.Errorf("agentId encode: %w", err)
	}

	// 4) Per-key signature exactly as contract does
	const KeyTypeECDSA = 1
	keyTypes := []uint8{KeyTypeECDSA}
	keyData := [][]byte{pubBytes}
	sigs := make([][]byte, 1)

	msgHash, err := solidityKeccakMessage(agentID, pubBytes, agentAddr) // nonce=0
	if err != nil {
		return fmt.Errorf("message encode: %w", err)
	}
	ethSigned := personalHash32(msgHash)
	sig, err := crypto.Sign(ethSigned[:], agentPriv)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	sigs[0] = sig

	// 5) Capabilities JSON
	capsJSON, _ := json.Marshal(a.Metadata.Capabilities)

	params := RegistrationParams{
		Did:          a.DID,
		Name:         a.Metadata.Name,
		Description:  a.Metadata.Description,
		Endpoint:     a.Metadata.Endpoint,
		Capabilities: string(capsJSON),
		KeyTypes:     keyTypes,
		KeyData:      keyData,
		Signatures:   sigs,
	}

	// 6) Preflight (eth_call with From = agent)
	if err := rm.preflightCall(params, agentAddr); err != nil {
		return fmt.Errorf("preflight: %w", err)
	}

	// 7) Send tx from agent
	data, err := rm.contractABI.Pack("registerAgent", params)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	ctx := context.Background()
	nonce, err := rm.cli.PendingNonceAt(ctx, agentAddr)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := rm.cli.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("gas price: %w", err)
	}

	tx := types.NewTransaction(nonce, rm.contractAddress, big.NewInt(0), 3_000_000, gasPrice, data)
	signed, err := types.SignTx(tx, types.NewEIP155Signer(rm.chainID), agentPriv)
	if err != nil {
		return fmt.Errorf("sign tx: %w", err)
	}
	if err := rm.cli.SendTransaction(ctx, signed); err != nil {
		return fmt.Errorf("send tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, rm.cli, signed)
	if err != nil {
		return err
	}
	if receipt.Status == 0 {
		return errors.New("tx reverted")
	}
	fmt.Printf("   TX: %s | Block: %d | GasUsed: %d\n", signed.Hash().Hex(), receipt.BlockNumber.Uint64(), receipt.GasUsed)
	return nil
}

// ExistsByDID checks presence via getAgentByDID view.
func (rm *RegistrationManager) ExistsByDID(did string) (bool, error) {
	data, err := rm.contractABI.Pack("getAgentByDID", did)
	if err != nil {
		return false, err
	}
	msg := ethereum.CallMsg{To: &rm.contractAddress, Data: data}
	_, callErr := rm.cli.CallContract(context.Background(), msg, nil)
	if callErr != nil {
		if strings.Contains(strings.ToLower(callErr.Error()), "agent not found") {
			return false, nil
		}
		return false, callErr
	}
	return true, nil
}

// FundAgentsIfNeeded sends ETH to agent EOAs if under target amount.
func (rm *RegistrationManager) FundAgentsIfNeeded(funder *ecdsa.PrivateKey, ks []AgentKeyData, amount *big.Int) error {
	ctx := context.Background()
	from := crypto.PubkeyToAddress(funder.PublicKey)
	nonce, err := rm.cli.PendingNonceAt(ctx, from)
	if err != nil {
		return err
	}
	gasPrice, err := rm.cli.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}

	for _, k := range ks {
		addr := common.HexToAddress(k.Address)
		bal, _ := rm.cli.BalanceAt(ctx, addr, nil)
		if bal != nil && bal.Cmp(amount) >= 0 {
			fmt.Printf("   %s: balance >= %s, skip\n", addr.Hex(), amount.String())
			continue
		}
		tx := types.NewTransaction(nonce, addr, amount, 21000, gasPrice, nil)
		signed, err := types.SignTx(tx, types.NewEIP155Signer(rm.chainID), funder)
		if err != nil {
			return err
		}
		if err := rm.cli.SendTransaction(ctx, signed); err != nil {
			return err
		}
		if _, err := bind.WaitMined(ctx, rm.cli, signed); err != nil {
			return err
		}
		fmt.Printf("   funded %s wei -> %s\n", amount.String(), addr.Hex())
		nonce++
	}
	return nil
}

// -------- Helpers --------

func loadDemo(p string) (*DemoData, error) {
	b, err := ioutil.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var d DemoData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}
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
	// Uncompressed 65B (0x04 + X + Y)
	if len(raw) == 65 && raw[0] == 0x04 {
		return raw, nil
	}
	// Compressed 33B → decompress
	if len(raw) == 33 && (raw[0] == 0x02 || raw[0] == 0x03) {
		pk, err := crypto.DecompressPubkey(raw)
		if err != nil {
			return nil, err
		}
		return crypto.FromECDSAPub(pk), nil
	}
	// Raw 64B (X||Y) → add prefix 0x04
	if len(raw) == 64 {
		return append([]byte{0x04}, raw...), nil
	}
	return nil, fmt.Errorf("unexpected public key length %d", len(raw))
}

// agentId = keccak256(abi.encode(did, firstKey))
func solidityKeccakAgentID(did string, firstKey []byte) (common.Hash, error) {
	args := abi.Arguments{
		{Type: mustNewType("string")},
		{Type: mustNewType("bytes")},
	}
	packed, err := args.Pack(did, firstKey)
	if err != nil {
		return common.Hash{}, err
	}
	return crypto.Keccak256Hash(packed), nil
}

// messageHash = keccak256(abi.encode(agentId, keyBytes, msg.sender, nonce(=0)))
func solidityKeccakMessage(agentID common.Hash, keyBytes []byte, sender common.Address) (common.Hash, error) {
	args := abi.Arguments{
		{Type: mustNewType("bytes32")},
		{Type: mustNewType("bytes")},
		{Type: mustNewType("address")},
		{Type: mustNewType("uint256")},
	}
	packed, err := args.Pack(agentID, keyBytes, sender, big.NewInt(0))
	if err != nil {
		return common.Hash{}, err
	}
	return crypto.Keccak256Hash(packed), nil
}

// personal hash = keccak256("\x19Ethereum Signed Message:\n32" || msgHash)
func personalHash32(msgHash common.Hash) common.Hash {
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	return crypto.Keccak256Hash(append(prefix, msgHash.Bytes()...))
}

func mustNewType(t string) abi.Type {
	ty, err := abi.NewType(t, "", nil)
	if err != nil {
		panic(err)
	}
	return ty
}

func (rm *RegistrationManager) preflightCall(params RegistrationParams, from common.Address) error {
	data, err := rm.contractABI.Pack("registerAgent", params)
	if err != nil {
		return err
	}
	msg := ethereum.CallMsg{
		From: from,
		To:   &rm.contractAddress,
		Data: data,
	}
	_, callErr := rm.cli.CallContract(context.Background(), msg, nil)
	if callErr != nil {
		return fmt.Errorf("revert: %s", callErr.Error())
	}
	return nil
}
