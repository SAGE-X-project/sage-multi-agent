//go:build demo
// +build demo

package main

import (
    "bytes"
    "crypto/ecdsa"
    "crypto/sha256"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "path/filepath"
    "time"

    ethcrypto "github.com/ethereum/go-ethereum/crypto"
    "github.com/sage-x-project/sage-multi-agent/config"
    rfc9421 "github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
)

func main() {
    fmt.Println("==============================================")
    fmt.Println("Agent Messaging Demo (RFC9421 HTTP)")
    fmt.Println("==============================================")

    cfg, err := config.LoadAgentConfig("")
    if err != nil { log.Fatalf("config: %v", err) }

    type agent struct{ did string; priv *ecdsa.PrivateKey }
    agents := map[string]agent{}
    for name, a := range cfg.Agents {
        if name == "client" { continue }
        did, priv, err := loadID(a.KeyFile)
        if err != nil { log.Printf("skip %s: %v", name, err); continue }
        agents[name] = agent{did: did, priv: priv}
        fmt.Printf("Loaded %s (DID=%s)\n", name, did)
    }

    chain := []struct{ from, to, message string }{
        {"root", "planning", "Initiate resource planning for Q2"},
        {"planning", "ordering", "Reserve capacity for projected demand"},
        {"ordering", "root", "Capacity reserved, confirmation #CAP-2024-Q2"},
    }
    v := rfc9421.NewHTTPVerifier()
    for i, step := range chain {
        fmt.Printf("\n[Step %d] %s → %s\n  %s\n", i+1, step.from, step.to, step.message)
        a := agents[step.from]
        path := fmt.Sprintf("/agent/%s/process", step.to)
        hdr, err := sign(a.did, a.priv, http.MethodPost, path, []byte(step.message))
        if err != nil { fmt.Printf("  Sign error: %v\n", err); continue }
        if err := verify(v, hdr, http.MethodPost, path, []byte(step.message), &a.priv.PublicKey); err != nil {
            fmt.Printf("  Verify error: %v\n", err)
        } else {
            fmt.Println("  Verified OK")
        }
    }
}

func loadID(keyFile string) (string, *ecdsa.PrivateKey, error) {
    if keyFile == "" { keyFile = filepath.Join("keys", "root.key") }
    f, err := os.Open(keyFile)
    if err != nil { return "", nil, err }
    defer f.Close()
    var rec struct{ DID, PrivateKey string }
    if err := json.NewDecoder(f).Decode(&rec); err != nil { return "", nil, err }
    b, err := hex.DecodeString(rec.PrivateKey)
    if err != nil { return "", nil, err }
    k, err := ethcrypto.ToECDSA(b); if err != nil { return "", nil, err }
    return rec.DID, k, nil
}

func sign(did string, priv *ecdsa.PrivateKey, method, path string, body []byte) (http.Header, error) {
    req, _ := http.NewRequest(method, "http://demo.local"+path, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    sum := sha256.Sum256(body)
    req.Header.Set("Content-Digest", fmt.Sprintf("sha-256=:%s:", base64.StdEncoding.EncodeToString(sum[:])))
    params := &rfc9421.SignatureInputParams{
        CoveredComponents: []string{"\"@method\"", "\"@target-uri\"", "\"@authority\"", "\"content-type\"", "\"content-digest\""},
        KeyID: did,
        Algorithm: "es256k",
        Created: time.Now().Unix(),
        Expires: time.Now().Add(5*time.Minute).Unix(),
    }
    if err := rfc9421.NewHTTPVerifier().SignRequest(req, "sig1", params, priv); err != nil { return nil, err }
    return req.Header.Clone(), nil
}

func verify(v *rfc9421.HTTPVerifier, hdr http.Header, method, path string, body []byte, pub interface{}) error {
    req, _ := http.NewRequest(method, "http://demo.local"+path, bytes.NewReader(body))
    req.Header = hdr.Clone()
    return v.VerifyRequest(req, pub, nil)
}

