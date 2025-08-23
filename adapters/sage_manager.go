package adapters

import (
	"context"
	"log"
	"sync"

	"github.com/sage-multi-agent/types"
)

// SAGEManager manages SAGE signing and verification for the entire system
type SAGEManager struct {
	signers    map[string]*MessageSigner
	verifier   *MessageVerifier
	enabled    bool
	mu         sync.RWMutex
}

// NewSAGEManager creates a new SAGE manager
func NewSAGEManager(verifierHelper *VerifierHelper) (*SAGEManager, error) {
	// Create verifier
	verifier, err := NewMessageVerifier(verifierHelper)
	if err != nil {
		return nil, err
	}

	return &SAGEManager{
		signers:  make(map[string]*MessageSigner),
		verifier: verifier,
		enabled:  true, // Default to enabled
	}, nil
}

// GetOrCreateSigner gets or creates a signer for an agent
func (sm *SAGEManager) GetOrCreateSigner(agentType string, verifierHelper *VerifierHelper) (*MessageSigner, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if signer already exists
	if signer, exists := sm.signers[agentType]; exists {
		return signer, nil
	}

	// Create new signer
	signer, err := NewMessageSigner(agentType, verifierHelper)
	if err != nil {
		return nil, err
	}

	// Set current enabled state
	signer.SetEnabled(sm.enabled)
	
	// Store signer
	sm.signers[agentType] = signer
	
	log.Printf("[SAGE] Created signer for agent: %s", agentType)
	return signer, nil
}

// GetVerifier returns the message verifier
func (sm *SAGEManager) GetVerifier() *MessageVerifier {
	return sm.verifier
}

// SetEnabled enables or disables SAGE for all agents
func (sm *SAGEManager) SetEnabled(enabled bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.enabled = enabled
	
	// Update all existing signers
	for agentType, signer := range sm.signers {
		signer.SetEnabled(enabled)
		log.Printf("[SAGE] %s signing for agent: %s", 
			map[bool]string{true: "Enabled", false: "Disabled"}[enabled], agentType)
	}
	
	// Update verifier
	sm.verifier.SetEnabled(enabled)
	
	log.Printf("[SAGE] System-wide SAGE %s", 
		map[bool]string{true: "enabled", false: "disabled"}[enabled])
}

// IsEnabled returns whether SAGE is enabled
func (sm *SAGEManager) IsEnabled() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.enabled
}

// GetStatus returns the current SAGE status
func (sm *SAGEManager) GetStatus() *types.SAGEStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	agentStatuses := make(map[string]bool)
	for agentType, signer := range sm.signers {
		agentStatuses[agentType] = signer.IsEnabled()
	}

	return &types.SAGEStatus{
		Enabled:       sm.enabled,
		AgentSigners:  agentStatuses,
		VerifierEnabled: sm.verifier.IsEnabled(),
	}
}

// SignAndVerifyTest performs a test signing and verification
func (sm *SAGEManager) SignAndVerifyTest(ctx context.Context, agentType string, verifierHelper *VerifierHelper) (*types.SAGETestResult, error) {
	// Get or create signer
	signer, err := sm.GetOrCreateSigner(agentType, verifierHelper)
	if err != nil {
		return &types.SAGETestResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Test message
	testContent := "SAGE test message"
	testMetadata := map[string]interface{}{
		"test": true,
		"type": "verification_test",
	}

	// Sign the message
	message, err := signer.SignMessage(ctx, testContent, testMetadata)
	if err != nil {
		return &types.SAGETestResult{
			Success: false,
			Error:   err.Error(),
			Stage:   "signing",
		}, nil
	}

	// If signing is disabled, message will be nil
	if message == nil {
		return &types.SAGETestResult{
			Success: true,
			Stage:   "signing_disabled",
			Details: map[string]string{
				"sage_enabled": "false",
			},
		}, nil
	}

	// Verify the message
	verifyResult, err := sm.verifier.VerifyMessage(ctx, message)
	if err != nil {
		return &types.SAGETestResult{
			Success: false,
			Error:   err.Error(),
			Stage:   "verification",
		}, nil
	}

	return &types.SAGETestResult{
		Success:      verifyResult.Verified,
		Stage:        "complete",
		SignedBy:     message.AgentDID,
		VerifiedBy:   agentType,
		Details:      verifyResult.Details,
	}, nil
}