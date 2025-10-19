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
    fmt.Println("Unit Testing RFC9421 Signing and Verification")
    fmt.Println("==================================================")

    did, priv, err := loadKey(filepath.Join("keys", "root.key"))
    if err != nil { panic(err) }
    fmt.Printf("Loaded root DID: %s\n", did)

    // Sign request
    body := []byte("Hello, this is a test message for RFC9421 verification")
    req, _ := http.NewRequest(http.MethodPost, "http://demo.local/api/test", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    sum := sha256.Sum256(body)
    req.Header.Set("Content-Digest", fmt.Sprintf("sha-256=:%s:", base64.StdEncoding.EncodeToString(sum[:])))
    params := &rfc9421.SignatureInputParams{
        CoveredComponents: []string{"\"@method\"", "\"@target-uri\"", "\"@authority\"", "\"content-type\"", "\"content-digest\""},
        KeyID: did,
        Algorithm: "es256k",
        Created: time.Now().Unix(),
    }
    signer := rfc9421.NewHTTPVerifier()
    if err := signer.SignRequest(req, "sig1", params, priv); err != nil { panic(err) }
    fmt.Println("Signed request headers added: Signature, Signature-Input")

    // Verify
    v := rfc9421.NewHTTPVerifier()
    if err := v.VerifyRequest(req, &priv.PublicKey, nil); err != nil {
        fmt.Printf("Verification FAILED: %v\n", err)
    } else {
        fmt.Println("Verification SUCCESS")
    }

    // Toggle demo
    enabled := false
    if !enabled {
        fmt.Println("\nSigning disabled: skipping signature as expected")
    }

    fmt.Println("\n==================================================")
    fmt.Println("Unit Test Complete!")
}

func loadKey(p string) (string, *ecdsa.PrivateKey, error) {
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

