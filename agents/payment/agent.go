// package payment

package payment

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	server "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	keys "github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/did"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// Wallet represents a cryptocurrency wallet
type Wallet struct {
	Address string             `json:"address"`
	Owner   string             `json:"owner"`
	Balance map[string]float64 `json:"balance"` // currency -> amount
}

// Transaction represents a payment transaction
type Transaction struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"`
	TxHash   string  `json:"tx_hash"`
}

// PaymentAgent handles cryptocurrency payments.
// In this version, the agent forwards /process to an external payment agent.
// Set PAYMENT_EXTERNAL_URL (or PAYMENT_UPSTREAM) to http://host:port of that external agent.
type PaymentAgent struct {
	Name         string
	Port         int
	SAGEEnabled  bool
	wallets      map[string]*Wallet
	transactions map[string]*Transaction

	// inbound verification
	didV  verifier.DIDVerifier
	didMW *server.DIDAuthMiddleware

	// outbound signing
	myDID     did.AgentDID
	myKeyPair sagecrypto.KeyPair
	a2a       *a2aclient.A2AClient

	// upstream external payment endpoint base (e.g., http://localhost:28083)
	upstreamBase string
}

// NewPaymentAgent constructs the PaymentAgent.
// Upstream endpoint is read from env (PAYMENT_EXTERNAL_URL or PAYMENT_UPSTREAM).
func NewPaymentAgent(name string, port int) *PaymentAgent {
	pa := &PaymentAgent{
		Name:         name,
		Port:         port,
		SAGEEnabled:  true,
		wallets:      map[string]*Wallet{},
		transactions: map[string]*Transaction{},
	}
	// Load identity from keys/payment.key (optional but required to sign)
	if didStr, kp, err := loadAgentIdentity("payment"); err != nil {
		log.Printf("[payment] load identity failed: %v (will send unsigned upstream)", err)
	} else {
		pa.myDID = did.AgentDID(didStr)
		pa.myKeyPair = kp
		pa.a2a = a2aclient.NewA2AClient(pa.myDID, pa.myKeyPair, nil)
	}
	// Upstream base from environment
	if v := strings.TrimSpace(os.Getenv("PAYMENT_EXTERNAL_URL")); v != "" {
		pa.upstreamBase = strings.TrimRight(v, "/")
	} else if v := strings.TrimSpace(os.Getenv("PAYMENT_UPSTREAM")); v != "" {
		pa.upstreamBase = strings.TrimRight(v, "/")
	}
	return pa
}

// Start HTTP server with DID auth middleware; optional flag tracks SAGEEnabled dynamically
func (pa *PaymentAgent) Start() error {
	db, err := loadLocalKeys()
	if err != nil {
		log.Printf("[payment] load keys failed: %v", err)
	}

	if db != nil {
		pa.didMW = server.NewDIDAuthMiddleware(&fileEthereumClient{db: db})
		pa.didMW.SetOptional(!pa.SAGEEnabled) // require signature only when SAGE is ON
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/process", pa.handleProcessRequest)
	mux.HandleFunc("/status", pa.handleStatus)
	mux.HandleFunc("/toggle-sage", pa.handleToggleSAGE)

	var handler http.Handler = mux
	if pa.didMW != nil {
		// Always wrap once; optional flag toggles verification requirement at runtime
		handler = pa.didMW.Wrap(handler)
	}

	log.Printf("Payment Agent starting on port %d (upstream=%s)", pa.Port, pa.upstreamBase)
	return http.ListenAndServe(fmt.Sprintf(":%d", pa.Port), handler)
}

// handleProcessRequest receives an AgentMessage and forwards it to the external payment agent.
// If SAGE is ON and the agent has a DID+key, the outbound request is DID-signed.
func (pa *PaymentAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var msg types.AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// No upstream? Respond with a simple stub to keep the flow working.
	if pa.upstreamBase == "" {
		stub := types.AgentMessage{
			ID:        "payment-stub-" + fmt.Sprint(time.Now().UnixNano()),
			From:      pa.Name,
			To:        msg.From,
			Content:   "[payment] upstream not configured; handled locally (stub)",
			Type:      "response",
			Timestamp: time.Now(),
			Metadata:  map[string]any{"agent_type": "payment", "mode": "local-stub"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stub)
		return
	}

	// Forward the same message to the external payment agent
	body, _ := json.Marshal(msg)
	url := pa.upstreamBase + "/process"

	reqOut, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	reqOut.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var err error
	if pa.SAGEEnabled && pa.a2a != nil {
		// Sign outbound request to upstream when SAGE is ON
		resp, err = pa.a2a.Do(r.Context(), reqOut)
	} else {
		// Plain HTTP when SAGE is OFF
		resp, err = http.DefaultClient.Do(reqOut)
	}
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Relay upstream response as-is to the caller (client-api → root → payment → external → back)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// handleStatus provides basic runtime info
func (pa *PaymentAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"name":          pa.Name,
		"type":          "payment",
		"port":          pa.Port,
		"sage_enabled":  pa.SAGEEnabled,
		"upstream_base": pa.upstreamBase,
		"time":          time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleToggleSAGE flips optional flag on middleware and affects outbound signing mode
func (pa *PaymentAgent) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	pa.SAGEEnabled = req.Enabled
	if pa.didMW != nil {
		pa.didMW.SetOptional(!pa.SAGEEnabled)
	}
	log.Printf("[payment] SAGE %v (verify optional=%v)", pa.SAGEEnabled, !pa.SAGEEnabled)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": req.Enabled})
}

// ---------- Local DID verification helpers ----------

type localKeys struct {
	pub  map[did.AgentDID]map[did.KeyType]interface{}
	keys map[did.AgentDID][]did.AgentKey
}
type fileEthereumClient struct{ db *localKeys }

func (c *fileEthereumClient) ResolveAllPublicKeys(ctx context.Context, agentDID did.AgentDID) ([]did.AgentKey, error) {
	if keys, ok := c.db.keys[agentDID]; ok {
		return keys, nil
	}
	return nil, fmt.Errorf("no keys for DID: %s", agentDID)
}
func (c *fileEthereumClient) ResolvePublicKeyByType(ctx context.Context, agentDID did.AgentDID, keyType did.KeyType) (interface{}, error) {
	if m, ok := c.db.pub[agentDID]; ok {
		if pk, ok2 := m[keyType]; ok2 {
			return pk, nil
		}
	}
	return nil, fmt.Errorf("key type %v not found for %s", keyType, agentDID)
}
func loadLocalKeys() (*localKeys, error) {
	path := filepath.Join("keys", "all_keys.json")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	// FIX: proper separate JSON tags
	var data struct {
		Agents []struct {
			DID       string `json:"did"`
			PublicKey string `json:"publicKey"`
			Type      string `json:"type"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	db := &localKeys{pub: make(map[did.AgentDID]map[did.KeyType]interface{}), keys: make(map[did.AgentDID][]did.AgentKey)}
	for _, a := range data.Agents {
		d := did.AgentDID(a.DID)
		if _, ok := db.pub[d]; !ok {
			db.pub[d] = make(map[did.KeyType]interface{})
		}
		pk, err := parseSecp256k1ECDSAPublicKey(a.PublicKey)
		if err != nil {
			log.Printf("[payment] skip DID %s: %v", a.DID, err)
			continue
		}
		db.pub[d][did.KeyTypeECDSA] = pk
		db.keys[d] = []did.AgentKey{{
			Type:      did.KeyTypeECDSA,
			KeyData:   mustUncompressedECDSA(pk),
			Verified:  true,
			CreatedAt: time.Now(),
		}}
	}
	return db, nil
}
func parseSecp256k1ECDSAPublicKey(hexStr string) (*ecdsa.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid hex pubkey: %w", err)
	}
	pub, err := secp256k1.ParsePubKey(b)
	if err != nil {
		return nil, fmt.Errorf("parse pubkey: %w", err)
	}
	return pub.ToECDSA(), nil
}
func mustUncompressedECDSA(pk *ecdsa.PublicKey) []byte {
	byteLen := (pk.Curve.Params().BitSize + 7) / 8
	out := make([]byte, 1+2*byteLen)
	out[0] = 0x04
	pk.X.FillBytes(out[1 : 1+byteLen])
	pk.Y.FillBytes(out[1+byteLen:])
	return out
}
func loadAgentIdentity(name string) (string, sagecrypto.KeyPair, error) {
	path := filepath.Join("keys", fmt.Sprintf("%s.key", name))
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	// FIX: proper separate JSON tags
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
