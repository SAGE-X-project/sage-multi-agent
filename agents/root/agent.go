package root

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/sage-x-project/sage-multi-agent/adapters"
	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage-multi-agent/websocket"
)

// RootAgent is the main routing agent that directs requests to appropriate specialized agents
type RootAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool
	agents      map[string]*AgentInfo
	hub         *websocket.Hub
	sageManager *adapters.FlexibleSAGEManager // Changed to FlexibleSAGEManager
	mu          sync.RWMutex
}

// AgentInfo stores information about registered agents
type AgentInfo struct {
	Name     string
	Endpoint string
	Type     string // planning, ordering, payment
	Active   bool
}

// NewRootAgent creates a new root agent instance
func NewRootAgent(name string, port int) *RootAgent {
	return &RootAgent{
		Name:        name,
		Port:        port,
		SAGEEnabled: true,
		agents:      make(map[string]*AgentInfo),
		hub:         websocket.NewHub(),
	}
}

// RegisterAgent registers a specialized agent
func (ra *RootAgent) RegisterAgent(agentType, name, endpoint string) {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	ra.agents[agentType] = &AgentInfo{
		Name:     name,
		Endpoint: endpoint,
		Type:     agentType,
		Active:   true,
	}

	log.Printf("Registered agent: %s (%s) at %s", name, agentType, endpoint)
}

// RouteRequest routes incoming requests to appropriate agents
func (ra *RootAgent) RouteRequest(ctx context.Context, request *types.AgentMessage) (*types.AgentMessage, error) {
	ra.mu.RLock()
	defer ra.mu.RUnlock()

	// Determine which agent should handle the request
	agentType := ra.determineAgentType(request.Content)

	agent, exists := ra.agents[agentType]
	if !exists || !agent.Active {
		return nil, fmt.Errorf("no active agent available for type: %s", agentType)
	}

	log.Printf("Routing request to %s agent: %s", agentType, agent.Name)

	// If SAGE is enabled, process message with SAGE
	if ra.SAGEEnabled && ra.sageManager != nil {
		processedMessage, err := ra.sageManager.ProcessMessageWithSAGE(request)
		if err != nil {
			log.Printf("Failed to process message with SAGE: %v", err)
			// Continue without SAGE features for non-DID entities
		} else {
			request = processedMessage
		}
	}

	// Forward request to the appropriate agent
	response, err := ra.forwardToAgent(ctx, agent, request)
	if err != nil {
		return nil, fmt.Errorf("failed to forward to agent: %v", err)
	}

	// If SAGE is enabled, verify the response (flexible mode allows non-DID responses)
	if ra.SAGEEnabled && ra.sageManager != nil {
		valid, err := ra.sageManager.VerifyMessage(response)
		if err != nil {
			log.Printf("Response verification error: %v", err)
			// Continue if it's a non-DID entity
		} else if !valid {
			log.Printf("Response verification failed - invalid signature")
			return nil, fmt.Errorf("response verification failed")
		}
	}

	return response, nil
}

// determineAgentType analyzes the request content to determine which agent should handle it
func (ra *RootAgent) determineAgentType(content string) string {
	contentLower := strings.ToLower(content)

	// Keywords for Planning Agent
	planningKeywords := []string{"hotel", "travel", "plan", "trip", "accommodation", "reserve", "booking"}
	for _, keyword := range planningKeywords {
		if strings.Contains(contentLower, keyword) {
			return "planning"
		}
	}

	// Keywords for Ordering Agent
	orderingKeywords := []string{"order", "buy", "purchase", "shop", "product", "item", "cart"}
	for _, keyword := range orderingKeywords {
		if strings.Contains(contentLower, keyword) {
			return "ordering"
		}
	}

	// Keywords for Payment Agent
	paymentKeywords := []string{"pay", "payment", "transfer", "coin", "crypto", "wallet", "send"}
	for _, keyword := range paymentKeywords {
		if strings.Contains(contentLower, keyword) {
			return "payment"
		}
	}

	// Default to planning agent
	return "planning"
}

// forwardToAgent forwards a request to a specific agent
func (ra *RootAgent) forwardToAgent(ctx context.Context, agent *AgentInfo, request *types.AgentMessage) (*types.AgentMessage, error) {
	// Create HTTP client
	client := &http.Client{}

	// Marshal request
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", agent.Endpoint+"/process", strings.NewReader(string(requestBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	var response types.AgentMessage
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

// ToggleSAGE enables or disables SAGE protocol
func (ra *RootAgent) ToggleSAGE(enabled bool) {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	ra.SAGEEnabled = enabled
	log.Printf("SAGE protocol %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])

	// Notify all connected clients about SAGE status change
	if ra.hub != nil {
		status := map[string]interface{}{
			"type":    "sage_status",
			"enabled": enabled,
		}
		statusJSON, _ := json.Marshal(status)
		ra.hub.Broadcast(statusJSON)
	}
}

// Start starts the root agent server
func (ra *RootAgent) Start() error {
	// Initialize SAGE manager if enabled
	if ra.SAGEEnabled {
		sageManager, err := adapters.NewSAGEManagerWithKeyType(ra.Name, "secp256k1")
		if err != nil {
			log.Printf("Failed to initialize SAGE manager: %v", err)
			// Continue without SAGE
		} else {
			// Wrap with FlexibleSAGEManager to allow non-DID entities
			ra.sageManager = adapters.NewFlexibleSAGEManager(sageManager)
			ra.sageManager.SetAllowNonDID(true) // Allow non-DID messages by default
			log.Printf("Flexible SAGE manager initialized for %s (non-DID messages allowed)", ra.Name)
		}
	}

	// Start WebSocket hub
	go ra.hub.Run()

	// Create a new ServeMux for this agent
	mux := http.NewServeMux()
	mux.HandleFunc("/process", ra.handleProcessRequest)
	mux.HandleFunc("/status", ra.handleStatus)
	mux.HandleFunc("/toggle-sage", ra.handleToggleSAGE)
	mux.HandleFunc("/ws", ra.handleWebSocket)

	// Register default agents
	ra.RegisterAgent("planning", "PlanningAgent", "http://localhost:8081")
	ra.RegisterAgent("ordering", "OrderingAgent", "http://localhost:8082")
	ra.RegisterAgent("payment", "PaymentAgent", "http://localhost:8083")

	log.Printf("Root Agent starting on port %d", ra.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", ra.Port), mux)
}

// handleProcessRequest handles incoming process requests
func (ra *RootAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request types.AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response, err := ra.RouteRequest(r.Context(), &request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleStatus returns the agent status
func (ra *RootAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	ra.mu.RLock()
	defer ra.mu.RUnlock()

	status := map[string]interface{}{
		"name":         ra.Name,
		"sage_enabled": ra.SAGEEnabled,
		"agents":       ra.agents,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleToggleSAGE handles SAGE toggle requests
func (ra *RootAgent) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	ra.ToggleSAGE(req.Enabled)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"enabled": req.Enabled})
}

// handleWebSocket handles WebSocket connections
func (ra *RootAgent) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// WebSocket handler would go here
	// For now, just return a placeholder
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte("WebSocket support coming soon"))
}