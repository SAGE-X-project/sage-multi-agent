package adapters

import (
	"context"

	"github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/did"
	"github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// EthereumResolverAdapter adapts EthereumClient to match the Resolver interface
type EthereumResolverAdapter struct {
	client *ethereum.EthereumClient
}

// NewEthereumResolverAdapter creates a new adapter
func NewEthereumResolverAdapter(client *ethereum.EthereumClient) *EthereumResolverAdapter {
	return &EthereumResolverAdapter{
		client: client,
	}
}

// Resolve retrieves agent metadata by DID
func (a *EthereumResolverAdapter) Resolve(ctx context.Context, agentDID did.AgentDID) (*did.AgentMetadata, error) {
	return a.client.Resolve(ctx, agentDID)
}

// ResolvePublicKey retrieves only the public key for an agent
func (a *EthereumResolverAdapter) ResolvePublicKey(ctx context.Context, agentDID did.AgentDID) (interface{}, error) {
	// Call the client's method and convert crypto.PublicKey to interface{}
	pubKey, err := a.client.ResolvePublicKey(ctx, agentDID)
	if err != nil {
		return nil, err
	}
	return pubKey, nil // crypto.PublicKey can be returned as interface{}
}

// ResolveKEMKey retrieves the KEM key (for RFC 9180) for an agent
func (a *EthereumResolverAdapter) ResolveKEMKey(ctx context.Context, agentDID did.AgentDID) (interface{}, error) {
    md, err := a.client.Resolve(ctx, agentDID)
    if err != nil {
        return nil, err
    }
    if !md.IsActive {
        return nil, did.ErrInactiveAgent
    }
    return md.PublicKEMKey, nil
}

// VerifyMetadata checks if the provided metadata matches the on-chain data
func (a *EthereumResolverAdapter) VerifyMetadata(ctx context.Context, agentDID did.AgentDID, metadata *did.AgentMetadata) (*did.VerificationResult, error) {
	return a.client.VerifyMetadata(ctx, agentDID, metadata)
}

// ListAgentsByOwner retrieves all agents owned by a specific address
func (a *EthereumResolverAdapter) ListAgentsByOwner(ctx context.Context, ownerAddress string) ([]*did.AgentMetadata, error) {
	return a.client.ListAgentsByOwner(ctx, ownerAddress)
}

// Search finds agents matching the given criteria
func (a *EthereumResolverAdapter) Search(ctx context.Context, criteria did.SearchCriteria) ([]*did.AgentMetadata, error) {
	return a.client.Search(ctx, criteria)
}

// Registry interface implementation

// Register registers a new agent on the blockchain
func (a *EthereumResolverAdapter) Register(ctx context.Context, req *did.RegistrationRequest) (*did.RegistrationResult, error) {
	return a.client.Register(ctx, req)
}

// Update updates agent metadata
func (a *EthereumResolverAdapter) Update(ctx context.Context, agentDID did.AgentDID, updates map[string]interface{}, keyPair crypto.KeyPair) error {
	return a.client.Update(ctx, agentDID, updates, keyPair)
}

// Deactivate deactivates an agent
func (a *EthereumResolverAdapter) Deactivate(ctx context.Context, agentDID did.AgentDID, keyPair crypto.KeyPair) error {
	return a.client.Deactivate(ctx, agentDID, keyPair)
}

// GetRegistrationStatus checks the status of a registration transaction
func (a *EthereumResolverAdapter) GetRegistrationStatus(ctx context.Context, txHash string) (*did.RegistrationResult, error) {
	return a.client.GetRegistrationStatus(ctx, txHash)
}
