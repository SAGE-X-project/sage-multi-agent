package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// SAGEHandler exposes minimal GET/POST endpoints to view or flip a local flag.
// NOTE: Actual HTTP DID verification is enforced by middleware on each agent,
// and request signing is handled by the caller (ClientAPI) via A2A client.
type SAGEHandler struct {
	enabled bool
}

// NewSAGEHandler returns a new handler (enabled by default).
func NewSAGEHandler() *SAGEHandler {
	return &SAGEHandler{enabled: true}
}

// HandleSAGEConfig supports:
//   - GET: returns current local flag
//   - POST: sets local flag
func (h *SAGEHandler) HandleSAGEConfig(w http.ResponseWriter, r *http.Request) {
	// Minimal CORS for browser testing
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetConfig(w, r)
	case http.MethodPost:
		h.handleSetConfig(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *SAGEHandler) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	status := &types.SAGEStatus{
		Enabled:         h.enabled,
		VerifierEnabled: h.enabled,
		AgentSigners:    map[string]bool{},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (h *SAGEHandler) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req types.SAGEConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Enabled != nil {
		h.enabled = *req.Enabled
		log.Printf("[SAGE API] set enabled=%v", h.enabled)
	}
	// Return current state
	h.handleGetConfig(w, r)
}

// HandleSAGETest just echoes success since signing is request-level now.
func (h *SAGEHandler) HandleSAGETest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	res := &types.SAGETestResult{
		Success: true,
		Stage:   "middleware",
		Details: map[string]string{"note": "DID auth is enforced by HTTP middleware"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// RegisterRoutes registers all SAGE-related endpoints for simple UI/testing.
func (h *SAGEHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/sage/config", h.HandleSAGEConfig)
	mux.HandleFunc("/api/sage/test", h.HandleSAGETest)
	log.Println("[SAGE API] Routes registered: /api/sage/config, /api/sage/test")
}
