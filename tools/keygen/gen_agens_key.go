// tools/keys/gen_agent_keys.go
// SPDX-License-Identifier: MIT
//
// One canonical key generator for SAGE demo agents.
// - Uses secp256k1 (ECDSA) keys via SAGE key package
// - Reads agent names & DIDs from the demo metadata JSON
// - Writes per-agent JWK files:   keys/<name>.jwk   (private JWK, 0600)
// - Writes summary for registrar: generated_agent_keys.json (legacy shape, kept for compatibility)
// - Optionally updates demo metadata's metadata.publicKey (uncompressed 0x04+X+Y)

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

	agentcrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/crypto/keys"
)

// Demo metadata (only the parts we need)
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
		Sage         map[string]interface{} `json:"sage"`
	} `json:"metadata"`
}
type DemoData struct {
	Agents []DemoAgent `json:"agents"`
}

// Summary record consumed by tools/registration/register_agents.go
type AgentKeyData struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`  // 0x04 + X + Y (uncompressed, 65 bytes)
	PrivateKey string `json:"privateKey"` // 0x-prefixed hex (32 bytes)
	Address    string `json:"address"`    // 0x...
}

func main() {
	// Flags
	demoPath := flag.String("demo", "../sage-fe/demo-agents-metadata.json", "Path to demo metadata JSON (provides agent names & DIDs)")
	outDir := flag.String("out", "keys", "Directory for per-agent key files (JWK)")
	summary := flag.String("summary", "generated_agent_keys.json", "Output path for registrar summary JSON (legacy shape)")
	updateDemo := flag.Bool("update-demo", true, "Update demo metadata .metadata.publicKey with generated key")
	flag.Parse()

	// Load demo metadata
	demo, err := readDemo(*demoPath)
	if err != nil {
		log.Fatalf("read demo: %v", err)
	}

	// Ensure output dir
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	var all []AgentKeyData
	jwkExp := formats.NewJWKExporter()

	fmt.Println("================================================")
	fmt.Println(" Generating secp256k1 keys for SAGE agents (JWK + legacy summary)")
	fmt.Println("================================================")

	for i := range demo.Agents {
		a := &demo.Agents[i]
		if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.DID) == "" {
			fmt.Printf(" - skip entry %d (missing name or DID)\n", i)
			continue
		}

		// Generate secp256k1 KeyPair (SAGE key package)
		kp, err := keys.GenerateSecp256k1KeyPair()
		if err != nil {
			log.Fatalf("generate key for %s: %v", a.Name, err)
		}

		// Export full private JWK (for runtime import by agents)
		jwkBytes, err := jwkExp.Export(kp, agentcrypto.KeyFormatJWK)
		if err != nil {
			log.Fatalf("export JWK for %s: %v", a.Name, err)
		}

		perPath := filepath.Join(*outDir, fmt.Sprintf("%s.jwk", a.Name))
		if err := writeRaw(perPath, jwkBytes, 0o600); err != nil {
			log.Fatalf("write %s: %v", perPath, err)
		}

		// For legacy summary & demo publicKey field we still emit uncompressed pub + priv hex
		ecdsaPriv := kp.PrivateKey().(*ecdsa.PrivateKey)
		pubUncompressed := geth.FromECDSAPub(&ecdsaPriv.PublicKey) // 65 bytes (0x04 + X + Y)
		if len(pubUncompressed) != 65 || pubUncompressed[0] != 0x04 {
			log.Fatalf("unexpected pubkey length for %s: %d", a.Name, len(pubUncompressed))
		}
		privBytes := geth.FromECDSA(ecdsaPriv)                  // 32 bytes
		addr := geth.PubkeyToAddress(ecdsaPriv.PublicKey).Hex() // 0x...

		pubHex := "0x" + hex.EncodeToString(pubUncompressed)
		privHex := "0x" + hex.EncodeToString(privBytes)

		fmt.Printf(" - %s\n", a.Name)
		fmt.Printf("   DID:      %s\n", a.DID)
		fmt.Printf("   Address:  %s\n", addr)
		fmt.Printf("   JWK:      %s\n", perPath)

		all = append(all, AgentKeyData{
			Name:       a.Name,
			DID:        a.DID,
			PublicKey:  pubHex,
			PrivateKey: privHex,
			Address:    addr,
		})

		if *updateDemo {
			a.Metadata.PublicKey = pubHex
		}
	}

	// Write legacy summary JSON for the registrar tooling (unchanged shape)
	if err := writeJSON(*summary, all); err != nil {
		log.Fatalf("write %s: %v", *summary, err)
	}
	fmt.Printf("\nSummary written: %s  (records=%d)\n", *summary, len(all))

	if *updateDemo {
		if err := writeJSON(*demoPath, demo); err != nil {
			log.Fatalf("update demo %s: %v", *demoPath, err)
		}
		fmt.Printf("Demo metadata updated: %s (metadata.publicKey set per agent)\n", *demoPath)
	}

	fmt.Println("\nIMPORTANT: Demo keys only. Do not use in production.")
}

func readDemo(path string) (*DemoData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var d DemoData
	if err := json.NewDecoder(f).Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

func writeJSON(path string, v interface{}) error {
	// Secret-ish files (*.jwk, *.key, generated_agent_keys.json) → 0600
	mode := os.FileMode(0o644)
	if strings.HasSuffix(path, ".key") || strings.HasSuffix(path, ".jwk") || strings.Contains(path, "generated_agent_keys.json") {
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
