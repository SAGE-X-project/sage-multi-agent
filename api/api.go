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
// It may sign the request (client->root) via A2A. Routing is done by Root.
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
// - Header X-SAGE-Enabled: true|false (propagated to Root /toggle-sage; controls ONLY sub-agents' outbound signing)
// - Body: {"prompt": "..."}; if JSON decode fails, treat body as plain text prompt
func (g *ClientAPI) HandleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sageEnabled := strings.EqualFold(r.Header.Get("X-SAGE-Enabled"), "true")
	scenario := r.Header.Get("X-Scenario")

	// Read raw body once; we may need it for tolerant parsing and for replays.
	rawIn, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	var prompt string
	if len(rawIn) > 0 {
		// Try JSON first
		var reqIn types.PromptRequest
		if err := json.Unmarshal(rawIn, &reqIn); err == nil && reqIn.Prompt != "" {
			prompt = reqIn.Prompt
		} else {
			// Fallback: accept plain text payload as prompt
			prompt = strings.TrimSpace(string(rawIn))
		}
	}

	// Toggle Root flag (controls ONLY sub-agents' outbound signing to external).
	_ = g.toggleSAGE(r.Context(), g.rootBase+"/toggle-sage", sageEnabled)

	// Build AgentMessage for Root. No domain -> Root routes.
	msg := types.AgentMessage{
		ID:        "api-" + time.Now().Format("20060102T150405.000000000"),
		From:      "client-api",
		To:        "root",
		Content:   prompt,
		Timestamp: time.Now(),
		Type:      "request",
		Metadata:  map[string]any{"scenario": scenario},
	}
	body, _ := json.Marshal(msg)

	// Provide GetBody so any signer/middleware can safely re-read the payload.
	reqOut, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, g.rootBase+"/process", bytes.NewReader(body))
	reqOut.Header.Set("Content-Type", "application/json")
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
