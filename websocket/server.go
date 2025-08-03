package websocket

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// LogServer handles WebSocket connections and broadcasts log messages
type LogServer struct {
	hub    *Hub
	port   int
	server *http.Server
	mu     sync.Mutex
}

// NewLogServer creates a new LogServer instance
func NewLogServer(port int) *LogServer {
	return &LogServer{
		hub:  NewHub(),
		port: port,
	}
}

// Start starts the WebSocket server
func (s *LogServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Start the hub
	go s.hub.Run()

	// Create HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	// Start the server
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("WebSocket server error: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the WebSocket server
func (s *LogServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// BroadcastLog sends a log message to all connected clients
func (s *LogServer) BroadcastLog(message string) {
	s.hub.Broadcast([]byte(message))
}

// handleWebSocket handles WebSocket connections
func (s *LogServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Failed to upgrade connection: %v\n", err)
		return
	}

	client := NewClient(s.hub, conn)
	client.hub.register <- client

	// Start client routines
	go client.writePump()
	go client.readPump()
} 