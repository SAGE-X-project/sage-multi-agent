package adapters

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/sage-multi-agent/config"
	"github.com/sage-multi-agent/types"
	"github.com/sage-x-project/sage/core"
	"github.com/sage-x-project/sage/core/rfc9421"
	"github.com/sage-x-project/sage/did"
)

// MessageVerifier provides message verification capabilities for agents
type MessageVerifier struct {
	verificationService *core.VerificationService
	didManager          *did.Manager
	agentConfig         *config.Config
	enabled             bool // SAGE verification enabled/disabled
	skipOnError         bool // Continue processing even if verification fails
}

// NewMessageVerifier creates a new message verifier
func NewMessageVerifier(verifierHelper *VerifierHelper) (*MessageVerifier, error) {
	// Load agent configuration
	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load agent config: %w", err)
	}

	// Create verification service
	verificationService := core.NewVerificationService(verifierHelper.GetDIDManager())

	return &MessageVerifier{
		verificationService: verificationService,
		didManager:          verifierHelper.GetDIDManager(),
		agentConfig:         agentConfig,
		enabled:             true,   // Default to enabled
		skipOnError:         false,  // Default to reject on error for security
	}, nil
}

// SetEnabled sets whether SAGE verification is enabled
func (mv *MessageVerifier) SetEnabled(enabled bool) {
	mv.enabled = enabled
	log.Printf("[SAGE] Verification %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])
}

// IsEnabled returns whether SAGE verification is enabled
func (mv *MessageVerifier) IsEnabled() bool {
	return mv.enabled
}

// SetSkipOnError sets whether to continue processing if verification fails
func (mv *MessageVerifier) SetSkipOnError(skip bool) {
	mv.skipOnError = skip
}

// VerifyMessage verifies a message signature using SAGE
func (mv *MessageVerifier) VerifyMessage(ctx context.Context, message *rfc9421.Message) (*types.SAGEVerificationResult, error) {
	result := &types.SAGEVerificationResult{
		Verified:       false,
		SignatureValid: false,
		Timestamp:      time.Now().Unix(),
		Details:        make(map[string]string),
	}

	// If verification is disabled, return success
	if !mv.enabled {
		result.Verified = true
		result.SignatureValid = true
		result.Details["status"] = "verification_disabled"
		return result, nil
	}

	// Set agent DID in result
	result.AgentDID = message.AgentDID

	// Verify the message with custom options
	opts := rfc9421.DefaultVerificationOptions()
	opts.VerifyMetadata = false // Don't verify metadata fields as they're agent properties, not message properties
	
	verifyResult, err := mv.verificationService.VerifyAgentMessage(
		ctx,
		message,
		opts,
	)
	
	if err != nil {
		result.Error = fmt.Sprintf("Verification failed: %v", err)
		result.Details["error"] = err.Error()
		
		if mv.skipOnError {
			log.Printf("[SAGE] Verification error (continuing): %v", err)
			return result, nil
		}
		log.Printf("[SAGE] Verification failed (rejecting message): %v", err)
		return result, fmt.Errorf("SAGE verification failed: %w", err)
	}

	// Update result based on verification
	result.Verified = verifyResult.Valid
	result.SignatureValid = verifyResult.Valid
	
	if verifyResult.Valid {
		result.Details["agent_name"] = verifyResult.AgentName
		result.Details["agent_owner"] = verifyResult.AgentOwner
		result.Details["verified_at"] = verifyResult.VerifiedAt.Format(time.RFC3339)
		
		// Add capabilities if present
		if verifyResult.Capabilities != nil {
			for k, v := range verifyResult.Capabilities {
				result.Details[fmt.Sprintf("capability_%s", k)] = fmt.Sprintf("%v", v)
			}
		}
		
		log.Printf("[SAGE] Message verified successfully from agent: %s (%s)", 
			verifyResult.AgentName, message.AgentDID)
	} else {
		result.Error = verifyResult.Error
		result.Details["verification_error"] = verifyResult.Error
		log.Printf("[SAGE] Message verification failed: %s", verifyResult.Error)
	}

	return result, nil
}

// VerifyRequestHeaders verifies an HTTP request with SAGE headers
func (mv *MessageVerifier) VerifyRequestHeaders(ctx context.Context, headers map[string]string, body []byte) (*types.SAGEVerificationResult, error) {
	result := &types.SAGEVerificationResult{
		Verified:       false,
		SignatureValid: false,
		Timestamp:      time.Now().Unix(),
		Details:        make(map[string]string),
	}

	// If verification is disabled, return success
	if !mv.enabled {
		result.Verified = true
		result.SignatureValid = true
		result.Details["status"] = "verification_disabled"
		return result, nil
	}

	// Extract required headers
	agentDID := headers["X-Agent-DID"]
	if agentDID == "" {
		result.Error = "Missing X-Agent-DID header"
		return result, nil
	}
	result.AgentDID = agentDID

	messageID := headers["X-Message-ID"]
	if messageID == "" {
		result.Error = "Missing X-Message-ID header"
		return result, nil
	}

	timestampStr := headers["X-Timestamp"]
	if timestampStr == "" {
		result.Error = "Missing X-Timestamp header"
		return result, nil
	}

	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		result.Error = fmt.Sprintf("Invalid timestamp format: %v", err)
		return result, nil
	}

	nonce := headers["X-Nonce"]
	if nonce == "" {
		result.Error = "Missing X-Nonce header"
		return result, nil
	}

	signatureHex := headers["X-Signature"]
	if signatureHex == "" {
		result.Error = "Missing X-Signature header"
		return result, nil
	}

	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		result.Error = fmt.Sprintf("Invalid signature format: %v", err)
		return result, nil
	}

	algorithm := headers["X-Signature-Algorithm"]
	if algorithm == "" {
		algorithm = string(rfc9421.AlgorithmECDSASecp256k1) // Default
	}

	// Create message from headers
	message := &rfc9421.Message{
		AgentDID:     agentDID,
		MessageID:    messageID,
		Timestamp:    timestamp,
		Nonce:        nonce,
		Body:         body,
		Signature:    signature,
		Algorithm:    algorithm,
		SignedFields: []string{"agent_did", "message_id", "timestamp", "nonce", "body"},
		Headers:      headers,
	}

	// Verify the message
	return mv.VerifyMessage(ctx, message)
}

// VerifyAgentToAgentMessage verifies a message between agents
func (mv *MessageVerifier) VerifyAgentToAgentMessage(
	ctx context.Context,
	fromAgent string,
	toAgent string,
	content string,
	signature []byte,
) (*types.SAGEVerificationResult, error) {
	// Get agent configurations
	fromCfg, exists := mv.agentConfig.Agents[fromAgent]
	if !exists {
		return &types.SAGEVerificationResult{
			Verified: false,
			Error:    fmt.Sprintf("Unknown agent: %s", fromAgent),
		}, nil
	}

	// Create message for verification
	message := &rfc9421.Message{
		AgentDID:     fromCfg.DID,
		Body:         []byte(content),
		Signature:    signature,
		Algorithm:    string(rfc9421.AlgorithmECDSASecp256k1),
		SignedFields: []string{"body"},
		Timestamp:    time.Now(), // Use current time for quick verification
		Metadata: map[string]interface{}{
			"from_agent": fromAgent,
			"to_agent":   toAgent,
		},
	}

	return mv.VerifyMessage(ctx, message)
}

// QuickVerify performs a quick signature verification without full metadata checks
func (mv *MessageVerifier) QuickVerify(ctx context.Context, agentDID string, message []byte, signature []byte) error {
	if !mv.enabled {
		return nil // Skip verification if disabled
	}

	return mv.verificationService.QuickVerify(ctx, agentDID, message, signature)
}