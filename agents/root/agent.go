// Package root: RootAgent does in-proc routing AND owns outbound HTTP to external agents.
// It signs outbound HTTP (RFC 9421 via A2A client) and optionally uses HPKE for payload
// encryption. Sub-agents focus on business logic; Root handles network crypto.
//
// Korean summary:
// - Root owns outbound HTTP to external services, RFC 9421 signing, and HPKE encrypt/decrypt.
// - Sub-agents (planning/medical) handle local business logic; payment is sent only to the external server.
// - Use local fallbacks for planning/medical only when no external URL is configured (payment has no fallback).
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

	// A2A & transport
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	prototx "github.com/sage-x-project/sage-multi-agent/protocol"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	// Use sage-a2a-go v1.7.0 Agent Framework for DID, crypto, keys, and session management
	"github.com/sage-x-project/sage-a2a-go/pkg/agent/framework/did"
	"github.com/sage-x-project/sage-a2a-go/pkg/agent/framework/keys"
	"github.com/sage-x-project/sage-a2a-go/pkg/agent/framework/session"
	"github.com/sage-x-project/sage-multi-agent/types"
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	"github.com/sage-x-project/sage/pkg/agent/hpke"

	// [LLM] light shim client
	"github.com/sage-x-project/sage-multi-agent/llm"
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

	logger *log.Logger

	// Outbound signing & HTTP
	httpClient  *http.Client
	sageEnabled bool
	myDID       sagedid.AgentDID
	myKey       keys.KeyPair
	a2a         *a2aclient.A2AClient

	// External base URLs per agent (routing target)
	extBase map[string]string // key: "planning"|"medical"|"payment" -> base URL

	// HPKE per-target state
	hpkeStates sync.Map // key: target string -> *hpkeState
	resolver   *did.Resolver

	// [LLM] lazy-initialized NLG client
	llmClient llm.Client
}

// hpkeState holds per-target HPKE session context.
type hpkeState struct {
	cli  *hpke.Client
	sMgr *session.Manager
	kid  string
}

// ---- Construction ----

func NewRootAgent(name string, port int) *RootAgent {
	mux := http.NewServeMux()

	// Resolve external URLs from env (defaults allow per-agent separation)
	ext := map[string]string{
		"planning": strings.TrimRight(envOr("PLANNING_EXTERNAL_URL", ""), "/"),
		"ordering": strings.TrimRight(envOr("ORDERING_EXTERNAL_URL", ""), "/"),
		"medical":  strings.TrimRight(envOr("MEDICAL_URL", "http://localhost:5500/medical"), "/"),
		"payment":  strings.TrimRight(envOr("PAYMENT_URL", "http://localhost:5500/payment"), "/"),
	}

	ra := &RootAgent{
		name:        name,
		port:        port,
		mux:         mux,
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

// ---- [LLM] ensure ----

func (r *RootAgent) ensureLLM() {
	if r.llmClient != nil {
		return
	}
	if c, err := llm.NewFromEnv(); err == nil {
		r.llmClient = c
		log.Printf("[root] LLM ready")
	} else {
		r.logger.Printf("[root] LLM disabled: %v", err)
	}
}

// ---- Signing & HTTP (A2A) ----

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
	kp, err := keys.LoadFromJWKFile(jwk)
	if err != nil {
		return fmt.Errorf("load ROOT_JWK_FILE: %w", err)
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
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_ADDRESS"), "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	resolver, err := did.NewResolver(did.Config{
		RPCEndpoint:     rpc,
		ContractAddress: contract,
		PrivateKey:      priv,
	})
	if err != nil {
		return fmt.Errorf("HPKE: init resolver: %w", err)
	}
	r.resolver = resolver
	return nil
}

// ---- HPKE per-target management ----

func (r *RootAgent) IsHPKEEnabled(target string) bool {
	_, ok := r.hpkeStates.Load(strings.ToLower(strings.TrimSpace(target)))
	return ok
}

func (r *RootAgent) CurrentHPKEKID(target string) string {
	key := strings.ToLower(strings.TrimSpace(target))
	if v, ok := r.hpkeStates.Load(key); ok {
		if st, ok2 := v.(*hpkeState); ok2 {
			return st.kid
		}
	}
	return ""
}

func (r *RootAgent) DisableHPKE(target string) {
	key := strings.ToLower(strings.TrimSpace(target))
	r.hpkeStates.Delete(key)
}

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
	serverAlias := target
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
	cli := hpke.NewClient(t, r.resolver.GetKeyClient(), r.myKey, clientDID, hpke.DefaultInfoBuilder{}, sMgr.GetUnderlying())

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
	sess, ok := st.sMgr.GetUnderlying().GetByKeyID(st.kid)
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
	sess, ok := st.sMgr.GetUnderlying().GetByKeyID(kid)
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

// pickAgent decides which agent to route to.
// Returns "payment" | "medical" | "planning" | "ordering" | ""(chat)
func (r *RootAgent) pickAgent(msg *types.AgentMessage) string {
	// 0) explicit metadata override
	if msg != nil && msg.Metadata != nil {
		if v, ok := msg.Metadata["domain"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				s = strings.ToLower(strings.TrimSpace(s))
				switch s {
				case "payment", "medical", "planning", "ordering":
					return s
				case "chat":
					return ""
				}
			}
		}
	}

	// 1) content-based intent (regardless of external URL presence)
	c := strings.ToLower(strings.TrimSpace(msg.Content))

	if isPaymentActionIntent(c) {
		return "payment"
	}
	if isMedicalActionIntent(c) {
		return "medical"
	}
	if isPlanningActionIntent(c) {
		return "planning"
	}
	if isOrderingActionIntent(c) {
		return "ordering"
	}

	return ""
}

// pickLang chooses language by header -> metadata -> content detection.
func pickLang(r *http.Request, msg *types.AgentMessage) string {
	// 1) Header
	if hv := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Lang"))); hv == "ko" || hv == "en" {
		return hv
	}
	// 2) Metadata
	if msg != nil && msg.Metadata != nil {
		if v, ok := msg.Metadata["lang"]; ok {
			if s, ok2 := v.(string); ok2 {
				s = strings.ToLower(strings.TrimSpace(s))
				if s == "ko" || s == "en" {
					return s
				}
				// common aliases
				if s == "kr" || s == "kor" || s == "korean" {
					return "ko"
				}
				if s == "english" || s == "us" || s == "en-us" || s == "en-gb" {
					return "en"
				}
			}
		}
	}
	// 3) Detect by content
	return llm.DetectLang(msg.Content)
}

// ---- Outbound send (Root owns external I/O) ----

// ---- Outbound send (Root owns external I/O) ----

func (r *RootAgent) sendExternal(ctx context.Context, agent string, msg *types.AgentMessage) (*types.AgentMessage, error) {
	base := r.externalURLFor(agent)
	if base == "" {
		return nil, fmt.Errorf("no external URL configured for agent=%s", agent)
	}

	body, _ := json.Marshal(msg)

	useSAGE := r.sageEnabled
	if v := ctx.Value(ctxUseSAGEKey); v != nil {
		if b, ok := v.(bool); ok {
			useSAGE = b
		}
	}
	wantHPKE := r.IsHPKEEnabled(agent)
	if v := ctx.Value(ctxHPKERawKey); v != nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			wantHPKE = strings.EqualFold(strings.TrimSpace(s), "true")
		}
	}
	if !useSAGE {
		wantHPKE = false
	}

	if useSAGE && r.a2a == nil {
		if err := r.initSigning(); err != nil {
			return nil, err
		}
	}
	if wantHPKE && !r.IsHPKEEnabled(agent) {
		if err := r.EnableHPKE(ctx, agent, hpkeKeysPath()); err != nil {
			r.logger.Printf("[root] HPKE init failed target=%s: %v", agent, err)
		}
	}

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

	emitHeaders := useSAGE || wantHPKE
	tx := prototx.NewA2ATransport(r, base, false, emitHeaders)
	sm := &transport.SecureMessage{
		ID:       uuid.NewString(),
		Payload:  body,
		DID:      string(r.myDID),
		Metadata: map[string]string{"ctype": "application/json"},
		Role:     "agent",
	}
	if kid != "" {
		sm.Metadata["hpke_kid"] = kid
	}

	resp, err := tx.Send(ctx, sm)
	if err != nil {
		return nil, fmt.Errorf("transport send: %w", err)
	}

	// --- Tamper suspicion heuristics (based on upstream response text) ---
	respText := strings.TrimSpace(string(resp.Data))
	respLow := strings.ToLower(respText)
	isSigAuthFail := looksLikeSigAuthFailure(respLow)

	if !resp.Success {
		// If upstream rejected our RFC9421 signature, warn loudly (likely body/Content-Digest mutated by proxy).
		if isSigAuthFail {
			r.logger.Printf("[root][alert][tamper] ⚠️ upstream rejected signature (agent=%s base=%s). "+
				"Likely body or Content-Digest was rewritten by a proxy/gateway. "+
				"Consider disabling gateway tamper mode or enabling HPKE end-to-end. details=%s",
				agent, base, redact(respText, 240))
		} else if looksLikeContentDigestIssue(respLow) {
			r.logger.Printf("[root][alert][tamper] ⚠️ upstream reported Content-Digest mismatch (agent=%s base=%s). details=%s",
				agent, base, redact(respText, 240))
		}

		reason := strings.TrimSpace(respText)
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
			Metadata: map[string]any{
				"upstream":      base,
				"sigAuthFailed": isSigAuthFail,
				"tamperSuspect": isSigAuthFail || looksLikeContentDigestIssue(respLow),
				"useSAGE":       useSAGE,
				"hpkeEnabled":   wantHPKE,
				"hpke_kid":      kid,
			},
		}, nil
	}

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

// ---- tiny helpers used above ----
func strFrom(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok2 := v.(string); ok2 && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
func intFrom(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int64(t)
			case int:
				return int64(t)
			case int64:
				return t
			case string:
				if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

// Planning: require at least "task"/"goal"
type planningSlots struct {
	Task      string // what to plan
	Timeframe string // optional: when
	Context   string // optional: notes
}

func extractPlanningSlots(msg *types.AgentMessage) (s planningSlots, missing []string) {
	getS := func(keys ...string) string {
		if msg.Metadata == nil {
			return ""
		}
		for _, k := range keys {
			if v, ok := msg.Metadata[k]; ok {
				if str, ok2 := v.(string); ok2 && strings.TrimSpace(str) != "" {
					return strings.TrimSpace(str)
				}
			}
		}
		return ""
	}
	s.Task = getS("planning.task", "task", "goal", "계획", "할일")
	s.Timeframe = getS("planning.timeframe", "timeframe", "when", "기간", "언제")
	s.Context = getS("planning.context", "context", "note", "메모")

	// Lightweight JSON fallback
	if s.Task == "" && strings.HasPrefix(strings.TrimSpace(msg.Content), "{") {
		var m map[string]any
		if json.Unmarshal([]byte(msg.Content), &m) == nil {
			if v, ok := m["task"].(string); ok && strings.TrimSpace(v) != "" {
				s.Task = strings.TrimSpace(v)
			}
			if v, ok := m["timeframe"].(string); ok && strings.TrimSpace(v) != "" {
				s.Timeframe = strings.TrimSpace(v)
			}
			if v, ok := m["context"].(string); ok && strings.TrimSpace(v) != "" {
				s.Context = strings.TrimSpace(v)
			}
		}
	}
	if s.Task == "" {
		missing = append(missing, "task/goal(계획 대상)")
	}
	return
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
			"ext": map[string]any{
				"planning": r.externalURLFor("planning") != "",
				"medical":  r.externalURLFor("medical") != "",
				"payment":  r.externalURLFor("payment") != "",
			},
			"sage_enabled": r.sageEnabled,
			"time":         time.Now().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Root-level SAGE toggle
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
		resp := map[string]any{
			"root": r.sageEnabled,
			"ext": map[string]bool{
				"planning": r.externalURLFor("planning") != "",
				"medical":  r.externalURLFor("medical") != "",
				"payment":  r.externalURLFor("payment") != "",
			},
			"hpke": map[string]any{
				"planning": map[string]any{
					"enabled": r.IsHPKEEnabled("planning"),
					"kid":     r.CurrentHPKEKID("planning"),
				},
				"medical": map[string]any{
					"enabled": r.IsHPKEEnabled("medical"),
					"kid":     r.CurrentHPKEKID("medical"),
				},
				"payment": map[string]any{
					"enabled": r.IsHPKEEnabled("payment"),
					"kid":     r.CurrentHPKEKID("payment"),
				},
			},
			"time": time.Now().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// HPKE runtime toggle at Root (per target)
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

	// HPKE status (per target)
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

	// Main in-proc processing (full handler)
	r.mux.HandleFunc("/process", func(w http.ResponseWriter, req *http.Request) {
		// CORS headers for browser requests
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SAGE-Enabled, X-HPKE-Enabled, X-Scenario, X-Conversation-ID, X-SAGE-Context-ID")

		// Handle preflight OPTIONS request
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Method guard
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Decode inbound message
		var msg types.AgentMessage
		if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		defer req.Body.Close()
		lang := pickLang(req, &msg)
		cid := convIDFrom(req, &msg)

		forcePayment := shouldForcePayment(cid, msg.Content)

		forceMedical := false
		if hasMedCtx(cid) {
			st := getMedCtx(cid)
			if strings.TrimSpace(st.Await) != "" ||
				strings.TrimSpace(st.Slots.Condition) != "" ||
				strings.TrimSpace(st.Symptoms) != "" {
				c := strings.ToLower(strings.TrimSpace(msg.Content))
				if !isPaymentActionIntent(c) && !isPlanningActionIntent(c) {
					forceMedical = true
				}
			}
		}
		agent := ""
		if forcePayment {
			agent = "payment"
		} else if forceMedical {
			agent = "medical"
		} else {
			agent = r.pickAgent(&msg)
			mode := strings.ToLower(strings.TrimSpace(os.Getenv("ROOT_INTENT_MODE")))
			if mode == "" {
				mode = "hybrid"
			}
			if mode == "llm" || (agent == "" && mode == "hybrid") {
				if ro, ok := r.llmRoute(req.Context(), msg.Content); ok && ro.Domain != "" {
					agent = ro.Domain
					if msg.Metadata == nil {
						msg.Metadata = map[string]any{}
					}
					if ro.Lang != "" {
						msg.Metadata["lang"] = ro.Lang
					}
				}
			}
		}
		// -------- CHAT MODE: no routing; answer with LLM directly --------
		if agent == "" && !forcePayment {
			r.ensureLLM()
			reply := ""
			if r.llmClient != nil {
				sys := map[string]string{
					"ko": "너는 간결한 한국어 어시스턴트야. 짧게 답해.",
					"en": "You are a concise assistant. Reply briefly.",
				}[lang]
				if out, err := r.llmClient.Chat(req.Context(), sys, strings.TrimSpace(msg.Content)); err == nil {
					reply = strings.TrimSpace(out)
				}
			}
			if reply == "" {
				if lang == "ko" {
					reply = "이해했어요: " + strings.TrimSpace(msg.Content)
				} else {
					reply = "Got it: " + strings.TrimSpace(msg.Content)
				}
			}
			out := types.AgentMessage{
				ID: msg.ID + "-chat", From: "root", To: msg.From, Type: "response", Content: reply,
				Timestamp: time.Now(), Metadata: map[string]any{"lang": lang, "mode": "chat"},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)
			return
		}

		switch agent {
		// -------- INTENT MODE --------
		// ====== RootAgent: payment flow (DROP-IN REPLACEMENT with LOGS) ======
		case "payment":
			{
				// ---- Common: entry/stage logging ----
				stage, token := getStageToken(cid)
				r.logger.Printf("[root][payment][enter] cid=%s stage=%s token=%s lang=%s text=%q",
					cid, stage, token, lang, strings.TrimSpace(msg.Content))

				// ==== Confirmation step handling ====
				if stage == "await_confirm" && token != "" {
					yes, no := parseYesNo(msg.Content)
					r.logger.Printf("[root][payment][confirm] parsed yes=%v no=%v", yes, no)

					if !yes && !no {
						intent := r.llmConfirmIntent(req.Context(), lang, msg.Content)
						r.logger.Printf("[root][payment][confirm] llm intent=%s", intent)
						switch intent {
						case "yes":
							yes = true
						case "no":
							no = true
						}
						if !yes && !no {
							// Try additional slot extraction even in confirmation step
							slots := getPayCtx(cid)
							r.logger.Printf("[root][payment][confirm] before-merge slots: method=%q to=%q recipient=%q shipping=%q merchant=%q budget=%d amount=%d item=%q model=%q",
								slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.BudgetKRW, slots.AmountKRW, slots.Item, slots.Model)

							if xo, ok := r.llmExtractPayment(req.Context(), lang, msg.Content); ok {

								r.logger.Printf("[root][payment][confirm] xo: mode=%s method=%q to=%q shipping=%q merchant=%q amount=%d budget=%d item=%q model=%q",
									xo.Fields.Mode, xo.Fields.Method, xo.Fields.To, xo.Fields.Shipping, xo.Fields.Merchant, xo.Fields.AmountKRW, xo.Fields.BudgetKRW, xo.Fields.Item, xo.Fields.Model)

								// 🔧 Hotfix: merge both To and Recipient (prevents missing recipient)
								slots = mergePaySlots(slots, paySlots{
									Mode: xo.Fields.Mode, To: xo.Fields.To,
									AmountKRW: xo.Fields.AmountKRW, BudgetKRW: xo.Fields.BudgetKRW,
									Method: xo.Fields.Method, Item: xo.Fields.Item, Model: xo.Fields.Model,
									Merchant: xo.Fields.Merchant, Shipping: xo.Fields.Shipping, CardLast4: xo.Fields.CardLast4,
								})
								r.logger.Printf("[root][payment][confirm] after-merge slots: method=%q to=%q recipient=%q shipping=%q merchant=%q budget=%d amount=%d item=%q model=%q",
									slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.BudgetKRW, slots.AmountKRW, slots.Item, slots.Model)

								missing := computeMissingPayment(slots)
								r.logger.Printf("[root][payment][confirm] missing=%v", missing)

								if len(missing) == 0 {
									preview := buildPaymentPreview(lang, slots)
									putPayCtxFull(cid, slots, "await_confirm", token)
									r.logger.Printf("[root][payment][confirm] preview-ready; await_confirm token=%s", token)
									out := types.AgentMessage{
										ID: msg.ID + "-preview", From: "root", To: msg.From, Type: "confirm",
										Content:   preview + "\n" + r.buildConfirmPromptLLM(req.Context(), lang, slots),
										Timestamp: time.Now(),
										Metadata:  map[string]any{"await": "payment.confirm", "lang": lang, "domain": "payment", "mode": slots.Mode, "confirmToken": token},
									}
									w.Header().Set("Content-Type", "application/json")
									w.WriteHeader(http.StatusOK)
									_ = json.NewEncoder(w).Encode(out)
									return
								}

								q := r.askForMissingPaymentWithLLM(req.Context(), lang, slots, missing, msg.Content)
								putPayCtxFull(cid, slots, "collect", "")
								r.logger.Printf("[root][payment][confirm] ask-missing %v q=%q", missing, q)
								out := types.AgentMessage{
									ID: msg.ID + "-needinfo", ContextID: cid, From: "root", To: msg.From, Type: "clarify",
									Content: q, Timestamp: time.Now(),
									Metadata: map[string]any{"await": "payment.slots", "missing": strings.Join(missing, ", "), "lang": lang, "domain": "payment", "mode": slots.Mode},
								}
								w.Header().Set("Content-Type", "application/json")
								w.WriteHeader(http.StatusOK)
								_ = json.NewEncoder(w).Encode(out)
								return
							}
						}
					}

					if yes {
						// 1) Load final slots
						slots := getPayCtx(cid)
						r.logger.Printf("[root][payment][send] YES; final slots: method=%q to=%q recipient=%q shipping=%q merchant=%q amount=%d budget=%d item=%q model=%q",
							slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.AmountKRW, slots.BudgetKRW, slots.Item, slots.Model)

						// 2) Inject required metadata
						if msg.Metadata == nil {
							msg.Metadata = map[string]any{}
						}
						msg.Metadata["lang"] = lang

						// amount: fall back to budget when explicit amount is missing
						amt := slots.AmountKRW
						if amt <= 0 && slots.BudgetKRW > 0 {
							amt = slots.BudgetKRW
							msg.Metadata["payment.amountIsEstimated"] = true
						}
						if amt > 0 {
							msg.Metadata["payment.amountKRW"] = amt
							msg.Metadata["amountKRW"] = amt // 호환 키
						}

						// recipient/method/item/merchant/shipping/card last4
						if v := strings.TrimSpace(firstNonEmpty(slots.Recipient, slots.To)); v != "" {
							msg.Metadata["payment.to"] = v
							msg.Metadata["to"] = v // 호환 키
							msg.Metadata["recipient"] = v
						}
						if v := strings.TrimSpace(slots.Method); v != "" {
							msg.Metadata["payment.method"] = v
							msg.Metadata["method"] = v
						}
						if v := firstNonEmpty(strings.TrimSpace(slots.Model), strings.TrimSpace(slots.Item)); v != "" {
							msg.Metadata["payment.item"] = v
							msg.Metadata["item"] = v
						}
						if v := strings.TrimSpace(slots.Merchant); v != "" {
							msg.Metadata["payment.merchant"] = v
						}
						if v := strings.TrimSpace(slots.Shipping); v != "" {
							msg.Metadata["payment.shipping"] = v
						}
						if v := strings.TrimSpace(slots.CardLast4); v != "" {
							msg.Metadata["payment.cardLast4"] = v
						}
						r.logger.Printf("[root][payment][send] injected meta: amount=%d method=%q to/recipient=%q shipping=%q merchant=%q",
							amt, slots.Method, firstNonEmpty(slots.Recipient, slots.To), slots.Shipping, slots.Merchant)

						// 3) Handle per-request SAGE/HPKE headers
						sageRaw := strings.TrimSpace(req.Header.Get("X-SAGE-Enabled"))
						hpkeRaw := strings.TrimSpace(req.Header.Get("X-HPKE-Enabled"))
						r.logger.Printf("[root][payment][send] headers SAGE=%q HPKE=%q", sageRaw, hpkeRaw)

						if hpkeRaw != "" && strings.EqualFold(hpkeRaw, "true") {
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
						ctx2 := req.Context()
						if sageRaw != "" {
							ctx2 = context.WithValue(ctx2, ctxUseSAGEKey, strings.EqualFold(sageRaw, "true"))
						}
						if hpkeRaw != "" {
							ctx2 = context.WithValue(ctx2, ctxHPKERawKey, hpkeRaw)
						}

						// 4) Send to external (actual payment)
						r.logger.Printf("[root][payment][send] -> sendExternal(payment)")
						outPtr, err := r.sendExternal(ctx2, "payment", &msg)
						if err != nil {
							r.logger.Printf("[root][payment][send][error] %v", err)
							http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
							return
						}
						out := *outPtr
						if strings.EqualFold(out.Type, "error") || strings.HasPrefix(strings.ToLower(out.Content), "external error:") {
							r.logger.Printf("[root][payment][forward][ERR] cid=%s %s", cid, redact(out.Content, 240))
							if looksLikeSigAuthFailure(strings.ToLower(out.Content)) || looksLikeContentDigestIssue(strings.ToLower(out.Content)) {
								r.logger.Printf("[root][alert][tamper] ⚠️ cid=%s suspected tamper via gateway (payment). Check GW tamper mode/ATTACK_MESSAGE. details=%s",
									cid, redact(out.Content, 240))
							}
						} else {
							r.logger.Printf("[root][payment][forward] cid=%s -> external ok", cid)
						}
						// Clear context on success
						if strings.EqualFold(out.Type, "response") && !strings.HasPrefix(strings.ToLower(out.Content), "external error:") {
							r.logger.Printf("[root][payment][ctx] delPayCtx cid=%s", cid)
							delPayCtx(cid)
						}

						// Response
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
						return
					}

					if no {
						r.logger.Printf("[root][payment][confirm] NO -> back to collect")
						putPayCtxFull(cid, getPayCtx(cid), "collect", "")
						out := types.AgentMessage{
							ID: msg.ID + "-cancel", ContextID: cid, From: "root", To: msg.From, Type: "response",
							Content: map[string]string{
								"ko": "취소했어요. 무엇을 바꿀까요? (제품/배송지/결제수단/수신자/예산 등)",
								"en": "Cancelled. What should I change? (item/shipping/method/recipient/budget)",
							}[lang],
							Timestamp: time.Now(),
							Metadata:  map[string]any{"await": "payment.slots", "lang": lang, "domain": "payment"},
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(out)
						return
					}

					r.logger.Printf("[root][payment][confirm] ambiguous -> ask confirm again")
					out := types.AgentMessage{
						ID: msg.ID + "-confirm", From: "root", To: msg.From, Type: "clarify",
						Content:   r.buildConfirmPromptLLM(req.Context(), lang, getPayCtx(cid)),
						Timestamp: time.Now(),
						Metadata:  map[string]any{"await": "payment.confirm", "lang": lang, "domain": "payment"},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(out)
					return
				}

				// ==== Collect stage ====
				slots := getPayCtx(cid)
				r.logger.Printf("[root][payment][collect] before-merge slots: method=%q to=%q recipient=%q shipping=%q merchant=%q budget=%d amount=%d item=%q model=%q",
					slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.BudgetKRW, slots.AmountKRW, slots.Item, slots.Model)

				// LLM extraction → augment with manual extraction
				if xo, ok := r.llmExtractPayment(req.Context(), lang, msg.Content); ok {

					r.logger.Printf("[root][payment][collect] xo: mode=%s method=%q to=%q shipping=%q merchant=%q amount=%d budget=%d item=%q model=%q",
						xo.Fields.Mode, xo.Fields.Method, xo.Fields.To, xo.Fields.Shipping, xo.Fields.Merchant, xo.Fields.AmountKRW, xo.Fields.BudgetKRW, xo.Fields.Item, xo.Fields.Model)

					// 🔧 Hotfix: merge both To and Recipient
					slots = mergePaySlots(slots, paySlots{
						Mode: xo.Fields.Mode, To: xo.Fields.To,
						AmountKRW: xo.Fields.AmountKRW, BudgetKRW: xo.Fields.BudgetKRW,
						Method: xo.Fields.Method, Item: xo.Fields.Item, Model: xo.Fields.Model,
						Merchant: xo.Fields.Merchant, Shipping: xo.Fields.Shipping,
					})
				} else {
					if s2, _, _ := extractPaymentSlots(&msg); true {
						if s2.To == "" && strings.TrimSpace(s2.Recipient) != "" {
							s2.To = s2.Recipient
						}
						if s2.Recipient == "" && strings.TrimSpace(s2.To) != "" {
							s2.Recipient = s2.To
						}
						slots = mergePaySlots(slots, s2)
					}
				}

				if strings.TrimSpace(slots.Mode) == "" {
					slots.Mode = classifyPaymentMode(msg.Content, slots)
				}
				r.logger.Printf("[root][payment][collect] after-merge slots: method=%q to=%q recipient=%q shipping=%q merchant=%q budget=%d amount=%d item=%q model=%q",
					slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.BudgetKRW, slots.AmountKRW, slots.Item, slots.Model)

				missing := computeMissingPayment(slots) // To/Method/Amount or Budget 등 규칙 반영
				r.logger.Printf("[root][payment][collect] missing=%v", missing)

				if len(missing) > 0 {
					q := r.askForMissingPaymentWithLLM(req.Context(), lang, slots, missing, msg.Content)
					putPayCtxFull(cid, slots, "collect", "")
					r.logger.Printf("[root][payment][collect] ask-missing %v q=%q", missing, q)
					out := types.AgentMessage{
						ID: msg.ID + "-needinfo", From: "root", To: msg.From, Type: "clarify",
						Content:   strings.TrimSpace(q),
						Timestamp: time.Now(),
						Metadata:  map[string]any{"await": "payment.slots", "missing": strings.Join(missing, ", "), "lang": lang, "domain": "payment", "mode": slots.Mode},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(out)
					return
				}

				// ==== Preview + confirm ====
				preview := buildPaymentPreview(lang, slots)
				token2 := uuid.NewString()
				putPayCtxFull(cid, slots, "await_confirm", token2)
				r.logger.Printf("[root][payment][collect] preview-ready; await_confirm token=%s", token2)
				out := types.AgentMessage{
					ID: msg.ID + "-preview", ContextID: cid, From: "root", To: msg.From, Type: "confirm",
					Content:   preview + "\n" + r.buildConfirmPromptLLM(req.Context(), lang, slots),
					Timestamp: time.Now(),
					Metadata:  map[string]any{"await": "payment.confirm", "lang": lang, "domain": "payment", "mode": slots.Mode, "confirmToken": token2},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(out)
				return
			}

		// ===== MEDICAL =====
		case "medical":
			lang := pickLang(req, &msg)
			cid := convIDFrom(req, &msg)

			r.logger.Printf("[root][medical][enter] cid=%s lang=%s text=%q", cid, lang, strings.TrimSpace(msg.Content))

			// Load context & accumulate history
			st := getMedCtx(cid)
			utter := strings.TrimSpace(msg.Content)
			if utter != "" {
				st.Transcript = append(st.Transcript, utter)
			}
			if st.FirstQ == "" && utter != "" {
				st.FirstQ = utter
			}

			// 1) Merge local keyword extraction
			cur := extractMedicalCore(&msg)

			// Await hint: if previous turn asked for "symptoms/condition", accept this input as-is
			if st.Await == "symptoms" && strings.TrimSpace(cur.Symptoms) == "" && utter != "" {
				cur.Symptoms = utter
			}
			if st.Await == "condition" && strings.TrimSpace(cur.Slots.Condition) == "" && utter != "" {
				cur.Slots.Condition = utter
			}

			st = mergeMedCtx(st, cur)
			putMedCtx(cid, st)
			r.logger.Printf("[root][medical][merge] cid=%s cond=%q symptoms.len=%d",
				cid, st.Slots.Condition, len(strings.TrimSpace(st.Symptoms)))

			// 2) Augment via LLM extraction
			var xo medicalXO
			if got, ok := r.llmExtractMedical(req.Context(), lang, utter); ok {
				xo = got

				// Fill only empty fields from LLM result (symptoms handled separately)
				st = mergeMedCtx(st, medCtx{
					Slots: medicalSlots{
						Condition:   xo.Fields.Condition,
						Topic:       xo.Fields.Topic,
						Audience:    xo.Fields.Audience,
						Duration:    xo.Fields.Duration,
						Age:         xo.Fields.Age,
						Medications: xo.Fields.Medications,
						Symptoms:    xo.Fields.Symptoms, // LLM이 준 증상 텍스트(있다면)
					},
				})
				putMedCtx(cid, st)

				r.logger.Printf("[root][medical][llm-xo] cid=%s cond=%q topic=%q symptoms.len=%d missing=%v ask=%q",
					cid, st.Slots.Condition, st.Slots.Topic, len(strings.TrimSpace(st.Symptoms)), xo.Missing, xo.Ask)
			}

			// 3) Forwarding condition (no fast-path: require both condition+symptoms filled)
			if strings.TrimSpace(st.Slots.Condition) != "" && strings.TrimSpace(st.Symptoms) != "" {
				// Build metadata (include history)
				if msg.Metadata == nil {
					msg.Metadata = map[string]any{}
				}
				msg.Metadata["lang"] = lang
				msg.Metadata["medical.condition"] = strings.TrimSpace(st.Slots.Condition)
				if v := strings.TrimSpace(st.Slots.Topic); v != "" {
					msg.Metadata["medical.topic"] = v
				}
				if v := strings.TrimSpace(st.Symptoms); v != "" {
					msg.Metadata["medical.symptoms"] = v
				}
				if v := strings.TrimSpace(st.Slots.Duration); v != "" {
					msg.Metadata["medical.duration"] = v
				}
				if v := strings.TrimSpace(st.Slots.Medications); v != "" {
					msg.Metadata["medical.meds"] = v
				}
				if v := strings.TrimSpace(st.Slots.Age); v != "" {
					msg.Metadata["medical.age"] = v
				}
				if v := strings.TrimSpace(st.FirstQ); v != "" {
					msg.Metadata["medical.initial_question"] = v
				}
				msg.Metadata["medical.last_message"] = utter
				if len(st.Transcript) > 0 {
					msg.Metadata["medical.history"] = st.Transcript
					msg.Metadata["medical.history_len"] = len(st.Transcript)
				}

				if r.externalURLFor("medical") == "" {
					r.logger.Printf("[root][medical][error-no-external] cid=%s: MEDICAL_URL not configured", cid)
					http.Error(w, "medical external not configured", http.StatusServiceUnavailable)
					return
				}

				// Handle SAGE/HPKE headers
				sageRaw := strings.TrimSpace(req.Header.Get("X-SAGE-Enabled"))
				hpkeRaw := strings.TrimSpace(req.Header.Get("X-HPKE-Enabled"))
				r.logger.Printf("[root][medical][send] headers SAGE=%q HPKE=%q (forward)", sageRaw, hpkeRaw)

				if hpkeRaw != "" && strings.EqualFold(hpkeRaw, "true") {
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

				ctx2 := req.Context()
				if sageRaw != "" {
					ctx2 = context.WithValue(ctx2, ctxUseSAGEKey, strings.EqualFold(sageRaw, "true"))
				}
				if hpkeRaw != "" {
					ctx2 = context.WithValue(ctx2, ctxHPKERawKey, hpkeRaw)
				}

				// External send
				outPtr, err := r.sendExternal(ctx2, "medical", &msg)
				if err != nil {
					r.logger.Printf("[root][medical][forward][err] cid=%s: %v", cid, err)
					http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
					return
				}
				out := *outPtr
				if strings.EqualFold(out.Type, "error") || strings.HasPrefix(strings.ToLower(out.Content), "external error:") {
					r.logger.Printf("[root][medical][forward][ERR] cid=%s %s", cid, redact(out.Content, 240))
					if looksLikeSigAuthFailure(strings.ToLower(out.Content)) || looksLikeContentDigestIssue(strings.ToLower(out.Content)) {
						r.logger.Printf("[root][alert][tamper] ⚠️ cid=%s suspected tamper via gateway (medical). Check GW tamper mode/ATTACK_MESSAGE. details=%s",
							cid, redact(out.Content, 240))
					}
				} else {
					r.logger.Printf("[root][medical][forward] cid=%s -> external ok", cid)
				}
				// If conversation continues, you can skip reset; here we just clear awaiting state.
				st.Await = ""
				putMedCtx(cid, st)

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
				return
			}

			// 4) Missing → generate question (prefer LLM ask, else fallback rules)
			{
				missing := medicalMissing(st)

				ask := strings.TrimSpace(xo.Ask)
				if ask == "" {
					switch {
					case strings.TrimSpace(st.Symptoms) == "" && strings.TrimSpace(st.Slots.Condition) == "":
						ask = r.askForCondAndSymptomsLLM(req.Context(), lang, msg.Content)
						st.Await = "symptoms"
					case strings.TrimSpace(st.Symptoms) == "":
						ask = r.askForSymptomsLLM(req.Context(), lang, st.Slots.Condition, msg.Content)
						st.Await = "symptoms"
					case strings.TrimSpace(st.Slots.Condition) == "":
						if langOrDefault(lang) == "ko" {
							ask = "어떤 질병/상태에 대한 상담인지 알려주세요. (예: 당뇨병, 고혈압, 천식 등)"
						} else {
							ask = "Which condition is this about? (e.g., diabetes, hypertension, asthma)"
						}
						st.Await = "condition"
					default:
						// Safe defaults
						if langOrDefault(lang) == "ko" {
							ask = "현재 겪고 있는 주요 증상을 한 문장으로 알려주세요."
						} else {
							ask = "Please describe your main symptoms in one short sentence."
						}
						st.Await = "symptoms"
					}
				} else {
					// If LLM ask exists but await is empty, prioritize symptoms
					if st.Await == "" && strings.TrimSpace(st.Symptoms) == "" {
						st.Await = "symptoms"
					}
				}

				putMedCtx(cid, st)
				r.logger.Printf("[root][medical][ask] cid=%s await=%s missing=%v q=%q", cid, st.Await, missing, ask)

				clar := types.AgentMessage{
					ID:        msg.ID + "-needinfo",
					ContextID: cid,
					From:      "root",
					To:        msg.From,
					Type:      "clarify",
					Content:   ask,
					Timestamp: time.Now(),
					Metadata: map[string]any{
						"await":   "medical." + st.Await,
						"missing": strings.Join(missing, ", "),
						"lang":    lang,
					},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(clar)
				return
			}

		// ===== PLANNING =====
		case "planning":
			lang := pickLang(req, &msg)

			// 1) Try LLM slot-extractor first
			if xo, ok := r.llmExtractPlanning(req.Context(), lang, msg.Content); ok {
				if len(xo.Missing) > 0 {
					clar := types.AgentMessage{
						ID:        msg.ID + "-needinfo",
						From:      "root",
						To:        msg.From,
						Type:      "clarify",
						Content:   strings.TrimSpace(xo.Ask),
						Timestamp: time.Now(),
						Metadata: map[string]any{
							"await":   "planning.slots",
							"missing": strings.Join(xo.Missing, ", "),
							"hint": map[string]string{
								"ko": `예: {"task":"출장 일정 수립","timeframe":"다음 주","context":"회의 장소는 판교"}`,
								"en": `Ex: {"task":"Plan a business trip","timeframe":"next week","context":"meeting in Pangyo"}`,
							}[lang],
							"lang": lang,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(clar)
					return
				}
				fillMsgMetaFromPlanning(&msg, xo.Fields, lang)
			} else {
				// 2) Fallback to rule-based extractor
				slots, missing := extractPlanningSlots(&msg)
				if len(missing) > 0 {
					q := r.askForMissingPlanningWithLLM(req.Context(), lang, missing, msg.Content)
					clar := types.AgentMessage{
						ID:        msg.ID + "-needinfo",
						From:      "root",
						To:        msg.From,
						Type:      "clarify",
						Content:   q,
						Timestamp: time.Now(),
						Metadata: map[string]any{
							"await":   "planning.slots",
							"missing": strings.Join(missing, ", "),
							"hint": map[string]string{
								"ko": `예: {"task":"출장 일정 수립","timeframe":"다음 주","context":"회의 장소는 판교"}`,
								"en": `Ex: {"task":"Plan a business trip","timeframe":"next week","context":"meeting in Pangyo"}`,
							}[lang],
							"lang": lang,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(clar)
					return
				}
				if msg.Metadata == nil {
					msg.Metadata = map[string]any{}
				}
				msg.Metadata["planning.task"] = slots.Task
				if slots.Timeframe != "" {
					msg.Metadata["planning.timeframe"] = slots.Timeframe
				}
				if slots.Context != "" {
					msg.Metadata["planning.context"] = slots.Context
				}
				msg.Metadata["lang"] = lang
			}

			// If no external URL, summarize locally with LLM
			if r.externalURLFor("planning") == "" {
				r.ensureLLM()
				if r.llmClient == nil {
					out := types.AgentMessage{
						ID:        msg.ID + "-planning",
						From:      "root",
						To:        msg.From,
						Type:      "response",
						Content:   map[string]string{"ko": "계획 요약을 생성하려면 LLM 설정이 필요해요.", "en": "LLM is required to generate a planning summary."}[lang],
						Timestamp: time.Now(),
						Metadata:  map[string]any{"lang": lang, "mode": "planning", "domain": "planning"},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(out)
					return
				}
				ps := planningSlots{
					Task:      strFrom(msg.Metadata, "planning.task", "task", "goal"),
					Timeframe: strFrom(msg.Metadata, "planning.timeframe", "timeframe"),
					Context:   strFrom(msg.Metadata, "planning.context", "context"),
				}
				answer := r.llmPlanningAnswer(req.Context(), lang, msg.Content, ps)

				out := types.AgentMessage{
					ID:        msg.ID + "-planning",
					From:      "root",
					To:        msg.From,
					Type:      "response",
					Content:   answer,
					Timestamp: time.Now(),
					Metadata:  map[string]any{"lang": lang, "mode": "planning", "domain": "planning"},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(out)
				return
			}
		}

		// -------- Per-request toggles (SAGE/HPKE) --------
		sageRaw := strings.TrimSpace(req.Header.Get("X-SAGE-Enabled"))
		hpkeRaw := strings.TrimSpace(req.Header.Get("X-HPKE-Enabled"))
		if hpkeRaw != "" && strings.EqualFold(hpkeRaw, "true") {
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
			ctx = context.WithValue(ctx, ctxHPKERawKey, hpkeRaw)
		}

		// -------- External send through Root (signing/HPKE handled inside) --------
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

func classifyPaymentMode(text string, s paySlots) string {
	c := strings.ToLower(strings.TrimSpace(text))
	if s.AmountKRW > 0 || containsAny(c, "송금", "이체", "보내", "send", "transfer", "지불") {
		return "transfer"
	}
	return "purchase"
}

// ===== Planning: fill helper =====

func fillMsgMetaFromPlanning(msg *types.AgentMessage, s planningSlots, lang string) {
	if msg.Metadata == nil {
		msg.Metadata = map[string]any{}
	}
	if s.Task != "" {
		msg.Metadata["planning.task"] = s.Task
	}
	if s.Timeframe != "" {
		msg.Metadata["planning.timeframe"] = s.Timeframe
	}
	if s.Context != "" {
		msg.Metadata["planning.context"] = s.Context
	}
	msg.Metadata["lang"] = lang
}

// ---- [LLM] intent router ----

func (r *RootAgent) llmPlanningAnswer(ctx context.Context, lang string, userText string, s planningSlots) string {
	r.ensureLLM()
	if r.llmClient == nil {
		if lang == "ko" {
			return "계획 요약을 생성하려면 LLM 설정이 필요해요."
		}
		return "LLM is required to generate a plan."
	}

	sys := map[string]string{
		"ko": `너는 일정/계획 요약 도우미야.
- 불릿 없이 4~6줄로 간결히.
- "목표, 기간, 핵심 단계, 리스크/준비물" 순서로 정리.
- 명령형 대신 제안형 어조.
- 날짜/시간 표현은 모호하면 상대가 알아듣게 중립적으로.`,
		"en": `You are a planning assistant.
- 4~6 short lines, no bullets.
- Cover: goal, timeframe, key steps, risks/prep.
- Use suggestive tone, avoid hard commitments.`,
	}[langOrDefault(lang)]

	usr := fmt.Sprintf(
		"Language=%s\nTask=%s\nTimeframe=%s\nContext=%s\nUserText=%s",
		langOrDefault(lang), s.Task, s.Timeframe, s.Context, strings.TrimSpace(userText),
	)
	out, err := r.llmClient.Chat(ctx, sys, usr)
	if err != nil || strings.TrimSpace(out) == "" {
		if lang == "ko" {
			return "요청하신 계획을 정리하지 못했어요. 핵심 목표/기간/제약을 한 번 더 알려주세요."
		}
		return "I couldn't generate the plan. Please share goal/timeframe/constraints again."
	}
	return strings.TrimSpace(out)
}

// ---- intent & cues ----

// helper cues

func isQuestionLike(s string) bool {
	return strings.HasSuffix(strings.TrimSpace(s), "?") ||
		containsAny(strings.ToLower(s), "인가", "인가요", "일까", "일까요", "what", "how", "why", "when", "where", "which", "can ", "could ", "should ")
}

func isOrderish(s string) bool {
	return containsAny(strings.ToLower(s),
		"해줘", "해주세요", "해라", "please", "make", "create", "구매해", "사줘", "결제해", "order", "buy for me", "보내", "송금", "이체",
	)
}

func blankOr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func convIDFrom(r *http.Request, msg *types.AgentMessage) string {
	if v := strings.TrimSpace(r.Header.Get("X-SAGE-Context-Id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Conversation-Id")); v != "" {
		return v
	}
	if msg != nil && strings.TrimSpace(msg.ContextID) != "" {
		return strings.TrimSpace(msg.ContextID)
	}
	return "ctx-default"
}

func sanitizeConvID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// replace spaces and unsafe chars
	repl := strings.NewReplacer(" ", "-", "\t", "-", "\n", "-", ":", "-", "/", "-", "\\", "-", "@", "-", "#", "-")
	s = repl.Replace(s)
	if s == "" {
		return "default"
	}
	return s
}

// looksLikeSigAuthFailure returns true if the text suggests an RFC 9421 signature failure.
func looksLikeSigAuthFailure(s string) bool {
	return containsAny(s,
		"unauthorized",
		"signature verification failed",
		"invalid signature",
		"signature invalid",
		"http message signatures",
		"rfc 9421",
	)
}

// looksLikeContentDigestIssue checks for Content-Digest mismatch hints.
func looksLikeContentDigestIssue(s string) bool {
	return containsAny(s,
		"content-digest",
		"content digest",
		"digest mismatch",
	)
}

// redact shortens long log payloads.
func redact(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
