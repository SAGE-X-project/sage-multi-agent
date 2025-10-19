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
    "github.com/sage-x-project/sage-multi-agent/config"
    rfc9421 "github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
)

func main() {
    fmt.Println("==============================================")
    fmt.Println("Blockchain SAGE Verification Demo (Local Mode)")
    fmt.Println("==============================================")

    rpcURL := os.Getenv("LOCAL_RPC_ENDPOINT")
    if rpcURL == "" { rpcURL = "http://127.0.0.1:8545" }
    contract := os.Getenv("LOCAL_CONTRACT_ADDRESS")
    if contract == "" { contract = "0x0000000000000000000000000000000000000000" }
    fmt.Printf("RPC URL: %s\nContract: %s\n", rpcURL, contract)

    cfg, _ := config.LoadAgentConfig("")
    for name, a := range cfg.Agents {
        if name == "client" { continue }
        fmt.Printf("Agent %s DID: %s Endpoint: %s\n", name, a.DID, a.Endpoint)
    }

    // Local signing + verification as stand-in for on-chain key resolution
    did, priv, err := loadKey(filepath.Join("keys", "root.key"))
    if err != nil { panic(err) }
    body := []byte("Test message for blockchain verification (local)")
    req, _ := http.NewRequest(http.MethodPost, "http://demo.local/a2a", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    sum := sha256.Sum256(body)
    req.Header.Set("Content-Digest", fmt.Sprintf("sha-256=:%s:", base64.StdEncoding.EncodeToString(sum[:])))
    params := &rfc9421.SignatureInputParams{CoveredComponents: []string{"\"@method\"","\"@target-uri\"","\"@authority\"","\"content-type\"","\"content-digest\""}, KeyID: did, Algorithm: "es256k", Created: time.Now().Unix()}
    if err := rfc9421.NewHTTPVerifier().SignRequest(req, "sig1", params, priv); err != nil { panic(err) }
    if err := rfc9421.NewHTTPVerifier().VerifyRequest(req, &priv.PublicKey, nil); err != nil {
        fmt.Printf("Verification failed: %v\n", err)
    } else {
        fmt.Println("Verification succeeded (local)")
    }
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

