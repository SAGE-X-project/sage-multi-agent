// Package middleware provides high-level abstractions for HTTP middleware.
// This package wraps sage-a2a-go's DID authentication middleware to provide
// a simpler API that integrates with the agent framework.
package middleware

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/did"
)

// DIDAuth provides DID-based authentication middleware for HTTP servers.
// This wraps sage-a2a-go's DIDAuthMiddleware to integrate with the agent framework.
type DIDAuth struct {
	// underlying is the sage-a2a-go middleware
	underlying *server.DIDAuthMiddleware
}

// Config contains configuration for DID authentication middleware.
type Config struct {
	// Resolver is the DID resolver to use for verification
	Resolver *did.Resolver

	// Optional indicates whether authentication is optional
	// If true, requests without valid signatures are allowed
	// If false, requests must have valid signatures
	Optional bool
}

// NewDIDAuth creates a new DID authentication middleware.
// This middleware verifies HTTP message signatures according to RFC 9421.
//
// Parameters:
//   - config: Middleware configuration
//
// Returns:
//   - *DIDAuth: The initialized middleware
//   - error: Error if middleware cannot be created
//
// Example:
//
//	auth, err := middleware.NewDIDAuth(middleware.Config{
//	    Resolver: resolver,
//	    Optional: false,
//	})
func NewDIDAuth(config Config) (*DIDAuth, error) {
	if config.Resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}

	// Create sage-a2a-go middleware using the resolver's underlying clients
	mw := server.NewDIDAuthMiddleware(
		config.Resolver.GetDIDClient(),
		config.Resolver.GetKeyClient(),
	)
	mw.SetOptional(config.Optional)

	return &DIDAuth{
		underlying: mw,
	}, nil
}

// GetUnderlying returns the underlying sage-a2a-go DIDAuthMiddleware.
// This is used for integration with HTTP servers.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, HTTP servers will accept this DIDAuth type directly.
//
// Returns:
//   - *server.DIDAuthMiddleware: The underlying middleware
func (d *DIDAuth) GetUnderlying() *server.DIDAuthMiddleware {
	return d.underlying
}

// ComputeContentDigest creates an RFC 9421-compatible Content-Digest header.
// This uses SHA-256 hashing as required by the SAGE protocol.
//
// Parameters:
//   - body: The HTTP request/response body bytes
//
// Returns:
//   - string: The Content-Digest header value (format: "sha-256=:base64:")
//
// Example:
//
//	digest := middleware.ComputeContentDigest(requestBody)
//	// digest = "sha-256=:uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek=:"
func ComputeContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	b64 := base64.StdEncoding.EncodeToString(sum[:])
	return fmt.Sprintf("sha-256=:%s:", b64)
}

// Future API methods (to be implemented when migrating to sage-a2a-go):
//
// Verify(r *http.Request) (*VerificationResult, error)
//   - Verifies an HTTP request's signature
//   - Returns verification result with DID, key ID, etc.
//
// Sign(r *http.Request, privateKey KeyPair, keyID string) error
//   - Signs an HTTP request with RFC 9421 signature
//
// Middleware() func(http.Handler) http.Handler
//   - Returns standard Go HTTP middleware function
//   - Can be used with any HTTP router/framework
