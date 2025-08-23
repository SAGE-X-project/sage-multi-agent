package websocket

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sage-multi-agent/types"
)

// EnhancedLogServer manages WebSocket connections with production features
type EnhancedLogServer struct {
	hub           *Hub
	port          int
	server        *http.Server
	logBuffer     []types.AgentLog
	bufferMutex   sync.RWMutex
	maxBufferSize int
	clients       map[string]*types.ConnectionStatus
	clientsMutex  sync.RWMutex
	startTime     time.Time
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

// NewEnhancedLogServer creates a new enhanced WebSocket log server
func NewEnhancedLogServer(port int) *EnhancedLogServer {
	return &EnhancedLogServer{
		hub:           NewHub(),
		port:          port,
		logBuffer:     make([]types.AgentLog, 0, 100),
		maxBufferSize: 100,
		clients:       make(map[string]*types.ConnectionStatus),
		startTime:     time.Now(),
		stopChan:      make(chan struct{}),
	}
}

// Start starts the enhanced WebSocket server
func (s *EnhancedLogServer) Start() error {
	// Start the hub
	go s.hub.Run()

	// Start heartbeat sender
	s.wg.Add(1)
	go s.startHeartbeat()

	// Create HTTP server
	mux := http.NewServeMux()
	
	// WebSocket endpoint with CORS support
	mux.HandleFunc("/ws", s.handleWebSocket)
	
	// Health check endpoint
	mux.HandleFunc("/health", s.handleHealthCheck)
	
	// Stats endpoint
	mux.HandleFunc("/stats", s.handleStats)

	// CORS middleware wrapper
	handler := s.corsMiddleware(mux)

	s.server = &http.Server{
		Addr:           fmt.Sprintf(":%d", s.port),
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	// Start the server
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("Enhanced WebSocket server starting on port %d", s.port)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WebSocket server error: %v", err)
		}
	}()

	log.Printf("Enhanced WebSocket server started successfully on port %d", s.port)
	return nil
}

// Stop gracefully stops the WebSocket server
func (s *EnhancedLogServer) Stop() error {
	log.Printf("Stopping enhanced WebSocket server...")
	
	// Send disconnection status to all clients
	s.sendConnectionStatus(false)
	
	// Signal stop to goroutines
	close(s.stopChan)
	
	// Shutdown HTTP server
	if s.server != nil {
		if err := s.server.Close(); err != nil {
			log.Printf("Error closing server: %v", err)
		}
	}
	
	// Wait for goroutines to finish
	s.wg.Wait()
	
	log.Printf("Enhanced WebSocket server stopped")
	return nil
}

// handleWebSocket handles WebSocket upgrade and connection
func (s *EnhancedLogServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade connection
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			// In production, implement proper origin checking
			return true
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}

	// Create client
	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())
	client := NewClient(s.hub, conn)
	
	// Register client
	s.registerClient(clientID)
	client.hub.register <- client

	// Send connection confirmation
	s.sendConnectionConfirmation(client, clientID)
	
	// Send buffered logs to new client
	s.sendBufferedLogsToClient(client)

	// Start client routines
	go client.writePump()
	go client.readPump()
	
	// Handle client disconnection
	go func() {
		<-client.done
		s.unregisterClient(clientID)
	}()
}

// BroadcastLog sends a simple log message to all connected clients
func (s *EnhancedLogServer) BroadcastLog(message string) {
	log := types.NewAgentLog(types.LogTypeRouting, "system", message)
	s.BroadcastAgentLog(log)
}

// BroadcastAgentLog sends an agent log to all connected clients
func (s *EnhancedLogServer) BroadcastAgentLog(log *types.AgentLog) {
	// Add to buffer
	s.addToBuffer(*log)

	// Create WebSocket message
	wsMsg := types.NewWebSocketMessage(types.WSTypeLog, log)
	
	// Broadcast to all clients
	if data, err := wsMsg.ToJSON(); err == nil {
		s.hub.Broadcast(data)
		fmt.Printf("[WS] Broadcasting log: %s from %s\n", log.Content, log.From)
	} else {
		fmt.Printf("Failed to marshal log message: %v\n", err)
	}
}

// BroadcastError sends an error message to all connected clients
func (s *EnhancedLogServer) BroadcastError(from, errorMsg string) {
	log := types.NewAgentLog(types.LogTypeError, from, errorMsg)
	log.Level = types.LogLevelError
	s.BroadcastAgentLog(log)
}

// BroadcastStatus sends a status update to all connected clients
func (s *EnhancedLogServer) BroadcastStatus(status interface{}) {
	wsMsg := types.NewWebSocketMessage(types.WSTypeStatus, status)
	if data, err := wsMsg.ToJSON(); err == nil {
		s.hub.Broadcast(data)
	}
}

// BroadcastSAGEVerification sends SAGE verification results
func (s *EnhancedLogServer) BroadcastSAGEVerification(result *types.SAGEVerificationResult) {
	log := types.NewAgentLog(types.LogTypeSAGE, "sage-verifier", fmt.Sprintf("Verification: %v", result.Verified))
	log.Level = types.LogLevelInfo
	
	if !result.Verified {
		log.Level = types.LogLevelWarning
		log.Content = fmt.Sprintf("SAGE verification failed: %s", result.Error)
	}
	
	s.BroadcastAgentLog(log)
}

// addToBuffer adds a log to the buffer with size management
func (s *EnhancedLogServer) addToBuffer(log types.AgentLog) {
	s.bufferMutex.Lock()
	defer s.bufferMutex.Unlock()

	s.logBuffer = append(s.logBuffer, log)
	
	// Maintain buffer size
	if len(s.logBuffer) > s.maxBufferSize {
		s.logBuffer = s.logBuffer[len(s.logBuffer)-s.maxBufferSize:]
	}
}

// sendBufferedLogsToClient sends buffered logs to a specific client
func (s *EnhancedLogServer) sendBufferedLogsToClient(client *Client) {
	s.bufferMutex.RLock()
	logs := make([]types.AgentLog, len(s.logBuffer))
	copy(logs, s.logBuffer)
	s.bufferMutex.RUnlock()

	for _, log := range logs {
		wsMsg := types.NewWebSocketMessage(types.WSTypeLog, log)
		if data, err := wsMsg.ToJSON(); err == nil {
			select {
			case client.send <- data:
			default:
				// Client's send channel is full, skip
			}
		}
	}
}

// sendConnectionConfirmation sends initial connection confirmation to client
func (s *EnhancedLogServer) sendConnectionConfirmation(client *Client, clientID string) {
	confirmation := map[string]interface{}{
		"connected": true,
		"clientId":  clientID,
		"timestamp": time.Now().Format(time.RFC3339),
		"version":   "1.0.0",
	}
	
	wsMsg := types.NewWebSocketMessage(types.WSTypeConnection, confirmation)
	if data, err := wsMsg.ToJSON(); err == nil {
		select {
		case client.send <- data:
		default:
		}
	}
}

// registerClient registers a new client connection
func (s *EnhancedLogServer) registerClient(clientID string) {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()
	
	s.clients[clientID] = &types.ConnectionStatus{
		Connected:   true,
		ClientID:    clientID,
		ConnectedAt: time.Now(),
	}
	
	log.Printf("[WS] Client registered: %s", clientID)
}

// unregisterClient removes a client connection
func (s *EnhancedLogServer) unregisterClient(clientID string) {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()
	
	delete(s.clients, clientID)
	log.Printf("[WS] Client unregistered: %s", clientID)
}

// startHeartbeat sends periodic heartbeat messages
func (s *EnhancedLogServer) startHeartbeat() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.clientsMutex.RLock()
			clientCount := len(s.clients)
			s.clientsMutex.RUnlock()
			
			heartbeat := map[string]interface{}{
				"timestamp": time.Now().Format(time.RFC3339),
				"uptime":    time.Since(s.startTime).Seconds(),
				"clients":   clientCount,
			}
			
			wsMsg := types.NewWebSocketMessage(types.WSTypeHeartbeat, heartbeat)
			if data, err := wsMsg.ToJSON(); err == nil {
				s.hub.Broadcast(data)
			}
		}
	}
}

// sendConnectionStatus sends connection status update
func (s *EnhancedLogServer) sendConnectionStatus(connected bool) {
	status := map[string]interface{}{
		"connected": connected,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	wsMsg := types.NewWebSocketMessage(types.WSTypeConnection, status)
	if data, err := wsMsg.ToJSON(); err == nil {
		s.hub.Broadcast(data)
	}
}

// handleHealthCheck handles health check requests
func (s *EnhancedLogServer) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	s.clientsMutex.RLock()
	clientCount := len(s.clients)
	s.clientsMutex.RUnlock()
	
	health := types.HealthCheckResponse{
		Status:    types.StatusHealthy,
		Timestamp: time.Now().Format(time.RFC3339),
		Version:   "1.0.0",
		Services: map[string]types.ServiceStatus{
			"websocket": {
				Name:      "WebSocket Server",
				Status:    types.StatusUp,
				Latency:   0.1,
				LastCheck: time.Now().Format(time.RFC3339),
			},
		},
	}
	
	// Add client count to response
	if wsService, ok := health.Services["websocket"]; ok && wsService.Status == types.StatusUp {
		wsService.Error = fmt.Sprintf("Connected clients: %d", clientCount)
		health.Services["websocket"] = wsService
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(health)
}

// handleStats handles statistics requests
func (s *EnhancedLogServer) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := s.GetStats()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// GetStats returns server statistics
func (s *EnhancedLogServer) GetStats() map[string]interface{} {
	s.bufferMutex.RLock()
	bufferSize := len(s.logBuffer)
	s.bufferMutex.RUnlock()
	
	s.clientsMutex.RLock()
	clientCount := len(s.clients)
	clientList := make([]string, 0, clientCount)
	for id := range s.clients {
		clientList = append(clientList, id)
	}
	s.clientsMutex.RUnlock()

	return map[string]interface{}{
		"uptime":       time.Since(s.startTime).Seconds(),
		"clients":      clientCount,
		"client_ids":   clientList,
		"buffer_size":  bufferSize,
		"max_buffer":   s.maxBufferSize,
		"port":         s.port,
		"started_at":   s.startTime.Format(time.RFC3339),
	}
}

// corsMiddleware adds CORS headers to responses
func (s *EnhancedLogServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SAGE-Enabled, X-Scenario")
		w.Header().Set("Access-Control-Max-Age", "3600")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}