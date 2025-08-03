package sage

import (
	"fmt"
	"net/http"

	"github.com/sage-x-project/sage/core/rfc9421"
	"github.com/sage-x-project/sage/did"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
)

// SageProvider implements the auth.Provider interface
type SageProvider struct {
	// Add any fields you need here
	didManager *did.Manager
}

// Authenticate implements the auth.Provider interface
func (p *SageProvider) Authenticate(r *http.Request) (*auth.User, error) {
	verifier := rfc9421.NewHTTPVerifier()

	agentDID := r.Header.Get("X-Agent-DID")
	if agentDID == "" {	
		return nil, fmt.Errorf("missing X-Agent-DID header")
	}

	publicKey, err := p.didManager.ResolvePublicKey(r.Context(), did.AgentDID(agentDID))
	if err != nil {
		return nil, fmt.Errorf("Failed to resolve agent DID: %v", err)
	}
	err = verifier.VerifyRequest(r, publicKey, nil)
	if err != nil {
		return nil, fmt.Errorf("Invalid signature: %v", err)
	}

	// @Jenn: Do we need to check the agent capabilities?
	userID := agentDID
	return &auth.User{
		ID: userID,
	}, nil
}

func (p *SageProvider) ConfigureClient(client *http.Client) *http.Client {
	return client
}

// NewCustomProvider creates a new instance of CustomProvider
func NewSageProvider() *SageProvider {
	// Initialize DID manager
	didManager := did.NewManager()

	config := &did.RegistryConfig{
		ContractAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f7F1a",
		RPCEndpoint:     "https://eth-mainnet.example.com",
	}
	didManager.Configure(did.ChainEthereum, config)
	return &SageProvider{
		didManager: didManager,
	}
}

