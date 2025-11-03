// package medical provides a reusable MedicalAgent with the same security model
// as payment: RFC9421 DID signature verification middleware + HPKE (handshake/data)
// + plain JSON fallback when HPKE is off.
// Endpoints: /status, /process (identical behavior/headers to payment).
// Uses internal agent framework for crypto, HPKE, and DID resolution (Eager pattern).
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
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/internal/agentmux"
	"github.com/sage-x-project/sage-multi-agent/types"

	// Use sage-a2a-go v1.7.0 Agent Framework for HPKE and crypto
	framework "github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	// Middleware
	"github.com/sage-x-project/sage-a2a-go/pkg/server"

	// [LLM] shim
	"github.com/sage-x-project/sage-multi-agent/llm"
)

// -------- Public API --------

type MedicalAgent struct {
	RequireSignature bool // true = RFC9421 required, false = allow plaintext (no verify)

	// internals
	logger *log.Logger

	// Framework agent (manages HPKE, keys, DID, etc.)
	agent *framework.Agent

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
	logger := log.New(os.Stdout, "[medical] ", log.LstdFlags)

	// Create framework agent (Eager pattern - HPKE always initialized if keys present)
	fwAgent, err := framework.NewAgentFromEnv("medical", "MEDICAL", true, requireSignature)
	if err != nil {
		logger.Printf("[medical] Framework agent init failed: %v (continuing without HPKE)", err)
		fwAgent = nil // graceful degradation
	}

	ma := &MedicalAgent{
		RequireSignature: requireSignature,
		logger:           logger,
		agent:            fwAgent,
	}

	// ===== DID middleware =====
	if ma.RequireSignature {
		mw, err := a2autil.BuildDIDMiddleware(true)
		if err != nil {
			ma.logger.Printf("[medical] DID middleware init failed: %v (running without verify)", err)
			ma.mw = nil
		} else {
			mw.SetErrorHandler(newCompactDIDErrorHandler(ma.logger))
			ma.mw = mw
		}
	} else {
		ma.logger.Printf("[medical] DID middleware disabled (requireSignature=false)")
		ma.mw = nil
	}

	// ===== Open mux: /status =====
	open := http.NewServeMux()
	open.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		hpkeReady := ma.agent != nil && ma.agent.GetHTTPServer() != nil
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "medical",
			"type":         "medical",
			"sage_enabled": ma.RequireSignature,
			"hpke_ready":   hpkeReady,
			"time":         time.Now().Format(time.RFC3339),
		})
	})
	ma.openMux = open

	// ===== Protected mux: /process =====
	protected := http.NewServeMux()
	protected.HandleFunc("/medical/process", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		// Rehydrate minimal SecureMessage context from headers (data-mode)
		did := strings.TrimSpace(r.Header.Get("X-SAGE-DID"))
		mid := strings.TrimSpace(r.Header.Get("X-SAGE-Message-ID"))
		ctxID := strings.TrimSpace(r.Header.Get("X-SAGE-Context-ID"))
		taskID := strings.TrimSpace(r.Header.Get("X-SAGE-Task-ID"))

		// HPKE path?
		if isHPKE(r) {
			if ma.agent == nil || ma.agent.GetHTTPServer() == nil {
				ma.logger.Printf("[medical] HPKE not available")
				http.Error(w, "hpke disabled", http.StatusBadRequest)
				return
			}

			kid := strings.TrimSpace(r.Header.Get("X-KID"))

			// --- Handshake (no KID) ---
			if kid == "" {
				r.Body = io.NopCloser(bytes.NewReader(body))
				ma.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
				return
			}

			// --- Data mode (has KID) ---
			sess, ok := ma.agent.GetSessionManager().GetUnderlying().GetByKeyID(kid)
			if !ok {
				// Try to treat as handshake if session vanished
				r.Body = io.NopCloser(bytes.NewReader(body))
				ma.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
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

			resp, _ := ma.appHandler(r.Context(), sm)
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
		resp, _ := ma.appHandler(r.Context(), sm)
		if !resp.Success {
			http.Error(w, "application error", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp.Data)
	})
	ma.protMux = protected
	ma.handler = agentmux.BuildAgentHandler("medical", open, protected, ma.mw)
	// ===== Compose final handler =====
	var h http.Handler = open
	if ma.mw != nil {
		wrapped := ma.mw.Wrap(protected)
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
		root.Handle("/medical/", protected)  // prefix match to handle /medical/process
		h = root
	}
	ma.handler = h

	// HPKE is now eagerly initialized via framework agent (no ensureHPKE needed)

	// [LLM] lazy: only init when used
	if c, err := llm.NewFromEnv(); err == nil {
		ma.llmClient = c
		ma.logger.Printf("[medical] LLM ready")
	} else {
		ma.logger.Printf("[medical] LLM disabled: %v", err)
	}

	return ma, nil
}

// Return the handler
func (e *MedicalAgent) Handler() http.Handler { return e.handler }

// Start server
func (e *MedicalAgent) Start(addr string) error {
	if e.handler == nil {
		return fmt.Errorf("handler not initialized")
	}
	e.httpSrv = &http.Server{Addr: addr, Handler: e.handler}
	hpkeReady := e.agent != nil && e.agent.GetHTTPServer() != nil
	e.logger.Printf("[boot] medical on %s (requireSig=%v, hpke_ready=%v)", addr, e.RequireSignature, hpkeReady)
	return e.httpSrv.ListenAndServe()
}

// Shutdown server
func (e *MedicalAgent) Shutdown(ctx context.Context) error {
	if e.httpSrv == nil {
		return nil
	}
	return e.httpSrv.Shutdown(ctx)
}

// -------- Application handler (LLM-driven medical info with history) --------
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

	// ===== Metadata collection (prefer fields set by Root) =====
	lang := getMetaString(in.Metadata, "lang")
	if lang == "" {
		lang = llm.DetectLang(in.Content)
	}
	if lang != "ko" && lang != "en" {
		lang = "ko"
	}

	condition := getMetaString(in.Metadata, "medical.condition", "condition")
	topic := getMetaString(in.Metadata, "medical.topic", "topic")
	symptoms := getMetaString(in.Metadata, "medical.symptoms", "symptoms")
	duration := getMetaString(in.Metadata, "medical.duration", "duration")
	meds := getMetaString(in.Metadata, "medical.meds", "medications")
	age := getMetaInt64(in.Metadata, "medical.age", "age")
	sex := getMetaString(in.Metadata, "medical.sex", "sex")
	severity := getMetaString(in.Metadata, "medical.severity", "severity")
	allergy := getMetaString(in.Metadata, "medical.allergies", "allergies")
	conds := getMetaString(in.Metadata, "medical.conditions", "conditions")

	initialQ := getMetaString(in.Metadata, "medical.initial_question", "initial_question")
	lastMsg := getMetaString(in.Metadata, "medical.last_message", "last_message")
	history := getMetaStringSlice(in.Metadata, "medical.history", "history", "medical.transcript", "transcript")
	histN := getMetaInt64(in.Metadata, "medical.history_len", "history_len")
	if histN > 0 && len(history) > int(histN) {
		history = history[len(history)-int(histN):]
	}

	// User question body (fallback to lastMsg/symptoms if missing)
	query := strings.TrimSpace(in.Content)
	if query == "" {
		if lastMsg != "" {
			query = lastMsg
		} else if symptoms != "" {
			query = fmt.Sprintf("증상: %s", symptoms)
		}
	}

	// ===== Build LLM prompt =====
	sys := map[string]string{
		"ko": "너는 의료 정보 도우미야. 진단/처방 없이, 안전하고 일반적인 의학 정보를 한 문장으로만 제공해. 응급 징후가 의심되면 전문의 진료를 권유해. 목록/코드블록/장황한 설명 금지.",
		"en": "You are a medical info assistant. Provide ONE short, safe, general informational sentence. No diagnosis/prescription. If red flags are possible, suggest seeing a professional. No lists or code blocks.",
	}[lang]

	var sb strings.Builder
	// Summary context block (LLM-friendly format)
	if query != "" {
		fmt.Fprintf(&sb, "UserQuestion: %s\n", query)
	}
	if condition != "" {
		fmt.Fprintf(&sb, "Condition: %s\n", condition)
	}
	if topic != "" {
		fmt.Fprintf(&sb, "Topic: %s\n", topic)
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
		fmt.Fprintf(&sb, "OtherConditions: %s\n", conds)
	}
	if initialQ != "" {
		fmt.Fprintf(&sb, "InitialQuestion: %s\n", initialQ)
	}
	if len(history) > 0 {
		fmt.Fprintf(&sb, "History(last %d):\n", len(history))
		for _, line := range history {
			fmt.Fprintf(&sb, "- %s\n", strings.TrimSpace(line))
		}
	}
	if lastMsg != "" {
		fmt.Fprintf(&sb, "LastUserMessage: %s\n", lastMsg)
	}
	// Enforce output format
	if lang == "ko" {
		fmt.Fprint(&sb, "Output: 한 문장 한국어 답변만.\n")
	} else {
		fmt.Fprint(&sb, "Output: ONE-sentence answer only.\n")
	}
	usr := sb.String()

	// ===== LLM call (fallback on failure) =====
	text := ""
	if e.llmClient != nil {
		out, err := e.llmClient.Chat(ctx, sys, usr)
		e.logger.Println("[medical][llm]", usr, out)
		if err != nil {
			e.logger.Printf("[medical][llm] chat error: %v", err)
		} else if trimmed := strings.TrimSpace(out); trimmed != "" {
			e.logger.Printf("[medical][llm] chat ok bytes=%d", len(trimmed))
			text = trimmed
		} else {
			e.logger.Printf("[medical][llm] chat returned empty text")
		}
	} else {
		e.logger.Printf("[medical][llm] client not initialized (using fallback)")
	}

	if text == "" {
		if lang == "en" {
			text = "This is general health information, not a diagnosis. If symptoms are severe or worsening (e.g., chest pain, trouble breathing, confusion), seek emergency care; otherwise rest, hydrate, monitor, and see a clinician if symptoms persist."
		} else {
			text = "이 답변은 일반 건강 정보이며 진단이 아닙니다. 흉통·호흡곤란·의식 변화 등 심한 증상은 즉시 응급실을 방문하고, 경미하면 휴식·수분섭취·경과 관찰 후 지속 시 의료진과 상담하세요."
		}
	}

	// ===== Response message =====
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
				"condition":        condition,
				"topic":            topic,
				"symptoms":         symptoms,
				"age":              age,
				"sex":              sex,
				"duration":         duration,
				"severity":         severity,
				"meds":             meds,
				"allergies":        allergy,
				"other_conditions": conds,
				"initial_question": initialQ,
				"history":          history,
				"history_len":      len(history),
				"last_message":     lastMsg,
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

// Reuse if already exists; add if not
func getMetaString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
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

func getMetaStringSlice(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case []string:
				out := make([]string, 0, len(t))
				for _, s := range t {
					if s2 := strings.TrimSpace(s); s2 != "" {
						out = append(out, s2)
					}
				}
				return out
			case []any:
				out := make([]string, 0, len(t))
				for _, e := range t {
					if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
						out = append(out, strings.TrimSpace(s))
					}
				}
				return out
			case string:
				s := strings.TrimSpace(t)
				if s == "" {
					return nil
				}
				seps := func(r rune) bool { return r == '\n' || r == ',' }
				parts := strings.FieldsFunc(s, seps)
				out := make([]string, 0, len(parts))
				for _, p := range parts {
					if q := strings.TrimSpace(p); q != "" {
						out = append(out, q)
					}
				}
				return out
			}
		}
	}
	return nil
}
