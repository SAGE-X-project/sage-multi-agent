package adapters

import (
	"fmt"
	"log"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// FlexibleSAGEManager wraps the original SAGEManager with flexible DID handling
type FlexibleSAGEManager struct {
	*SAGEManager
	allowNonDID bool // Allow messages from non-DID entities
}

// NewFlexibleSAGEManager creates a new flexible SAGE manager
func NewFlexibleSAGEManager(sm *SAGEManager) *FlexibleSAGEManager {
	return &FlexibleSAGEManager{
		SAGEManager: sm,
		allowNonDID: true, // Default to allowing non-DID messages
	}
}

// VerifyMessage verifies a message with flexible DID handling
func (fsm *FlexibleSAGEManager) VerifyMessage(msg *types.AgentMessage) (bool, error) {
	// Check if sender has DID
	hasDID := fsm.hasRegisteredDID(msg.From)

	if !hasDID {
		if fsm.allowNonDID {
			// Allow message but log warning
			log.Printf("[SAGE] Warning: Message from non-DID entity '%s' - allowing in flexible mode", msg.From)
			return true, nil
		} else {
			// Strict mode - reject non-DID messages
			return false, fmt.Errorf("DID not found for entity: %s", msg.From)
		}
	}

	// If SAGEManager is nil or SAGE is disabled, allow messages with DID
	if fsm.SAGEManager == nil || !fsm.enabled {
		return true, nil
	}

	// If DID exists, perform full SAGE verification
	return fsm.SAGEManager.VerifyMessage(msg)
}

// hasRegisteredDID checks if an entity has a registered DID
func (fsm *FlexibleSAGEManager) hasRegisteredDID(entityID string) bool {
	// Special cases for known entities without DID
	knownNonDIDEntities := []string{"client", "user", "test", "demo"}
	for _, entity := range knownNonDIDEntities {
		if entityID == entity {
			return false
		}
	}

	// Check if DID exists in blockchain (simplified check)
	// In real implementation, this would query the blockchain
	if fsm.SAGEManager != nil && fsm.verifier != nil && fsm.verifier.didManager != nil {
		// Try to resolve DID - check if entity has a registered DID
		// For now, we'll assume entities with "did:" prefix have DIDs
		if strings.HasPrefix(entityID, "did:") {
			return true
		}
	}

	return false
}

// SetAllowNonDID sets whether to allow non-DID messages
func (fsm *FlexibleSAGEManager) SetAllowNonDID(allow bool) {
	fsm.allowNonDID = allow
	if allow {
		log.Println("[SAGE] Flexible mode enabled - allowing non-DID messages")
	} else {
		log.Println("[SAGE] Strict mode enabled - requiring DID for all messages")
	}
}

// ProcessMessageWithSAGE processes a message with SAGE features when available
func (fsm *FlexibleSAGEManager) ProcessMessageWithSAGE(msg *types.AgentMessage) (*types.AgentMessage, error) {
	hasDID := fsm.hasRegisteredDID(msg.From)

	if !hasDID && fsm.allowNonDID {
		// Process without SAGE features
		log.Printf("[SAGE] Processing message from '%s' without SAGE features", msg.From)
		return msg, nil
	}

	// Process with full SAGE features
	return fsm.SAGEManager.SignMessage(msg)
}