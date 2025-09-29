package adapters

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/core/rfc9421"
)

// EnhancedMessageHandler handles incoming messages with full request/response support
type EnhancedMessageHandler struct {
	agentType      string
	sageManager    *SAGEManager
	verifierHelper *VerifierHelper
	messenger      *AgentMessenger
}

// NewEnhancedMessageHandler creates a new enhanced message handler
func NewEnhancedMessageHandler(
	agentType string, 
	sageManager *SAGEManager, 
	verifierHelper *VerifierHelper,
	messenger *AgentMessenger,
) *EnhancedMessageHandler {
	return &EnhancedMessageHandler{
		agentType:      agentType,
		sageManager:    sageManager,
		verifierHelper: verifierHelper,
		messenger:      messenger,
	}
}

// HandleMessage processes an incoming message with SAGE verification
func (h *EnhancedMessageHandler) HandleMessage(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendErrorResponse(w, http.StatusBadRequest, "Failed to read request body", err)
		return
	}
	
	var requestData map[string]interface{}
	if err := json.Unmarshal(body, &requestData); err != nil {
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid JSON", err)
		return
	}
	
	// Extract SAGE headers
	headers := map[string]string{
		"X-Agent-DID":  r.Header.Get("X-Agent-DID"),
		"X-Message-ID": r.Header.Get("X-Message-ID"),
		"X-Timestamp":  r.Header.Get("X-Timestamp"),
		"X-Nonce":      r.Header.Get("X-Nonce"),
		"X-Algorithm":  r.Header.Get("X-Algorithm"),
		"X-Signature":  r.Header.Get("X-Signature"),
		"X-Metadata":   r.Header.Get("X-Metadata"),
	}
	
	// Check if SAGE is enabled
	if !h.sageManager.IsEnabled() {
		log.Printf("[HANDLER] SAGE disabled, processing message without verification")
		h.processMessage(ctx, w, requestData, nil)
		return
	}
	
	// Get verifier
	verifier := h.sageManager.GetVerifier()
	if verifier == nil {
		h.sendErrorResponse(w, http.StatusInternalServerError, "Verifier not available", nil)
		return
	}
	
	// Verify the message using headers
	verifyResult, err := verifier.VerifyRequestHeaders(ctx, headers, body)
	if err != nil || !verifyResult.Verified {
		log.Printf("[HANDLER] Message verification failed: %v", err)
		
		// Send SAGE error response
		sageError := types.NewSAGEVerificationError(
			types.SAGEErrorCodeInvalidSignature,
			"Message signature verification failed",
			headers["X-Agent-DID"],
			headers["X-Message-ID"],
		)
		
		h.sendSAGEErrorResponse(w, requestData["from"].(string), sageError)
		return
	}
	
	log.Printf("[HANDLER] Message verified from agent: %s", verifyResult.AgentDID)
	
	// Process the verified message
	h.processMessage(ctx, w, requestData, verifyResult)
}

// processMessage handles the verified message
func (h *EnhancedMessageHandler) processMessage(
	ctx context.Context,
	w http.ResponseWriter,
	requestData map[string]interface{},
	verifyResult *types.SAGEVerificationResult,
) {
	// Parse as AgentMessageRequest
	var request types.AgentMessageRequest
	
	// Manual parsing to handle interface{} types
	request.Body, _ = requestData["body"].(string)
	request.FromAgentDID, _ = requestData["from"].(string)
	request.ToAgentDID, _ = requestData["to"].(string)
	request.MessageID, _ = requestData["message_id"].(string)
	request.Algorithm = "ECDSA-secp256k1"
	
	// Parse timestamp
	if ts, ok := requestData["timestamp"].(float64); ok {
		request.Timestamp = time.Unix(int64(ts), 0)
	}
	
	// Parse request context if present
	if reqCtx, ok := requestData["request_context"].(map[string]interface{}); ok {
		request.RequestContext = &types.RequestContext{
			RequestID: reqCtx["request_id"].(string),
			Nonce:     reqCtx["nonce"].(string),
		}
		
		if timeout, ok := reqCtx["response_timeout"].(float64); ok {
			request.RequestContext.ResponseTimeout = time.Duration(timeout) * time.Second
		}
		
		if fields, ok := reqCtx["expected_response_fields"].([]interface{}); ok {
			for _, field := range fields {
				if f, ok := field.(string); ok {
					request.RequestContext.ExpectedResponseFields = append(
						request.RequestContext.ExpectedResponseFields, f)
				}
			}
		}
	}
	
	// Parse metadata
	if metadata, ok := requestData["metadata"].(map[string]interface{}); ok {
		request.Metadata = metadata
	}
	
	// Log the received message
	log.Printf("[HANDLER-%s] Received message from %s: %s", 
		h.agentType, request.FromAgentDID, request.Body)
	
	// Generate response if request expects one
	if request.RequestContext != nil {
		responseBody := h.generateResponse(request.Body)
		
		// Create and sign response
		response, signedResponse, err := h.messenger.CreateResponse(
			ctx,
			&request,
			h.agentType,
			responseBody,
		)
		if err != nil {
			h.sendErrorResponse(w, http.StatusInternalServerError, "Failed to create response", err)
			return
		}
		
		// Send the response
		responseData := map[string]interface{}{
			"response":        response,
			"signed_message":  h.serializeSignedMessage(signedResponse),
			"status":          "success",
			"agent_type":      h.agentType,
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(responseData)
		
		log.Printf("[HANDLER-%s] Sent response for request %s", h.agentType, request.MessageID)
	} else {
		// Just acknowledge receipt
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "accepted",
			"message_id": request.MessageID,
			"agent_type": h.agentType,
		})
	}
}

// generateResponse generates a response based on the request
func (h *EnhancedMessageHandler) generateResponse(requestBody string) string {
	// Simple response generation based on agent type and request
	responses := map[string][]string{
		"root": {
			"System status: All agents operational",
			"Task acknowledged and distributed to sub-agents",
			"Command executed successfully",
		},
		"ordering": {
			"Order processing initiated",
			"Current capacity: 1000 orders/hour, 85% utilization",
			"Optimal route calculated: Route-A -> Route-B -> Destination",
		},
		"planning": {
			"Planning sequence initiated for specified period",
			"Resource allocation completed: 70% allocated, 30% reserved",
			"Performance metrics retrieved: Efficiency 92%, Accuracy 98%",
		},
	}
	
	agentResponses, exists := responses[h.agentType]
	if !exists {
		return fmt.Sprintf("Message received and processed by %s agent", h.agentType)
	}
	
	// Return a random response for the agent type
	return agentResponses[time.Now().UnixNano()%int64(len(agentResponses))]
}

// serializeSignedMessage converts a signed message to a map for JSON encoding
func (h *EnhancedMessageHandler) serializeSignedMessage(msg *rfc9421.Message) map[string]interface{} {
	return map[string]interface{}{
		"agent_did":   msg.AgentDID,
		"message_id":  msg.MessageID,
		"timestamp":   msg.Timestamp.Unix(),
		"nonce":       msg.Nonce,
		"algorithm":   msg.Algorithm,
		"signature":   hex.EncodeToString(msg.Signature),
		"metadata":    msg.Metadata,
	}
}

// sendErrorResponse sends an error response
func (h *EnhancedMessageHandler) sendErrorResponse(w http.ResponseWriter, status int, message string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	response := map[string]interface{}{
		"error":   message,
		"status":  "error",
		"agent":   h.agentType,
	}
	
	if err != nil {
		response["details"] = err.Error()
	}
	
	json.NewEncoder(w).Encode(response)
}

// sendSAGEErrorResponse sends a SAGE verification error response
func (h *EnhancedMessageHandler) sendSAGEErrorResponse(w http.ResponseWriter, toAgent string, sageError *types.SAGEVerificationError) {
	errorResponse := types.NewSAGEErrorResponse(h.agentType, toAgent, sageError)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(errorResponse)
}