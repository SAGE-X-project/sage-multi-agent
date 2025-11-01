package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// ClientAPI is a thin HTTP facade for frontend -> Root.
// Frontend sends a single request per user action:
//   - POST /api/request
//   - Headers:
//     X-SAGE-Enabled: true|false  (per-request A2A signature toggle)
//     X-HPKE-Enabled: true|false  (per-request HPKE toggle; SAGE=false forces HPKE=false)
//   - Body: {"prompt": "..."}
//
// ClientAPI forwards the prompt to Root and passes SAGE/HPKE flags via headers only (no body metadata).
// Root does in‑proc routing to sub‑agents (planning/medical/payment).
// NOTE: For backward compatibility, this API also hits Root /toggle-sage to reflect the header
//
//	into the legacy global toggle; per‑request behavior is still driven by message metadata.
type ClientAPI struct {
	rootBase    string
	paymentBase string // legacy; unused
	httpClient  *http.Client
	a2aClient   *a2aclient.A2AClient
}

func NewClientAPI(rootBase, paymentBase string, httpClient *http.Client) *ClientAPI {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ClientAPI{
		rootBase:    strings.TrimRight(rootBase, "/"),
		paymentBase: strings.TrimRight(paymentBase, "/"),
		httpClient:  httpClient,
	}
}

func NewClientAPIWithA2A(rootBase, paymentBase string, httpClient *http.Client, a2a *a2aclient.A2AClient) *ClientAPI {
	api := NewClientAPI(rootBase, paymentBase, httpClient)
	api.a2aClient = a2a
	return api
}

// Single endpoint: /api/request
// - Headers from frontend (see file header)
// - Body: {"prompt": "..."}; if JSON decode fails, treat body as plain text prompt
// - Response: PromptResponse { response, sageVerification, metadata, logs? }
func (g *ClientAPI) HandleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-request security toggles from frontend
	sageEnabled := strings.EqualFold(r.Header.Get("X-SAGE-Enabled"), "true")
	hpkeRaw := r.Header.Get("X-HPKE-Enabled")
	hpkeEnabled := strings.EqualFold(hpkeRaw, "true")
	scenario := r.Header.Get("X-Scenario")

	// Read raw body once
	rawIn, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	var prompt string
	if len(rawIn) > 0 {
		var reqIn types.PromptRequest
		if err := json.Unmarshal(rawIn, &reqIn); err == nil && strings.TrimSpace(reqIn.Prompt) != "" {
			prompt = reqIn.Prompt
		} else {
			prompt = strings.TrimSpace(string(rawIn))
		}
	}

	// Reject invalid combo only if HPKE header explicitly set
	if hpkeRaw != "" && hpkeEnabled && !sageEnabled {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "bad_request",
			"message": "HPKE requires SAGE to be enabled (X-SAGE-Enabled: true)",
		})
		return
	}

	// Legacy global switch (optional)
	_ = g.toggleSAGE(r.Context(), g.rootBase+"/toggle-sage", sageEnabled)

	// Build AgentMessage → Root
	meta := map[string]any{
		"scenario":    scenario,
		"sageEnabled": sageEnabled,
	}
	if hpkeRaw != "" {
		meta["hpkeEnabled"] = hpkeEnabled
	}

	msg := types.AgentMessage{
		ID:        "api-" + time.Now().Format("20060102T150405.000000000"),
		From:      "client-api",
		To:        "root",
		Content:   prompt,
		Timestamp: time.Now(),
		Type:      "request",
		Metadata:  meta,
	}
	body, _ := json.Marshal(msg)

	// Proxy request to Root (/process)
	reqOut, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, g.rootBase+"/process", bytes.NewReader(body))
	reqOut.Header.Set("Content-Type", "application/json")

	// **중요: per-request 토글 헤더를 Root로 그대로 전달**
	if sageEnabled {
		reqOut.Header.Set("X-SAGE-Enabled", "true")
	} else {
		reqOut.Header.Set("X-SAGE-Enabled", "false")
	}
	if hpkeRaw != "" {
		// 명시된 경우에만 전달 (미명시 시 서버 기본/세션 유지)
		if hpkeEnabled {
			reqOut.Header.Set("X-HPKE-Enabled", "true")
		} else {
			reqOut.Header.Set("X-HPKE-Enabled", "false")
		}
	}
	if scenario != "" {
		reqOut.Header.Set("X-Scenario", scenario)
	}

	// Rewindable body (사인/미들웨어 대비)
	reqOut.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	var resp *http.Response
	var err error
	if sageEnabled && g.a2aClient != nil {
		resp, err = g.a2aClient.Do(r.Context(), reqOut)
	} else {
		resp, err = g.httpClient.Do(reqOut)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var agentResp types.AgentMessage
	rawBody, _ := io.ReadAll(resp.Body)
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &agentResp); err != nil {
			agentResp = types.AgentMessage{
				From:    "root",
				To:      "client-api",
				Type:    "response",
				Content: strings.TrimSpace(string(rawBody)),
			}
		}
	}

	verification := &types.SAGEVerificationResult{
		Verified:       sageEnabled && resp.StatusCode/100 == 2,
		SignatureValid: sageEnabled && resp.StatusCode/100 == 2,
		Timestamp:      time.Now().Unix(),
		Details:        map[string]string{"scenario": scenario},
	}

	out := types.PromptResponse{
		Response:         agentResp.Content,
		Logs:             nil,
		SAGEVerification: verification,
		Metadata: &types.ResponseMetadata{
			RequestID:      msg.ID,
			ProcessingTime: 0,
			AgentPath:      []string{"client-api", "root"},
			Timestamp:      time.Now().Format(time.RFC3339),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_ = json.NewEncoder(w).Encode(out)
}

func (g *ClientAPI) toggleSAGE(ctx context.Context, url string, enabled bool) error {
	body, _ := json.Marshal(map[string]bool{"enabled": enabled})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	if g.a2aClient != nil {
		resp, err := g.a2aClient.Do(ctx, req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}
