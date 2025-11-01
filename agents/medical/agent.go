// package medical provides a reusable MedicalAgent with the same security model
// as payment: RFC9421 DID signature verification middleware + HPKE (handshake/data)
// + plain JSON fallback when HPKE is off.
// Endpoints: /status, /process (identical behavior/headers to payment).
package medical

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
	"strconv"
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

	// [LLM] shim
	"github.com/sage-x-project/sage-multi-agent/llm"
)

// -------- Public API --------

type MedicalAgent struct {
	RequireSignature bool // true = RFC9421 required, false = allow plaintext (no verify)

	// internals
	logger *log.Logger

	// HPKE (lazy enabled)
	hpkeMgr *session.Manager
	hpkeSrv *hpke.Server
	hsrv    *sagehttp.HTTPServer // handshake adapter
	hpkeMu  sync.Mutex           // lazy enable lock

	mw      *server.DIDAuthMiddleware // from a2autil.BuildDIDMiddleware
	openMux *http.ServeMux            // /status
	protMux *http.ServeMux            // /process
	handler http.Handler              // final handler
	httpSrv *http.Server

	// [LLM] lazy client
	llmClient llm.Client
}

// NewMedicalAgent builds the agent (same signature as payment.NewPaymentAgent).
func NewMedicalAgent(requireSignature bool) (*MedicalAgent, error) {
	agent := &MedicalAgent{
		RequireSignature: requireSignature,
		logger:           log.New(os.Stdout, "[medical] ", log.LstdFlags),
	}

	// ===== DID middleware =====
	if agent.RequireSignature {
		mw, err := a2autil.BuildDIDMiddleware(true)
		if err != nil {
			agent.logger.Printf("[medical] DID middleware init failed: %v (running without verify)", err)
			agent.mw = nil
		} else {
			mw.SetErrorHandler(newCompactDIDErrorHandler(agent.logger))
			agent.mw = mw
		}
	} else {
		agent.logger.Printf("[medical] DID middleware disabled (requireSignature=false)")
		agent.mw = nil
	}

	// ===== Open mux: /status =====
	open := http.NewServeMux()
	open.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "medical",
			"type":         "medical",
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
			if err := agent.ensureHPKE(); err != nil {
				agent.logger.Printf("[medical] ensureHPKE: %v", err)
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

	// ===== Optional eager HPKE boot =====
	_ = agent.ensureHPKE()

	// [LLM] lazy: only init when used
	if c, err := llm.NewFromEnv(); err == nil {
		agent.llmClient = c
	} else {
		agent.logger.Printf("[medical] LLM disabled: %v", err)
	}

	return agent, nil
}

// 핸들러 반환
func (e *MedicalAgent) Handler() http.Handler { return e.handler }

// 서버 실행
func (e *MedicalAgent) Start(addr string) error {
	if e.handler == nil {
		return fmt.Errorf("handler not initialized")
	}
	e.httpSrv = &http.Server{Addr: addr, Handler: e.handler}
	e.logger.Printf("[boot] medical on %s (requireSig=%v, hpke_ready=%v)", addr, e.RequireSignature, e.hpkeSrv != nil)
	return e.httpSrv.ListenAndServe()
}

// 서버 종료
func (e *MedicalAgent) Shutdown(ctx context.Context) error {
	if e.httpSrv == nil {
		return nil
	}
	return e.httpSrv.Shutdown(ctx)
}

// -------- Lazy HPKE enable --------

func (e *MedicalAgent) ensureHPKE() error {
	e.hpkeMu.Lock()
	defer e.hpkeMu.Unlock()

	if e.hpkeSrv != nil && e.hpkeMgr != nil && e.hsrv != nil {
		return nil
	}

	sigPath := strings.TrimSpace(os.Getenv("MEDICAL_JWK_FILE"))
	kemPath := strings.TrimSpace(os.Getenv("MEDICAL_KEM_JWK_FILE"))
	if sigPath == "" || kemPath == "" {
		e.logger.Printf("[boot] medical HPKE disabled (missing MEDICAL_JWK_FILE or MEDICAL_KEM_JWK_FILE)")
		return fmt.Errorf("missing MEDICAL_JWK_FILE or MEDICAL_KEM_JWK_FILE")
	}

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
	serverDID := strings.TrimSpace(nameToDID["medical"])
	if serverDID == "" {
		return fmt.Errorf("HPKE: server DID not found for name 'medical' in %s", keysPath)
	}

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

	e.logger.Printf("[boot] medical HPKE enabled (lazy)")
	return nil
}

// -------- Application handler (LLM-driven medical info) --------

// -------- Application handler (LLM-driven medical info) --------
func (e *MedicalAgent) appHandler(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
	var in types.AgentMessage
	if err := json.Unmarshal(msg.Payload, &in); err != nil {
		return &transport.Response{
			Success:   false,
			MessageID: msg.ID,
			TaskID:    msg.TaskID,
			Error:     fmt.Errorf("bad json: %w", err),
		}, nil
	}

	// Extract optional slots from metadata (Root가 채워줬다면 사용)
	symptoms := getMetaString(in.Metadata, "medical.symptoms", "symptoms")
	age := getMetaInt64(in.Metadata, "medical.age", "age")
	sex := getMetaString(in.Metadata, "medical.sex", "sex")
	duration := getMetaString(in.Metadata, "medical.duration", "duration")
	severity := getMetaString(in.Metadata, "medical.severity", "severity")
	meds := getMetaString(in.Metadata, "medical.meds", "medications")
	allergy := getMetaString(in.Metadata, "medical.allergies", "allergies")
	conds := getMetaString(in.Metadata, "medical.conditions", "conditions")

	lang := getMetaString(in.Metadata, "lang")
	if lang == "" {
		lang = llm.DetectLang(in.Content)
	}
	if lang != "ko" && lang != "en" {
		lang = "ko"
	}

	// 사용자 질문/컨텍스트 정리
	query := strings.TrimSpace(in.Content)
	if query == "" && symptoms != "" {
		query = fmt.Sprintf("증상: %s", symptoms)
	}

	// LLM 프롬프트 (안전/정보용, 한 문장)
	sys := map[string]string{
		"ko": "너는 의료 정보 도우미야. 진단/처방 없이 안전한 일반 의학 정보를 '한 문장'으로만 제공해. 응급 징후가 의심되면 전문의 진료를 권유하고, 과도한 세부사항/코드블록/목록은 금지.",
		"en": "You are a medical info assistant. Provide one short, safe, general informational sentence. No diagnosis/prescription. If red flags are possible, suggest seeing a professional. No lists or code blocks.",
	}[lang]

	var sb strings.Builder
	if query != "" {
		fmt.Fprintf(&sb, "UserQuestion: %s\n", query)
	}
	if symptoms != "" {
		fmt.Fprintf(&sb, "Symptoms: %s\n", symptoms)
	}
	if age > 0 {
		fmt.Fprintf(&sb, "Age: %d\n", age)
	}
	if sex != "" {
		fmt.Fprintf(&sb, "Sex: %s\n", sex)
	}
	if duration != "" {
		fmt.Fprintf(&sb, "Duration: %s\n", duration)
	}
	if severity != "" {
		fmt.Fprintf(&sb, "Severity: %s\n", severity)
	}
	if meds != "" {
		fmt.Fprintf(&sb, "Medications: %s\n", meds)
	}
	if allergy != "" {
		fmt.Fprintf(&sb, "Allergies: %s\n", allergy)
	}
	if conds != "" {
		fmt.Fprintf(&sb, "Conditions: %s\n", conds)
	}
	usr := sb.String()

	// LLM 호출 (없거나 실패 시 보수적 폴백)
	text := ""
	if e.llmClient != nil {
		if out, err := e.llmClient.Chat(ctx, sys, usr); err == nil && strings.TrimSpace(out) != "" {
			text = strings.TrimSpace(out)
		}
	}
	if text == "" {
		if lang == "en" {
			text = "This is general health information, not a diagnosis. If symptoms are severe or worsening (e.g., chest pain, trouble breathing, confusion), seek emergency care; otherwise rest, hydrate, monitor, and see a clinician if symptoms persist."
		} else {
			text = "이 답변은 일반 건강 정보이며 진단이 아닙니다. 흉통·호흡곤란·의식 변화 등 심한 증상은 즉시 응급실을 방문하고, 경미하면 휴식·수분섭취·경과 관찰 후 지속 시 의료진과 상담하세요."
		}
	}

	out := types.AgentMessage{
		ID:        in.ID + "-medical",
		From:      "medical",
		To:        in.From,
		Type:      "response",
		Content:   text,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"agent": "medical",
			"hpke":  msg.Metadata["hpke"],
			"context": map[string]any{
				"symptoms": symptoms,
				"age":      age,
				"sex":      sex,
				"duration": duration,
				"severity": severity,
				"meds":     meds,
				"allergy":  allergy,
				"conds":    conds,
			},
		},
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

func loadServerSigningKeyFromEnv() (sagecrypto.KeyPair, error) {
	path := strings.TrimSpace(os.Getenv("MEDICAL_JWK_FILE"))
	if path == "" {
		return nil, fmt.Errorf("missing MEDICAL_JWK_FILE for server signing key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read MEDICAL_JWK_FILE (%s): %w", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import MEDICAL_JWK_FILE (%s) as JWK: %w", path, err)
	}
	return kp, nil
}

func loadServerKEMFromEnv() (sagecrypto.KeyPair, error) {
	path := strings.TrimSpace(os.Getenv("MEDICAL_KEM_JWK_FILE"))
	if path == "" {
		return nil, fmt.Errorf("missing MEDICAL_KEM_JWK_FILE for server KEM key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read MEDICAL_KEM_JWK_FILE (%s): %w", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import MEDICAL_KEM_JWK_FILE (%s) as JWK: %w", path, err)
	}
	return kp, nil
}

func buildResolver() (sagedid.Resolver, error) {
	rpc := firstNonEmpty(os.Getenv("ETH_RPC_URL"), "http://127.0.0.1:8545")
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_ADDRESS"), "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	cfgV4 := &sagedid.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv, // optional (read-only)
		GasPrice:           0,
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}
	ethV4, err := dideth.NewEthereumClient(cfgV4)
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

func getMetaString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok2 := v.(string); ok2 && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func getMetaInt64(m map[string]any, keys ...string) int64 {
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

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}
