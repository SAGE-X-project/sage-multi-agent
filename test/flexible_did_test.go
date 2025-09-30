package test

import (
	"testing"

	"github.com/sage-x-project/sage-multi-agent/adapters"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// TestFlexibleDIDSupport tests flexible DID support with different key types
func TestFlexibleDIDSupport(t *testing.T) {
	tests := []struct {
		name        string
		senderID    string
		hasDID      bool
		allowNonDID bool
		expectPass  bool
	}{
		{
			name:        "Message from DID entity",
			senderID:    "did:sage:0x123",
			hasDID:      true,
			allowNonDID: true,
			expectPass:  true,
		},
		{
			name:        "Message from non-DID entity (flexible mode)",
			senderID:    "client",
			hasDID:      false,
			allowNonDID: true,
			expectPass:  true,
		},
		{
			name:        "Message from non-DID entity (strict mode)",
			senderID:    "client",
			hasDID:      false,
			allowNonDID: false,
			expectPass:  false,
		},
		{
			name:        "Message from test entity",
			senderID:    "test",
			hasDID:      false,
			allowNonDID: true,
			expectPass:  true,
		},
		{
			name:        "Message from demo entity",
			senderID:    "demo",
			hasDID:      false,
			allowNonDID: true,
			expectPass:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create base SAGE manager
			baseSM := &adapters.SAGEManager{}

			// Create flexible SAGE manager
			fsm := adapters.NewFlexibleSAGEManager(baseSM)

			// Enable SAGE for strict mode testing
			if !tt.allowNonDID {
				fsm.SetEnabled(true)
			}

			fsm.SetAllowNonDID(tt.allowNonDID)

			// Create test message
			msg := &types.AgentMessage{
				ID:      "test_msg_1",
				From:    tt.senderID,
				To:      "receiver",
				Content: "Test message",
				Type:    "request",
			}

			// Verify message
			pass, err := fsm.VerifyMessage(msg)

			if tt.expectPass {
				if !pass {
					t.Errorf("Expected message to pass verification, but failed: %v", err)
				}
			} else {
				if pass {
					t.Errorf("Expected message to fail verification, but passed")
				}
			}
		})
	}
}

// TestFlexibleDIDModeSwitch tests switching between flexible and strict modes
func TestFlexibleDIDModeSwitch(t *testing.T) {
	baseSM := &adapters.SAGEManager{}
	fsm := adapters.NewFlexibleSAGEManager(baseSM)

	msg := &types.AgentMessage{
		ID:      "test_msg_2",
		From:    "client",
		To:      "agent",
		Content: "Test message",
		Type:    "request",
	}

	// Test flexible mode
	fsm.SetAllowNonDID(true)
	pass, err := fsm.VerifyMessage(msg)
	if !pass {
		t.Errorf("Message should pass in flexible mode, error: %v", err)
	}

	// Switch to strict mode
	fsm.SetAllowNonDID(false)
	pass, err = fsm.VerifyMessage(msg)
	if pass {
		t.Errorf("Message should fail in strict mode")
	}

	// Switch back to flexible mode
	fsm.SetAllowNonDID(true)
	pass, err = fsm.VerifyMessage(msg)
	if !pass {
		t.Errorf("Message should pass again in flexible mode, error: %v", err)
	}
}

// TestProcessMessageWithSAGE tests message processing with SAGE features
func TestProcessMessageWithSAGE(t *testing.T) {
	baseSM := &adapters.SAGEManager{}
	fsm := adapters.NewFlexibleSAGEManager(baseSM)
	fsm.SetAllowNonDID(true)

	tests := []struct {
		name     string
		senderID string
		hasDID   bool
	}{
		{
			name:     "Process DID message",
			senderID: "did:sage:0x123",
			hasDID:   true,
		},
		{
			name:     "Process non-DID message",
			senderID: "client",
			hasDID:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &types.AgentMessage{
				ID:      "test_msg_3",
				From:    tt.senderID,
				To:      "agent",
				Content: "Test message",
				Type:    "request",
			}

			processed, err := fsm.ProcessMessageWithSAGE(msg)
			if err != nil {
				t.Errorf("Failed to process message: %v", err)
			}
			if processed == nil {
				t.Errorf("Processed message should not be nil")
			}
		})
	}
}