package types

import (
	"time"
)

// AgentMessage represents a message between agents (simplified structure for agent APIs)
type AgentMessage struct {
	ID        string                 `json:"id"`
	From      string                 `json:"from"`
	To        string                 `json:"to"`
	Content   string                 `json:"content"`
	Timestamp time.Time              `json:"timestamp"`
	Type      string                 `json:"type"` // request, response, notification
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Note: AgentLog is defined in messages.go