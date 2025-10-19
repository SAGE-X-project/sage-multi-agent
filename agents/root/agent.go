// package root

package root

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
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
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	servermw "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	rfc9421 "github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
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
	myECDSA   *ecdsa.PrivateKey
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
		if ecdsaPriv, err := loadAgentECDSA("root"); err == nil {
			ra.myECDSA = ecdsaPriv
		} else {
			log.Printf("[root] ECDSA load failed: %v (fallback to a2a signer)", err)
		}
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
	if req.Host == "" { // ensure authority matches during signing
		req.Host = req.URL.Host
	}
	// Ensure Content-Digest is present for RFC9421 verification downstream
	{
		sum := sha256.Sum256(body)
		req.Header.Set("Content-Digest", fmt.Sprintf("sha-256=:%s:", base64.StdEncoding.EncodeToString(sum[:])))
	}
	// Optional packet trace
	tracePackets := strings.EqualFold(os.Getenv("SAGE_PACKET_TRACE"), "true") || os.Getenv("SAGE_PACKET_TRACE") == "1"
	traceBody := strings.EqualFold(os.Getenv("SAGE_TRACE_INCLUDE_BODY"), "true") || os.Getenv("SAGE_TRACE_INCLUDE_BODY") == "1"
	if tracePackets {
		if traceBody {
			b := string(body)
			if len(b) > 512 {
				b = b[:512] + "..."
			}
			log.Printf("[packet-trace][root→%s] POST %s len=%d Digest=%q Sig=%t SigInput=%t Body=%s",
				agent.Type, req.URL.Path, len(body), req.Header.Get("Content-Digest"), req.Header.Get("Signature") != "", req.Header.Get("Signature-Input") != "", b)
		} else {
			log.Printf("[packet-trace][root→%s] POST %s len=%d Digest=%q Sig=%t SigInput=%t",
				agent.Type, req.URL.Path, len(body), req.Header.Get("Content-Digest"), req.Header.Get("Signature") != "", req.Header.Get("Signature-Input") != "")
		}
	}

	var resp *http.Response
	if ra.SAGEEnabled && ra.myECDSA != nil {
		// Explicit RFC9421 es256k signing for compatibility with verifier
		params := &rfc9421.SignatureInputParams{
			// Drop @authority to avoid Host variations causing false negatives
			CoveredComponents: []string{"\"@method\"", "\"@target-uri\"", "\"content-type\"", "\"content-digest\""},
			KeyID:             string(ra.myDID),
			Algorithm:         "es256k",
			Created:           time.Now().Unix(),
		}
		signer := rfc9421.NewHTTPVerifier()
		if err = signer.SignRequest(req, "sig1", params, ra.myECDSA); err != nil {
			return nil, fmt.Errorf("sign request: %w", err)
		}
		resp, err = http.DefaultClient.Do(req)
	} else if ra.SAGEEnabled && ra.a2aClient != nil {
		// Fallback to A2A signer
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
	// Root uses SAGEEnabled only for OUTBOUND signing; inbound verification remains optional.
	ra.SAGEEnabled = enabled
	log.Printf("[root] SAGE %v (inbound verify optional=true)", enabled)

	// Notify WS clients
	if ra.hub != nil {
		msg := map[string]any{"type": "sage_status", "enabled": enabled}
		if b, _ := json.Marshal(msg); b != nil {
			ra.hub.Broadcast(b)
		}
	}
}

// Start HTTP server: wrap only sensitive paths with DIDAuth; keep /status open
func (ra *RootAgent) Start() error {
	go ra.hub.Run()

	// Public mux (no signature required)
	public := http.NewServeMux()
	public.HandleFunc("/status", ra.handleStatus)
	public.HandleFunc("/ws", ra.handleWebSocket)
	public.HandleFunc("/toggle-sage", ra.handleToggleSAGE)

	// Secured mux (Root does not require inbound signatures; verification optional)
	secured := http.NewServeMux()
	secured.HandleFunc("/process", ra.handleProcessRequest)
	secured.HandleFunc("/toggle-sage", ra.handleToggleSAGE)

	var securedHandler http.Handler = secured

	// Attach DID middleware (optional mode) only to secured endpoints
	if ra.didV != nil {
		mw := servermw.NewDIDAuthMiddlewareWithVerifier(ra.didV)
		// Always optional at Root (never block inbound when signatures missing)
		mw.SetOptional(true)
		ra.didMW = mw
		securedHandler = mw.Wrap(securedHandler)
	}

	// Root mux mounts public routes and secured routes
	rootMux := http.NewServeMux()
	rootMux.Handle("/", public) // /status, /ws
	rootMux.Handle("/process", securedHandler)

	log.Printf("Root Agent starting on port %d", ra.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", ra.Port), rootMux)
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
		// Propagate downstream status code when possible instead of always 500
		if code := parseDownstreamStatus(err.Error()); code != 0 {
			http.Error(w, err.Error(), code)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// parseDownstreamStatus extracts an HTTP status code from an error message of the form
// "downstream <url> returned <code>: <body>". Returns 0 if not found.
func parseDownstreamStatus(msg string) int {
	const marker = " returned "
	i := strings.Index(msg, marker)
	if i < 0 {
		return 0
	}
	rest := msg[i+len(marker):]
	// rest should begin with code, e.g., "401: ..."
	var code int
	if n, _ := fmt.Sscanf(rest, "%d", &code); n == 1 {
		if code >= 100 && code <= 599 {
			return code
		}
	}
	return 0
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

// loadAgentECDSA loads the agent ECDSA private key from keys/<name>.key (hex), returning *ecdsa.PrivateKey
func loadAgentECDSA(name string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join("keys", fmt.Sprintf("%s.key", name))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rec struct {
		DID        string `json:"did"`
		PrivateKey string `json:"privateKey"`
	}
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimPrefix(rec.PrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	// Convert using go-ethereum to ensure canonical ECDSA format
	k, err := ethcrypto.ToECDSA(b)
	if err != nil {
		return nil, err
	}
	return k, nil
}
