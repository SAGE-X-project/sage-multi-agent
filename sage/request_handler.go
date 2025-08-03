package sage

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"time"

	"github.com/sage-x-project/sage/core/rfc9421"
	"github.com/sage-x-project/sage/crypto/keys"
)

// SageHttpRequestHandler implements the client.RequestHandler interface
type SageHttpRequestHandler struct {
	verifier *rfc9421.HTTPVerifier
	agentDID string
	privateKey ed25519.PrivateKey
}

func (h *SageHttpRequestHandler) Handle(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var err error
	var resp *http.Response

	defer func() {
		if err != nil && resp != nil {
			resp.Body.Close()
		}
	}()

	if client == nil {
		return nil, fmt.Errorf("a2aClient.httpRequestHandler: http client is nil")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-DID", h.agentDID)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	// Sign the request with SAGE
	params := &rfc9421.SignatureInputParams{
		CoveredComponents: []string{
			`"@method"`,
			`"@path"`,
			`"content-type"`,
			`"date"`,
			`"x-agent-did"`,
		},
		KeyID:     h.agentDID,
		Algorithm: "ed25519",
		Created:   time.Now().Unix(),
	}

	err = h.verifier.SignRequest(req, "sig1", params, h.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2aClient.httpRequestHandler: http request failed: %w", err)
	}
	return resp, nil
}

func NewSageHttpRequestHandler(agentDID string) (*SageHttpRequestHandler, error) {
	// Generate or load key pair
	keyPair, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	privateKey, ok := keyPair.PrivateKey().(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("invalid private key type")
	}
	return &SageHttpRequestHandler{
		verifier:   rfc9421.NewHTTPVerifier(),
		agentDID:   agentDID,
		privateKey: privateKey,
	}, nil
}