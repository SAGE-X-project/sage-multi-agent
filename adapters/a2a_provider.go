package adapters

import (
    "fmt"
    "strings"

    "github.com/a2aproject/a2a-go/a2a"
    "github.com/sage-x-project/sage-multi-agent/config"
    "github.com/sage-x-project/sage/pkg/agent/core"
    "github.com/sage-x-project/sage/pkg/agent/did"
)

// A2AAgentCardProvider: a2a-go gRPC 핸들러에 제공할 AgentCard 생성기.
// SAGE DID/검증 구성은 유지합니다.
type A2AAgentCardProvider struct {
    card            *a2a.AgentCard
    verificationSvc *core.VerificationService
    didManager      *did.Manager
}

// NewA2AAgentCardProvider initializes DID/verification and prepares an a2a.AgentCard.
func NewA2AAgentCardProvider(name, url string) (*A2AAgentCardProvider, error) {
    envConfig, err := config.LoadEnv()
    if err != nil {
        return nil, fmt.Errorf("failed to load environment config: %w", err)
    }

    // Configure DID Manager for selected network
    network := envConfig.SageNetwork
    if network == "" { network = "local" }

    address, rpc, _, err := config.GetContractInfo(network, envConfig)
    if err != nil {
        return nil, fmt.Errorf("get contract info: %w", err)
    }

    dm := did.NewManager()
    chain := did.ChainEthereum
    switch strings.ToLower(network) {
    case "kaia", "klaytn", "kairos", "cypress":
        chain = did.Chain("kaia")
    }
    if err := dm.Configure(chain, &did.RegistryConfig{ContractAddress: address, RPCEndpoint: rpc}); err != nil {
        return nil, fmt.Errorf("configure did manager: %w", err)
    }

    vs := core.NewVerificationService(dm)

    card := &a2a.AgentCard{
        Name:               name,
        Description:        "SAGE multi-agent",
        URL:                url,
        PreferredTransport: a2a.TransportProtocolGRPC,
        DefaultInputModes:  []string{"text/plain"},
        DefaultOutputModes: []string{"text/plain"},
        Capabilities:       a2a.AgentCapabilities{Streaming: false},
    }

    return &A2AAgentCardProvider{card: card, verificationSvc: vs, didManager: dm}, nil
}

// Card returns an AgentCard for a2a-go handlers.
func (p *A2AAgentCardProvider) Card() *a2a.AgentCard { return p.card }

// GetDIDManager exposes initialized DID manager (used by SAGE flows elsewhere).
func (p *A2AAgentCardProvider) GetDIDManager() *did.Manager { return p.didManager }

// GetVerificationService exposes verification service.
func (p *A2AAgentCardProvider) GetVerificationService() *core.VerificationService { return p.verificationSvc }
