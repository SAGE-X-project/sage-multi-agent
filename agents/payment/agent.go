// package payment converts the former cmd/payment server into a reusable agent module.
// It exposes a PaymentAgent that can be embedded into any HTTP stack.
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/internal/agentmux"
	"github.com/sage-x-project/sage-multi-agent/llm"
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

// NewPaymentAgent builds the agent.
func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
	agent := &PaymentAgent{
		RequireSignature: requireSignature,
		logger:           log.New(os.Stdout, "[payment] ", log.LstdFlags),
	}

	// ===== DID middleware =====
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
	open.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
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
	protected.HandleFunc("/payment/process", func(w http.ResponseWriter, r *http.Request) {
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
				agent.logger.Printf("[payment] ensureHPKE error: %v", err)
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
				// Try to treat as handshake if session vanished
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
	agent.handler = agentmux.BuildAgentHandler("payment", open, protected, agent.mw)
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
		agent.logger.Printf("[payment] LLM disabled: %v", err)
	}

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

func (e *PaymentAgent) ensureHPKE() error {
	e.hpkeMu.Lock()
	defer e.hpkeMu.Unlock()

	if e.hpkeSrv != nil && e.hpkeMgr != nil && e.hsrv != nil {
		return nil
	}

	sigPath := strings.TrimSpace(os.Getenv("PAYMENT_JWK_FILE"))
	kemPath := strings.TrimSpace(os.Getenv("PAYMENT_KEM_JWK_FILE"))
	if sigPath == "" || kemPath == "" {
		e.logger.Printf("[boot] payment HPKE disabled (missing PAYMENT_JWK_FILE or PAYMENT_KEM_JWK_FILE)")
		return fmt.Errorf("missing PAYMENT_JWK_FILE or PAYMENT_KEM_JWK_FILE")
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
	serverDID := strings.TrimSpace(nameToDID["payment"])
	if serverDID == "" {
		return fmt.Errorf("HPKE: server DID not found for name 'payment' in %s", keysPath)
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

	e.logger.Printf("[boot] payment HPKE enabled (lazy)")
	return nil
}

// -------- Application handler (extended with LLM) --------

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

	// Extract slots passed by Root (fallback to naive parsing).
	to := getMetaString(in.Metadata, "payment.to", "to", "recipient")
	method := getMetaString(in.Metadata, "payment.method", "method")
	item := getMetaString(in.Metadata, "item", "payment.item")
	memo := getMetaString(in.Metadata, "memo", "payment.memo")
	amount := getMetaInt64(in.Metadata, "payment.amountKRW", "amountKRW", "amount")
	if amount <= 0 {
		if b := getMetaInt64(in.Metadata, "payment.budgetKRW", "budgetKRW"); b > 0 {
			amount = b
			if in.Metadata == nil {
				in.Metadata = map[string]any{}
			}
			in.Metadata["payment.amountIsEstimated"] = true
		}
	}

	// If essential fields are missing, just echo (legacy behavior).
	budget := getMetaInt64(in.Metadata, "payment.budgetKRW", "budgetKRW")
	hasMoney := (amount > 0 || budget > 0)
	useEcho := (strings.TrimSpace(to) == "" || !hasMoney || strings.TrimSpace(method) == "")
	lang := getMetaString(in.Metadata, "lang")
	if lang == "" {
		lang = llm.DetectLang(in.Content) // fallback
	}
	if lang != "ko" && lang != "en" {
		lang = "ko"
	}

	if e.llmClient == nil || useEcho {
		out := types.AgentMessage{
			ID:        in.ID + "-ok",
			From:      "payment",
			To:        in.From,
			Type:      "response",
			Content:   fmt.Sprintf("External payment processed at %s (echo): %s", time.Now().Format(time.RFC3339), strings.TrimSpace(in.Content)),
			Timestamp: time.Now(),
		}
		b, _ := json.Marshal(out)
		return &transport.Response{Success: true, MessageID: msg.ID, TaskID: msg.TaskID, Data: b}, nil
	}

	// === LLM로 영수증 한 줄 생성 (실패 시 템플릿 폴백) ===
	text := e.generateReceipt(ctx, lang, to, amount, method, item, memo)
	// === 끝 ===

	out := types.AgentMessage{
		ID:        in.ID + "-receipt",
		From:      "payment",
		To:        in.From,
		Type:      "response",
		Content:   text,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"receipt": map[string]any{
				"to":          to,
				"amountKRW":   amount,
				"method":      method,
				"item":        item,
				"memo":        memo,
				"orderId":     fmt.Sprintf("ORD-%04d", time.Now().Unix()%10000),
				"generatedAt": time.Now().Format(time.RFC3339),
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

// -------- LLM Receipt generator --------

func (e *PaymentAgent) generateReceipt(ctx context.Context, lang, to string, amount int64, method, item, memo string) string {
	// System prompt keeps it terse and single-line.
	sys := map[string]string{
		"ko": `너는 결제 영수증 생성기야.
- 딱 한 줄로만 출력하고, 이모지/불릿/따옴표/코드블록/여분 공백/개행 없이.
- 형식 예시(참고용): 영수증: 수신자=홍길동, 금액=1,250,000원, 방법=카드, 품목=iPhone 15 Pro, 메모=생일선물 · 2025-11-01T12:30:00Z
- 필드가 비어있으면 생략.
- KRW는 천단위 콤마와 "원"을 사용.
- 너무 장문 금지(140자 이내).`,
		"en": `You generate a one-line payment receipt.
- Exactly one line, no emojis/bullets/quotes/code blocks, no extra whitespace.
- Example (for style only): Receipt: to=Alice, amount=₩1,250,000, method=card, item=iPhone 15 Pro, memo=birthday · 2025-11-01T12:30:00Z
- Omit empty fields.
- Use thousands separators and the KRW symbol or "₩".
- Keep it under ~140 chars.`,
	}[lang]
	// Normalize labels for method per language
	mlabel := methodLabel(lang, method)
	now := time.Now().UTC().Format(time.RFC3339)
	amt := "₩" + withComma(amount)
	if lang == "ko" {
		amt = withComma(amount) + "원"
	}

	usr := fmt.Sprintf(
		"lang=%s\nto=%s\namount=%s\nmethod=%s\nitem=%s\nmemo=%s\ntimestamp=%s",
		lang, strings.TrimSpace(to), amt, mlabel, strings.TrimSpace(item), strings.TrimSpace(memo), now,
	)

	if e.llmClient != nil {
		if out, err := e.llmClient.Chat(ctx, sys, usr); err == nil {
			if s := strings.TrimSpace(out); s != "" && !strings.Contains(s, "\n") {
				return s
			}
		}
	}

	// Fallback template (no LLM or failure)
	ts := now
	var parts []string
	if to != "" {
		if lang == "ko" {
			parts = append(parts, "수신자="+to)
		} else {
			parts = append(parts, "to="+to)
		}
	}
	if lang == "ko" {
		parts = append(parts, "금액="+amt)
		parts = append(parts, "방법="+mlabel)
		if item != "" {
			parts = append(parts, "품목="+item)
		}
		if memo != "" {
			parts = append(parts, "메모="+memo)
		}
		return "영수증: " + strings.Join(parts, ", ") + " · " + ts
	}
	parts = append(parts, "amount="+amt, "method="+mlabel)
	if item != "" {
		parts = append(parts, "item="+item)
	}
	if memo != "" {
		parts = append(parts, "memo="+memo)
	}
	return "Receipt: " + strings.Join(parts, ", ") + " · " + ts
}

func methodLabel(lang, method string) string {
	m := strings.ToLower(strings.TrimSpace(method))
	if lang == "ko" {
		switch m {
		case "card", "credit", "debit":
			return "카드"
		case "bank", "transfer", "account":
			return "계좌이체"
		case "kakaopay":
			return "카카오페이"
		case "naverpay":
			return "네이버페이"
		case "toss":
			return "토스"
		case "cash":
			return "현금"
		default:
			return method
		}
	}
	switch m {
	case "kakaopay":
		return "kakaopay"
	case "naverpay":
		return "naverpay"
	case "toss":
		return "toss"
	default:
		return m
	}
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
	path := strings.TrimSpace(os.Getenv("PAYMENT_JWK_FILE"))
	if path == "" {
		return nil, fmt.Errorf("missing PAYMENT_JWK_FILE for server signing key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read PAYMENT_JWK_FILE (%s): %w", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import PAYMENT_JWK_FILE (%s) as JWK: %w", path, err)
	}
	return kp, nil
}

func loadServerKEMFromEnv() (sagecrypto.KeyPair, error) {
	path := strings.TrimSpace(os.Getenv("PAYMENT_KEM_JWK_FILE"))
	if path == "" {
		return nil, fmt.Errorf("missing PAYMENT_KEM_JWK_FILE for server KEM key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read PAYMENT_KEM_JWK_FILE (%s): %w", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import PAYMENT_KEM_JWK_FILE (%s) as JWK: %w", path, err)
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
	return func(w http.ResponseWriter, _ *http.Request, err error) {
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
				if n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(t), ",", ""), 10, 64); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

func withComma(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := ""
	if n < 0 {
		neg = "-"
		s = s[1:]
	}
	out := ""
	for i, c := range reverse(s) {
		if i > 0 && i%3 == 0 {
			out = "," + out
		}
		out = string(c) + out
	}
	return neg + out
}

func reverse(s string) []rune {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return r
}
