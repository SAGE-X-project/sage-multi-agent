//go:build demo
// +build demo

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/agents/root"
	apihandlers "github.com/sage-x-project/sage-multi-agent/api"
	"github.com/sage-x-project/sage-multi-agent/types"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	keys "github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func main() {
	fmt.Println("==============================================")
	fmt.Println("HTTP → Client(A2A) → Root(sign) → Payment(verify)")
	fmt.Println("==============================================")

	// Start Payment agent
	pay := payment.NewPaymentAgent("PaymentAgent", 18083)
	pay.SAGEEnabled = true
	go func() {
		if err := pay.Start(); err != nil {
			log.Fatalf("payment: %v", err)
		}
	}()

	// Start Root agent with payment route
	rt := root.NewRootAgent("RootAgent", 18080)
	rt.SAGEEnabled = true
	rt.RegisterAgent("payment", "PaymentAgent", "http://localhost:18083")
	go func() {
		if err := rt.Start(); err != nil {
			log.Fatalf("root: %v", err)
		}
	}()

	time.Sleep(800 * time.Millisecond)

	// Client HTTP server: use api.Server to manage SAGE toggles and forwarding
	mux := http.NewServeMux()
	apiSrv := apihandlers.NewAgentGateway("http://localhost:18080", "http://localhost:18083", nil)
	mux.HandleFunc("/send/prompt", apiSrv.HandlePrompt)
	go func() { _ = http.ListenAndServe(":18085", mux) }()

	time.Sleep(300 * time.Millisecond)

	// Send prompt to client API which adapts to A2A
	req := types.PromptRequest{Prompt: "please pay 100 USDC to merchant"}
	b, _ := json.Marshal(req)
	res, err := http.Post("http://localhost:18085/send/prompt", "application/json", bytes.NewReader(b))
	if err != nil {
		log.Fatalf("send: %v", err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	fmt.Printf("Response (%d): %s\n", res.StatusCode, string(out))
}

func mustLoadKeyPair(agent string) (did.AgentDID, sagecrypto.KeyPair) {
	p := filepath.Join("keys", fmt.Sprintf("%s.key", agent))
	f, err := os.Open(p)
	if err != nil {
		log.Fatalf("open %s: %v", p, err)
	}
	defer f.Close()
	var rec struct{ DID, PrivateKey string }
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		log.Fatalf("decode %s: %v", p, err)
	}
	sk, err := hex.DecodeString(rec.PrivateKey)
	if err != nil {
		log.Fatalf("hex: %v", err)
	}
	priv := secp256k1.PrivKeyFromBytes(sk)
	kp, err := keys.NewSecp256k1KeyPair(priv, "")
	if err != nil {
		log.Fatalf("keypair: %v", err)
	}
	return did.AgentDID(rec.DID), kp
}
