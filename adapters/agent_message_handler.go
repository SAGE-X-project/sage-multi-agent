package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
)

// AgentMessageHandler handles incoming messages with SAGE verification
type AgentMessageHandler struct {
	agentType      string
	sageManager    *SAGEManager
	verifierHelper *VerifierHelper
}

// NewAgentMessageHandler creates a new message handler for an agent
func NewAgentMessageHandler(agentType string, sageManager *SAGEManager, verifierHelper *VerifierHelper) *AgentMessageHandler {
	return &AgentMessageHandler{
		agentType:      agentType,
		sageManager:    sageManager,
		verifierHelper: verifierHelper,
	}
}

// HandleMessage processes an incoming message with SAGE verification
func (h *AgentMessageHandler) HandleMessage(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var messageRequest struct {
		From      string                 `json:"from"`
		To        string                 `json:"to"`
		Message   string                 `json:"message"`
		Metadata  map[string]interface{} `json:"metadata,omitempty"`
		Signature []byte                 `json:"signature,omitempty"`
		MessageID string                 `json:"messageId,omitempty"`
		Timestamp string                 `json:"timestamp,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&messageRequest); err != nil {
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Check if SAGE is enabled
	if !h.sageManager.IsEnabled() {
		// Process without verification
		h.processMessage(ctx, w, messageRequest.From, messageRequest.To, messageRequest.Message, messageRequest.Metadata)
		return
	}

	// Extract SAGE headers for verification
	headers := make(map[string]string)
	headers["X-Agent-DID"] = r.Header.Get("X-Agent-DID")
	headers["X-Message-ID"] = r.Header.Get("X-Message-ID")
	headers["X-Timestamp"] = r.Header.Get("X-Timestamp")
	headers["X-Nonce"] = r.Header.Get("X-Nonce")
	headers["X-Signature"] = r.Header.Get("X-Signature")
	headers["X-Signature-Algorithm"] = r.Header.Get("X-Signature-Algorithm")

	// Verify the message
	verifier := h.sageManager.GetVerifier()
	body := []byte(messageRequest.Message)

	verifyResult, err := verifier.VerifyRequestHeaders(ctx, headers, body)

	// Check verification result
	if err != nil || !verifyResult.Verified {
		// Create verification error
		sageError := types.NewSAGEVerificationError(
			types.SAGEErrorCodeInvalidSignature,
			"Message signature verification failed",
			headers["X-Agent-DID"],
			headers["X-Message-ID"],
		)

		if err != nil {
			sageError.Details["error"] = err.Error()
		}
		if verifyResult != nil && verifyResult.Error != "" {
			sageError.Details["verification_error"] = verifyResult.Error
		}

		// Send error response back to sender
		h.sendSAGEErrorResponse(w, messageRequest.From, sageError)

		// Log the rejection
		log.Printf("[SAGE] Message from %s rejected: %s", messageRequest.From, sageError.Error())
		return
	}

	// Verification successful, process the message
	log.Printf("[SAGE] Message from %s verified successfully", messageRequest.From)
	h.processMessage(ctx, w, messageRequest.From, messageRequest.To, messageRequest.Message, messageRequest.Metadata)
}

// processMessage handles the actual message processing after verification
func (h *AgentMessageHandler) processMessage(ctx context.Context, w http.ResponseWriter, from, to, message string, metadata map[string]interface{}) {
	// This is where the actual agent logic would process the message
	// For now, we'll just return a success response

	response := struct {
		Status    string                 `json:"status"`
		From      string                 `json:"from"`
		To        string                 `json:"to"`
		Message   string                 `json:"message"`
		Timestamp time.Time              `json:"timestamp"`
		Metadata  map[string]interface{} `json:"metadata,omitempty"`
	}{
		Status:    "received",
		From:      h.agentType,
		To:        from,
		Message:   fmt.Sprintf("Message received and processed by %s", h.agentType),
		Timestamp: time.Now(),
		Metadata:  metadata,
	}

	// If SAGE is enabled, sign the response
	if h.sageManager.IsEnabled() {
		signer, err := h.sageManager.GetOrCreateSigner(h.agentType, h.verifierHelper)
		if err == nil {
			responseJSON, _ := json.Marshal(response)
			signedHeaders, err := signer.SignRequest(ctx, "POST", fmt.Sprintf("/agent/%s/response", from), responseJSON)
			if err == nil {
				// Add signed headers to response
				for key, value := range signedHeaders {
					w.Header().Set(key, value)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// sendSAGEErrorResponse sends a SAGE verification error response
func (h *AgentMessageHandler) sendSAGEErrorResponse(w http.ResponseWriter, to string, sageError *types.SAGEVerificationError) {
	errorResponse := types.NewSAGEErrorResponse(h.agentType, to, sageError)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized) // 401 for authentication/verification failures

	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		log.Printf("[SAGE] Failed to send error response: %v", err)
	}
}

// sendErrorResponse sends a generic error response
func (h *AgentMessageHandler) sendErrorResponse(w http.ResponseWriter, statusCode int, message string, err error) {
	errorResp := struct {
		Error   string    `json:"error"`
		Message string    `json:"message"`
		Time    time.Time `json:"time"`
	}{
		Error:   err.Error(),
		Message: message,
		Time:    time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(errorResp)
}

// CreateMessageFromRequest creates an RFC9421 message from HTTP request
func CreateMessageFromRequest(r *http.Request, body []byte) (*rfc9421.Message, error) {
	// Extract headers
	agentDID := r.Header.Get("X-Agent-DID")
	if agentDID == "" {
		return nil, fmt.Errorf("missing X-Agent-DID header")
	}

	messageID := r.Header.Get("X-Message-ID")
	if messageID == "" {
		return nil, fmt.Errorf("missing X-Message-ID header")
	}

	timestampStr := r.Header.Get("X-Timestamp")
	if timestampStr == "" {
		return nil, fmt.Errorf("missing X-Timestamp header")
	}

	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp format: %w", err)
	}

	nonce := r.Header.Get("X-Nonce")
	if nonce == "" {
		return nil, fmt.Errorf("missing X-Nonce header")
	}

	algorithm := r.Header.Get("X-Signature-Algorithm")
	if algorithm == "" {
		algorithm = string(rfc9421.AlgorithmECDSASecp256k1)
	}

	// Create message
	message := &rfc9421.Message{
		AgentDID:     agentDID,
		MessageID:    messageID,
		Timestamp:    timestamp,
		Nonce:        nonce,
		Body:         body,
		Algorithm:    algorithm,
		SignedFields: []string{"agent_did", "message_id", "timestamp", "nonce", "body"},
		Headers:      make(map[string]string),
	}

	// Copy relevant headers
	for key, values := range r.Header {
		if len(values) > 0 {
			message.Headers[key] = values[0]
		}
	}

	return message, nil
}
