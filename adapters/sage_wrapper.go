package adapters

import (
	"context"
	"fmt"

	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
)

// VerifyMessage verifies an AgentMessage using SAGE
func (sm *SAGEManager) VerifyMessage(msg *types.AgentMessage) (bool, error) {
	if !sm.enabled || sm.verifier == nil {
		return true, nil // Pass through if SAGE is disabled
	}

	// Convert AgentMessage to RFC9421 Message for verification
	rfcMsg := &rfc9421.Message{
		MessageID: msg.ID,
		AgentDID:  msg.From,
		Body:      []byte(msg.Content),
		Algorithm: string(rfc9421.AlgorithmECDSASecp256k1),
	}

	result, err := sm.verifier.VerifyMessage(context.Background(), rfcMsg)
	if err != nil {
		return false, err
	}

	return result.Verified, nil
}

// SignMessage signs an AgentMessage using SAGE
func (sm *SAGEManager) SignMessage(msg *types.AgentMessage) (*types.AgentMessage, error) {
	if !sm.enabled {
		return msg, nil // Return original message if SAGE is disabled
	}

	// Get signer for the agent (we need a verifierHelper instance)
	// For now, create a temporary one - in production this should be injected
	verifierHelper, err := NewVerifierHelper("keys", false)
	if err != nil {
		return nil, fmt.Errorf("failed to create verifier helper: %w", err)
	}

	signer, err := sm.GetOrCreateSigner(msg.From, verifierHelper)
	if err != nil {
		return nil, fmt.Errorf("failed to get signer: %w", err)
	}

	// Sign the message content
	signedMsg, err := signer.SignMessage(context.Background(), msg.Content, msg.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to sign message: %w", err)
	}

	// Update message with signature info
	if signedMsg != nil {
		msg.ID = signedMsg.MessageID
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]interface{})
		}
		msg.Metadata["signature"] = fmt.Sprintf("%x", signedMsg.Signature)
		msg.Metadata["algorithm"] = signedMsg.Algorithm
	}

	return msg, nil
}

// NewSAGEManagerWithKeyType creates a new SAGE manager with specified key type (simplified)
func NewSAGEManagerWithKeyType(agentName string, keyType string) (*SAGEManager, error) {
	// Create verifier helper
	verifierHelper, err := NewVerifierHelper("keys", false)
	if err != nil {
		return nil, fmt.Errorf("failed to create verifier helper: %w", err)
	}

	// Create SAGE manager
	manager, err := NewSAGEManager(verifierHelper)
	if err != nil {
		return nil, fmt.Errorf("failed to create SAGE manager: %w", err)
	}

	return manager, nil
}
