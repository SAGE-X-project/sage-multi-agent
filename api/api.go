package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// ClientAPI is a thin HTTP facade for frontend → Root.
// It forwards requests and (optionally) signs via A2A when SAGE is enabled.
type ClientAPI struct {
	rootBase    string
	paymentBase string // kept for legacy, not used in internal-call mode
	httpClient  *http.Client
	a2aClient   *a2aclient.A2AClient // used for signed requests
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

// Public handlers (same flow, different domain tag)
func (g *ClientAPI) HandlePrompt(w http.ResponseWriter, r *http.Request) {
	g.handleDomain(w, r, "prompt")
}
func (g *ClientAPI) HandlePayment(w http.ResponseWriter, r *http.Request) {
	g.handleDomain(w, r, "payment")
}

func (g *ClientAPI) handleDomain(w http.ResponseWriter, r *http.Request, domain string) {
	// Frontend controls SAGE ON/OFF
	sageEnabled := strings.EqualFold(r.Header.Get("X-SAGE-Enabled"), "true")
	scenario := r.Header.Get("X-Scenario")

	var reqIn types.PromptRequest
	_ = json.NewDecoder(r.Body).Decode(&reqIn)
	_ = r.Body.Close()

	// Toggle only Root (Payment is in-process)
	_ = g.toggleSAGE(r.Context(), g.rootBase+"/toggle-sage", sageEnabled)

	// Build message for Root
	msg := types.AgentMessage{
		ID:        fmt.Sprintf("api-%d", time.Now().UnixNano()),
		From:      "client-api",
		To:        "root",
		Content:   reqIn.Prompt,
		Timestamp: time.Now(),
		Type:      "request",
		Metadata:  map[string]any{"scenario": scenario, "domain": domain},
	}
	body, _ := json.Marshal(msg)

	// Forward to Root /process (signed if SAGE ON)
	reqOut, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, g.rootBase+"/process", bytes.NewReader(body))
	reqOut.Header.Set("Content-Type", "application/json")

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
				ID:        "",
				From:      "root",
				To:        "client-api",
				Content:   strings.TrimSpace(string(rawBody)),
				Timestamp: time.Now(),
				Type:      "response",
			}
		}
	}

	now := time.Now().Format(time.RFC3339)
	logs := []types.AgentLog{
		{Type: "client-api", From: "client-api", Content: "Received request", Timestamp: now, OriginalPrompt: reqIn.Prompt},
		{Type: "client-api", From: "client-api", To: "root", Content: "Forwarded to root /process", Timestamp: now},
		{Type: "root", From: "root", To: "payment", Content: "Relayed to payment (in-process)", Timestamp: now},
		{Type: "payment", From: "payment", Content: "Processed external call", Timestamp: now},
	}
	verification := &types.SAGEVerificationResult{
		Verified:       sageEnabled && resp.StatusCode/100 == 2,
		SignatureValid: sageEnabled && resp.StatusCode/100 == 2,
		Timestamp:      time.Now().Unix(),
		Details:        map[string]string{"scenario": scenario, "domain": domain},
	}

	out := types.PromptResponse{
		Response:         agentResp.Content,
		Logs:             logs,
		SAGEVerification: verification,
		Metadata: &types.ResponseMetadata{
			RequestID:      msg.ID,
			ProcessingTime: 0,
			AgentPath:      []string{"client-api", "root", "payment", "external"},
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
