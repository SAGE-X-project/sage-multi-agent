package adapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sage-multi-agent/config"
	"github.com/sage-x-project/sage/core"
	"github.com/sage-x-project/sage/core/rfc9421"
	"github.com/sage-x-project/sage/did"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/log"
)

// A2AProvider implements the trpc-a2a-go auth.Provider interface using SAGE
type A2AProvider struct {
	verificationService *core.VerificationService
	didManager          *did.Manager
	agentConfig         *config.Config
	envConfig           *config.EnvConfig
}

// NewA2AProvider creates a new A2A provider with SAGE integration
func NewA2AProvider() (*A2AProvider, error) {
	// Load configurations
	envConfig, err := config.LoadEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}
	
	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		log.Warn("Failed to load agent config, continuing without it: %v", err)
		agentConfig = nil
	}
	
	// Initialize DID manager
	didManager := did.NewManager()
	
	// Get network from environment or agent config
	network := envConfig.SageNetwork // Use network from env
	if network == "" {
		network = "local" // Default to local if not set
	}
	if agentConfig != nil && agentConfig.Network.Chain != "" {
		network = agentConfig.Network.Chain // Override with agent config if provided
	}
	
	contractAddr, rpcEndpoint, _, err := config.GetContractInfo(network, envConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get contract info for network %s: %w", network, err)
	}
	
	// Configure DID manager for the blockchain
	chain := did.ChainEthereum // Default to Ethereum
	switch strings.ToLower(network) {
	case "kaia", "klaytn", "kairos", "cypress":
		chain = did.Chain("kaia")
	case "local", "localhost", "hardhat":
		chain = did.ChainEthereum // Local uses Ethereum chain type
	default:
		chain = did.ChainEthereum
	}
	
	registryConfig := &did.RegistryConfig{
		ContractAddress: contractAddr,
		RPCEndpoint:     rpcEndpoint,
	}
	
	if err := didManager.Configure(chain, registryConfig); err != nil {
		return nil, fmt.Errorf("failed to configure DID manager: %w", err)
	}
	
	// Create verification service
	verificationService := core.NewVerificationService(didManager)
	
	return &A2AProvider{
		verificationService: verificationService,
		didManager:          didManager,
		agentConfig:         agentConfig,
		envConfig:           envConfig,
	}, nil
}

// Authenticate implements the auth.Provider interface with SAGE verification
func (p *A2AProvider) Authenticate(r *http.Request) (*auth.User, error) {
	agentDID := r.Header.Get("X-Agent-DID")
	if agentDID == "" {
		log.Error("Authentication failed: missing X-Agent-DID header")
		return nil, fmt.Errorf("missing X-Agent-DID header")
	}

	// Try to resolve the public key with retry logic
	var publicKey interface{}
	var err error
	maxRetries := 3
	
	// Use context with timeout for DID resolution
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	
	for i := 0; i < maxRetries; i++ {
		publicKey, err = p.didManager.ResolvePublicKey(ctx, did.AgentDID(agentDID))
		if err == nil {
			break
		}
		
		if i < maxRetries-1 {
			log.Warn("Failed to resolve DID %s (attempt %d/%d): %v", agentDID, i+1, maxRetries, err)
			time.Sleep(time.Second * time.Duration(i+1)) // Exponential backoff
		}
	}
	
	if err != nil {
		log.Error("Failed to resolve agent DID %s after %d attempts: %v", agentDID, maxRetries, err)
		return nil, fmt.Errorf("failed to resolve agent DID: %v", err)
	}

	// Verify the request signature using SAGE's HTTP verifier
	httpVerifier := rfc9421.NewHTTPVerifier()
	if err := httpVerifier.VerifyRequest(r, publicKey, nil); err != nil {
		log.Error("Invalid signature for agent %s: %v", agentDID, err)
		return nil, fmt.Errorf("invalid signature: %v", err)
	}

	// Check agent capabilities if configured
	if p.agentConfig != nil {
		if agentCfg, err := p.agentConfig.GetAgentByDID(agentDID); err == nil {
			// Validate capabilities against request if needed
			log.Info("Authenticated agent %s (%s) with capabilities: %v", 
				agentDID, agentCfg.Name, agentCfg.Capabilities.Skills)
		}
	}

	return &auth.User{
		ID: agentDID,
		// Note: Additional fields can be added when auth.User struct supports them
	}, nil
}

// ConfigureClient configures the HTTP client if needed
func (p *A2AProvider) ConfigureClient(client *http.Client) *http.Client {
	// Add any client configuration if needed
	return client
}

// GetDIDManager returns the DID manager instance
func (p *A2AProvider) GetDIDManager() *did.Manager {
	return p.didManager
}

// GetVerificationService returns the verification service instance
func (p *A2AProvider) GetVerificationService() *core.VerificationService {
	return p.verificationService
}