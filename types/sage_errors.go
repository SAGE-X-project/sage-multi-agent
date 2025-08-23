package types

import (
	"fmt"
	"time"
)

// SAGEVerificationError represents a SAGE verification failure
type SAGEVerificationError struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	AgentDID  string    `json:"agentDid,omitempty"`
	MessageID string    `json:"messageId,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Details   map[string]string `json:"details,omitempty"`
}

// Error implements the error interface
func (e *SAGEVerificationError) Error() string {
	return fmt.Sprintf("SAGE verification error [%s]: %s", e.Code, e.Message)
}

// NewSAGEVerificationError creates a new SAGE verification error
func NewSAGEVerificationError(code, message, agentDID, messageID string) *SAGEVerificationError {
	return &SAGEVerificationError{
		Code:      code,
		Message:   message,
		AgentDID:  agentDID,
		MessageID: messageID,
		Timestamp: time.Now(),
		Details:   make(map[string]string),
	}
}

// SAGEErrorResponse represents an error response sent back to the sender
type SAGEErrorResponse struct {
	Type      string                 `json:"type"`
	Error     *SAGEVerificationError `json:"error"`
	RequestID string                 `json:"requestId,omitempty"`
	From      string                 `json:"from"`
	To        string                 `json:"to"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewSAGEErrorResponse creates a new error response
func NewSAGEErrorResponse(from, to string, err *SAGEVerificationError) *SAGEErrorResponse {
	return &SAGEErrorResponse{
		Type:      "sage_verification_error",
		Error:     err,
		From:      from,
		To:        to,
		Timestamp: time.Now(),
	}
}

// SAGE error codes
const (
	SAGEErrorCodeInvalidSignature    = "INVALID_SIGNATURE"
	SAGEErrorCodeAgentNotRegistered  = "AGENT_NOT_REGISTERED"
	SAGEErrorCodePublicKeyNotFound   = "PUBLIC_KEY_NOT_FOUND"
	SAGEErrorCodeExpiredMessage      = "EXPIRED_MESSAGE"
	SAGEErrorCodeInvalidDID          = "INVALID_DID"
	SAGEErrorCodeBlockchainError     = "BLOCKCHAIN_ERROR"
	SAGEErrorCodeVerificationDisabled = "VERIFICATION_DISABLED"
)