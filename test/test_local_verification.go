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
    "net/http"
    "os"
    "path/filepath"
    "time"

    ethcrypto "github.com/ethereum/go-ethereum/crypto"
    rfc9421 "github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
)

func main() {
    fmt.Println("==============================================")
    fmt.Println("Local RFC9421 Signature Verification Test")
    fmt.Println("==============================================")

    did, priv, err := load("keys/root.key")
    if err != nil { panic(err) }
    fmt.Printf("Root DID: %s\n", did)

    // Build request
    body := []byte(`{"action":"test","data":"sample"}`)
    req, _ := http.NewRequest(http.MethodPost, "http://demo.local/api/local", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    sum := sha256.Sum256(body)
    req.Header.Set("Content-Digest", fmt.Sprintf("sha-256=:%s:", base64.StdEncoding.EncodeToString(sum[:])))

    // Sign
    params := &rfc9421.SignatureInputParams{
        CoveredComponents: []string{"\"@method\"", "\"@target-uri\"", "\"@authority\"", "\"content-type\"", "\"content-digest\""},
        KeyID: did,
        Algorithm: "es256k",
        Created: time.Now().Unix(),
    }
    if err := rfc9421.NewHTTPVerifier().SignRequest(req, "sig1", params, priv); err != nil { panic(err) }
    fmt.Println("Request signed")

    // Verify
    if err := rfc9421.NewHTTPVerifier().VerifyRequest(req, &priv.PublicKey, nil); err != nil {
        fmt.Printf("Verification failed: %v\n", err)
    } else {
        fmt.Println("Verification successful!")
    }

    fmt.Println("\n==============================================")
    fmt.Println("Local Verification Test Complete!")
}

func load(p string) (string, *ecdsa.PrivateKey, error) {
    if p == "" { p = filepath.Join("keys", "root.key") }
    f, err := os.Open(p)
    if err != nil { return "", nil, err }
    defer f.Close()
    var rec struct{ DID, PrivateKey string }
    if err := json.NewDecoder(f).Decode(&rec); err != nil { return "", nil, err }
    b, err := hex.DecodeString(rec.PrivateKey)
    if err != nil { return "", nil, err }
    k, err := ethcrypto.ToECDSA(b)
    if err != nil { return "", nil, err }
    return rec.DID, k, nil
}

