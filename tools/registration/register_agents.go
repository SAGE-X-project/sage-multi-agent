//go:build reg_agent
// +build reg_agent

// tools/registration/register_agents.go
package main

import (
	"bytes"
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

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/sage-x-project/sage/pkg/agent/did"
	agentcard "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
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
	contract := flag.String("contract", "", "AgentCardRegistry address")
	rpcURL := flag.String("rpc", "", "RPC URL")
	keysFile := flag.String("keys", "generated_agent_keys.json", "Agent keys JSON")
	agentsFlag := flag.String("agents", "", "Comma-separated agent names to register")
	fundingKeyHex := flag.String("funding-key", "", "Funder private key (optional)")
	fundingAmountWei := flag.String("funding-amount-wei", "10000000000000000", "Wei to fund per agent (default 0.01 ETH)")
	tryActivate := flag.Bool("try-activate", false, "Try activation if allowed")
	waitSeconds := flag.Int("wait-seconds", 65, "Seconds between commit and reveal (>=60)")

	confirmationBlocks := flag.Int("confirm", 0, "")
	maxRetries := flag.Int("retries", 24, "")
	gasPriceWei := flag.Uint64("gas-price-wei", 0, "")
	flag.Parse()
	_, _, _ = confirmationBlocks, maxRetries, gasPriceWei

	if strings.TrimSpace(*contract) == "" {
		if v := strings.TrimSpace(os.Getenv("SAGE_REGISTRY_V4_ADDRESS")); v != "" {
			*contract = v
		} else {
			*contract = "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"
		}
	}
	if strings.TrimSpace(*rpcURL) == "" {
		if v := strings.TrimSpace(os.Getenv("ETH_RPC_URL")); v != "" {
			*rpcURL = v
		} else {
			*rpcURL = "http://127.0.0.1:8545"
		}
	}

	keys, err := loadKeys(*keysFile)
	if err != nil {
		log.Fatalf("load keys: %v", err)
	}
	selected := parseAgentsFilter(*agentsFlag)
	if len(selected) == 0 {
		selected = parseAgentsFilter(os.Getenv("SAGE_AGENTS"))
	}

	fmt.Println("======================================")
	fmt.Println(" SAGE AgentCard Registration (ECDSA)")
	fmt.Println("======================================")
	fmt.Printf(" RPC:      %s\n", *rpcURL)
	fmt.Printf(" Contract: %s\n", *contract)
	fmt.Printf(" Keys:     %s\n", filepath.Base(*keysFile))
	fmt.Println("======================================")

	if fk := strings.TrimSpace(*fundingKeyHex); fk != "" {
		if err := fundAgentsIfNeeded(*rpcURL, fk, keys, *fundingAmountWei); err != nil {
			log.Fatalf("funding error: %v", err)
		}
		fmt.Println("Funding done.")
	} else {
		fmt.Println("No funding key provided; agents must already have gas.")
	}

	agents := buildAgentsFromKeys(keys, selected)

	viewCfg := &did.RegistryConfig{
		RPCEndpoint:     *rpcURL,
		ContractAddress: *contract,
		PrivateKey:      "",
	}
	viewClient, err := agentcard.NewAgentCardClient(viewCfg)
	if err != nil {
		log.Fatalf("init view client: %v", err)
	}

	// chainId & registry address for signing
	cli, err := ethclient.Dial(*rpcURL)
	if err != nil {
		log.Fatalf("rpc dial: %v", err)
	}
	defer cli.Close()
	chainID, err := cli.NetworkID(context.Background())
	if err != nil {
		log.Fatalf("network id: %v", err)
	}
	registryAddr := common.HexToAddress(*contract)

	for _, a := range agents {
		k := findKey(keys, a.Name)
		if k == nil || strings.TrimSpace(k.PrivateKey) == "" {
			fmt.Printf(" - %s: missing key -> skip\n", a.Name)
			continue
		}

		pubBytes, err := ensureUncompressed(k.PublicKey)
		if err != nil || len(pubBytes) != 65 || pubBytes[0] != 0x04 {
			fmt.Printf("   Invalid public key for %s: %v\n", a.Name, err)
			continue
		}

		agentPriv, err := gethcrypto.HexToECDSA(normHex(k.PrivateKey))
		if err != nil {
			fmt.Printf("   Agent key parse failed for %s: %v\n", a.Name, err)
			continue
		}
		ownerAddr := gethcrypto.PubkeyToAddress(agentPriv.PublicKey)

		// FIX: sign exactly what the contract expects
		sig, err := signECDSAOwnership(agentPriv, chainID, registryAddr, ownerAddr)
		if err != nil {
			fmt.Printf("   sign failed for %s: %v\n", a.Name, err)
			continue
		}

		params, err := buildRegParamsECDSA(a, pubBytes, sig)
		if err != nil {
			fmt.Printf("   Build params failed for %s: %v\n", a.Name, err)
			continue
		}

		perAgentCfg := &did.RegistryConfig{
			RPCEndpoint:     *rpcURL,
			ContractAddress: *contract,
			PrivateKey:      normHex(k.PrivateKey),
		}
		client, err := agentcard.NewAgentCardClient(perAgentCfg)
		if err != nil {
			fmt.Printf("   init client failed for %s: %v\n", a.Name, err)
			continue
		}

		ctx := context.Background()

		if _, err := viewClient.GetAgentByDID(ctx, a.DID); err == nil {
			fmt.Printf(" - %s: already registered (DID=%s)\n", a.Name, a.DID)
			continue
		}

		fmt.Printf("\n Committing %s (DID=%s)…\n", a.Name, a.DID)
		status, err := client.CommitRegistration(ctx, params)
		if err != nil {
			fmt.Printf("   Commit failed: %v\n", err)
			continue
		}
		fmt.Printf("   Commit hash: 0x%x — waiting %ds\n", status.CommitHash, *waitSeconds)
		time.Sleep(time.Duration(*waitSeconds) * time.Second)

		fmt.Println("   Revealing…")
		status, err = client.RegisterAgent(ctx, status)
		if err != nil {
			fmt.Printf("   Register failed: %v\n", err)
			continue
		}
		fmt.Printf("   Registered. AgentID: 0x%x\n", status.AgentID)
		fmt.Printf("   Can activate at: %s\n", status.CanActivateAt.Format(time.RFC3339))

		if *tryActivate && time.Now().After(status.CanActivateAt) {
			fmt.Println("   Activating…")
			if err := client.ActivateAgent(ctx, status); err != nil {
				fmt.Printf("   Activate failed: %v\n", err)
			} else {
				fmt.Println("   Activated ✅")
			}
		}
		time.Sleep(800 * time.Millisecond)
	}

	fmt.Println("\nVerification:")
	for _, a := range agents {
		if ag, err := viewClient.GetAgentByDID(context.Background(), a.DID); err == nil {
			state := "Registered"
			if ag.IsActive {
				state = "Active"
			}
			fmt.Printf(" - %s: %s (owner=%s)\n", a.Name, state, ag.Owner)
		} else {
			fmt.Printf(" - %s: Not found (%v)\n", a.Name, err)
		}
	}
}

/* === helpers === */

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
	if len(raw) == 65 && raw[0] == 0x04 {
		return raw, nil
	}
	if len(raw) == 33 && (raw[0] == 0x02 || raw[0] == 0x03) {
		pk, err := gethcrypto.DecompressPubkey(raw)
		if err != nil {
			return nil, err
		}
		return gethcrypto.FromECDSAPub(pk), nil
	}
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
		if q := strings.TrimSpace(p); q != "" {
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

func buildRegParamsECDSA(a DemoAgent, ecdsaPub []byte, ecdsaSig []byte) (*did.RegistrationParams, error) {
	capsJSON := "{}"
	if a.Metadata.Capabilities != nil {
		if b, err := json.Marshal(a.Metadata.Capabilities); err == nil {
			capsJSON = string(b)
		}
	}
	keys := [][]byte{ecdsaPub}
	keyTypes := []did.KeyType{did.KeyTypeECDSA}
	sigs := [][]byte{ecdsaSig}
	return &did.RegistrationParams{
		DID:          a.DID,
		Name:         a.Metadata.Name,
		Description:  a.Metadata.Description,
		Endpoint:     a.Metadata.Endpoint,
		Capabilities: capsJSON,
		Keys:         keys,
		KeyTypes:     keyTypes,
		Signatures:   sigs,
	}, nil
}

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

/* ==== exact message builders that match the contract ==== */

func pad32BigEndian(n *big.Int) []byte {
	var out [32]byte
	b := n.Bytes()
	copy(out[32-len(b):], b)
	return out[:]
}

func signECDSAOwnership(priv *ecdsa.PrivateKey, chainID *big.Int, registry common.Address, owner common.Address) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("SAGE Agent Registration:")
	buf.Write(pad32BigEndian(chainID))
	buf.Write(registry.Bytes()) // 20B
	buf.Write(owner.Bytes())    // 20B
	msgHash := gethcrypto.Keccak256Hash(buf.Bytes())
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	ethSigned := gethcrypto.Keccak256Hash(append(prefix, msgHash.Bytes()...))
	sig, err := gethcrypto.Sign(ethSigned.Bytes(), priv)
	if err != nil {
		return nil, err
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// legacy (no longer used, kept for reference)
func signSelfRegistrationMessage(_ *ecdsa.PrivateKey, _ string, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("unused")
}
