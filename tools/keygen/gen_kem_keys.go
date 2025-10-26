//go:build reg_kem_key
// +build reg_kem_key

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"
)

/*
Key changes
- Add address field to generated_kem_keys.json (private JSON)
- Load name→DID and name→address mappings from signing summary (generated_agent_keys.json)
- If only DID is present and address is empty, parse 0x-address from DID and fill address
- Include address (optional) in public JSON as well (consumers may ignore it)
*/

// ---------- JSON Model ----------

type KemAgent struct {
    Name          string `json:"name"`
    DID           string `json:"did"`
    Address       string `json:"address,omitempty"` // added
    X25519Private string `json:"x25519Private"`     // 0x-hex (32B)
    X25519Public  string `json:"x25519Public"`      // 0x-hex (32B)
}

type KemPrivateList struct {
	Agents []KemAgent `json:"agents"`
}

type KemPublicList struct {
    Agents []struct {
        Name         string `json:"name"`
        DID          string `json:"did"`
        Address      string `json:"address,omitempty"` // added (optional)
        X25519Public string `json:"x25519Public"`
    } `json:"agents"`
}

// Structure compatible with signing summary (generated_agent_keys.json)
type SigningSummaryRow struct {
    Name    string `json:"name"`
    DID     string `json:"did"`
    Address string `json:"address"` // 0x...
}

// OKP JWK (X25519)
type OKPJWK struct {
    Kty string `json:"kty"`           // "OKP"
    Crv string `json:"crv"`           // "X25519"
    X   string `json:"x"`             // b64url(pub)
    D   string `json:"d,omitempty"`   // b64url(priv) — only in private files
    Kid string `json:"kid,omitempty"` // DID or name
    Use string `json:"use,omitempty"` // "enc"
    Alg string `json:"alg,omitempty"` // "X25519"
}
type JWKSet struct {
	Keys []OKPJWK `json:"keys"`
}

func main() {
	agentsFlag := flag.String("agents", "payment,external", "Comma-separated agent names")
	kemOutDir := flag.String("out", "keys/kem", "Output directory for KEM artifacts")
	kemJSON := flag.String("kem-json", "keys/kem/generated_kem_keys.json", "Private KEM JSON (includes x25519Private)")
	pubJSON := flag.String("public-json", "keys/kem/kem_all_keys.json", "Public KEM JSON (no private material)")
	pemDir := flag.String("pem-dir", "keys/kem/pem", "Optional PEM dir for per-agent files")

    // JWK output paths
    jwkDir := flag.String("jwk-out", "keys/kem", "Directory to write per-agent X25519 JWK files")
    jwkSetPub := flag.String("jwkset-public", "keys/kem/kem_jwks.json", "Public JWK Set (no private key)")

    // Fill DID/Address: read name→DID/Address mapping from signing summary
    signingSummary := flag.String("signing-summary", "generated_agent_keys.json", "Path to signing summary (for DID/address mapping)")
	flag.Parse()

	names := splitCSV(*agentsFlag)
	if len(names) == 0 {
		log.Fatal("no agents")
	}

	mustMkdirAll(*kemOutDir, 0o755)
	if dir := filepath.Dir(*kemJSON); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if dir := filepath.Dir(*pubJSON); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if *pemDir != "" {
		_ = os.MkdirAll(*pemDir, 0o755)
	}
	if *jwkDir != "" {
		_ = os.MkdirAll(*jwkDir, 0o755)
	}
	if dir := filepath.Dir(*jwkSetPub); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}

    // name -> DID/Address mapping
    nameToDID := map[string]string{}
    nameToAddr := map[string]string{}
    loadDIDFromEnvInto(nameToDID, names)
    loadDIDAddrFromSigningSummaryInto(nameToDID, nameToAddr, *signingSummary) // supplement missing env values with summary

	var privList KemPrivateList
	var pubList KemPublicList
	var jwkSetPublic JWKSet

	fmt.Println("===============================================")
	fmt.Println(" Generating X25519 KEM keys (HPKE) ONLY")
	fmt.Println("  - Private JSON:", *kemJSON)
	fmt.Println("  - Public  JSON:", *pubJSON)
	if *pemDir != "" {
		fmt.Println("  - PEM out dir :", *pemDir)
	}
	fmt.Println("  - Per-agent JWK dir:", *jwkDir)
	fmt.Println("  - Public JWK Set   :", *jwkSetPub)
	fmt.Println("  - Signing summary  :", *signingSummary)
	fmt.Println("===============================================")

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

        did := strings.TrimSpace(nameToDID[name])   // empty if missing
        addr := strings.TrimSpace(nameToAddr[name]) // empty if missing

        // If only DID is set and address is empty, parse 0x-address from DID
        if addr == "" && did != "" {
            if a := parseAddressFromDID(did); a != "" {
                addr = a
            }
        }

		// 1) X25519 generate
		priv, pub, err := genX25519()
		if err != nil {
			log.Fatalf("generate X25519 for %s: %v", name, err)
		}

		privHex := "0x" + hex.EncodeToString(priv)
		pubHex := "0x" + hex.EncodeToString(pub)

        // 2) Emit JSON (private/public)
        privList.Agents = append(privList.Agents, KemAgent{
            Name:          name,
            DID:           did,
            Address:       addr, // include
            X25519Private: privHex,
            X25519Public:  pubHex,
        })
		pubList.Agents = append(pubList.Agents, struct {
			Name         string `json:"name"`
			DID          string `json:"did"`
			Address      string `json:"address,omitempty"`
			X25519Public string `json:"x25519Public"`
		}{Name: name, DID: did, Address: addr, X25519Public: pubHex})

        // 3) PEM (optional)
        if *pemDir != "" {
            writePEM(filepath.Join(*pemDir, fmt.Sprintf("%s_x25519_priv.pem", name)), "X25519 PRIVATE KEY", priv)
            writePEM(filepath.Join(*pemDir, fmt.Sprintf("%s_x25519_pub.pem", name)), "X25519 PUBLIC KEY", pub)
        }

        // 4) JWK per-agent (OKP/X25519)
        kid := did
        if kid == "" {
            kid = name
        }
		privJWK := OKPJWK{
			Kty: "OKP",
			Crv: "X25519",
			X:   b64u(pub),
			D:   b64u(priv),
			Kid: kid,
			Use: "enc",
			Alg: "X25519",
		}
		pubJWK := OKPJWK{
			Kty: "OKP",
			Crv: "X25519",
			X:   b64u(pub),
			Kid: kid,
			Use: "enc",
			Alg: "X25519",
		}

        //   - private JWK file
        privPath := filepath.Join(*jwkDir, fmt.Sprintf("%s.x25519.jwk", name))
        mustWriteJSONPrivate(privPath, privJWK)

        //   - append to public JWK Set
        jwkSetPublic.Keys = append(jwkSetPublic.Keys, pubJWK)

		fmt.Printf(" - %s  DID=%s  addr=%s  pub=%s  (jwk:%s)\n",
			name, firstNonEmpty(did, "(unset)"), firstNonEmpty(addr, "(unset)"), pubHex, privPath)
	}

    // 5) Write outputs
    mustWriteJSON(*kemJSON, privList)       // 0600
    mustWriteJSON(*pubJSON, pubList)        // 0644
    mustWriteJSON(*jwkSetPub, jwkSetPublic) // 0644

	fmt.Println("DONE.")
}

/*** helpers ***/

func splitCSV(s string) []string {
	parts := strings.Split(strings.TrimSpace(s), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		q := strings.TrimSpace(p)
		if q != "" {
			out = append(out, q)
		}
	}
	return out
}

// env: SAGE_AGENT_<NAME>_DID
func loadDIDFromEnvInto(m map[string]string, names []string) {
	for _, n := range names {
		key := "SAGE_AGENT_" + toEnvKey(n) + "_DID"
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			m[n] = v
		}
	}
}

// Supplement name→DID/Address from generated_agent_keys.json
func loadDIDAddrFromSigningSummaryInto(did map[string]string, addr map[string]string, summaryPath string) {
	b, err := os.ReadFile(summaryPath)
	if err != nil {
		return
	}
    // Primary form: []SigningSummaryRow
    var rows []SigningSummaryRow
	if err := json.Unmarshal(b, &rows); err == nil && len(rows) > 0 {
		for _, r := range rows {
			if strings.TrimSpace(did[r.Name]) == "" && strings.TrimSpace(r.DID) != "" {
				did[r.Name] = strings.TrimSpace(r.DID)
			}
			if strings.TrimSpace(addr[r.Name]) == "" && strings.TrimSpace(r.Address) != "" {
				addr[r.Name] = strings.TrimSpace(r.Address)
			}
		}
		return
	}
    // fallback: wrapping such as {"agents":[...]}
    var obj map[string]any
	if err := json.Unmarshal(b, &obj); err == nil {
		if v, ok := obj["agents"]; ok {
			ba, _ := json.Marshal(v)
			var rows2 []SigningSummaryRow
			if json.Unmarshal(ba, &rows2) == nil {
				for _, r := range rows2 {
					if strings.TrimSpace(did[r.Name]) == "" && strings.TrimSpace(r.DID) != "" {
						did[r.Name] = strings.TrimSpace(r.DID)
					}
					if strings.TrimSpace(addr[r.Name]) == "" && strings.TrimSpace(r.Address) != "" {
						addr[r.Name] = strings.TrimSpace(r.Address)
					}
				}
			}
		}
	}
}

// "did:sage:ethereum:0xABCD..." → "0xABCD..."
func parseAddressFromDID(did string) string {
	did = strings.TrimSpace(did)
	if did == "" {
		return ""
	}
	const prefix = "did:sage:ethereum:"
	if !strings.HasPrefix(strings.ToLower(did), prefix) {
		return ""
	}
	part := did[len(prefix):]
    // Support both did:sage:ethereum:0xADDR and did:sage:ethereum:0xADDR:nonce forms
	if idx := strings.IndexByte(part, ':'); idx >= 0 {
		part = part[:idx]
	}
	if strings.HasPrefix(part, "0x") && len(part) == 42 {
		return part
	}
	return ""
}

func agentDIDFromEnv(name string) string {
	key := "SAGE_AGENT_" + toEnvKey(name) + "_DID"
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return ""
}

func toEnvKey(name string) string {
	up := strings.ToUpper(name)
	var b []rune
	for _, r := range up {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		} else {
			b = append(b, '_')
		}
	}
	return string(b)
}

func genX25519() (priv, pub []byte, err error) {
	priv = make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return nil, nil, err
	}
	// clamp per RFC7748
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	base := make([]byte, 32)
	base[0] = 9
	pub, err = curve25519.X25519(priv, base)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

func mustMkdirAll(path string, mode os.FileMode) {
	if err := os.MkdirAll(path, mode); err != nil {
		log.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteJSON(path string, v interface{}) {
	mode := os.FileMode(0o644)
	base := filepath.Base(path)
    if strings.Contains(base, "generated_kem_keys.json") || strings.HasSuffix(base, ".jwk") {
        mode = 0o600 // contains private material
    }
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		log.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Fatal(err)
	}
}

func mustWriteJSONPrivate(path string, v interface{}) { mustWriteJSON(path, v) }

func writePEM(path, typ string, raw []byte) {
	data := "-----BEGIN " + typ + "-----\n" +
		base64.StdEncoding.EncodeToString(raw) + "\n" +
		"-----END " + typ + "-----\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
}

func b64u(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
