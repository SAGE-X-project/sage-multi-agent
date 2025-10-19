// cmd/client-api/main.go
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	apihandlers "github.com/sage-x-project/sage-multi-agent/api"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func loadClientIdentity(path string) (string, sagecrypto.KeyPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	var rec struct {
		DID        string `json:"did"`
		PrivateKey string `json:"privateKey"`
	}
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return "", nil, err
	}
	privBytes, err := hex.DecodeString(strings.TrimPrefix(rec.PrivateKey, "0x"))
	if err != nil {
		return "", nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	priv := secp256k1.PrivKeyFromBytes(privBytes)
	kp, err := keys.NewSecp256k1KeyPair(priv, "")
	if err != nil {
		return "", nil, err
	}
	return rec.DID, kp, nil
}

func main() {
	port := flag.Int("port", 8086, "client api port")
	root := flag.String("root", "http://localhost:18080", "root agent base URL")
	payment := flag.String("payment", "http://localhost:18083", "payment agent base URL")
	clientKey := flag.String("client-key", filepath.Join("keys", "client.key"), "client key file")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var a2a *a2aclient.A2AClient
	if didStr, kp, err := loadClientIdentity(*clientKey); err != nil {
		log.Printf("[client-api] no client DID key (%v): running without A2A signing", err)
	} else {
		a2a = a2aclient.NewA2AClient(did.AgentDID(didStr), kp, nil)
		log.Printf("[client-api] DID loaded: %s", didStr)
	}

	api := apihandlers.NewClientAPIWithA2A(*root, *payment, http.DefaultClient, a2a)

	mux := http.NewServeMux()
	mux.HandleFunc("/send/prompt", api.HandlePrompt)
	mux.HandleFunc("/api/payment", api.HandlePayment)
	mux.HandleFunc("/api/ordering", api.HandleOrdering)
	mux.HandleFunc("/api/planning", api.HandlePlanning)

	sh := apihandlers.NewSAGEHandler()
	sh.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[client-api] listening on %s (root=%s payment=%s)", addr, *root, *payment)
	log.Fatal(http.ListenAndServe(addr, mux))
}
