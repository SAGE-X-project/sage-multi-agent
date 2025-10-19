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

    ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type agentKey struct {
    DID        string `json:"did"`
    Name       string `json:"name,omitempty"`
    PublicKey  string `json:"publicKey"`
    PrivateKey string `json:"privateKey,omitempty"`
    Address    string `json:"address,omitempty"`
}

type keyStore struct {
    Agents []agentKey `json:"agents"`
}

func readKeyFile(path string) (did string, priv *ecdsa.PrivateKey, pubHex, addr string, err error) {
    f, e := os.Open(path)
    if e != nil { return "", nil, "", "", e }
    defer f.Close()
    var rec struct{ DID, PrivateKey string }
    if e = json.NewDecoder(f).Decode(&rec); e != nil { return "", nil, "", "", e }
    b, e := hex.DecodeString(strings.TrimPrefix(rec.PrivateKey, "0x"))
    if e != nil { return "", nil, "", "", e }
    priv, e = ethcrypto.ToECDSA(b)
    if e != nil { return "", nil, "", "", e }
    pubBytes := ethcrypto.FromECDSAPub(&priv.PublicKey)
    pubHex = "0x" + hex.EncodeToString(pubBytes)
    addr = ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()
    return rec.DID, priv, pubHex, addr, nil
}

func upsertAgent(ks *keyStore, rec agentKey) {
    for i := range ks.Agents {
        if strings.EqualFold(ks.Agents[i].DID, rec.DID) || (ks.Agents[i].Name != "" && ks.Agents[i].Name == rec.Name) {
            ks.Agents[i] = rec
            return
        }
    }
    ks.Agents = append(ks.Agents, rec)
}

func main() {
    keysDir := flag.String("keys", "keys", "keys directory")
    allPath := flag.String("all", filepath.Join("keys", "all_keys.json"), "path to all_keys.json")
    agents := flag.String("agents", "root,payment,planning,ordering,client", "comma-separated agent names to sync if key exists")
    flag.Parse()

    // Load existing all_keys.json (if present)
    ks := &keyStore{}
    if f, err := os.Open(*allPath); err == nil {
        _ = json.NewDecoder(f).Decode(ks)
        f.Close()
    }

    for _, name := range strings.Split(*agents, ",") {
        name = strings.TrimSpace(name)
        if name == "" { continue }
        path := filepath.Join(*keysDir, fmt.Sprintf("%s.key", name))
        if _, err := os.Stat(path); err != nil { continue }
        did, _, pubHex, addr, err := readKeyFile(path)
        if err != nil {
            log.Printf("[%s] skip: %v", name, err)
            continue
        }
        rec := agentKey{DID: did, Name: name, PublicKey: pubHex, Address: addr}
        upsertAgent(ks, rec)
        log.Printf("synced %s -> DID=%s addr=%s", name, did, addr)
    }

    if err := os.MkdirAll(filepath.Dir(*allPath), 0o755); err != nil {
        log.Fatal(err)
    }
    f, err := os.Create(*allPath)
    if err != nil { log.Fatal(err) }
    defer f.Close()
    enc := json.NewEncoder(f)
    enc.SetIndent("", "  ")
    if err := enc.Encode(ks); err != nil { log.Fatal(err) }
    fmt.Printf("updated %s\n", *allPath)
}

