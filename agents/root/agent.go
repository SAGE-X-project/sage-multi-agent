package root

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	a2aserver "github.com/sage-x-project/sage-a2a-go/pkg/server" // DID 미들웨어 타입
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// RootAgent routes client requests to domain agents (payment/planning/ordering).
type RootAgent struct {
	name   string
	port   int
	mux    *http.ServeMux
	server *http.Server

	// domain -> baseURL (e.g., "payment" -> "http://localhost:18083")
	agents map[string]string

	// optional inbound DID middleware (client -> root)
	didMw *a2aserver.DIDAuthMiddleware

	httpClient *http.Client
	logger     *log.Logger

	paymentInproc *payment.PaymentAgent
}

// NewRootAgent constructs a root agent listening on :port.
func NewRootAgent(name string, port int) *RootAgent {
	mux := http.NewServeMux()
	ra := &RootAgent{
		name:       name,
		port:       port,
		mux:        mux,
		agents:     make(map[string]string),
		httpClient: http.DefaultClient,
		logger:     log.Default(),
	}
	ra.mountRoutes()
	return ra
}

// SetDIDAuthMiddleware installs inbound DID verification (optional).
func (r *RootAgent) SetDIDAuthMiddleware(mw *a2aserver.DIDAuthMiddleware) {
	r.didMw = mw
	// 재마운트 필요 없음. /process 핸들러에서 wrap 여부를 체크함.
}

func (r *RootAgent) SetPaymentInproc(p *payment.PaymentAgent) {
	r.paymentInproc = p
}

// RegisterAgent registers a downstream agent endpoint for a domain.
//
//	role: "payment" | "planning" | "ordering" (자유 문자열)
//	name: 사람이 읽기 쉬운 이름 (현재는 로그용)
//	baseURL: "http://host:port"
func (r *RootAgent) RegisterAgent(role, name, baseURL string) {
	r.agents[role] = trimRightSlash(baseURL)
	r.logger.Printf("[root] registered agent: role=%s name=%s url=%s", role, name, r.agents[role])
}

// Start begins HTTP server.
func (r *RootAgent) Start() error {
	addr := fmt.Sprintf(":%d", r.port)
	r.server = &http.Server{
		Addr:    addr,
		Handler: r.mux,
	}
	r.logger.Printf("[root] starting on %s", addr)
	return r.server.ListenAndServe()
}

// --- routes ---

func (r *RootAgent) mountRoutes() {
	// health
	r.mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"name":         r.name,
			"type":         "root",
			"port":         r.port,
			"agents":       r.agents,
			"sage_enabled": r.didMw != nil, // 미들웨어 유무만 노출(엄밀한 의미의 on/off는 환경에 맞게 바꾸세요)
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// toggle inbound verification strictness (demo)
	// body: {"enabled": true}  => require signatures (optional=false)
	r.mux.HandleFunc("/toggle-sage", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.didMw == nil {
			http.Error(w, "didauth middleware not installed", http.StatusNotImplemented)
			return
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		// enabled=true => 서명 필수(optional=false)
		r.didMw.SetOptional(!in.Enabled)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "enabled": in.Enabled})
	})

	// main processing endpoint (client -> root)
	// 요청 본문은 types.AgentMessage
	// main processing endpoint (client -> root)
	// 요청 본문은 types.AgentMessage
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var msg types.AgentMessage
		if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		role := detectDomain(&msg) // metadata.domain 우선, 없으면 content 힌트

		// ✅ [최소수정] payment는 HTTP로 넘기지 말고 in-proc으로 처리
		if role == "payment" && r.paymentInproc != nil {
			out, err := r.paymentInproc.Process(req.Context(), msg)
			if err != nil {
				http.Error(w, "payment in-proc error: "+err.Error(), http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			// 내부 에이전트가 error 타입 리턴시 적절히 상태코드 조정(원하면 유지/삭제 가능)
			if out.Type == "error" {
				w.WriteHeader(http.StatusBadGateway)
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}

		// ⬇️ 기존 로직: payment 이외 도메인은 원래처럼 HTTP 포워드
		base := r.agents[role]
		if base == "" {
			http.Error(w, fmt.Sprintf("no agent registered for domain '%s'", role), http.StatusBadGateway)
			return
		}

		out, code, err := r.forward(req.Context(), base+"/process", &msg)
		if err != nil {
			http.Error(w, fmt.Sprintf("forward error: %v", err), code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(out)
	})

	// DID 미들웨어가 있으면 감싸고, 없으면 그대로
	if r.didMw != nil {
		r.mux.Handle("/process", r.didMw.Wrap(handler))
	} else {
		r.mux.Handle("/process", handler)
	}
}

// forward posts AgentMessage to target /process and returns AgentMessage back.
func (r *RootAgent) forward(ctx context.Context, url string, msg *types.AgentMessage) (types.AgentMessage, int, error) {
	b, _ := json.Marshal(msg)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	// (선택) Content-Digest를 넣고 싶다면 여기서 계산해도 됩니다.
	// req.Header.Set("Content-Digest", a2autil.ComputeContentDigest(b))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return types.AgentMessage{}, http.StatusBadGateway, err
	}
	defer resp.Body.Close()

	var out types.AgentMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		// 다운스트림이 텍스트만 줬다면 감싸서 반환
		return types.AgentMessage{
			ID:        msg.ID + "-raw",
			From:      "root",
			To:        msg.From,
			Type:      "response",
			Content:   fmt.Sprintf("downstream status=%d (unparsed body)", resp.StatusCode),
			Timestamp: time.Now(),
		}, resp.StatusCode, nil
	}

	return out, resp.StatusCode, nil
}

// --- helpers ---

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func detectDomain(msg *types.AgentMessage) string {
	// 1) metadata.domain 우선
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["domain"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				return s
			}
		}
	}
	// 2) content 힌트 (아주 단순한 데모용)
	c := msg.Content
	if containsAnyFold(c, "pay", "payment", "송금", "결제") {
		return "payment"
	}
	if containsAnyFold(c, "order", "주문") {
		return "ordering"
	}
	if containsAnyFold(c, "plan", "planning", "계획") {
		return "planning"
	}
	// default
	return "planning"
}

func containsAnyFold(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		// case-insensitive
		if bytes.Contains(bytes.ToLower([]byte(s)), bytes.ToLower([]byte(n))) {
			return true
		}
	}
	return false
}
