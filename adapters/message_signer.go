package adapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sage-multi-agent/config"
	"github.com/sage-x-project/sage/core/rfc9421"
	"github.com/sage-x-project/sage/crypto"
	"github.com/sage-x-project/sage/did"
)

// MessageSigner provides message signing capabilities for agents
type MessageSigner struct {
	cryptoManager *crypto.Manager
	agentConfig   *config.Config
	agentType     string
	agentDID      string
	keyPair       crypto.KeyPair
	enabled       bool // SAGE signing enabled/disabled
}

// NewMessageSigner creates a new message signer for an agent
func NewMessageSigner(agentType string, verifierHelper *VerifierHelper) (*MessageSigner, error) {
	// Load agent configuration
	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load agent config: %w", err)
	}

	// Get agent configuration
	agentCfg, exists := agentConfig.Agents[agentType]
	if !exists {
		return nil, fmt.Errorf("agent type '%s' not found in configuration", agentType)
	}

	// Load agent's key
	keyPair, err := verifierHelper.LoadAgentKey(agentType)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent key: %w", err)
	}

	return &MessageSigner{
		cryptoManager: verifierHelper.GetCryptoManager(),
		agentConfig:   agentConfig,
		agentType:     agentType,
		agentDID:      agentCfg.DID,
		keyPair:       keyPair,
		enabled:       true, // Default to enabled
	}, nil
}

// SetEnabled sets whether SAGE signing is enabled
func (ms *MessageSigner) SetEnabled(enabled bool) {
	ms.enabled = enabled
}

// IsEnabled returns whether SAGE signing is enabled
func (ms *MessageSigner) IsEnabled() bool {
	return ms.enabled
}

// SignMessage signs a message using RFC-9421 format
func (ms *MessageSigner) SignMessage(ctx context.Context, content string, metadata map[string]interface{}) (*rfc9421.Message, error) {
	if !ms.enabled {
		return nil, nil // Return nil when signing is disabled
	}

	// Generate message ID and nonce
	messageID := ms.generateMessageID()
	nonce := ms.generateNonce()
	
	// Create RFC-9421 message
	message := &rfc9421.Message{
		AgentDID:  ms.agentDID,
		MessageID: messageID,
		Timestamp: time.Now(),
		Nonce:     nonce,
		Headers: map[string]string{
			"X-Agent-Type": ms.agentType,
			"X-Agent-DID":  ms.agentDID,
		},
		Body:      []byte(content),
		Algorithm: ms.getSignatureAlgorithm(),
		KeyID:     ms.keyPair.ID(),
		SignedFields: []string{
			"agent_did",
			"message_id", 
			"timestamp",
			"nonce",
			"body",
		},
		Metadata: metadata,
	}

	// Construct signature base
	signatureBase := ms.constructSignatureBase(message)
	
	// Sign the message
	signature, err := ms.keyPair.Sign([]byte(signatureBase))
	if err != nil {
		return nil, fmt.Errorf("failed to sign message: %w", err)
	}
	
	message.Signature = signature
	
	return message, nil
}

// SignRequest signs an HTTP request for agent-to-agent communication
func (ms *MessageSigner) SignRequest(ctx context.Context, method, path string, body []byte) (map[string]string, error) {
	if !ms.enabled {
		return nil, nil // Return nil when signing is disabled
	}

	// Create headers for signed request
	headers := map[string]string{
		"X-Agent-DID":       ms.agentDID,
		"X-Agent-Type":      ms.agentType,
		"X-Message-ID":      ms.generateMessageID(),
		"X-Timestamp":       time.Now().Format(time.RFC3339),
		"X-Nonce":          ms.generateNonce(),
		"X-Signature-Input": "agent_did,message_id,timestamp,nonce,body",
	}

	// Create message for signing
	message := &rfc9421.Message{
		AgentDID:     ms.agentDID,
		MessageID:    headers["X-Message-ID"],
		Timestamp:    time.Now(),
		Nonce:        headers["X-Nonce"],
		Body:         body,
		Algorithm:    ms.getSignatureAlgorithm(),
		SignedFields: []string{"agent_did", "message_id", "timestamp", "nonce", "body"},
	}

	// Construct and sign
	signatureBase := ms.constructSignatureBase(message)
	signature, err := ms.keyPair.Sign([]byte(signatureBase))
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	// Add signature to headers
	headers["X-Signature"] = hex.EncodeToString(signature)
	headers["X-Signature-Algorithm"] = string(ms.getSignatureAlgorithm())

	return headers, nil
}

// constructSignatureBase builds the signature base string according to RFC-9421
func (ms *MessageSigner) constructSignatureBase(msg *rfc9421.Message) string {
	verifier := rfc9421.NewVerifier()
	return verifier.ConstructSignatureBase(msg)
}

// getSignatureAlgorithm returns the signature algorithm based on the DID chain
func (ms *MessageSigner) getSignatureAlgorithm() string {
	chain, _, err := did.ParseDID(did.AgentDID(ms.agentDID))
	if err != nil {
		// Default to ECDSA for Ethereum
		return string(rfc9421.AlgorithmECDSASecp256k1)
	}

	switch chain {
	case did.ChainEthereum:
		return string(rfc9421.AlgorithmECDSASecp256k1)
	case did.ChainSolana:
		return string(rfc9421.AlgorithmEdDSA)
	default:
		return string(rfc9421.AlgorithmECDSASecp256k1)
	}
}

// generateMessageID generates a unique message ID
func (ms *MessageSigner) generateMessageID() string {
	return fmt.Sprintf("%s-%d-%s", ms.agentType, time.Now().UnixNano(), ms.generateNonce()[:8])
}

// generateNonce generates a random nonce
func (ms *MessageSigner) generateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}