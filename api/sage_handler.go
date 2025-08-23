package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/sage-multi-agent/adapters"
	"github.com/sage-multi-agent/types"
)

// SAGEHandler handles SAGE-related API endpoints
type SAGEHandler struct {
	sageManager    *adapters.SAGEManager
	verifierHelper *adapters.VerifierHelper
}

// NewSAGEHandler creates a new SAGE handler
func NewSAGEHandler(sageManager *adapters.SAGEManager, verifierHelper *adapters.VerifierHelper) *SAGEHandler {
	return &SAGEHandler{
		sageManager:    sageManager,
		verifierHelper: verifierHelper,
	}
}

// HandleSAGEConfig handles GET and POST for SAGE configuration
func (h *SAGEHandler) HandleSAGEConfig(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	
	// Handle preflight
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case "GET":
		h.handleGetConfig(w, r)
	case "POST":
		h.handleSetConfig(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetConfig returns the current SAGE configuration
func (h *SAGEHandler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	status := h.sageManager.GetStatus()
	
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("[SAGE API] Failed to encode status: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// handleSetConfig updates the SAGE configuration
func (h *SAGEHandler) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req types.SAGEConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Update configuration
	if req.Enabled != nil {
		h.sageManager.SetEnabled(*req.Enabled)
		log.Printf("[SAGE API] SAGE enabled set to: %v", *req.Enabled)
	}

	if req.SkipOnError != nil {
		h.sageManager.GetVerifier().SetSkipOnError(*req.SkipOnError)
		log.Printf("[SAGE API] Skip on error set to: %v", *req.SkipOnError)
	}

	// Return updated status
	status := h.sageManager.GetStatus()
	
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("[SAGE API] Failed to encode status: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// HandleSAGETest handles SAGE signing and verification test
func (h *SAGEHandler) HandleSAGETest(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	
	// Handle preflight
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request for agent type
	var req struct {
		AgentType string `json:"agentType"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to root agent if not specified
		req.AgentType = "root"
	}

	// Perform test
	result, err := h.sageManager.SignAndVerifyTest(r.Context(), req.AgentType, h.verifierHelper)
	if err != nil {
		log.Printf("[SAGE API] Test failed: %v", err)
		http.Error(w, "Test failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("[SAGE API] Failed to encode result: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// RegisterRoutes registers all SAGE-related routes
func (h *SAGEHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/sage/config", h.HandleSAGEConfig)
	mux.HandleFunc("/api/sage/test", h.HandleSAGETest)
	log.Println("[SAGE API] Routes registered: /api/sage/config, /api/sage/test")
}