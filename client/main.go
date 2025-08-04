package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type PromptRequest struct {
	Prompt string `json:"prompt"`
}

type Server struct {
	a2aClient *client.A2AClient
}

func NewServer(rootAgentURL string) (*Server, error) {
	a2aClient, err := client.NewA2AClient(rootAgentURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create A2A client: %v", err)
	}
	return &Server{a2aClient: a2aClient}, nil
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Create and send message to root agent
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart(req.Prompt)},
	)

	params := protocol.SendMessageParams{
		Message: message,
	}

	result, err := s.a2aClient.SendMessage(context.Background(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to send message: %v", err), http.StatusInternalServerError)
		return
	}

	// Extract response text
	var responseText string
	switch result.Result.GetKind() {
	case protocol.KindMessage:
		msg := result.Result.(*protocol.Message)
		responseText = extractText(*msg)
	case protocol.KindTask:
		task := result.Result.(*protocol.Task)
		if task.Status.Message != nil {
			responseText = extractText(*task.Status.Message)
		} else {
			http.Error(w, "No response message from agent", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, fmt.Sprintf("Unexpected response type: %T", result.Result), http.StatusInternalServerError)
		return
	}

	// Send response back to frontend
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"response": responseText,
	})
}

// extractText extracts the text content from a message
func extractText(message protocol.Message) string {
	var result string
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			result += textPart.Text
		}
	}
	return result
}

func main() {
	// Parse command-line flags
	port := flag.Int("port", 8086, "Port to listen on")
	rootAgentURL := flag.String("root-url", "http://localhost:8080", "URL for the root agent")
	flag.Parse()

	// Create server
	server, err := NewServer(*rootAgentURL)
	if err != nil {
		log.Fatal("Failed to create server: %v", err)
	}

	// Set up routes
	http.HandleFunc("/send/prompt", server.handlePrompt)

	// Start server
	addr := fmt.Sprintf(":%d", *port)
	log.Info("Starting client server on %s", addr)
	log.Fatal("Server failed: %v", http.ListenAndServe(addr, nil))
}
