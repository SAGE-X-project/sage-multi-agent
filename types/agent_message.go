package types

import (
	"time"
)

// AgentMessageRequest represents a message request from one agent to another
type AgentMessageRequest struct {
	// Message content
	Body string `json:"body"`
	
	// Sender identification (DID registered in contract)
	FromAgentDID string `json:"from_agent_did"`
	
	// Receiver identification (DID registered in contract)
	ToAgentDID string `json:"to_agent_did"`
	
	// Unique message ID for tracking
	MessageID string `json:"message_id"`
	
	// Timestamp of the message
	Timestamp time.Time `json:"timestamp"`
	
	// Signature algorithm used
	Algorithm string `json:"algorithm"`
	
	// Request context for response correlation
	RequestContext *RequestContext `json:"request_context,omitempty"`
	
	// Metadata for additional information
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// RequestContext contains information needed for response correlation
type RequestContext struct {
	// Original request ID that expects a response
	RequestID string `json:"request_id"`
	
	// Nonce for replay protection
	Nonce string `json:"nonce"`
	
	// Expected response fields
	ExpectedResponseFields []string `json:"expected_response_fields,omitempty"`
	
	// Timeout for response
	ResponseTimeout time.Duration `json:"response_timeout,omitempty"`
}

// AgentMessageResponse represents a response from one agent to another
type AgentMessageResponse struct {
	// Response content
	Body string `json:"body"`
	
	// Original request context
	InResponseTo *ResponseContext `json:"in_response_to"`
	
	// Responder identification (DID registered in contract)
	FromAgentDID string `json:"from_agent_did"`
	
	// Original requester identification
	ToAgentDID string `json:"to_agent_did"`
	
	// Unique response ID
	ResponseID string `json:"response_id"`
	
	// Timestamp of the response
	Timestamp time.Time `json:"timestamp"`
	
	// Signature algorithm used for response
	Algorithm string `json:"algorithm"`
	
	// Response status
	Status string `json:"status"` // "success", "error", "partial"
	
	// Metadata for additional information
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ResponseContext links a response to its original request
type ResponseContext struct {
	// Original request ID
	OriginalRequestID string `json:"original_request_id"`
	
	// Original sender DID (for verification)
	OriginalSenderDID string `json:"original_sender_did"`
	
	// Original nonce (for correlation)
	OriginalNonce string `json:"original_nonce"`
	
	// Original message digest (optional, for integrity)
	OriginalMessageDigest string `json:"original_message_digest,omitempty"`
}

// AgentConversation tracks a request-response conversation between agents
type AgentConversation struct {
	ConversationID string                `json:"conversation_id"`
	Request        *AgentMessageRequest  `json:"request"`
	Response       *AgentMessageResponse `json:"response,omitempty"`
	StartTime      time.Time             `json:"start_time"`
	EndTime        *time.Time            `json:"end_time,omitempty"`
	Status         string                `json:"status"` // "pending", "completed", "timeout", "error"
}