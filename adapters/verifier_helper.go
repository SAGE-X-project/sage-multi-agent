package adapters

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sage-x-project/sage-multi-agent/config"
	"github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/storage"
	"github.com/sage-x-project/sage/pkg/agent/did"
	"github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// VerifierHelper provides agent verification with SAGE integration
type VerifierHelper struct {
	didManager       *did.Manager
	cryptoManager    *crypto.Manager
	keyStorage       crypto.KeyStorage
	agentConfig      *config.Config
	envConfig        *config.EnvConfig
	skipVerification bool
	keyDir           string
}

// NewVerifierHelper creates a new verifier helper
func NewVerifierHelper(keyDir string, skipVerification bool) (*VerifierHelper, error) {
	if skipVerification {
		log.Printf("[INFO] Agent verification skipped (--skip-verification flag)")

		// Create crypto manager even in skip mode for key generation
		cryptoManager := crypto.NewManager()

		// Set up file storage for keys
		fileStorage, err := storage.NewFileKeyStorage(keyDir)
		if err != nil {
			log.Printf("[WARN] Failed to create file storage, using memory: %v", err)
			fileStorage = storage.NewMemoryKeyStorage()
		}
		cryptoManager.SetStorage(fileStorage)

		return &VerifierHelper{
			skipVerification: true,
			cryptoManager:    cryptoManager,
			keyStorage:       fileStorage,
			keyDir:           keyDir,
		}, nil
	}

	// Load configurations
	envConfig, err := config.LoadEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	agentConfig, err := config.LoadAgentConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load agent config: %w", err)
	}

	// Initialize SAGE managers
	didManager := did.NewManager()
	cryptoManager := crypto.NewManager()

	// Set up file storage for keys
	fileStorage, err := storage.NewFileKeyStorage(keyDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create file storage: %w", err)
	}
	cryptoManager.SetStorage(fileStorage)

	// Configure DID manager for the blockchain
	network := agentConfig.Network.Chain
	contractAddr, rpcEndpoint, _, err := config.GetContractInfo(network, envConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get contract info: %w", err)
	}

	chain := did.ChainEthereum
	if network == "kaia" || network == "klaytn" {
		chain = did.Chain("kaia")
	} else if network == "local" {
		// Local network uses Ethereum chain type
		chain = did.ChainEthereum
	}

	registryConfig := &did.RegistryConfig{
		ContractAddress: contractAddr,
		RPCEndpoint:     rpcEndpoint,
	}

	if err := didManager.Configure(chain, registryConfig); err != nil {
		return nil, fmt.Errorf("failed to configure DID manager: %w", err)
	}

	// Create and set Ethereum client for the DID manager
	if chain == did.ChainEthereum {
		ethClient, err := ethereum.NewEthereumClient(registryConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Ethereum client: %w", err)
		}

		// Use adapter to match interface requirements
		adapter := NewEthereumResolverAdapter(ethClient)

		// Set both as Registry and Resolver
		// The adapter implements Resolver, and ethClient implements Registry
		if err := didManager.SetClient(chain, adapter); err != nil {
			// If adapter doesn't work, try setting ethClient directly
			log.Printf("[WARN] Failed to set adapter, trying direct client: %v", err)
			if err := didManager.SetClient(chain, ethClient); err != nil {
				log.Printf("[ERROR] Failed to set Ethereum client: %v", err)
				// Continue anyway, some operations might still work
			}
		}

		log.Printf("[INFO] Ethereum client initialized for chain: %s", chain)
	}

	log.Printf("[INFO] Configured DID verifier:")
	log.Printf("  Network: %s", network)
	log.Printf("  Contract: %s", contractAddr)
	log.Printf("  RPC: %s", rpcEndpoint)

	return &VerifierHelper{
		didManager:       didManager,
		cryptoManager:    cryptoManager,
		keyStorage:       fileStorage,
		agentConfig:      agentConfig,
		envConfig:        envConfig,
		skipVerification: false,
		keyDir:           keyDir,
	}, nil
}

// VerifyOrRegisterAgent checks if agent is registered, registers if not
func (vh *VerifierHelper) VerifyOrRegisterAgent(agentType string) error {
	if vh.skipVerification {
		log.Printf("[INFO] Skipping verification for agent: %s", agentType)
		return nil
	}

	// Get agent configuration
	agentCfg, exists := vh.agentConfig.Agents[agentType]
	if !exists {
		return fmt.Errorf("agent type '%s' not found in configuration", agentType)
	}

	log.Printf("[INFO] Checking registration for agent: %s (DID: %s)", agentCfg.Name, agentCfg.DID)

	// Load or generate key for this agent
	keyPair, err := vh.LoadOrGenerateKey(agentType, agentCfg.DID)
	if err != nil {
		return fmt.Errorf("failed to load/generate key for agent %s: %w", agentType, err)
	}

	// Try to resolve the agent from the blockchain
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	metadata, err := vh.didManager.ResolveAgent(ctx, did.AgentDID(agentCfg.DID))
	if err != nil {
		// Agent not registered, attempt to register
		log.Printf("[WARN] Agent %s is NOT registered on blockchain", agentCfg.Name)
		log.Printf("[INFO] Attempting to register agent %s...", agentCfg.Name)

		// Convert capabilities to map
		capabilities := make(map[string]interface{})
		if agentCfg.Capabilities.Type != "" {
			capabilities["type"] = agentCfg.Capabilities.Type
		}
		if agentCfg.Capabilities.Version != "" {
			capabilities["version"] = agentCfg.Capabilities.Version
		}
		if len(agentCfg.Capabilities.Skills) > 0 {
			capabilities["skills"] = agentCfg.Capabilities.Skills
		}

		// Prepare registration request
		regRequest := &did.RegistrationRequest{
			DID:          did.AgentDID(agentCfg.DID),
			Name:         agentCfg.Name,
			Description:  fmt.Sprintf("%s agent for SAGE multi-agent system", agentCfg.Name),
			Endpoint:     agentCfg.Endpoint,
			Capabilities: capabilities,
			KeyPair:      keyPair,
		}

		// Get the chain
		network := vh.agentConfig.Network.Chain
		chain := did.ChainEthereum
		if network == "kaia" || network == "klaytn" {
			chain = did.Chain("kaia")
		}

		// Register the agent
		result, err := vh.didManager.RegisterAgent(ctx, chain, regRequest)
		if err != nil {
			log.Printf("[ERROR] Failed to register agent %s: %v", agentCfg.Name, err)
			log.Println()
			log.Println("========================================")
			log.Println("MANUAL REGISTRATION REQUIRED")
			log.Println("========================================")
			log.Printf("The agent '%s' with DID '%s' could not be automatically registered.\n", agentCfg.Name, agentCfg.DID)
			log.Println()
			log.Println("This may be due to:")
			log.Println("  1. Insufficient ETH for gas fees")
			log.Println("  2. Contract signature verification requirements")
			log.Println("  3. Network connectivity issues")
			log.Println()
			log.Println("To manually register this agent:")
			log.Printf("  1. Fund the agent address with ETH for gas\n")
			log.Printf("  2. Run: go run cli/register/main.go --agent %s\n", agentType)
			log.Println("========================================")

			return fmt.Errorf("agent %s registration failed: %w", agentCfg.Name, err)
		}

		log.Printf("[SUCCESS] Agent %s registered successfully!", agentCfg.Name)
		log.Printf("  Transaction: %s", result.TransactionHash)
		log.Printf("  Block: %d", result.BlockNumber)

		// Verify registration was successful
		metadata, err = vh.didManager.ResolveAgent(ctx, did.AgentDID(agentCfg.DID))
		if err != nil {
			return fmt.Errorf("failed to verify registration: %w", err)
		}
	}

	// Verify the agent is active
	if metadata != nil && !metadata.IsActive {
		log.Printf("[WARN] Agent %s is registered but INACTIVE", agentCfg.Name)
		return fmt.Errorf("agent %s is registered but inactive", agentCfg.Name)
	}

	log.Printf("[SUCCESS]  Agent %s is registered and active", agentCfg.Name)
	log.Printf("  - Owner: %s", metadata.Owner)
	log.Printf("  - Endpoint: %s", metadata.Endpoint)
	log.Printf("  - Created: %s", metadata.CreatedAt.Format(time.RFC3339))

	return nil
}

// LoadOrGenerateKey loads or generates a key for an agent
func (vh *VerifierHelper) LoadOrGenerateKey(agentType, agentDID string) (crypto.KeyPair, error) {
	keyID := fmt.Sprintf("%s-key", agentType)

	// Try to load existing key
	keyPair, err := vh.cryptoManager.LoadKeyPair(keyID)
	if err == nil {
		log.Printf("[INFO] Loaded existing key for agent %s (ID: %s)", agentType, keyID)
		return keyPair, nil
	}

	// Generate new key (use secp256k1 for Ethereum compatibility)
	keyPair, err = vh.cryptoManager.GenerateKeyPair(crypto.KeyTypeSecp256k1)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Store the key with agent-specific ID instead of random ID
	// We need to use the storage directly to control the ID
	if err := vh.keyStorage.Store(keyID, keyPair); err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	log.Printf("[INFO] Generated new secp256k1 key for agent %s and stored with ID: %s", agentType, keyID)
	return keyPair, nil
}

// LoadAgentKey loads the key for a specific agent
func (vh *VerifierHelper) LoadAgentKey(agentType string) (crypto.KeyPair, error) {
	// If skip verification is enabled and no config loaded, generate a temp key
	if vh.skipVerification && vh.agentConfig == nil {
		placeholderDID := fmt.Sprintf("did:sage:agent:%s", agentType)
		return vh.LoadOrGenerateKey(agentType, placeholderDID)
	}

	if vh.agentConfig == nil {
		return nil, fmt.Errorf("agent configuration not loaded")
	}

	agentCfg, exists := vh.agentConfig.Agents[agentType]
	if !exists {
		return nil, fmt.Errorf("agent type '%s' not found in configuration", agentType)
	}

	return vh.LoadOrGenerateKey(agentType, agentCfg.DID)
}

// GetAgentMetadata retrieves metadata for a specific agent
func (vh *VerifierHelper) GetAgentMetadata(agentType string) (*did.AgentMetadata, error) {
	if vh.skipVerification {
		// Return dummy metadata when verification is skipped
		if vh.agentConfig == nil {
			// Return minimal metadata if no config is loaded
			return &did.AgentMetadata{
				DID:          did.AgentDID(fmt.Sprintf("did:sage:agent:%s", agentType)),
				Name:         agentType,
				Endpoint:     fmt.Sprintf("http://localhost:808%d", len(agentType)%10),
				IsActive:     true,
				Capabilities: make(map[string]interface{}),
				CreatedAt:    time.Now(),
			}, nil
		}

		agentCfg := vh.agentConfig.Agents[agentType]

		// Convert capabilities
		capabilities := make(map[string]interface{})
		if agentCfg.Capabilities.Type != "" {
			capabilities["type"] = agentCfg.Capabilities.Type
		}
		if agentCfg.Capabilities.Version != "" {
			capabilities["version"] = agentCfg.Capabilities.Version
		}

		return &did.AgentMetadata{
			DID:          did.AgentDID(agentCfg.DID),
			Name:         agentCfg.Name,
			Endpoint:     agentCfg.Endpoint,
			IsActive:     true,
			Capabilities: capabilities,
			CreatedAt:    time.Now(),
		}, nil
	}

	agentCfg, exists := vh.agentConfig.Agents[agentType]
	if !exists {
		return nil, fmt.Errorf("agent type '%s' not found in configuration", agentType)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return vh.didManager.ResolveAgent(ctx, did.AgentDID(agentCfg.DID))
}

// GetCryptoManager returns the crypto manager instance
func (vh *VerifierHelper) GetCryptoManager() *crypto.Manager {
	return vh.cryptoManager
}

// GetDIDManager returns the DID manager instance
func (vh *VerifierHelper) GetDIDManager() *did.Manager {
	return vh.didManager
}
