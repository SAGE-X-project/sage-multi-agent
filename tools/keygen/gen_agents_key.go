//go:build reg_agents_key
// +build reg_agents_key

// SPDX-License-Identifier: MIT
//
// One canonical key generator for SAGE demo agents.
// - Uses secp256k1 (ECDSA) keys via SAGE key package
// - Agents are provided via `--agents` or env `SAGE_AGENTS` (comma-separated)
// - Default agent list: "payment"
// - DID per agent can be overridden via env `SAGE_AGENT_<NAME>_DID`,
//   otherwise it defaults to DID derived from the generated key's Ethereum address:
//   `did:sage:ethereum:<0xAddress>`
// - Writes per-agent JWK files: keys/<name>.jwk (private JWK, 0600)
// - Writes legacy summary: generated_agent_keys.json (kept for compatibility)
// - NEW: Writes inbound verifier list: keys/all_keys.json
//        { "agents": [ { "DID": "...", "PublicKey": "0x04...", "Type": "secp256k1" } ] }

package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	geth "github.com/ethereum/go-ethereum/crypto"

	"github.com/sage-x-project/sage-a2a-go/pkg/crypto"
)

// ----- Inputs / Models -----

type AgentSpec struct {
	Name string
	DID  string // if empty, we will set to did:sage:ethereum:<address>
}

type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`  // 0x04 + X + Y (65 bytes, uncompressed)
	PrivateKey string `json:"privateKey"` // 0x-prefixed hex (32 bytes)
	Address    string `json:"address"`    // 0x...
}

// all_keys.json shape expected by inbound verifier (payment, etc.)
type AllKeys struct {
	Agents []AllKeysAgent `json:"agents"`
}
type AllKeysAgent struct {
	DID       string `json:"DID"`
	PublicKey string `json:"PublicKey"` // 0x04 + X + Y (uncompressed)
	Type      string `json:"Type"`      // "secp256k1"
}

func main() {
	// Flags
	agentsFlag := flag.String("agents", "", "Comma-separated agent names (overrides env SAGE_AGENTS). Default: payment")
	outDir := flag.String("out", "keys", "Directory for per-agent key files (JWK)")
	summary := flag.String("summary", "generated_agent_keys.json", "Output path for legacy summary JSON")
	allKeysPath := flag.String("all-keys", "keys/all_keys.json", "Output path for inbound verifier key list (all_keys.json)")
	flag.Parse()

	// Resolve agent names
	agentsCSV := strings.TrimSpace(*agentsFlag)
	if agentsCSV == "" {
		agentsCSV = strings.TrimSpace(os.Getenv("SAGE_AGENTS"))
	}
	if agentsCSV == "" {
		agentsCSV = "payment"
	}
	names := splitCSV(agentsCSV)

	// Prepare output dirs
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}
	if dir := filepath.Dir(*allKeysPath); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}

	var legacy []AgentKeyData
	var ak AllKeys

	fmt.Println("================================================")
	fmt.Println(" Generating secp256k1 keys for SAGE agents")
	fmt.Println("  - JWK per agent          ->", *outDir)
	fmt.Println("  - Legacy summary         ->", *summary)
	fmt.Println("  - Inbound verifier list  ->", *allKeysPath)
	fmt.Println("================================================")

	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}

		// Generate secp256k1 KeyPair
		kp, err := crypto.GenerateSecp256k1KeyPair()
		if err != nil {
			log.Fatalf("generate key for %s: %v", n, err)
		}

		// Export private JWK
		jwk, err := crypto.ExportPrivateKeyToJWK(kp.PrivateKey(), n)
		if err != nil {
			log.Fatalf("export JWK for %s: %v", n, err)
		}
		jwkBytes, err := crypto.MarshalJWK(jwk)
		if err != nil {
			log.Fatalf("marshal JWK for %s: %v", n, err)
		}
		perPath := filepath.Join(*outDir, fmt.Sprintf("%s.jwk", n))
		if err := writeRaw(perPath, jwkBytes, 0o600); err != nil {
			log.Fatalf("write %s: %v", perPath, err)
		}

		// Extract ECDSA priv/pub + address
		ecdsaPriv := kp.PrivateKey().(*ecdsa.PrivateKey)
		pubUncompressed := geth.FromECDSAPub(&ecdsaPriv.PublicKey) // 65 bytes (0x04 + X + Y)
		if len(pubUncompressed) != 65 || pubUncompressed[0] != 0x04 {
			log.Fatalf("unexpected pubkey length for %s: %d", n, len(pubUncompressed))
		}
		privBytes := geth.FromECDSA(ecdsaPriv)                  // 32 bytes
		addr := geth.PubkeyToAddress(ecdsaPriv.PublicKey).Hex() // 0x...

		pubHex := "0x" + hex.EncodeToString(pubUncompressed)
		privHex := "0x" + hex.EncodeToString(privBytes)

		// DID: env override or default to did:sage:ethereum:<address>
		did := agentDIDFromEnv(n)
		if strings.TrimSpace(did) == "" {
			did = "did:sage:ethereum:" + addr
		}

		fmt.Printf(" - %s\n", n)
		fmt.Printf("   DID:      %s\n", did)
		fmt.Printf("   Address:  %s\n", addr)
		fmt.Printf("   JWK:      %s\n", perPath)

		legacy = append(legacy, AgentKeyData{
			Name:       n,
			DID:        did,
			PublicKey:  pubHex,
			PrivateKey: privHex,
			Address:    addr,
		})

		ak.Agents = append(ak.Agents, AllKeysAgent{
			DID:       did,
			PublicKey: pubHex,
			Type:      "secp256k1",
		})
	}

	// Write legacy summary (0600 for semi-secret content: includes private keys)
	if err := writeJSON(*summary, legacy); err != nil {
		log.Fatalf("write %s: %v", *summary, err)
	}
	fmt.Printf("\nSummary written: %s  (records=%d)\n", *summary, len(legacy))

	// Write all_keys.json (0644; public material only)
	if err := writeJSON(*allKeysPath, ak); err != nil {
		log.Fatalf("write %s: %v", *allKeysPath, err)
	}
	fmt.Printf("Inbound verifier list written: %s\n", *allKeysPath)

	fmt.Println("\nIMPORTANT: Demo keys only. Do not use in production.")
}

// ----- Utils -----

func writeJSON(path string, v interface{}) error {
	// Secret-ish files (*.jwk, *.key, generated_agent_keys.json) → 0600
	mode := os.FileMode(0o644)
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".jwk") || strings.Contains(base, "generated_agent_keys.json") {
		mode = 0o600
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func writeRaw(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		q := strings.TrimSpace(p)
		if q != "" {
			out = append(out, q)
		}
	}
	return out
}

// If env SAGE_AGENT_<NAME>_DID is set, return it; otherwise empty.
// The default is resolved later to did:sage:ethereum:<address> (derived from the key).
func agentDIDFromEnv(name string) string {
	key := "SAGE_AGENT_" + toEnvKey(name) + "_DID"
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return ""
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
