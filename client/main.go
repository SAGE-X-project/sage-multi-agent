package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
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

func main() {
	port := flag.Int("port", 8086, "client server port")
	root := flag.String("root", "http://localhost:18080", "Root agent base URL")
	payment := flag.String("payment", "http://localhost:18083", "Payment agent base URL")
	clientKey := flag.String("client-key", filepath.Join("keys", "client.key"), "client key file")
	flag.Parse()

	// Load client DID+private key from keys/client.key
	clientDID, clientKP, err := loadClientIdentity(*clientKey)
	if err != nil {
		panic(err)
	}

	// A2A client is injected into the API so it can sign requests when SAGE is ON
	a2a := a2aclient.NewA2AClient(did.AgentDID(clientDID), clientKP, nil)

	// Build ClientAPI that can send plain HTTP or DID-signed (per X-SAGE-Enabled)
	api := apihandlers.NewClientAPIWithA2A(*root, *payment, nil, a2a)

	mux := http.NewServeMux()
	mux.HandleFunc("/send/prompt", api.HandlePrompt)
	mux.HandleFunc("/api/payment", api.HandlePayment)
	mux.HandleFunc("/api/ordering", api.HandleOrdering)
	mux.HandleFunc("/api/planning", api.HandlePlanning)

	// Optional: handy endpoints to inspect a local SAGE flag
	sh := apihandlers.NewSAGEHandler()
	sh.RegisterRoutes(mux)

	fmt.Printf("Client server listening on :%d (root=%s payment=%s)\n", *port, *root, *payment)
	_ = http.ListenAndServe(fmt.Sprintf(":%d", *port), mux)
}

func loadClientIdentity(path string) (string, sagecrypto.KeyPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	// FIX: Proper, separate JSON tags (the combined tag was invalid)
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
