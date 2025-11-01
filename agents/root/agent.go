// Package root: RootAgent does in-proc routing AND owns outbound HTTP to external agents.
// It signs outbound HTTP (RFC 9421 via A2A client) and optionally uses HPKE for payload
// encryption. Sub-agents focus on business logic; Root handles network crypto.
//
// 한국어 설명:
// - 외부 서비스로의 HTTP 전송, RFC9421 서명, HPKE 암복호화를 Root가 전담합니다.
// - 서브 에이전트(planning/medical)는 로컬 비즈니스 로직만 수행하고, payment는 외부 서버로만 보냅니다.
// - 외부 URL이 없을 때만 planning/medical에 대해 로컬 fallback을 사용합니다( payment는 fallback 제거 ).
package root

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
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

	// DID & crypto
	"github.com/sage-x-project/sage-multi-agent/types"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
	"github.com/sage-x-project/sage/pkg/agent/hpke"
	"github.com/sage-x-project/sage/pkg/agent/session"

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
	myKey       sagecrypto.KeyPair
	a2a         *a2aclient.A2AClient

	// External base URLs per agent (routing target)
	extBase map[string]string // key: "planning"|"medical"|"payment" -> base URL

	// HPKE per-target state
	hpkeStates sync.Map // key: target string -> *hpkeState
	resolver   sagedid.Resolver

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
		"medical":  strings.TrimRight(envOr("MEDICAL_EXTERNAL_URL", ""), "/"),
		"payment":  strings.TrimRight(envOr("PAYMENT_URL", "http://localhost:5500"), "/"),
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
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_ADDRESS"), "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	cfgV4 := &sagedid.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv, // optional for read-only
		GasPrice:           0,
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}
	ethV4, err := dideth.NewEthereumClient(cfgV4)
	if err != nil {
		return fmt.Errorf("HPKE: init resolver: %w", err)
	}
	r.resolver = ethV4
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

// pickAgent decides which agent to route to.
// Returns "payment" | "medical" | "planning" | ""(chat)
func (r *RootAgent) pickAgent(msg *types.AgentMessage) string {
	// 0) explicit metadata override
	if msg != nil && msg.Metadata != nil {
		if v, ok := msg.Metadata["domain"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				s = strings.ToLower(strings.TrimSpace(s))
				switch s {
				case "payment", "medical", "planning":
					return s
				case "chat":
					return ""
				}
			}
		}
	}

	// 1) content-based intent (외부 URL 유무와 무관)
	c := strings.ToLower(strings.TrimSpace(msg.Content))

	if isPaymentActionIntent(c) {
		return "payment"
	}
	if isMedicalInfoIntent(c) {
		return "medical"
	}
	if isPlanningActionIntent(c) {
		return "planning"
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
	if !resp.Success {
		reason := strings.TrimSpace(string(resp.Data))
		if reason == "" && resp.Error != nil {
			reason = resp.Error.Error()
		}
		if reason == "" {
			reason = "unknown upstream error"
		}
		return &types.AgentMessage{
			ID: msg.ID + "-exterr", From: "external-" + agent, To: msg.From,
			Type: "error", Content: "external error: " + reason, Timestamp: time.Now(),
		}, nil
	}

	if kid != "" {
		if pt, _, derr := r.decryptIfHPKEResponse(agent, kid, resp.Data); derr != nil {
			return &types.AgentMessage{
				ID: msg.ID + "-exterr", From: "external-" + agent, To: msg.From,
				Type: "error", Content: "external error: " + derr.Error(), Timestamp: time.Now(),
			}, nil
		} else {
			resp.Data = pt
		}
	}

	var out types.AgentMessage
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		out = types.AgentMessage{
			ID: msg.ID + "-ext", From: "external-" + agent, To: msg.From,
			Type: "response", Content: strings.TrimSpace(string(resp.Data)), Timestamp: time.Now(),
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
		forcePayment := false
		if stage, _ := getStageToken(cid); stage == "await_confirm" {
			forcePayment = true
		}
		// -------- Router: rules -> (optional) LLM router (controlled by ROOT_INTENT_MODE) --------
		// ROOT_INTENT_MODE: "rules" | "hybrid"(default) | "llm"
		agent := ""
		if forcePayment {
			agent = "payment"
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
				// ---- 공통: 진입/스테이지 로그 ----
				stage, token := getStageToken(cid)
				r.logger.Printf("[root][payment][enter] cid=%s stage=%s token=%s lang=%s text=%q",
					cid, stage, token, lang, strings.TrimSpace(msg.Content))

				// ==== 확인 단계 처리 ====
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
							// 확인 단계에서도 추가 슬롯 추출 시도
							slots := getPayCtx(cid)
							r.logger.Printf("[root][payment][confirm] before-merge slots: method=%q to=%q recipient=%q shipping=%q merchant=%q budget=%d amount=%d item=%q model=%q",
								slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.BudgetKRW, slots.AmountKRW, slots.Item, slots.Model)

							if xo, ok := r.llmExtractPayment(req.Context(), lang, msg.Content); ok {

								r.logger.Printf("[root][payment][confirm] xo: mode=%s method=%q to=%q shipping=%q merchant=%q amount=%d budget=%d item=%q model=%q",
									xo.Fields.Mode, xo.Fields.Method, xo.Fields.To, xo.Fields.Shipping, xo.Fields.Merchant, xo.Fields.AmountKRW, xo.Fields.BudgetKRW, xo.Fields.Item, xo.Fields.Model)

								// 🔧 핫픽스: To/Recipient 둘 다 병합 (이 줄이 없으면 수령자 누락 반복됨)
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
						// 1) 최종 슬롯 로드
						slots := getPayCtx(cid)
						r.logger.Printf("[root][payment][send] YES; final slots: method=%q to=%q recipient=%q shipping=%q merchant=%q amount=%d budget=%d item=%q model=%q",
							slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.AmountKRW, slots.BudgetKRW, slots.Item, slots.Model)

						// 2) 필수 메타데이터 주입
						if msg.Metadata == nil {
							msg.Metadata = map[string]any{}
						}
						msg.Metadata["lang"] = lang

						// amount: 명시 금액 없으면 예산으로 대체
						amt := slots.AmountKRW
						if amt <= 0 && slots.BudgetKRW > 0 {
							amt = slots.BudgetKRW
							msg.Metadata["payment.amountIsEstimated"] = true
						}
						if amt > 0 {
							msg.Metadata["payment.amountKRW"] = amt
							msg.Metadata["amountKRW"] = amt // 호환 키
						}

						// 수신자/결제수단/상품/상점/배송지/카드끝4자리
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

						// 3) per-request SAGE/HPKE 헤더 처리
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

						// 4) 외부 전송 (실결제)
						r.logger.Printf("[root][payment][send] -> sendExternal(payment)")
						outPtr, err := r.sendExternal(ctx2, "payment", &msg)
						if err != nil {
							r.logger.Printf("[root][payment][send][error] %v", err)
							http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
							return
						}
						out := *outPtr
						r.logger.Printf("[root][payment][send][resp] type=%s content.len=%d", out.Type, len(out.Content))

						// 성공 시 컨텍스트 정리
						if strings.EqualFold(out.Type, "response") && !strings.HasPrefix(strings.ToLower(out.Content), "external error:") {
							r.logger.Printf("[root][payment][ctx] delPayCtx cid=%s", cid)
							delPayCtx(cid)
						}

						// 응답
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

				// ==== 수집 단계 (collect) ====
				slots := getPayCtx(cid)
				r.logger.Printf("[root][payment][collect] before-merge slots: method=%q to=%q recipient=%q shipping=%q merchant=%q budget=%d amount=%d item=%q model=%q",
					slots.Method, slots.To, slots.Recipient, slots.Shipping, slots.Merchant, slots.BudgetKRW, slots.AmountKRW, slots.Item, slots.Model)

				// LLM 추출 → 수동 추출 보완
				if xo, ok := r.llmExtractPayment(req.Context(), lang, msg.Content); ok {

					r.logger.Printf("[root][payment][collect] xo: mode=%s method=%q to=%q shipping=%q merchant=%q amount=%d budget=%d item=%q model=%q",
						xo.Fields.Mode, xo.Fields.Method, xo.Fields.To, xo.Fields.Shipping, xo.Fields.Merchant, xo.Fields.AmountKRW, xo.Fields.BudgetKRW, xo.Fields.Item, xo.Fields.Model)

					// 🔧 핫픽스: To/Recipient 둘 다 병합
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

				// ==== 미리보기 + 확인 ====
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

			// 1) Try LLM slot-extractor first
			if xo, ok := r.llmExtractMedical(req.Context(), lang, msg.Content); ok {
				if len(xo.Missing) > 0 {
					clar := types.AgentMessage{
						ID:        msg.ID + "-needinfo",
						From:      "root",
						To:        msg.From,
						Type:      "clarify",
						Content:   strings.TrimSpace(xo.Ask),
						Timestamp: time.Now(),
						Metadata: map[string]any{
							"await":   "medical.slots",
							"missing": strings.Join(xo.Missing, ", "),
							"lang":    lang,
							"hint": map[string]string{
								"ko": `예: {"medical.condition":"당뇨병","medical.topic":"식단","audience":"본인","duration":"2주"}`,
								"en": `Ex: {"medical.condition":"diabetes","medical.topic":"diet","audience":"self","duration":"2 weeks"}`,
							}[lang],
						},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(clar)
					return
				}
				// Merge extracted fields
				fillMsgMetaFromMedical(&msg, xo.Fields, lang)

				// External if configured; else answer locally with LLM
				if r.externalURLFor("medical") != "" {
					outPtr, err := r.sendExternal(req.Context(), "medical", &msg)
					if err != nil {
						http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
						return
					}
					out := *outPtr
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(out)
					return
				}

				// Local medical answer via LLM
				r.ensureLLM()
				s, _ := extractMedicalSlots(&msg)
				answer := r.llmMedicalAnswer(req.Context(), lang, msg.Content, s)
				out := types.AgentMessage{
					ID:        msg.ID + "-medical",
					From:      "root",
					To:        msg.From,
					Type:      "response",
					Content:   answer,
					Timestamp: time.Now(),
					Metadata:  map[string]any{"lang": lang, "mode": "medical", "domain": "medical"},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(out)
				return
			}

			// 2) Fallback to existing rule-based path
			slots, missing := extractMedicalSlots(&msg)
			if len(missing) > 0 {
				q := r.askForMissingMedicalWithLLM(req.Context(), lang, missing, msg.Content)
				clar := types.AgentMessage{
					ID:        msg.ID + "-needinfo",
					From:      "root",
					To:        msg.From,
					Type:      "clarify",
					Content:   q,
					Timestamp: time.Now(),
					Metadata: map[string]any{
						"await":   "medical.slots",
						"missing": strings.Join(missing, ", "),
						"lang":    lang,
						"hint": map[string]string{
							"ko": `예: {"medical.condition":"당뇨병","medical.topic":"식단","audience":"본인","duration":"2주"}`,
							"en": `Ex: {"medical.condition":"diabetes","medical.topic":"diet","audience":"self","duration":"2 weeks"}`,
						}[lang],
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
			msg.Metadata["medical.condition"] = slots.Condition
			msg.Metadata["medical.topic"] = slots.Topic
			if slots.Audience != "" {
				msg.Metadata["medical.audience"] = slots.Audience
			}
			if slots.Duration != "" {
				msg.Metadata["medical.duration"] = slots.Duration
			}
			if slots.Age != "" {
				msg.Metadata["medical.age"] = slots.Age
			}
			if slots.Medications != "" {
				msg.Metadata["medical.meds"] = slots.Medications
			}
			msg.Metadata["lang"] = lang

			if r.externalURLFor("medical") != "" {
				outPtr, err := r.sendExternal(req.Context(), "medical", &msg)
				if err != nil {
					http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
					return
				}
				out := *outPtr
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(out)
				return
			}
			r.ensureLLM()
			answer := r.llmMedicalAnswer(req.Context(), lang, msg.Content, slots)
			out := types.AgentMessage{
				ID:        msg.ID + "-medical",
				ContextID: cid,
				From:      "root",
				To:        msg.From,
				Type:      "response",
				Content:   answer,
				Timestamp: time.Now(),
				Metadata:  map[string]any{"lang": lang, "mode": "medical", "domain": "medical"},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)
			return

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

			// 외부 URL 없으면 로컬 LLM로 요약
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

var amountRe = regexp.MustCompile(`(?i)(\d[\d,\.]*)\s*(원|krw|만원|usd|usdc|eth|btc)`)

func isPaymentActionIntent(c string) bool {
	// 질문투면 라우팅 보류(강한 지시어 있으면 허용)
	if isQuestionLike(c) && !isOrderish(c) && !containsAny(c, "보내", "송금", "이체", "지불해", "pay", "send", "transfer") {
		return false
	}
	if isOrderish(c) || containsAny(c, "보내", "송금", "이체", "결제해", "지불해", "pay", "send", "transfer") {
		return true
	}
	// 슬롯 힌트 2개 이상이면 결제 의도
	hits := 0
	if amountRe.FindStringIndex(c) != nil {
		hits++
	}
	if hasMethodCue(c) {
		hits++
	}
	if hasRecipientCue(c) {
		hits++
	}
	return hits >= 2
}

func isMedicalInfoIntent(c string) bool {
	c = strings.ToLower(strings.TrimSpace(c))

	// 대표 질환/영역
	if containsAny(c,
		"당뇨", "혈당", "고혈당", "저혈당", "당화혈색소", "insulin", "metformin",
		"정신", "우울", "불안", "조현", "bipolar", "adhd", "치매", "수면",
		"우울증", "공황", "강박", "ptsd",
		"hypertension", "고혈압", "고지혈", "cholesterol",
	) {
		return true
	}

	// 의료정보 톤
	if containsAny(c,
		"증상", "원인", "치료", "약", "복용", "부작용", "관리", "생활습관",
		"가이드라인", "권고안", "주의사항", "금기", "진단", "검사",
		"symptom", "treatment", "side effect", "guideline", "diagnosis",
	) && containsAny(c, "알려줘", "설명", "정보", "방법", "how", "what", "guide") {
		return true
	}

	// "~~ 먹어도 돼?" 같은 질문
	if containsAny(c, "먹어도 돼", "괜찮아", "해도 돼", "해도돼", "임신", "모유", "술", "운동") &&
		containsAny(c, "약", "복용", "병", "질환", "증상") {
		return true
	}

	return false
}

func isPlanningActionIntent(c string) bool {
	if containsAny(c, "계획해", "플랜 짜줘", "일정 짜줘", "plan", "schedule", "스케줄 만들어", "할일 정리") {
		return true
	}
	// '계획/일정' 키워드가 있고 질문투가 아니면 라우팅
	return containsAny(c, "계획", "일정", "플랜", "todo") && !isQuestionLike(c)
}

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
