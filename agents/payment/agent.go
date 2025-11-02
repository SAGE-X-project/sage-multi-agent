// package payment converts the former cmd/payment server into a reusable agent module.
// It exposes a PaymentAgent that can be embedded into any HTTP stack.
// Features:
// - DID signature verification (RFC 9421) via internal middleware
// - HPKE handshake (SecureMessage JSON) + data-mode HPKE decrypt/encrypt (Eager pattern)
// - Plain JSON fallback when HPKE is off
// - /status, /process endpoints identical to the cmd version
// - Uses internal agent framework for crypto, HPKE, and DID resolution
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
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/internal/agentmux"
	"github.com/sage-x-project/sage-multi-agent/llm"
	"github.com/sage-x-project/sage-multi-agent/types"

	// Use internal agent framework for HPKE and crypto
	"github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	// Middleware
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
)

// -------- Public API --------

type PaymentAgent struct {
	RequireSignature bool // true = RFC9421 required, false = allow plaintext (no verify)

	// internals
	logger *log.Logger

	// Framework agent (manages HPKE, keys, DID, etc.)
	agent *agent.Agent

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
	logger := log.New(os.Stdout, "[payment] ", log.LstdFlags)

	// Create framework agent (Eager pattern - HPKE always initialized if keys present)
	fwAgent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, requireSignature)
	if err != nil {
		logger.Printf("[payment] Framework agent init failed: %v (continuing without HPKE)", err)
		fwAgent = nil // graceful degradation
	}

	pa := &PaymentAgent{
		RequireSignature: requireSignature,
		logger:           logger,
		agent:            fwAgent,
	}

	// ===== DID middleware =====
	if pa.RequireSignature {
		mw, err := a2autil.BuildDIDMiddleware(true)
		if err != nil {
			pa.logger.Printf("[payment] DID middleware init failed: %v (running without verify)", err)
			pa.mw = nil
		} else {
			mw.SetErrorHandler(newCompactDIDErrorHandler(pa.logger))
			pa.mw = mw
		}
	} else {
		pa.logger.Printf("[payment] DID middleware disabled (requireSignature=false)")
		pa.mw = nil
	}

	// ===== Open mux: /status =====
	open := http.NewServeMux()
	open.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		hpkeReady := pa.agent != nil && pa.agent.GetHTTPServer() != nil
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "payment",
			"type":         "payment",
			"sage_enabled": pa.RequireSignature,
			"hpke_ready":   hpkeReady,
			"time":         time.Now().Format(time.RFC3339),
		})
	})
	pa.openMux = open

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
			if pa.agent == nil || pa.agent.GetHTTPServer() == nil {
				pa.logger.Printf("[payment] HPKE not available")
				http.Error(w, "hpke disabled", http.StatusBadRequest)
				return
			}

			kid := strings.TrimSpace(r.Header.Get("X-KID"))

			// --- Handshake (no KID) ---
			if kid == "" {
				r.Body = io.NopCloser(bytes.NewReader(body))
				pa.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
				return
			}

			// --- Data mode (has KID) ---
			sess, ok := pa.agent.GetSessionManager().GetUnderlying().GetByKeyID(kid)
			if !ok {
				// Try to treat as handshake if session vanished
				r.Body = io.NopCloser(bytes.NewReader(body))
				pa.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
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

			resp, _ := pa.appHandler(r.Context(), sm)
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
		resp, _ := pa.appHandler(r.Context(), sm)
		if !resp.Success {
			http.Error(w, "application error", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp.Data)
	})
	pa.protMux = protected
	pa.handler = agentmux.BuildAgentHandler("payment", open, protected, pa.mw)
	// ===== Compose final handler =====
	var h http.Handler = open
	if pa.mw != nil {
		wrapped := pa.mw.Wrap(protected)
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
	pa.handler = h

	// HPKE is now eagerly initialized via framework agent (no ensureHPKE needed)

	// [LLM] lazy: only init when used
	if c, err := llm.NewFromEnv(); err == nil {
		pa.llmClient = c
	} else {
		pa.logger.Printf("[payment] LLM disabled: %v", err)
	}

	return pa, nil
}

// Return the handler
func (e *PaymentAgent) Handler() http.Handler { return e.handler }

// Start server
func (e *PaymentAgent) Start(addr string) error {
	if e.handler == nil {
		return fmt.Errorf("handler not initialized")
	}
	e.httpSrv = &http.Server{Addr: addr, Handler: e.handler}
	hpkeReady := e.agent != nil && e.agent.GetHTTPServer() != nil
	e.logger.Printf("[boot] payment on %s (requireSig=%v, hpke_ready=%v)", addr, e.RequireSignature, hpkeReady)
	return e.httpSrv.ListenAndServe()
}

// Shutdown server
func (e *PaymentAgent) Shutdown(ctx context.Context) error {
	if e.httpSrv == nil {
		return nil
	}
	return e.httpSrv.Shutdown(ctx)
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

    // === Generate one-line receipt with LLM (fallback to template on failure) ===
    text := e.generateReceipt(ctx, lang, to, amount, method, item, memo)
    // === end ===

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
