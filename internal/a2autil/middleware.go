package a2autil

import (
	// Use internal agent framework instead of direct sage imports
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/did"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/middleware"
)

// DIDAuth wraps the a2a-go server middleware so callers don't depend on a2a-go directly.
type DIDAuth struct {
	Mw *server.DIDAuthMiddleware
}

// BuildDIDMiddleware creates DID authentication middleware using the agent framework.
// This replaces direct sage imports with the high-level agent framework.
//
// Environment variables (with defaults):
//   - ETH_RPC_URL: http://127.0.0.1:8545
//   - SAGE_REGISTRY_ADDRESS: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512
//   - SAGE_EXTERNAL_KEY: 0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a
//
// Parameters:
//   - optional: If true, requests without signatures are allowed
//
// Returns:
//   - *server.DIDAuthMiddleware: The initialized middleware
//   - error: Error if middleware cannot be created
func BuildDIDMiddleware(optional bool) (*server.DIDAuthMiddleware, error) {
	// Create DID resolver from environment variables (with defaults)
	resolver, err := did.NewResolverFromEnv()
	if err != nil {
		return nil, err
	}

	// Create DID authentication middleware
	auth, err := middleware.NewDIDAuth(middleware.Config{
		Resolver: resolver,
		Optional: optional,
	})
	if err != nil {
		return nil, err
	}

	return auth.GetUnderlying(), nil
}

// ComputeContentDigest makes RFC9421-compatible Content-Digest header (sha-256).
// This is re-exported from the middleware package for backward compatibility.
func ComputeContentDigest(body []byte) string {
	return middleware.ComputeContentDigest(body)
}
