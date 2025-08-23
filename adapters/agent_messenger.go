package adapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sage-multi-agent/config"
	"github.com/sage-multi-agent/types"
	"github.com/sage-x-project/sage/core/rfc9421"
)

// AgentMessenger handles agent-to-agent messaging with SAGE
type AgentMessenger struct {
	sageManager     *SAGEManager
	verifierHelper  *VerifierHelper
	config          *config.Config
	httpClient      *http.Client
	conversations   map[string]*types.AgentConversation
	conversationsMu sync.RWMutex
}

// NewAgentMessenger creates a new agent messenger service
func NewAgentMessenger(sageManager *SAGEManager, verifierHelper *VerifierHelper, cfg *config.Config) *AgentMessenger {
	return &AgentMessenger{
		sageManager:    sageManager,
		verifierHelper: verifierHelper,
		config:         cfg,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		conversations:  make(map[string]*types.AgentConversation),
	}
}

// SendMessage sends a signed message from one agent to another
func (am *AgentMessenger) SendMessage(
	ctx context.Context,
	fromAgentType string,
	toAgentType string,
	message string,
	expectResponse bool,
) (*types.AgentConversation, error) {
	
	// Get agent configurations
	fromAgent, exists := am.config.Agents[fromAgentType]
	if !exists {
		return nil, fmt.Errorf("sender agent %s not found", fromAgentType)
	}
	
	toAgent, exists := am.config.Agents[toAgentType]
	if !exists {
		return nil, fmt.Errorf("receiver agent %s not found", toAgentType)
	}
	
	// Create message request
	messageID := fmt.Sprintf("%s-%d-%s", fromAgentType, time.Now().UnixNano(), uuid.New().String()[:8])
	nonce := uuid.New().String()
	
	request := &types.AgentMessageRequest{
		Body:         message,
		FromAgentDID: fromAgent.DID,
		ToAgentDID:   toAgent.DID,
		MessageID:    messageID,
		Timestamp:    time.Now(),
		Algorithm:    "ECDSA-secp256k1",
		Metadata: map[string]interface{}{
			"agent_type": fromAgentType,
			"agent_name": fromAgent.Name,
		},
	}
	
	// Add request context if expecting response
	if expectResponse {
		request.RequestContext = &types.RequestContext{
			RequestID:       messageID,
			Nonce:           nonce,
			ResponseTimeout: 10 * time.Second,
			ExpectedResponseFields: []string{
				"response_body",
				"original_request_id",
				"original_sender_did",
			},
		}
	}
	
	// Create conversation tracking
	conversation := &types.AgentConversation{
		ConversationID: messageID,
		Request:        request,
		StartTime:      time.Now(),
		Status:         "pending",
	}
	
	// Store conversation
	am.conversationsMu.Lock()
	am.conversations[messageID] = conversation
	am.conversationsMu.Unlock()
	
	// Sign the message with SAGE
	signer, err := am.sageManager.GetOrCreateSigner(fromAgentType, am.verifierHelper)
	if err != nil {
		return nil, fmt.Errorf("failed to get signer for %s: %w", fromAgentType, err)
	}
	
	// Create metadata for signing
	signingMetadata := map[string]interface{}{
		"to_agent_did":   toAgent.DID,
		"from_agent_did": fromAgent.DID,
		"message_id":     messageID,
		"timestamp":      request.Timestamp.Unix(),
		"algorithm":      request.Algorithm,
	}
	
	if request.RequestContext != nil {
		signingMetadata["request_context"] = map[string]interface{}{
			"request_id": request.RequestContext.RequestID,
			"nonce":      request.RequestContext.Nonce,
		}
	}
	
	// Sign the message
	signedMessage, err := signer.SignMessage(ctx, message, signingMetadata)
	if err != nil {
		return nil, fmt.Errorf("failed to sign message: %w", err)
	}
	
	// Send the signed message to the receiver
	err = am.sendSignedMessage(ctx, toAgent.Endpoint, signedMessage, request)
	if err != nil {
		conversation.Status = "error"
		return conversation, fmt.Errorf("failed to send message: %w", err)
	}
	
	log.Printf("[MESSENGER] Sent message from %s to %s (ID: %s)", 
		fromAgentType, toAgentType, messageID)
	
	// If not expecting response, mark as completed
	if !expectResponse {
		conversation.Status = "completed"
		endTime := time.Now()
		conversation.EndTime = &endTime
	}
	
	return conversation, nil
}

// sendSignedMessage sends the signed message to the target agent
func (am *AgentMessenger) sendSignedMessage(
	ctx context.Context,
	endpoint string,
	signedMessage *rfc9421.Message,
	request *types.AgentMessageRequest,
) error {
	// Prepare the HTTP request
	url := fmt.Sprintf("%s/api/agent/message", endpoint)
	
	// Create request body
	requestBody := map[string]interface{}{
		"from":            request.FromAgentDID,
		"to":              request.ToAgentDID,
		"body":            request.Body,
		"message_id":      request.MessageID,
		"timestamp":       request.Timestamp.Unix(),
		"request_context": request.RequestContext,
		"metadata":        request.Metadata,
	}
	
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	
	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	// Add SAGE headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Agent-DID", signedMessage.AgentDID)
	httpReq.Header.Set("X-Message-ID", signedMessage.MessageID)
	httpReq.Header.Set("X-Timestamp", signedMessage.Timestamp.Format(time.RFC3339))
	httpReq.Header.Set("X-Nonce", signedMessage.Nonce)
	httpReq.Header.Set("X-Algorithm", signedMessage.Algorithm)
	httpReq.Header.Set("X-Signature", hex.EncodeToString(signedMessage.Signature))
	
	// Add metadata headers
	if signedMessage.Metadata != nil {
		metadataBytes, _ := json.Marshal(signedMessage.Metadata)
		httpReq.Header.Set("X-Metadata", string(metadataBytes))
	}
	
	// Send the request
	resp, err := am.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	// Check response status
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}

// HandleResponse handles a response message from another agent
func (am *AgentMessenger) HandleResponse(
	ctx context.Context,
	response *types.AgentMessageResponse,
	signedMessage *rfc9421.Message,
) error {
	if response.InResponseTo == nil {
		return fmt.Errorf("response missing original request context")
	}
	
	// Find the original conversation
	am.conversationsMu.Lock()
	conversation, exists := am.conversations[response.InResponseTo.OriginalRequestID]
	if exists {
		conversation.Response = response
		conversation.Status = "completed"
		endTime := time.Now()
		conversation.EndTime = &endTime
	}
	am.conversationsMu.Unlock()
	
	if !exists {
		log.Printf("[MESSENGER] Received response for unknown request: %s", 
			response.InResponseTo.OriginalRequestID)
		return nil
	}
	
	// Verify the response is from the expected agent
	if conversation.Request.ToAgentDID != response.FromAgentDID {
		return fmt.Errorf("response from unexpected agent: expected %s, got %s",
			conversation.Request.ToAgentDID, response.FromAgentDID)
	}
	
	// Verify the response is for the correct sender
	if conversation.Request.FromAgentDID != response.InResponseTo.OriginalSenderDID {
		return fmt.Errorf("response context mismatch: original sender DID doesn't match")
	}
	
	log.Printf("[MESSENGER] Received valid response for conversation %s", 
		conversation.ConversationID)
	
	return nil
}

// CreateResponse creates a response message for a received request
func (am *AgentMessenger) CreateResponse(
	ctx context.Context,
	request *types.AgentMessageRequest,
	responderAgentType string,
	responseBody string,
) (*types.AgentMessageResponse, *rfc9421.Message, error) {
	
	// Get responder agent configuration
	responderAgent, exists := am.config.Agents[responderAgentType]
	if !exists {
		return nil, nil, fmt.Errorf("responder agent %s not found", responderAgentType)
	}
	
	// Calculate message digest of original request
	digestData := fmt.Sprintf("%s:%s:%s:%d", 
		request.MessageID, request.FromAgentDID, request.Body, request.Timestamp.Unix())
	digest := sha256.Sum256([]byte(digestData))
	
	// Create response
	response := &types.AgentMessageResponse{
		Body:         responseBody,
		FromAgentDID: responderAgent.DID,
		ToAgentDID:   request.FromAgentDID,
		ResponseID:   fmt.Sprintf("%s-resp-%d-%s", responderAgentType, time.Now().UnixNano(), uuid.New().String()[:8]),
		Timestamp:    time.Now(),
		Algorithm:    "ECDSA-secp256k1",
		Status:       "success",
		InResponseTo: &types.ResponseContext{
			OriginalRequestID:     request.MessageID,
			OriginalSenderDID:     request.FromAgentDID,
			OriginalNonce:        request.RequestContext.Nonce,
			OriginalMessageDigest: hex.EncodeToString(digest[:]),
		},
		Metadata: map[string]interface{}{
			"agent_type": responderAgentType,
			"agent_name": responderAgent.Name,
		},
	}
	
	// Sign the response with SAGE
	signer, err := am.sageManager.GetOrCreateSigner(responderAgentType, am.verifierHelper)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get signer for %s: %w", responderAgentType, err)
	}
	
	// Create metadata for signing (include original request context)
	signingMetadata := map[string]interface{}{
		"response_id":    response.ResponseID,
		"from_agent_did": response.FromAgentDID,
		"to_agent_did":   response.ToAgentDID,
		"timestamp":      response.Timestamp.Unix(),
		"algorithm":      response.Algorithm,
		"in_response_to": map[string]interface{}{
			"original_request_id":     response.InResponseTo.OriginalRequestID,
			"original_sender_did":     response.InResponseTo.OriginalSenderDID,
			"original_nonce":          response.InResponseTo.OriginalNonce,
			"original_message_digest": response.InResponseTo.OriginalMessageDigest,
		},
	}
	
	// Sign the response
	signedMessage, err := signer.SignMessage(ctx, responseBody, signingMetadata)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign response: %w", err)
	}
	
	return response, signedMessage, nil
}

// GetConversation retrieves a conversation by ID
func (am *AgentMessenger) GetConversation(conversationID string) (*types.AgentConversation, bool) {
	am.conversationsMu.RLock()
	defer am.conversationsMu.RUnlock()
	
	conversation, exists := am.conversations[conversationID]
	return conversation, exists
}

// GetAllConversations returns all conversations
func (am *AgentMessenger) GetAllConversations() []*types.AgentConversation {
	am.conversationsMu.RLock()
	defer am.conversationsMu.RUnlock()
	
	conversations := make([]*types.AgentConversation, 0, len(am.conversations))
	for _, conv := range am.conversations {
		conversations = append(conversations, conv)
	}
	return conversations
}