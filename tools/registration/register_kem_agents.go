//go:build reg_kem
// +build reg_kem

// tools/registration/register_kem_agents.go
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
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/sage-x-project/sage-a2a-go/pkg/identity"
	"github.com/sage-x-project/sage-a2a-go/pkg/registry"
)

type signingRow struct {
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
	signingPath := flag.String("signing-keys", "generated_agent_keys.json", "Signing keys JSON")
	kemPath := flag.String("kem-keys", "keys/kem/generated_kem_keys.json", "KEM keys JSON")
	agentsFilter := flag.String("agents", "", "Comma-separated agent names")
	waitSeconds := flag.Int("wait-seconds", 65, "Seconds between commit and reveal (>=60)")
	tryActivate := flag.Bool("try-activate", true, "Try activation if allowed")
	flag.Parse()

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

	signingRows, err := loadSigning(*signingPath)
	if err != nil {
		fatalf("load signing keys: %v", err)
	}
	kemRows, err := loadKEMRows(*kemPath)
	if err != nil {
		fatalf("load KEM keys: %v", err)
	}
	selected := parseAgentsFilter(*agentsFilter)
	if len(selected) == 0 {
		selected = parseAgentsFilter(os.Getenv("SAGE_AGENTS"))
	}
	agents := buildAgentsFromSigning(signingRows, selected)

	fmt.Println("======================================")
	fmt.Println(" SAGE AgentCard Registration (ECDSA + X25519)")
	fmt.Println("======================================")
	fmt.Printf(" RPC:      %s\n", *rpcURL)
	fmt.Printf(" Contract: %s\n", *contract)
	fmt.Printf(" Signing:  %s\n", shortPath(*signingPath))
	fmt.Printf(" KEM:      %s\n", shortPath(*kemPath))
	if *agentsFilter != "" {
		fmt.Printf(" Agents:   %s\n", *agentsFilter)
	} else if os.Getenv("SAGE_AGENTS") != "" {
		fmt.Printf(" Agents:   %s (from env)\n", os.Getenv("SAGE_AGENTS"))
	} else {
		fmt.Println(" Agents:   ALL (from signing-keys)")
	}
	fmt.Println("======================================")

	viewClient, err := registry.NewRegistrationClient(&registry.ClientConfig{
		RPCURL:          *rpcURL,
		RegistryAddress: *contract,
		PrivateKey:      "", // View-only client
	})
	if err != nil {
		fatalf("init view client: %v")
	}

	// chainId & registry address for signing
	cli, err := ethclient.Dial(*rpcURL)
	if err != nil {
		fatalf("rpc dial: %v")
	}
	defer cli.Close()
	chainID, err := cli.NetworkID(context.Background())
	if err != nil {
		fatalf("network id: %v")
	}
	registryAddr := common.HexToAddress(*contract)

	ctx := context.Background()

    ownerCli, err := registry.NewRegistrationClient(&registry.ClientConfig{
        RPCURL:          *rpcURL,
        RegistryAddress: *contract,
        PrivateKey:      "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", // contract deployer/owner key
    })
	if err == nil {
		if err := ownerCli.GetSAGEClient().SetActivationDelay(ctx, 0); err != nil {
			fmt.Printf("SetActivationDelay(0) failed: %v\n", err)
		} else {
			fmt.Println("activationDelay set to 0s (for subsequent registrations)")
		}
	} else {
		fmt.Printf("owner client init failed: %v\n", err)
	}

    // (optional) log the current chain activationDelay
	if d, err := viewClient.GetSAGEClient().GetActivationDelay(ctx); err == nil {
		fmt.Printf(" Current activationDelay = %s\n", d)
	}
	for _, a := range agents {
		sk := findSigning(signingRows, a.Name)
		if sk == nil || strings.TrimSpace(sk.PrivateKey) == "" {
			fmt.Printf(" - %s: missing signing privateKey -> skip\n", a.Name)
			continue
		}
		pubBytes, err := ensureUncompressed(sk.PublicKey)
		if err != nil || len(pubBytes) != 65 || pubBytes[0] != 0x04 {
			fmt.Printf("   Invalid ECDSA public key for %s: %v\n", a.Name, err)
			continue
		}
		kr := findKEM(kemRows, a.Name)
		if kr == nil || strings.TrimSpace(kr.X25519Public) == "" || strings.TrimSpace(kr.DID) == "" {
			fmt.Printf(" - %s: missing KEM row or DID -> skip\n", a.Name)
			continue
		}
		xpub, err := hexDecode32(kr.X25519Public)
		if err != nil {
			fmt.Printf(" - %s: invalid x25519Public: %v -> skip\n", a.Name, err)
			continue
		}

		agentPriv, err := gethcrypto.HexToECDSA(normHex(sk.PrivateKey))
		if err != nil {
			fmt.Printf("   Agent key parse failed for %s: %v\n", a.Name, err)
			continue
		}
		ownerAddr := gethcrypto.PubkeyToAddress(agentPriv.PublicKey)

		// FIX: signatures that contract expects
		ecdsaSig, err := signECDSAOwnership(agentPriv, chainID, registryAddr, ownerAddr)
		if err != nil {
			fmt.Printf("   sign ECDSA failed: %v\n", err)
			continue
		}
		xSig, err := signX25519Ownership(agentPriv, xpub, chainID, registryAddr, ownerAddr)
		if err != nil {
			fmt.Printf("   sign X25519 failed: %v\n", err)
			continue
		}

		client, err := registry.NewRegistrationClient(&registry.ClientConfig{
			RPCURL:          *rpcURL,
			RegistryAddress: *contract,
			PrivateKey:      normHex(sk.PrivateKey),
		})
		if err != nil {
			fmt.Printf("   init client failed for %s: %v\n", a.Name, err)
			continue
		}

		// Build params with BOTH keys + BOTH signatures
		params, err := buildRegParamsECDSAPlusKEM(a, strings.TrimSpace(kr.DID), pubBytes, ecdsaSig, xpub, xSig)
		if err != nil {
			fmt.Printf("   Build params failed for %s: %v\n", a.Name, err)
			continue
		}

		if _, err := viewClient.GetSAGEClient().GetAgentByDID(ctx, strings.TrimSpace(kr.DID)); err == nil {
			fmt.Printf(" - %s: DID already registered; skip\n", a.Name)
			continue
		}

		fmt.Printf("\n Register (commit→reveal) %s as DID=%s …\n", a.Name, strings.TrimSpace(kr.DID))
		status, err := client.CommitRegistration(ctx, params)
		if err != nil {
			fmt.Printf("   Commit failed: %v\n", err)
			continue
		}
		fmt.Printf("   Commit hash: 0x%x — waiting %ds\n", status.CommitHash, *waitSeconds)
		time.Sleep(time.Duration(*waitSeconds) * time.Second)

		status, err = client.RegisterAgent(ctx, status)
		if err != nil {
			fmt.Printf("   Register failed: %v\n", err)
			continue
		}
		fmt.Printf("   Registered. AgentID: 0x%x — activate at %s\n", status.AgentID, status.CanActivateAt.Format(time.RFC3339))
		if wait := time.Until(status.CanActivateAt); wait > 0 {
			time.Sleep(wait + 800*time.Millisecond)
		}
		if *tryActivate {
			fmt.Println("   Activating…")
			if err := client.ActivateAgent(ctx, status); err != nil {
				fmt.Printf("   Activate failed: %v\n", err)
			} else {
				fmt.Println("   Activated ✅")
			}
		}

	}

	fmt.Println("\nVerification:")
	for _, a := range agents {
		kr := findKEM(kemRows, a.Name)
		if kr == nil || strings.TrimSpace(kr.DID) == "" {
			fmt.Printf(" - %s: (no KEM DID) skip verify\n", a.Name)
			continue
		}
		if ag, err := viewClient.GetSAGEClient().GetAgentByDID(context.Background(), strings.TrimSpace(kr.DID)); err == nil {
			state := "Registered"
			if ag.IsActive {
				state = "Active"
			}
			fmt.Printf(" - %s: %s (owner=%s, DID=%s)\n", a.Name, state, ag.Owner, ag.DID)
		} else {
			fmt.Printf(" - %s: Not found (%v)\n", a.Name, err)
		}
	}
}

/* === io & small helpers === */

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

// === KEM JSON loader (top-level array OR {"agents":[]}) ===
// Since both are in the same file, `go run -tags=reg_kem tools/registration/register_kem_agents.go` works standalone

type KemRow struct {
	Name          string `json:"name"`
	DID           string `json:"did,omitempty"`
	Address       string `json:"address,omitempty"`
    X25519Public  string `json:"x25519Public"`            // ← same as JSON
    X25519Private string `json:"x25519Private,omitempty"` // ← same as JSON
}
type kemWrapper struct {
	Agents []KemRow `json:"agents"`
}

func readAll(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return b, nil
}

func loadKEMRows(path string) ([]KemRow, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := readAll(path)
	if err != nil {
		return nil, err
	}
	var arr []KemRow
	if err := json.Unmarshal(b, &arr); err == nil {
		return arr, nil
	}
	var w kemWrapper
	if err := json.Unmarshal(b, &w); err == nil {
		return w.Agents, nil
	}
	return nil, fmt.Errorf("kem json not recognized: %s (need {\"agents\":[]} or top-level array)", path)
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
func findSigning(all []signingRow, name string) *signingRow {
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}
func findKEM(all []KemRow, name string) *KemRow {
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
func shortPath(p string) string         { return filepath.Clean(p) }
func fatalf(f string, a ...interface{}) { fmt.Printf("Error: "+f+"\n", a...); os.Exit(1) }

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
		a.DID = k.DID
		a.Metadata.Name = k.Name
		a.Metadata.Description = os.Getenv("SAGE_AGENT_" + toEnvKey(k.Name) + "_DESC")
		if a.Metadata.Description == "" {
			a.Metadata.Description = "SAGE Agent " + k.Name
		}
		a.Metadata.Version = "0.1.0"
		a.Metadata.Type = ""
		a.Metadata.Endpoint = ""
		a.Metadata.PublicKey = k.PublicKey
		a.Metadata.Capabilities = map[string]interface{}{}
		out = append(out, a)
	}
	return out
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

/* ==== params & signatures ==== */

func buildRegParamsECDSAPlusKEM(a DemoAgent, didStr string, ecdsaPub, ecdsaSig, x25519Pub, x25519Sig []byte) (*registry.RegistrationParams, error) {
	capsJSON := "{}"
	keys := [][]byte{ecdsaPub, x25519Pub}
	keyTypes := []identity.KeyType{identity.KeyTypeECDSA, identity.KeyTypeX25519}
	sigs := [][]byte{ecdsaSig, x25519Sig}
	return &registry.RegistrationParams{
		DID:          didStr,
		Name:         a.Metadata.Name,
		Description:  a.Metadata.Description,
		Endpoint:     a.Metadata.Endpoint,
		Capabilities: capsJSON,
		Keys:         keys,
		KeyTypes:     keyTypes,
		Signatures:   sigs,
	}, nil
}

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
	buf.Write(registry.Bytes())
	buf.Write(owner.Bytes())
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

func signX25519Ownership(priv *ecdsa.PrivateKey, x25519Pub32 []byte, chainID *big.Int, registry common.Address, owner common.Address) ([]byte, error) {
	if len(x25519Pub32) != 32 {
		return nil, fmt.Errorf("x25519 pub must be 32 bytes")
	}
	var buf bytes.Buffer
	buf.WriteString("SAGE X25519 Ownership:")
	buf.Write(x25519Pub32)
	buf.Write(pad32BigEndian(chainID))
	buf.Write(registry.Bytes())
	buf.Write(owner.Bytes())
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

func devWarpTo(ctx context.Context, rpcURL string, target time.Time) error {
	c, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		return err
	}
	defer c.Close()

    // 1) Fixed timestamp attempt (Hardhat/Anvil)
	ts := target.Unix()
	var dummy any
	if err := c.CallContext(ctx, &dummy, "evm_setNextBlockTimestamp", ts); err == nil {
		_ = c.CallContext(ctx, &dummy, "evm_mine")
		return nil
	}
	if err := c.CallContext(ctx, &dummy, "anvil_setNextBlockTimestamp", ts); err == nil {
		_ = c.CallContext(ctx, &dummy, "anvil_mine", 1)
		return nil
	}

    // 2) Incremental (Hardhat/Ganache)
	delta := time.Until(target).Seconds()
	if delta < 0 {
		delta = 0
	}
	if err := c.CallContext(ctx, &dummy, "evm_increaseTime", int64(delta)+2); err == nil {
		_ = c.CallContext(ctx, &dummy, "evm_mine")
		return nil
	}
	return fmt.Errorf("dev time-warp RPC not supported on this node")
}
