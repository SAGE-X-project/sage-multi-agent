package types

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
)

// PromptRequest represents an incoming prompt request from the frontend
type PromptRequest struct {
	Prompt      string           `json:"prompt"`
	SAGEEnabled bool             `json:"sageEnabled,omitempty"`
	Scenario    string           `json:"scenario,omitempty"`
	Metadata    *RequestMetadata `json:"metadata,omitempty"`
}

// RequestMetadata contains metadata for the request
type RequestMetadata struct {
	UserID    string `json:"userId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	ClientIP  string `json:"clientIp,omitempty"`
}

// PromptResponse represents the response to a prompt request
type PromptResponse struct {
	Response         string                  `json:"response"`
	Logs             []AgentLog              `json:"logs,omitempty"`
	SAGEVerification *SAGEVerificationResult `json:"sageVerification,omitempty"`
	Metadata         *ResponseMetadata       `json:"metadata,omitempty"`
	Error            *ErrorDetail            `json:"error,omitempty"`
}

// ResponseMetadata contains metadata for the response
type ResponseMetadata struct {
	RequestID      string   `json:"requestId"`
	ProcessingTime float64  `json:"processingTime"` // in milliseconds
	AgentPath      []string `json:"agentPath,omitempty"`
	Timestamp      string   `json:"timestamp"`
}

// WebSocketMessage represents a WebSocket message
type WebSocketMessage struct {
	Type      string      `json:"type"` // "log", "error", "status", "heartbeat", "connection"
	Payload   interface{} `json:"payload"`
	Timestamp string      `json:"timestamp"`
	MessageID string      `json:"messageId,omitempty"`
}

// AgentLog represents a log entry from an agent
type AgentLog struct {
	Type           string `json:"type"` // "routing", "planning", "medical", "gateway", "sage", "error"
	From           string `json:"from"`
	To             string `json:"to,omitempty"`
	Content        string `json:"content"`
	Timestamp      string `json:"timestamp"`
	MessageID      string `json:"messageId,omitempty"`
	OriginalPrompt string `json:"originalPrompt,omitempty"`
	TamperedPrompt string `json:"tamperedPrompt,omitempty"`
	Level          string `json:"level,omitempty"` // "info", "warning", "error", "debug"
}

// SAGEVerificationResult represents the result of SAGE protocol verification
type SAGEVerificationResult struct {
	Verified       bool              `json:"verified"`
	AgentDID       string            `json:"agentDid,omitempty"`
	SignatureValid bool              `json:"signatureValid"`
	Timestamp      int64             `json:"timestamp,omitempty"`
	Details        map[string]string `json:"details,omitempty"`
	Error          string            `json:"error,omitempty"`
}

// ErrorDetail represents detailed error information
type ErrorDetail struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Details     string `json:"details,omitempty"`
	Recoverable bool   `json:"recoverable"`
}

// ConnectionStatus represents WebSocket connection status
type ConnectionStatus struct {
	Connected    bool      `json:"connected"`
	ClientID     string    `json:"clientId"`
	ConnectedAt  time.Time `json:"connectedAt,omitempty"`
	LastPing     time.Time `json:"lastPing,omitempty"`
	MessageCount int       `json:"messageCount"`
}

// HealthCheckResponse represents the health status of the service
type HealthCheckResponse struct {
	Status    string                   `json:"status"` // "healthy", "degraded", "unhealthy"
	Timestamp string                   `json:"timestamp"`
	Version   string                   `json:"version"`
	Services  map[string]ServiceStatus `json:"services"`
}

// ServiceStatus represents the status of a dependent service
type ServiceStatus struct {
	Name      string  `json:"name"`
	Status    string  `json:"status"`            // "up", "down", "degraded"
	Latency   float64 `json:"latency,omitempty"` // in milliseconds
	LastCheck string  `json:"lastCheck"`
	Error     string  `json:"error,omitempty"`
}

// Constants for message types
const (
	// WebSocket message types
	WSTypeLog        = "log"
	WSTypeError      = "error"
	WSTypeStatus     = "status"
	WSTypeHeartbeat  = "heartbeat"
	WSTypeConnection = "connection"

	// Agent log types
	LogTypeRouting  = "routing"
	LogTypePlanning = "planning"
	LogTypeMEDICAL  = "medical"
	LogTypeGateway  = "gateway"
	LogTypeSAGE     = "sage"
	LogTypeError    = "error"

	// Log levels
	LogLevelInfo    = "info"
	LogLevelWarning = "warning"
	LogLevelError   = "error"
	LogLevelDebug   = "debug"

	// Service status
	StatusHealthy   = "healthy"
	StatusDegraded  = "degraded"
	StatusUnhealthy = "unhealthy"
	StatusUp        = "up"
	StatusDown      = "down"
)

// Helper functions

// NewWebSocketMessage creates a new WebSocket message
func NewWebSocketMessage(msgType string, payload interface{}) *WebSocketMessage {
	return &WebSocketMessage{
		Type:      msgType,
		Payload:   payload,
		Timestamp: time.Now().Format(time.RFC3339),
		MessageID: generateMessageID(),
	}
}

// NewAgentLog creates a new agent log entry
func NewAgentLog(logType, from, content string) *AgentLog {
	return &AgentLog{
		Type:      logType,
		From:      from,
		Content:   content,
		Timestamp: time.Now().Format(time.RFC3339),
		MessageID: generateMessageID(),
		Level:     LogLevelInfo,
	}
}

// ToJSON converts the message to JSON
func (m *WebSocketMessage) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// ToJSON converts the log to JSON
func (l *AgentLog) ToJSON() ([]byte, error) {
	return json.Marshal(l)
}

// generateMessageID generates a unique message ID
func generateMessageID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), generateRandomString(8))
}

// generateRandomString generates a random string of specified length
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// SAGEStatus represents the current SAGE configuration status
type SAGEStatus struct {
	Enabled         bool            `json:"enabled"`
	AgentSigners    map[string]bool `json:"agentSigners,omitempty"`
	VerifierEnabled bool            `json:"verifierEnabled"`
}

// SAGETestResult represents the result of a SAGE test
type SAGETestResult struct {
	Success    bool              `json:"success"`
	Error      string            `json:"error,omitempty"`
	Stage      string            `json:"stage,omitempty"`
	SignedBy   string            `json:"signedBy,omitempty"`
	VerifiedBy string            `json:"verifiedBy,omitempty"`
	Details    map[string]string `json:"details,omitempty"`
}

// SAGEConfigRequest represents a request to configure SAGE settings
type SAGEConfigRequest struct {
	Enabled     *bool `json:"enabled,omitempty"`
	SkipOnError *bool `json:"skipOnError,omitempty"`
}
