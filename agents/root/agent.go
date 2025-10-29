// Package root: RootAgent does in-proc routing AND owns outbound HTTP to external agents.
// It signs outbound HTTP (RFC 9421 via A2A client) and optionally uses HPKE for payload
// encryption. Sub-agents focus on business logic; Root handles network crypto.
//
// 한국어 설명:
// - 외부 서비스로의 HTTP 전송, RFC9421 서명, HPKE 암복호화를 Root가 전담합니다.
// - 서브 에이전트(planning/ordering)는 로컬 비즈니스 로직만 수행하고, payment는 외부 서버로만 보냅니다.
// - 외부 URL이 없을 때만 planning/ordering에 대해 로컬 fallback을 사용합니다( payment는 fallback 제거 ).
package root

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"

	// Sub agents (in-proc fallback for planning/ordering only)
	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/agents/planning"

	// A2A & transport
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	prototx "github.com/sage-x-project/sage-multi-agent/protocol"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	// DID & crypto
	"github.com/sage-x-project/sage-multi-agent/types"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
	"github.com/sage-x-project/sage/pkg/agent/hpke"
	"github.com/sage-x-project/sage/pkg/agent/session"
)

// ---- RootAgent ----

// ctx keys for per-request toggles
type ctxKey string

const (
	ctxUseSAGEKey ctxKey = "useSAGE"
	ctxHPKERawKey ctxKey = "hpkeRaw"
)

type RootAgent struct {
	name string
	port int

	mux    *http.ServeMux
	server *http.Server

	// IN-PROC agents (fallback only; Root owns network crypto)
	planning *planning.PlanningAgent
	ordering *ordering.OrderingAgent

	logger *log.Logger

	// Outbound signing & HTTP
	httpClient  *http.Client
	sageEnabled bool
	myDID       sagedid.AgentDID
	myKey       sagecrypto.KeyPair
	a2a         *a2aclient.A2AClient

	// External base URLs per agent (routing target)
	extBase map[string]string // key: "planning"|"ordering"|"payment" -> base URL

	// HPKE per-target state
	hpkeStates sync.Map // key: target string -> *hpkeState
	resolver   sagedid.Resolver
}

// hpkeState holds per-target HPKE session context.
type hpkeState struct {
	cli  *hpke.Client
	sMgr *session.Manager
	kid  string
}

// ---- Construction ----

func NewRootAgent(name string, port int, p *planning.PlanningAgent, o *ordering.OrderingAgent) *RootAgent {
	mux := http.NewServeMux()

	// Resolve external URLs from env (defaults allow per-agent separation)
	ext := map[string]string{
		"planning": strings.TrimRight(envOr("PLANNING_EXTERNAL_URL", ""), "/"),
		"ordering": strings.TrimRight(envOr("ORDERING_EXTERNAL_URL", ""), "/"),
		"payment":  strings.TrimRight(envOr("PAYMENT_EXTERNAL_URL", "http://localhost:5500"), "/"),
	}

	ra := &RootAgent{
		name:        name,
		port:        port,
		mux:         mux,
		planning:    p,
		ordering:    o,
		logger:      log.New(os.Stdout, "[root] ", log.LstdFlags),
		httpClient:  http.DefaultClient,
		a2a:         nil,
		sageEnabled: envBool("ROOT_SAGE_ENABLED", true),
		extBase:     ext,
	}
	// Lazy init: signing & resolver will be initialized on first use

	ra.mountRoutes()
	return ra
}

func (r *RootAgent) Start() error {
	addr := fmt.Sprintf(":%d", r.port)
	r.server = &http.Server{Addr: addr, Handler: r.mux}
	r.logger.Printf("[root] listening on %s", addr)
	return r.server.ListenAndServe()
}

// ---- Signing & HTTP (A2A) ----

// Do implements the http Doer used by transports. When SAGE (signing) is enabled,
// requests are signed via A2A client (RFC 9421). Otherwise plain http.Client is used.
func (r *RootAgent) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Per-request override via context (defaults to global)
	useSign := r.sageEnabled
	if v := ctx.Value(ctxUseSAGEKey); v != nil {
		if b, ok := v.(bool); ok {
			useSign = b
		}
	}
	if useSign {
		if r.a2a == nil {
			if err := r.initSigning(); err != nil {
				return nil, err
			}
		}
		return r.a2a.Do(ctx, req)
	}
	return r.httpClient.Do(req)
}

func (r *RootAgent) initSigning() error {
	jwk := strings.TrimSpace(os.Getenv("ROOT_JWK_FILE"))
	if jwk == "" {
		return fmt.Errorf("ROOT_JWK_FILE required for Root signing")
	}
	raw, err := os.ReadFile(jwk)
	if err != nil {
		return fmt.Errorf("read ROOT_JWK_FILE: %w", err)
	}
	imp := formats.NewJWKImporter()
	kp, err := imp.Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return fmt.Errorf("import ROOT_JWK_FILE: %w", err)
	}

	didStr := strings.TrimSpace(os.Getenv("ROOT_DID"))
	if didStr == "" {
		// Try derive Ethereum-style DID if ECDSA, else fallback to kp.ID
		if ecdsaPriv, ok := kp.PrivateKey().(*ecdsa.PrivateKey); ok {
			addr := ethcrypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex()
			didStr = "did:sage:ethereum:" + addr
		} else if id := strings.TrimSpace(kp.ID()); id != "" {
			didStr = "did:sage:generated:" + id
		} else {
			return fmt.Errorf("ROOT_DID not set and cannot derive from key")
		}
	}
	r.myKey = kp
	r.myDID = sagedid.AgentDID(didStr)
	r.a2a = a2aclient.NewA2AClient(r.myDID, r.myKey, r.httpClient)
	return nil
}

// ---- Resolver (for HPKE) ----

func (r *RootAgent) ensureResolver() error {
	if r.resolver != nil {
		return nil
	}
	rpc := firstNonEmpty(os.Getenv("ETH_RPC_URL"), "http://127.0.0.1:8545")
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_V4_ADDRESS"), "0x5FbDB2315678afecb367f032d93F642f64180aa3")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	cfgV4 := &sagedid.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv, // optional for read-only
		GasPrice:           0,
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}
	ethV4, err := dideth.NewEthereumClientV4(cfgV4)
	if err != nil {
		return fmt.Errorf("HPKE: init resolver: %w", err)
	}
	r.resolver = ethV4
	return nil
}

// ---- HPKE per-target management ----

// IsHPKEEnabled reports whether HPKE session exists for the target agent.
func (r *RootAgent) IsHPKEEnabled(target string) bool {
	_, ok := r.hpkeStates.Load(strings.ToLower(strings.TrimSpace(target)))
	return ok
}

// CurrentHPKEKID returns the current KID for target if present.
func (r *RootAgent) CurrentHPKEKID(target string) string {
	key := strings.ToLower(strings.TrimSpace(target))
	if v, ok := r.hpkeStates.Load(key); ok {
		if st, ok2 := v.(*hpkeState); ok2 {
			return st.kid
		}
	}
	return ""
}

// DisableHPKE clears HPKE state for target.
func (r *RootAgent) DisableHPKE(target string) {
	key := strings.ToLower(strings.TrimSpace(target))
	r.hpkeStates.Delete(key)
}

// EnableHPKE performs handshake to the target external service.
// keysFile contains DID mapping JSON (e.g., merged_agent_keys.json).
// Client DID alias defaults to "root", server alias defaults to "external" or "external-<target>".
func (r *RootAgent) EnableHPKE(ctx context.Context, target, keysFile string) error {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		target = "payment" // default
	}
	if err := r.ensureResolver(); err != nil {
		return err
	}
	if r.myKey == nil || strings.TrimSpace(string(r.myDID)) == "" {
		if err := r.initSigning(); err != nil {
			return fmt.Errorf("HPKE: initSigning failed: %w", err)
		}
	}

	nameToDID, err := loadDIDsFromKeys(firstNonEmpty(strings.TrimSpace(keysFile), "merged_agent_keys.json"))
	if err != nil {
		return fmt.Errorf("HPKE: load keys: %w", err)
	}
	clientDID := strings.TrimSpace(nameToDID["root"])
	if clientDID == "" {
		clientDID = string(r.myDID)
	}
	// Prefer alias "external-<target>" then fallback "external"
	serverAlias := "external-" + target
	serverDID := strings.TrimSpace(nameToDID[serverAlias])
	if serverDID == "" {
		serverDID = strings.TrimSpace(nameToDID["external"])
	}
	if serverDID == "" {
		return fmt.Errorf("HPKE: server DID alias not found (tried %q and \"external\")", serverAlias)
	}

	// Handshake transport uses hpkeHandshake=true for SecureMessage path.
	base := r.externalURLFor(target)
	if base == "" {
		return fmt.Errorf("HPKE: external URL not configured for %q", target)
	}
	// Handshake uses HPKE; emit A2A headers not strictly required, keep minimal
	t := prototx.NewA2ATransport(r, base, true, true)

	sMgr := session.NewManager()
	cli := hpke.NewClient(t, r.resolver, r.myKey, clientDID, hpke.DefaultInfoBuilder{}, sMgr)

	ctxInit, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctxID := "ctx-" + uuid.NewString()
	kid, err := cli.Initialize(ctxInit, ctxID, clientDID, serverDID)
	if err != nil {
		return fmt.Errorf("HPKE Initialize: %w", err)
	}
	if kid == "" {
		return fmt.Errorf("HPKE Initialize returned empty kid")
	}

	r.hpkeStates.Store(target, &hpkeState{cli: cli, sMgr: sMgr, kid: kid})
	r.logger.Printf("[root] HPKE initialized target=%s kid=%s clientDID=%s serverDID=%s", target, kid, clientDID, serverDID)
	return nil
}

func (r *RootAgent) encryptIfHPKE(target string, plaintext []byte) ([]byte, string, bool, error) {
	key := strings.ToLower(strings.TrimSpace(target))
	v, ok := r.hpkeStates.Load(key)
	if !ok {
		return nil, "", false, nil
	}
	st := v.(*hpkeState)
	sess, ok := st.sMgr.GetByKeyID(st.kid)
	if !ok {
		return nil, "", true, fmt.Errorf("HPKE: session not found for kid=%s", st.kid)
	}
	ct, err := sess.Encrypt(plaintext)
	if err != nil {
		return nil, "", true, fmt.Errorf("HPKE encrypt: %w", err)
	}
	return ct, st.kid, true, nil
}

func (r *RootAgent) decryptIfHPKEResponse(target, kid string, data []byte) ([]byte, bool, error) {
	if kid == "" {
		return data, false, nil
	}
	key := strings.ToLower(strings.TrimSpace(target))
	v, ok := r.hpkeStates.Load(key)
	if !ok {
		return nil, true, fmt.Errorf("HPKE: state missing")
	}
	st := v.(*hpkeState)
	sess, ok := st.sMgr.GetByKeyID(kid)
	if !ok {
		return nil, true, fmt.Errorf("HPKE: session not found for kid=%s", kid)
	}
	pt, err := sess.Decrypt(data)
	if err != nil {
		return nil, true, fmt.Errorf("HPKE decrypt response: %w", err)
	}
	return pt, true, nil
}

// ---- Routing helpers ----

func (r *RootAgent) externalURLFor(agent string) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if base, ok := r.extBase[agent]; ok {
		return strings.TrimRight(base, "/")
	}
	return ""
}

func (r *RootAgent) pickAgent(msg *types.AgentMessage) string {
	// 1) explicit domain
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["domain"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				s = strings.ToLower(strings.TrimSpace(s))
				switch s {
				case "planning", "ordering", "payment":
					return s
				}
			}
		}
	}

	// 2) heuristic by content
	c := strings.ToLower(strings.TrimSpace(msg.Content))
	switch {
	case containsAny(c, "pay", "payment", "send", "wallet", "transfer", "crypto", "usdc", "송금", "결제"):
		if r.externalURLFor("payment") != "" {
			return "payment"
		}
	case containsAny(c, "order", "주문", "buy", "purchase", "product", "catalog"):
		if r.ordering != nil || r.externalURLFor("ordering") != "" {
			return "ordering"
		}
	default:
		if r.planning != nil || r.externalURLFor("planning") != "" {
			return "planning"
		}
	}

	if r.planning != nil || r.externalURLFor("planning") != "" {
		return "planning"
	}
	if r.ordering != nil || r.externalURLFor("ordering") != "" {
		return "ordering"
	}
	if r.externalURLFor("payment") != "" {
		return "payment"
	}
	return ""
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		n = strings.TrimSpace(strings.ToLower(n))
		if n == "" {
			continue
		}
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// ---- Outbound send (Root owns external I/O) ----

// sendExternal signs (optionally HPKE-encrypts) and sends the message to the external
// agent endpoint chosen by agent name. Falls back to in-proc agent for planning/ordering
// if URL not configured. Payment has NO in-proc fallback.
//
// 한국어: 외부 URL이 없으면 planning/ordering만 로컬 fallback, payment는 반드시 외부로만 전송.
func (r *RootAgent) sendExternal(ctx context.Context, agent string, msg *types.AgentMessage) (*types.AgentMessage, error) {
	base := r.externalURLFor(agent)
	if base == "" {
		// Fallback to in-proc processing (planning/ordering only)
		switch agent {
		case "planning":
			if r.planning == nil {
				return nil, fmt.Errorf("no planning agent and no external URL")
			}
			out, err := r.planning.Process(ctx, *msg)
			return &out, err
		case "ordering":
			if r.ordering == nil {
				return nil, fmt.Errorf("no ordering agent and no external URL")
			}
			out, err := r.ordering.Process(ctx, *msg)
			return &out, err
		case "payment":
			return nil, fmt.Errorf("payment requires external URL (no in-proc fallback)")
		default:
			return nil, fmt.Errorf("unknown agent: %s", agent)
		}
	}

	body, _ := json.Marshal(msg)

	// Per-request overrides from HTTP headers (propagated via context)
	useSAGE := r.sageEnabled
	if v := ctx.Value(ctxUseSAGEKey); v != nil {
		if b, ok := v.(bool); ok {
			useSAGE = b
		}
	}
	// Default HPKE to current session; override only if header explicitly present
	wantHPKE := r.IsHPKEEnabled(agent)
	if v := ctx.Value(ctxHPKERawKey); v != nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			wantHPKE = strings.EqualFold(strings.TrimSpace(s), "true")
		}
	}
	// Policy: HPKE requires signing
	if !useSAGE {
		wantHPKE = false
	}

	// Prepare A2A signing (lazy)
	if useSAGE && r.a2a == nil {
		if err := r.initSigning(); err != nil {
			return nil, err
		}
	}

	// Lazy HPKE init if requested
	if wantHPKE && !r.IsHPKEEnabled(agent) {
		keys := hpkeKeysPath()
		if err := r.EnableHPKE(ctx, agent, keys); err != nil {
			r.logger.Printf("[root] HPKE init failed (lazy) target=%s: %v", agent, err)
		}
	}

	// Encrypt if HPKE session is active
	var kid string
	if wantHPKE {
		if ct, k, used, err := r.encryptIfHPKE(agent, body); used {
			if err != nil {
				return nil, fmt.Errorf("hpke: %w", err)
			}
			body = ct
			kid = k
			r.logger.Printf("[root] encrypt hpke target=%s kid=%s bytes=%d", agent, k, len(ct))
		} else {
			r.logger.Printf("[root] HPKE requested but no session; sending plaintext (%d bytes)", len(body))
		}
	} else {
		r.logger.Printf("[root] HPKE disabled by request (plaintext) bytes=%d", len(body))
	}

	// Build transport (data mode) for the chosen agent
	// When SAGE is OFF and HPKE is OFF, avoid emitting X-SAGE-* headers to keep plaintext truly unsigned.
	emitHeaders := useSAGE || wantHPKE
	tx := prototx.NewA2ATransport(r, base, false, emitHeaders)
	sm := &transport.SecureMessage{
		ID:      uuid.NewString(),
		Payload: body,
		DID:     string(r.myDID),
		Metadata: map[string]string{
			"ctype": "application/json",
		},
		Role: "agent",
	}
	if kid != "" {
		sm.Metadata["hpke_kid"] = kid
	}

	resp, err := tx.Send(ctx, sm)
	if err != nil {
		return nil, fmt.Errorf("transport send: %w", err)
	}
	if !resp.Success {
		reason := strings.TrimSpace(string(resp.Data))
		if reason == "" && resp.Error != nil {
			reason = resp.Error.Error()
		}
		if reason == "" {
			reason = "unknown upstream error"
		}
		return &types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "external-" + agent,
			To:        msg.From,
			Type:      "error",
			Content:   "external error: " + reason,
			Timestamp: time.Now(),
		}, nil
	}

	// Decrypt HPKE response if used
	if kid != "" {
		if pt, _, derr := r.decryptIfHPKEResponse(agent, kid, resp.Data); derr != nil {
			return &types.AgentMessage{
				ID:        msg.ID + "-exterr",
				From:      "external-" + agent,
				To:        msg.From,
				Type:      "error",
				Content:   "external error: " + derr.Error(),
				Timestamp: time.Now(),
			}, nil
		} else {
			resp.Data = pt
		}
	}

	var out types.AgentMessage
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		out = types.AgentMessage{
			ID:        msg.ID + "-ext",
			From:      "external-" + agent,
			To:        msg.From,
			Type:      "response",
			Content:   strings.TrimSpace(string(resp.Data)),
			Timestamp: time.Now(),
		}
	}
	return &out, nil
}

// ---- HTTP handlers ----

func (r *RootAgent) mountRoutes() {
	// health
	r.mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"name": r.name,
			"type": "root",
			"port": r.port,
			"agents": map[string]any{
				"planning": r.planning != nil,
				"ordering": r.ordering != nil,
			},
			"ext": map[string]any{
				"planning": r.externalURLFor("planning") != "",
				"ordering": r.externalURLFor("ordering") != "",
				"payment":  r.externalURLFor("payment") != "",
			},
			"sage_enabled": r.sageEnabled,
			"time":         time.Now().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Root-level SAGE toggle (global outbound signing)
	// POST {"enabled": true|false}
	r.mux.HandleFunc("/toggle-sage", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		r.sageEnabled = in.Enabled
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": in.Enabled, "scope": "root"})
	})

	// SAGE status
	r.mux.HandleFunc("/sage/status", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"root":     r.sageEnabled,
			"planning": r.planning != nil,
			"ordering": r.ordering != nil,
		})
	})

	// HPKE runtime toggle at Root (per target)
	// POST {"enabled":true, "target":"payment|ordering|planning", "keysFile":"merged_agent_keys.json"}
	r.mux.HandleFunc("/hpke/config", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Enabled  bool   `json:"enabled"`
			Target   string `json:"target,omitempty"`
			KeysFile string `json:"keysFile,omitempty"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		target := strings.ToLower(strings.TrimSpace(in.Target))
		if target == "" {
			target = "payment"
		}
		if !in.Enabled {
			r.DisableHPKE(target)
		} else {
			if err := r.EnableHPKE(req.Context(), target, strings.TrimSpace(in.KeysFile)); err != nil {
				http.Error(w, "hpke init failed: "+err.Error(), http.StatusBadGateway)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled": in.Enabled,
			"target":  target,
			"kid":     r.CurrentHPKEKID(target),
		})
	})

	// HPKE status (per target); query ?target=payment|ordering|planning (default payment)
	r.mux.HandleFunc("/hpke/status", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		target := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("target")))
		if target == "" {
			target = "payment"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"target":  target,
			"enabled": r.IsHPKEEnabled(target),
			"kid":     r.CurrentHPKEKID(target),
		})
	})

	// Main in-proc processing (Client API -> Root -> external or in-proc fallback)
	r.mux.HandleFunc("/process", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var msg types.AgentMessage
		if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		agent := r.pickAgent(&msg)
		if agent == "" {
			http.Error(w, "no agent available", http.StatusBadGateway)
			return
		}

		// Per-request toggles from headers
		// - HPKE requires SAGE=true; if explicitly conflicting, return 400
		sageRaw := strings.TrimSpace(req.Header.Get("X-SAGE-Enabled"))
		hpkeRaw := strings.TrimSpace(req.Header.Get("X-HPKE-Enabled"))

		if hpkeRaw != "" && strings.EqualFold(hpkeRaw, "true") {
			// HPKE를 명시적으로 켠 요청인데 SAGE가 명시적으로 꺼져 있다면 400
			if sageRaw != "" && !strings.EqualFold(sageRaw, "true") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":   "bad_request",
					"message": "HPKE requires SAGE to be enabled (X-SAGE-Enabled: true)",
				})
				return
			}
		}

		ctx := req.Context()
		if sageRaw != "" {
			ctx = context.WithValue(ctx, ctxUseSAGEKey, strings.EqualFold(sageRaw, "true"))
		}
		if hpkeRaw != "" {
			ctx = context.WithValue(ctx, ctxHPKERawKey, hpkeRaw) // "true"/"false" 그대로 넘김
		}

		outPtr, err := r.sendExternal(ctx, agent, &msg)
		if err != nil {
			http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
			return
		}
		out := *outPtr

		status := http.StatusOK
		if code, ok := httpStatusFromAgent(&out); ok {
			status = code
		}

		w.Header().Set("Content-Type", "application/json")
		if status/100 == 2 {
			w.Header().Set("X-SAGE-Verified", "true")
			w.Header().Set("X-SAGE-Signature-Valid", "true")
		} else {
			w.Header().Set("X-SAGE-Verified", "false")
			w.Header().Set("X-SAGE-Signature-Valid", "false")
		}

		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	})
}

// ---- Status helpers ----

func httpStatusFromAgent(out *types.AgentMessage) (int, bool) {
	if out.Metadata != nil {
		if code, ok := pickIntFromMeta(out.Metadata, "httpStatus", "status"); ok {
			return code, true
		}
	}
	if strings.EqualFold(out.Type, "error") {
		return http.StatusBadGateway, true
	}
	low := strings.ToLower(strings.TrimSpace(out.Content))
	const prefix = "external error:"
	if strings.HasPrefix(low, prefix) {
		rest := strings.TrimSpace(low[len(prefix):])
		if f := firstToken(rest); f != "" {
			if n, err := strconv.Atoi(f); err == nil && n >= 100 && n <= 599 {
				return n, true
			}
		}
		return http.StatusBadGateway, true
	}
	return 0, false
}

func pickIntFromMeta(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int(t), true
			case int:
				return t, true
			case int32:
				return int(t), true
			case int64:
				return int(t), true
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
					return n, true
				}
			}
		}
	}
	return 0, false
}

func firstToken(s string) string {
	for _, f := range strings.Fields(s) {
		return f
	}
	return ""
}

// ---- Env/utils ----

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envBool(k string, d bool) bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(k))); v != "" {
		return v == "1" || v == "true" || v == "on" || v == "yes"
	}
	return d
}

func parseBoolLike(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		low := strings.ToLower(strings.TrimSpace(t))
		return low == "1" || low == "true" || low == "on" || low == "yes"
	case float64:
		return t != 0
	default:
		return def
	}
}

func hpkeKeysPath() string {
	if v := strings.TrimSpace(os.Getenv("HPKE_KEYS")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("ROOT_HPKE_KEYS")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("HPKE_KEYS_PATH")); v != "" {
		return v
	}
	return "merged_agent_keys.json"
}

type agentKeyRow struct {
	Name string `json:"name"`
	DID  string `json:"did"`
}

func loadDIDsFromKeys(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []agentKeyRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		if n := strings.TrimSpace(r.Name); n != "" && strings.TrimSpace(r.DID) != "" {
			m[n] = r.DID
		}
	}
	return m, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
