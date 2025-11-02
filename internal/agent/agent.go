// Package agent provides a high-level framework for building SAGE protocol agents.
// This package abstracts all low-level sage types and provides a clean API
// that can be directly migrated to sage-a2a-go.
//
// The goal is to eliminate direct sage imports from agent code, allowing agents
// to focus on business logic while the framework handles crypto, DID resolution,
// and HPKE encryption.
package agent

import (
	"context"
	"fmt"
	"os"

	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	sagehttp "github.com/sage-x-project/sage/pkg/agent/transport/http"
	transport "github.com/sage-x-project/sage/pkg/agent/transport"

	agentdid "github.com/sage-x-project/sage-multi-agent/internal/agent/did"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/hpke"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/keys"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/middleware"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/session"
)

// Agent represents a high-level SAGE protocol agent.
// It encapsulates all cryptographic operations, DID resolution, and HPKE encryption,
// allowing business logic to focus on message handling.
type Agent struct {
	// Configuration
	name string
	did  sagedid.AgentDID

	// Core components
	keys           *keys.KeySet
	resolver       *agentdid.Resolver
	sessionManager *session.Manager
	middleware     *middleware.DIDAuth

	// HPKE components (optional, based on config)
	hpkeServer *hpke.Server
	hpkeClient *hpke.Client

	// HTTP server (for receiving messages)
	httpServer *sagehttp.HTTPServer
}

// Config contains configuration for creating an agent.
type Config struct {
	// Name is the agent's identifier (e.g., "payment", "medical")
	Name string

	// DID is the agent's decentralized identifier
	DID string

	// Key file paths
	SigningKeyFile string // Path to signing key JWK file
	KEMKeyFile     string // Path to KEM key JWK file (required if HPKEEnabled is true)

	// DID resolver configuration
	RPCEndpoint     string // Ethereum RPC endpoint
	ContractAddress string // SAGE registry contract address
	PrivateKey      string // Operator private key for registry operations

	// Optional features
	HPKEEnabled      bool // Enable HPKE encryption
	RequireSignature bool // Require HTTP signature verification
}

// NewAgent creates a new high-level SAGE protocol agent.
// This is the recommended method for initializing agents.
//
// Parameters:
//   - config: Agent configuration
//
// Returns:
//   - *Agent: The initialized agent
//   - error: Error if initialization fails
//
// Example:
//
//	agent, err := agent.NewAgent(agent.Config{
//	    Name:             "payment",
//	    DID:              "did:sage:payment",
//	    SigningKeyFile:   "/keys/payment_sign.jwk",
//	    KEMKeyFile:       "/keys/payment_kem.jwk",
//	    RPCEndpoint:      "http://127.0.0.1:8545",
//	    ContractAddress:  "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512",
//	    PrivateKey:       "0x47e179...",
//	    HPKEEnabled:      true,
//	    RequireSignature: true,
//	})
func NewAgent(config Config) (*Agent, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if config.DID == "" {
		return nil, fmt.Errorf("agent DID is required")
	}
	if config.SigningKeyFile == "" {
		return nil, fmt.Errorf("signing key file is required")
	}

	// Load keys
	keySet, err := keys.LoadKeySet(keys.KeyConfig{
		SigningKeyFile: config.SigningKeyFile,
		KEMKeyFile:     config.KEMKeyFile,
	})
	if err != nil {
		return nil, fmt.Errorf("load keys: %w", err)
	}

	// Create DID resolver
	resolver, err := agentdid.NewResolver(agentdid.Config{
		RPCEndpoint:     config.RPCEndpoint,
		ContractAddress: config.ContractAddress,
		PrivateKey:      config.PrivateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create resolver: %w", err)
	}

	// Create session manager
	sessionMgr := session.NewManager()

	// Create middleware
	mw, err := middleware.NewDIDAuth(middleware.Config{
		Resolver: resolver,
		Optional: !config.RequireSignature,
	})
	if err != nil {
		return nil, fmt.Errorf("create middleware: %w", err)
	}

	agent := &Agent{
		name:           config.Name,
		did:            sagedid.AgentDID(config.DID),
		keys:           keySet,
		resolver:       resolver,
		sessionManager: sessionMgr,
		middleware:     mw,
	}

	// Create HPKE server if enabled
	if config.HPKEEnabled {
		if config.KEMKeyFile == "" {
			return nil, fmt.Errorf("KEM key file is required when HPKE is enabled")
		}

		hpkeSrv, err := hpke.NewServer(hpke.ServerConfig{
			SigningKey:     keySet.SigningKey,
			KEMKey:         keySet.KEMKey,
			DID:            agent.did,
			Resolver:       resolver,
			SessionManager: sessionMgr,
		})
		if err != nil {
			return nil, fmt.Errorf("create HPKE server: %w", err)
		}

		agent.hpkeServer = hpkeSrv

		// Create HTTP server with HPKE handler
		agent.httpServer = sagehttp.NewHTTPServer(func(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
			return agent.hpkeServer.HandleMessage(ctx, msg)
		})
	}

	return agent, nil
}

// NewAgentFromEnv creates a new agent from environment variables.
// This is a convenience method for production deployments.
//
// Environment variables:
//   - {PREFIX}_DID: Agent DID
//   - {PREFIX}_JWK_FILE: Path to signing key JWK file
//   - {PREFIX}_KEM_JWK_FILE: Path to KEM key JWK file
//   - ETH_RPC_URL: Ethereum RPC endpoint (default: http://127.0.0.1:8545)
//   - SAGE_REGISTRY_ADDRESS: Registry contract (default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
//   - SAGE_EXTERNAL_KEY: Operator private key
//
// Parameters:
//   - name: Agent name (e.g., "payment")
//   - prefix: Environment variable prefix (e.g., "PAYMENT")
//   - hpkeEnabled: Whether to enable HPKE
//   - requireSignature: Whether to require signature verification
//
// Returns:
//   - *Agent: The initialized agent
//   - error: Error if initialization fails
//
// Example:
//
//	// With environment variables:
//	// PAYMENT_DID=did:sage:payment
//	// PAYMENT_JWK_FILE=/keys/payment_sign.jwk
//	// PAYMENT_KEM_JWK_FILE=/keys/payment_kem.jwk
//	agent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, true)
func NewAgentFromEnv(name, prefix string, hpkeEnabled, requireSignature bool) (*Agent, error) {
	didStr := os.Getenv(prefix + "_DID")
	if didStr == "" {
		return nil, fmt.Errorf("%s_DID environment variable is not set", prefix)
	}

	signKeyFile := os.Getenv(prefix + "_JWK_FILE")
	if signKeyFile == "" {
		return nil, fmt.Errorf("%s_JWK_FILE environment variable is not set", prefix)
	}

	kemKeyFile := os.Getenv(prefix + "_KEM_JWK_FILE")
	if hpkeEnabled && kemKeyFile == "" {
		return nil, fmt.Errorf("%s_KEM_JWK_FILE environment variable is required when HPKE is enabled", prefix)
	}

	// DID resolver config from environment (with defaults)
	rpcEndpoint := os.Getenv("ETH_RPC_URL")
	if rpcEndpoint == "" {
		rpcEndpoint = "http://127.0.0.1:8545"
	}

	contractAddress := os.Getenv("SAGE_REGISTRY_ADDRESS")
	if contractAddress == "" {
		contractAddress = "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"
	}

	privateKey := os.Getenv("SAGE_EXTERNAL_KEY")
	if privateKey == "" {
		privateKey = "0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a"
	}

	return NewAgent(Config{
		Name:             name,
		DID:              didStr,
		SigningKeyFile:   signKeyFile,
		KEMKeyFile:       kemKeyFile,
		RPCEndpoint:      rpcEndpoint,
		ContractAddress:  contractAddress,
		PrivateKey:       privateKey,
		HPKEEnabled:      hpkeEnabled,
		RequireSignature: requireSignature,
	})
}

// GetName returns the agent's name.
func (a *Agent) GetName() string {
	return a.name
}

// GetDID returns the agent's DID.
func (a *Agent) GetDID() sagedid.AgentDID {
	return a.did
}

// GetHTTPServer returns the HTTP server for receiving messages.
// This is used to integrate the agent with HTTP routers.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, agents will have a unified HTTP interface.
//
// Returns:
//   - *sagehttp.HTTPServer: The HTTP server instance (nil if HPKE is disabled)
func (a *Agent) GetHTTPServer() *sagehttp.HTTPServer {
	return a.httpServer
}

// GetHPKEServer returns the HPKE server instance.
// This is used for custom message handling.
//
// Returns:
//   - *hpke.Server: The HPKE server (nil if HPKE is disabled)
func (a *Agent) GetHPKEServer() *hpke.Server {
	return a.hpkeServer
}

// GetResolver returns the DID resolver instance.
// This is used for custom DID operations.
//
// Returns:
//   - *agentdid.Resolver: The DID resolver
func (a *Agent) GetResolver() *agentdid.Resolver {
	return a.resolver
}

// GetSessionManager returns the session manager instance.
// This is used for custom session operations.
//
// Returns:
//   - *session.Manager: The session manager
func (a *Agent) GetSessionManager() *session.Manager {
	return a.sessionManager
}

// GetKeys returns the agent's key set.
// This is used for custom crypto operations.
//
// Returns:
//   - *keys.KeySet: The agent's keys
func (a *Agent) GetKeys() *keys.KeySet {
	return a.keys
}

// CreateHPKEClient creates an HPKE client for sending encrypted messages to another agent.
// This is used by routing agents (like Root) to communicate with other agents.
//
// Parameters:
//   - transport: The underlying transport for sending messages
//
// Returns:
//   - *hpke.Client: The initialized HPKE client
//   - error: Error if client cannot be created
//
// Example:
//
//	transport := prototx.NewA2ATransport(...)
//	client, err := agent.CreateHPKEClient(transport)
func (a *Agent) CreateHPKEClient(tr transport.MessageTransport) (*hpke.Client, error) {
	return hpke.NewClient(hpke.ClientConfig{
		Transport:      tr,
		Resolver:       a.resolver,
		SigningKey:     a.keys.SigningKey,
		ClientDID:      a.did,
		SessionManager: a.sessionManager,
	})
}
