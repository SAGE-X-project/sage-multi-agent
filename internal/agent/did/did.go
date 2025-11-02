// Package did provides high-level abstractions for DID resolution.
// This package wraps sage's DID client and sage-a2a-go's registry client
// to provide a unified API that can be easily migrated to sage-a2a-go.
package did

import (
	"fmt"

	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
	"github.com/sage-x-project/sage-a2a-go/pkg/registry"
)

// Resolver provides DID resolution and public key retrieval.
// This abstraction combines sage's DID client and sage-a2a-go's registry client.
type Resolver struct {
	// didClient resolves DIDs to DID documents (using AgentCardClient from registry)
	didClient *dideth.AgentCardClient

	// keyClient retrieves public keys for signature verification
	keyClient *dideth.EthereumClient

	// registryClient handles agent registration
	registryClient *registry.RegistrationClient
}

// Config contains configuration for creating a DID resolver.
type Config struct {
	// RPCEndpoint is the Ethereum RPC endpoint URL
	RPCEndpoint string

	// ContractAddress is the SAGE registry contract address
	ContractAddress string

	// PrivateKey is the operator's private key (hex string, with or without 0x prefix)
	// This is used for write operations to the registry
	PrivateKey string
}

// NewResolver creates a new DID resolver with the provided configuration.
// This initializes both the DID resolution client and the registry client.
//
// Parameters:
//   - config: DID resolver configuration
//
// Returns:
//   - *Resolver: The initialized DID resolver
//   - error: Error if clients cannot be created
//
// Example:
//
//	resolver, err := did.NewResolver(did.Config{
//	    RPCEndpoint:     "http://127.0.0.1:8545",
//	    ContractAddress: "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512",
//	    PrivateKey:      "0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a",
//	})
func NewResolver(config Config) (*Resolver, error) {
	if config.RPCEndpoint == "" {
		return nil, fmt.Errorf("RPC endpoint is required")
	}
	if config.ContractAddress == "" {
		return nil, fmt.Errorf("contract address is required")
	}
	if config.PrivateKey == "" {
		return nil, fmt.Errorf("private key is required")
	}

	// Create registry client for DID resolution
	registryClient, err := registry.NewRegistrationClient(&registry.ClientConfig{
		RPCURL:          config.RPCEndpoint,
		RegistryAddress: config.ContractAddress,
		PrivateKey:      config.PrivateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create registry client: %w", err)
	}

	// Create Ethereum client for public key resolution
	keyClient, err := dideth.NewEthereumClient(&sagedid.RegistryConfig{
		RPCEndpoint:     config.RPCEndpoint,
		ContractAddress: config.ContractAddress,
	})
	if err != nil {
		return nil, fmt.Errorf("create key client: %w", err)
	}

	// Get SAGE DID client from registry wrapper
	didClient := registryClient.GetSAGEClient()

	return &Resolver{
		didClient:      didClient,
		keyClient:      keyClient,
		registryClient: registryClient,
	}, nil
}

// NewResolverFromEnv creates a new DID resolver from environment variables.
// This is a convenience method for production environments.
//
// Environment variables:
//   - ETH_RPC_URL: Ethereum RPC endpoint (default: http://127.0.0.1:8545)
//   - SAGE_REGISTRY_ADDRESS: Registry contract address (default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
//   - SAGE_EXTERNAL_KEY: Operator private key
//
// Returns:
//   - *Resolver: The initialized DID resolver
//   - error: Error if clients cannot be created
//
// Example:
//
//	resolver, err := did.NewResolverFromEnv()
func NewResolverFromEnv() (*Resolver, error) {
	return newResolverFromEnvWithDefaults()
}

// GetDIDClient returns the underlying SAGE DID client.
// This is used for HPKE server integration which requires sage's DID client.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, HPKE server will accept this Resolver type directly.
//
// Returns:
//   - *dideth.AgentCardClient: The underlying SAGE DID client
func (r *Resolver) GetDIDClient() *dideth.AgentCardClient {
	return r.didClient
}

// GetKeyClient returns the underlying Ethereum key client.
// This is used for middleware integration which requires sage's key client.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, middleware will accept this Resolver type directly.
//
// Returns:
//   - *dideth.EthereumClient: The underlying Ethereum client
func (r *Resolver) GetKeyClient() *dideth.EthereumClient {
	return r.keyClient
}

// GetRegistryClient returns the underlying registry client.
// This is used for agent registration operations.
//
// Returns:
//   - *registry.RegistrationClient: The underlying registry client
func (r *Resolver) GetRegistryClient() *registry.RegistrationClient {
	return r.registryClient
}

// Future API methods (to be implemented when migrating to sage-a2a-go):
//
// Resolve(did string) (*DIDDocument, error)
//   - Resolves a DID to its DID document
//
// GetPublicKey(did string, keyID string) (PublicKey, error)
//   - Retrieves a specific public key from a DID document
//
// VerifySignature(did string, message []byte, signature []byte) error
//   - Verifies a signature using the DID's public key
//
// Register(did string, document *DIDDocument) error
//   - Registers a new DID document in the registry
//
// Update(did string, document *DIDDocument) error
//   - Updates an existing DID document
