package adapters

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"time"

	"github.com/sage-x-project/sage/core/rfc9421"
	sagecrypto "github.com/sage-x-project/sage/crypto"
	"github.com/sage-x-project/sage/crypto/keys"
)

// A2ARequestHandler implements the trpc-a2a-go client.RequestHandler interface using SAGE
type A2ARequestHandler struct {
	httpVerifier *rfc9421.HTTPVerifier
	agentDID     string
	keyPair      sagecrypto.KeyPair
}

// NewA2ARequestHandler creates a new request handler with SAGE integration
func NewA2ARequestHandler(agentDID string) (*A2ARequestHandler, error) {
	// Generate or load key pair (Ed25519 for compatibility)
	keyPair, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	
	return &A2ARequestHandler{
		httpVerifier: rfc9421.NewHTTPVerifier(),
		agentDID:     agentDID,
		keyPair:      keyPair,
	}, nil
}

// NewA2ARequestHandlerWithKey creates a request handler with a specific key
func NewA2ARequestHandlerWithKey(agentDID string, keyPair sagecrypto.KeyPair) (*A2ARequestHandler, error) {
	return &A2ARequestHandler{
		httpVerifier: rfc9421.NewHTTPVerifier(),
		agentDID:     agentDID,
		keyPair:      keyPair,
	}, nil
}

// NewA2ARequestHandlerWithCryptoManager creates a request handler using crypto manager
func NewA2ARequestHandlerWithCryptoManager(agentDID string, cryptoManager *sagecrypto.Manager, keyID string) (*A2ARequestHandler, error) {
	// Load or generate key using crypto manager
	keyPair, err := cryptoManager.LoadKeyPair(keyID)
	if err != nil {
		// Generate new key if not found
		keyPair, err = cryptoManager.GenerateKeyPair(sagecrypto.KeyTypeEd25519)
		if err != nil {
			return nil, fmt.Errorf("failed to generate key: %w", err)
		}
		
		// Store the key
		if err := cryptoManager.StoreKeyPair(keyPair); err != nil {
			return nil, fmt.Errorf("failed to store key: %w", err)
		}
	}
	
	return &A2ARequestHandler{
		httpVerifier: rfc9421.NewHTTPVerifier(),
		agentDID:     agentDID,
		keyPair:      keyPair,
	}, nil
}

// Handle implements the client.RequestHandler interface
func (h *A2ARequestHandler) Handle(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var err error
	var resp *http.Response

	defer func() {
		if err != nil && resp != nil {
			resp.Body.Close()
		}
	}()

	if client == nil {
		return nil, fmt.Errorf("a2aRequestHandler: http client is nil")
	}

	// Set standard headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-DID", h.agentDID)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	// Prepare signature parameters
	params := &rfc9421.SignatureInputParams{
		CoveredComponents: []string{
			`"@method"`,
			`"@path"`,
			`"content-type"`,
			`"date"`,
			`"x-agent-did"`,
		},
		KeyID:     h.agentDID,
		Created:   time.Now().Unix(),
	}

	// Determine algorithm based on key type
	switch h.keyPair.Type() {
	case sagecrypto.KeyTypeEd25519:
		params.Algorithm = "ed25519"
	case sagecrypto.KeyTypeSecp256k1:
		params.Algorithm = "ecdsa-p256-sha256"
	default:
		return nil, fmt.Errorf("unsupported key type: %s", h.keyPair.Type())
	}

	// Get the private key for signing (must implement crypto.Signer)
	var signingKey crypto.Signer
	switch h.keyPair.Type() {
	case sagecrypto.KeyTypeEd25519:
		privateKey, ok := h.keyPair.PrivateKey().(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("invalid Ed25519 private key type")
		}
		signingKey = privateKey
	case sagecrypto.KeyTypeSecp256k1:
		// For secp256k1, the private key is an *ecdsa.PrivateKey which implements crypto.Signer
		privateKey, ok := h.keyPair.PrivateKey().(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("invalid Secp256k1 private key type")
		}
		signingKey = privateKey
	default:
		return nil, fmt.Errorf("unsupported key type for signing: %s", h.keyPair.Type())
	}

	// Sign the request using SAGE's HTTP verifier
	err = h.httpVerifier.SignRequest(req, "sig1", params, signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}
	
	// Send the request
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2aRequestHandler: http request failed: %w", err)
	}
	
	return resp, nil
}

// GetKeyPair returns the key pair used by this handler
func (h *A2ARequestHandler) GetKeyPair() sagecrypto.KeyPair {
	return h.keyPair
}

// GetAgentDID returns the agent DID
func (h *A2ARequestHandler) GetAgentDID() string {
	return h.agentDID
}