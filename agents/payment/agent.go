// package payment converts the former cmd/payment server into a reusable agent module.
// It exposes an PaymentAgent that can be embedded into any HTTP stack.
// Features:
// - DID signature verification (RFC 9421) via internal middleware
// - HPKE handshake (SecureMessage JSON) + data-mode HPKE decrypt/encrypt
// - Plain JSON fallback when HPKE is off (and can be enabled lazily on first HPKE request)
// - /status, /process endpoints identical to the cmd version
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"

	// DID / Resolver
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
	sagehttp "github.com/sage-x-project/sage/pkg/agent/transport/http"

	// HPKE
	"github.com/sage-x-project/sage/pkg/agent/hpke"
	"github.com/sage-x-project/sage/pkg/agent/session"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	// Keys
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
)

// -------- Public API --------

type PaymentAgent struct {
	RequireSignature bool // true = RFC9421 서명 필수, false = 서명 없어도 통과(검증은 시도 안 함)

	// internals
	logger *log.Logger

	// HPKE (lazy enabled)
	hpkeMgr *session.Manager
	hpkeSrv *hpke.Server
	hsrv    *sagehttp.HTTPServer // handshake adapter
	hpkeMu  sync.Mutex           // lazy enable 락

	mw      *server.DIDAuthMiddleware // a2autil.BuildDIDMiddleware 반환 타입
	openMux *http.ServeMux            // /status
	protMux *http.ServeMux            // /process (보호 경로)
	handler http.Handler              // 최종 핸들러 (미들웨어 포함)
	httpSrv *http.Server
}

// NewPaymentAgent builds the agent.
// HPKE는 부팅 시 env가 존재하면 즉시 켜고, 없어도 구동하며, 첫 HPKE 요청에서 lazy enable 한다.
func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
	agent := &PaymentAgent{
		RequireSignature: requireSignature,
		logger:           log.New(os.Stdout, "[payment] ", log.LstdFlags),
	}

	// ===== DID middleware =====
	// RequireSignature=false(SAGE OFF)면 미들웨어 자체를 비활성화해서 평문 완전 허용
	if agent.RequireSignature {
		mw, err := a2autil.BuildDIDMiddleware(true)
		if err != nil {
			agent.logger.Printf("[payment] DID middleware init failed: %v (running without verify)", err)
			agent.mw = nil
		} else {
			mw.SetErrorHandler(newCompactDIDErrorHandler(agent.logger))
			agent.mw = mw
		}
	} else {
		agent.logger.Printf("[payment] DID middleware disabled (requireSignature=false)")
		agent.mw = nil
	}

	// ===== Open mux: /status =====
	open := http.NewServeMux()
	open.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "payment",
			"type":         "payment",
			"sage_enabled": agent.RequireSignature,
			"hpke_ready":   agent.hpkeSrv != nil,
			"time":         time.Now().Format(time.RFC3339),
		})
	})
	agent.openMux = open

	// ===== Protected mux: /process =====
	protected := http.NewServeMux()
	protected.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		// Rehydrate minimal SecureMessage context from headers (data-mode)
		did := strings.TrimSpace(r.Header.Get("X-SAGE-DID"))
		mid := strings.TrimSpace(r.Header.Get("X-SAGE-Message-ID"))
		ctxID := strings.TrimSpace(r.Header.Get("X-SAGE-Context-ID"))
		taskID := strings.TrimSpace(r.Header.Get("X-SAGE-Task-ID"))

		// HPKE path?
		if isHPKE(r) {
			// ⇩ lazy enable 시도 (env에 키가 있으면 즉시 켬)
			if err := agent.ensureHPKE(); err != nil {
				http.Error(w, "hpke disabled", http.StatusBadRequest)
				return
			}

			kid := strings.TrimSpace(r.Header.Get("X-KID"))

			// --- Handshake (no KID) ---
			if kid == "" {
				if agent.hsrv == nil {
					http.Error(w, "hpke handshake disabled", http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				agent.hsrv.MessagesHandler().ServeHTTP(w, r)
				return
			}

			// --- Data mode (has KID) ---
			sess, ok := agent.hpkeMgr.GetByKeyID(kid)
			if !ok {
				// 혹시 핸드셰이크 JSON이 온 경우 어댑터로 재시도
				if agent.hsrv != nil {
					r.Body = io.NopCloser(bytes.NewReader(body))
					agent.hsrv.MessagesHandler().ServeHTTP(w, r)
					return
				}
				http.Error(w, "hpke session not found", http.StatusBadRequest)
				return
			}
			pt, err := sess.Decrypt(body)
			if err != nil {
				http.Error(w, "hpke decrypt failed", http.StatusBadRequest)
				return
			}
			sm := &transport.SecureMessage{
				ID:        mid,
				ContextID: ctxID,
				TaskID:    taskID,
				Payload:   pt,
				DID:       did,
				Metadata:  map[string]string{"hpke": "true"},
				Role:      "agent",
			}

			resp, _ := agent.appHandler(r.Context(), sm)
			if !resp.Success {
				http.Error(w, "application error", http.StatusBadRequest)
				return
			}
			ct, err := sess.Encrypt(resp.Data)
			if err != nil {
				http.Error(w, "hpke encrypt failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/sage+hpke")
			w.Header().Set("X-SAGE-HPKE", "v1")
			w.Header().Set("X-KID", kid)
			w.Header().Set("Content-Digest", a2autil.ComputeContentDigest(ct))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(ct)
			return
		}

		// --- Plain data-mode ---
		sm := &transport.SecureMessage{
			ID:        mid,
			ContextID: ctxID,
			TaskID:    taskID,
			Payload:   body,
			DID:       did,
			Metadata:  map[string]string{"hpke": "false"},
			Role:      "agent",
		}
		resp, _ := agent.appHandler(r.Context(), sm)
		if !resp.Success {
			http.Error(w, "application error", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp.Data)
	})
	agent.protMux = protected

	// ===== Compose final handler =====
	var h http.Handler = open
	if agent.mw != nil {
		wrapped := agent.mw.Wrap(protected)
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/status" {
				open.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	} else {
		root := http.NewServeMux()
		root.Handle("/status", open)
		root.Handle("/process", protected)
		h = root
	}
	agent.handler = h

	// ===== Optional eager HPKE boot (env가 이미 준비돼있다면) =====
	_ = agent.ensureHPKE() // 실패해도 그냥 평문 모드로 동작 계속

	return agent, nil
}

// 핸들러 반환
func (e *PaymentAgent) Handler() http.Handler { return e.handler }

// 서버 실행
func (e *PaymentAgent) Start(addr string) error {
	if e.handler == nil {
		return fmt.Errorf("handler not initialized")
	}
	e.httpSrv = &http.Server{Addr: addr, Handler: e.handler}
	e.logger.Printf("[boot] payment on %s (requireSig=%v, hpke_ready=%v)", addr, e.RequireSignature, e.hpkeSrv != nil)
	return e.httpSrv.ListenAndServe()
}

// 서버 종료
func (e *PaymentAgent) Shutdown(ctx context.Context) error {
	if e.httpSrv == nil {
		return nil
	}
	return e.httpSrv.Shutdown(ctx)
}

// -------- Lazy HPKE enable --------

// ensureHPKE: hpkeSrv/hsrv가 없으면 env에서 키/리졸버를 읽어 즉시 켠다.
func (e *PaymentAgent) ensureHPKE() error {
	e.hpkeMu.Lock()
	defer e.hpkeMu.Unlock()

	// 이미 준비됨
	if e.hpkeSrv != nil && e.hpkeMgr != nil && e.hsrv != nil {
		return nil
	}

	// env 키 경로 확인
	sigPath := strings.TrimSpace(os.Getenv("EXTERNAL_JWK_FILE"))
	kemPath := strings.TrimSpace(os.Getenv("EXTERNAL_KEM_JWK_FILE"))
	if sigPath == "" || kemPath == "" {
		e.logger.Printf("[boot] payment HPKE disabled (missing EXTERNAL_JWK_FILE or EXTERNAL_KEM_JWK_FILE)")
		return fmt.Errorf("missing EXTERNAL_JWK_FILE or EXTERNAL_KEM_JWK_FILE")
	}

	// 키/리졸버 로드
	hpkeMgr := session.NewManager()
	signKP, err := loadServerSigningKeyFromEnv()
	if err != nil {
		return fmt.Errorf("hpke signing key: %w", err)
	}
	resolver, err := buildResolver()
	if err != nil {
		return fmt.Errorf("hpke resolver: %w", err)
	}
	kemKP, err := loadServerKEMFromEnv()
	if err != nil {
		return fmt.Errorf("hpke kem key: %w", err)
	}

	keysPath := firstNonEmpty(os.Getenv("HPKE_KEYS_FILE"), "merged_agent_keys.json")
	nameToDID, err := loadDIDsFromKeys(keysPath)
	if err != nil {
		return fmt.Errorf("HPKE: load keys (%s): %w", keysPath, err)
	}
	serverDID := strings.TrimSpace(nameToDID["external"])
	if serverDID == "" {
		return fmt.Errorf("HPKE: server DID not found for name 'external' in %s", keysPath)
	}

	// 서버/핸드셰이크 어댑터 구성
	e.hpkeMgr = hpkeMgr
	e.hpkeSrv = hpke.NewServer(
		signKP,
		hpkeMgr,
		serverDID,
		resolver,
		&hpke.ServerOpts{KEM: kemKP},
	)
	e.hsrv = sagehttp.NewHTTPServer(func(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
		return e.hpkeSrv.HandleMessage(ctx, msg)
	})

	e.logger.Printf("[boot] payment HPKE enabled (lazy)")
	return nil
}

// -------- Application handler (echo) --------

func (e *PaymentAgent) appHandler(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
	var in types.AgentMessage
	if err := json.Unmarshal(msg.Payload, &in); err != nil {
		return &transport.Response{
			Success:   false,
			MessageID: msg.ID,
			TaskID:    msg.TaskID,
			Error:     fmt.Errorf("bad json: %w", err),
		}, nil
	}
	out := types.AgentMessage{
		ID:        in.ID + "-ok",
		From:      "payment",
		To:        in.From,
		Type:      "response",
		Content:   fmt.Sprintf("External payment processed at %s (echo): %s", time.Now().Format(time.RFC3339), strings.TrimSpace(in.Content)),
		Timestamp: time.Now(),
	}
	b, _ := json.Marshal(out)
	return &transport.Response{
		Success:   true,
		MessageID: msg.ID,
		TaskID:    msg.TaskID,
		Data:      b,
	}, nil
}

// -------- Internals (ported & helpers) --------

type agentKeyRow struct {
	Name string `json:"name"`
	DID  string `json:"did"`
}

func isHPKE(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/sage+hpke") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-SAGE-HPKE")), "v1") {
		return true
	}
	return false
}

// Load Ed25519 signing key from EXTERNAL_JWK_FILE (JWK).
func loadServerSigningKeyFromEnv() (sagecrypto.KeyPair, error) {
	path := strings.TrimSpace(os.Getenv("EXTERNAL_JWK_FILE"))
	if path == "" {
		return nil, fmt.Errorf("missing EXTERNAL_JWK_FILE for server signing key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read EXTERNAL_JWK_FILE (%s): %w", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import EXTERNAL_JWK_FILE (%s) as JWK: %w", path, err)
	}
	return kp, nil
}

// Load X25519 KEM key from EXTERNAL_KEM_JWK_FILE (JWK).
func loadServerKEMFromEnv() (sagecrypto.KeyPair, error) {
	path := strings.TrimSpace(os.Getenv("EXTERNAL_KEM_JWK_FILE"))
	if path == "" {
		return nil, fmt.Errorf("missing EXTERNAL_KEM_JWK_FILE for server KEM key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read EXTERNAL_KEM_JWK_FILE (%s): %w", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import EXTERNAL_KEM_JWK_FILE (%s) as JWK: %w", path, err)
	}
	return kp, nil
}

// Create a MultiChain resolver from env (ETH_RPC_URL / SAGE_REGISTRY_V4_ADDRESS / SAGE_EXTERNAL_KEY).
func buildResolver() (sagedid.Resolver, error) {
	rpc := firstNonEmpty(os.Getenv("ETH_RPC_URL"), "http://127.0.0.1:8545")
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_V4_ADDRESS"), "0x5FbDB2315678afecb367f032d93F642f64180aa3")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	cfgV4 := &sagedid.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv, // optional (read-only)
		GasPrice:           0,
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}
	ethV4, err := dideth.NewEthereumClientV4(cfgV4)
	if err != nil {
		return nil, fmt.Errorf("HPKE: init resolver failed: %w", err)
	}
	return ethV4, nil
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

// DID middleware compact error shape (unchanged)
func newCompactDIDErrorHandler(l *log.Logger) func(w http.ResponseWriter, r *http.Request, err error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		re := rootError(err)
		if l != nil {
			l.Printf("⚠️ [did-auth] %s", re.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "unauthorized",
			"reason": re.Error(),
		})
	}
}

func rootError(err error) error {
	e := err
	for {
		u := errors.Unwrap(e)
		if u == nil {
			return e
		}
		e = u
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
