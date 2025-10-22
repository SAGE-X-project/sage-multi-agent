package a2autil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"

	sagesigner "github.com/sage-x-project/sage-a2a-go/pkg/signer"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// ComputeContentDigest makes RFC9421-compatible Content-Digest header (sha-256).
func ComputeContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	b64 := base64.StdEncoding.EncodeToString(sum[:])
	return fmt.Sprintf("sha-256=:%s:", b64)
}

// SignedHTTPClient uses a custom A2A signer to sign requests, then sends via http.Client.
type SignedHTTPClient struct {
	DID        did.AgentDID
	KeyPair    sagecrypto.KeyPair
	Signer     sagesigner.A2ASigner
	HTTPClient *http.Client
}

func NewSignedHTTPClient(did did.AgentDID, kp sagecrypto.KeyPair, signer sagesigner.A2ASigner, hc *http.Client) *SignedHTTPClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &SignedHTTPClient{DID: did, KeyPair: kp, Signer: signer, HTTPClient: hc}
}

func (c *SignedHTTPClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if err := c.Signer.SignRequest(ctx, req, c.DID, c.KeyPair); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}
	return c.HTTPClient.Do(req)
}

func (c *SignedHTTPClient) PostJSON(ctx context.Context, url string, body []byte) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return c.Do(ctx, r)
}
