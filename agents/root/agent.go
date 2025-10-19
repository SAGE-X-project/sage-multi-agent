// package root

package root

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
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	servermw "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	keys "github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/did"

	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage-multi-agent/websocket"
)

// RootAgent routes requests to specialized agents (planning/ordering/payment)
type RootAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool

	agents map[string]*AgentInfo
	hub    *websocket.Hub
	mu     sync.RWMutex

	// Outbound A2A signer and inbound DID verifier (with dynamic optional switch)
	a2aClient *a2aclient.A2AClient
	didV      verifier.DIDVerifier
	didMW     *servermw.DIDAuthMiddleware

	myDID     did.AgentDID
	myKeyPair sagecrypto.KeyPair
}

// AgentInfo holds registered agent metadata
type AgentInfo struct {
	Name     string
	Endpoint string
	Type     string
	Active   bool
}

// NewRootAgent initializes the root agent
func NewRootAgent(name string, port int) *RootAgent {
	ra := &RootAgent{
		Name:        name,
		Port:        port,
		SAGEEnabled: true,
		agents:      make(map[string]*AgentInfo),
		hub:         websocket.NewHub(),
	}
	// Load self identity from keys/root.key
	if didStr, kp, err := loadAgentIdentity("root"); err != nil {
		log.Printf("[root] failed to load identity: %v (unsigned downstream)", err)
	} else {
		ra.myDID = did.AgentDID(didStr)
		ra.myKeyPair = kp
		ra.a2aClient = a2aclient.NewA2AClient(ra.myDID, ra.myKeyPair, nil)
	}
	// File-backed DID verifier
	if v, err := newLocalDIDVerifier(); err != nil {
		log.Printf("[root] verifier init failed: %v (no inbound verification)", err)
	} else {
		ra.didV = v
	}
	return ra
}

// RegisterAgent registers an agent endpoint by type
func (ra *RootAgent) RegisterAgent(agentType, name, endpoint string) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	ra.agents[agentType] = &AgentInfo{
		Name:     name,
		Endpoint: endpoint,
		Type:     agentType,
		Active:   true,
	}
	log.Printf("Registered agent: %s (%s) at %s", name, agentType, endpoint)
}

// RouteRequest decides a target agent and forwards
func (ra *RootAgent) RouteRequest(ctx context.Context, request *types.AgentMessage) (*types.AgentMessage, error) {
	ra.mu.RLock()
	defer ra.mu.RUnlock()

	agentType := ra.determineAgentType(request.Content)
	agent, ok := ra.agents[agentType]
	if !ok || !agent.Active {
		return nil, fmt.Errorf("no active agent for type: %s", agentType)
	}
	return ra.forwardToAgent(ctx, agent, request)
}

// Simple keyword-based router
func (ra *RootAgent) determineAgentType(content string) string {
	c := strings.ToLower(content)
	for _, kw := range []string{"hotel", "travel", "plan", "trip", "accommodation", "reserve", "booking"} {
		if strings.Contains(c, kw) {
			return "planning"
		}
	}
	for _, kw := range []string{"order", "buy", "purchase", "shop", "product", "item", "cart"} {
		if strings.Contains(c, kw) {
			return "ordering"
		}
	}
	for _, kw := range []string{"pay", "payment", "transfer", "coin", "crypto", "wallet", "send"} {
		if strings.Contains(c, kw) {
			return "payment"
		}
	}
	return "planning"
}

// Forward to a specific agent; sign only when SAGE is ON
func (ra *RootAgent) forwardToAgent(ctx context.Context, agent *AgentInfo, request *types.AgentMessage) (*types.AgentMessage, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(agent.Endpoint, "/") + "/process"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	if ra.SAGEEnabled && ra.a2aClient != nil {
		// A2A-sign to downstream (e.g., Payment) when SAGE is ON
		resp, err = ra.a2aClient.Do(ctx, req)
	} else {
		// Plain HTTP when SAGE is OFF
		resp, err = http.DefaultClient.Do(req)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("downstream %s returned %d: %s", agent.Endpoint, resp.StatusCode, string(respBytes))
	}

	var out types.AgentMessage
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ToggleSAGE flips signing (outbound) and verification (inbound)
func (ra *RootAgent) ToggleSAGE(enabled bool) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	ra.SAGEEnabled = enabled
	// Inbound verification policy: optional=false when SAGE is ON; optional=true when OFF
	if ra.didMW != nil {
		ra.didMW.SetOptional(!enabled)
	}
	log.Printf("[root] SAGE %v (verify optional=%v)", enabled, !enabled)

	// Notify WS clients
	if ra.hub != nil {
		msg := map[string]any{"type": "sage_status", "enabled": enabled}
		if b, _ := json.Marshal(msg); b != nil {
			ra.hub.Broadcast(b)
		}
	}
}

// Start HTTP server with DIDAuth middleware whose optional flag is updated by ToggleSAGE
func (ra *RootAgent) Start() error {
	go ra.hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/process", ra.handleProcessRequest)
	mux.HandleFunc("/status", ra.handleStatus)
	mux.HandleFunc("/toggle-sage", ra.handleToggleSAGE)
	mux.HandleFunc("/ws", ra.handleWebSocket)

	var handler http.Handler = mux
	if ra.didV != nil {
		mw := servermw.NewDIDAuthMiddlewareWithVerifier(ra.didV)
		// Initial policy from current SAGEEnabled
		mw.SetOptional(!ra.SAGEEnabled)
		ra.didMW = mw
		handler = mw.Wrap(handler)
	}

	log.Printf("Root Agent starting on port %d", ra.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", ra.Port), handler)
}

// Handlers
func (ra *RootAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req types.AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	resp, err := ra.RouteRequest(r.Context(), &req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (ra *RootAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	ra.mu.RLock()
	defer ra.mu.RUnlock()
	status := map[string]any{
		"name":         ra.Name,
		"sage_enabled": ra.SAGEEnabled,
		"agents":       ra.agents,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (ra *RootAgent) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
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
	ra.ToggleSAGE(req.Enabled)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": req.Enabled})
}

func (ra *RootAgent) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte("WebSocket support coming soon"))
}

// ---------- File-backed verifier helpers ----------

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
func newLocalDIDVerifier() (verifier.DIDVerifier, error) {
	db, err := loadLocalKeys()
	if err != nil {
		return nil, err
	}
	client := &fileEthereumClient{db: db}
	selector := verifier.NewDefaultKeySelector(client)
	sigV := verifier.NewRFC9421Verifier()
	return verifier.NewDefaultDIDVerifier(client, selector, sigV), nil
}
func loadLocalKeys() (*localKeys, error) {
	path := filepath.Join("keys", "all_keys.json")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	// FIX: proper separate JSON tags for each field
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
			log.Printf("[root] skip DID %s: %v", a.DID, err)
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
