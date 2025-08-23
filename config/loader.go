package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// AgentCapabilities defines the capabilities of an agent
type AgentCapabilities struct {
	Type               string   `yaml:"type" json:"type"`
	Skills             []string `yaml:"skills" json:"skills"`
	Version            string   `yaml:"version" json:"version"`
	Subagents          []string `yaml:"subagents,omitempty" json:"subagents,omitempty"`
	SupportedVendors   []string `yaml:"supported_vendors,omitempty" json:"supported_vendors,omitempty"`
	SupportedFeatures  []string `yaml:"supported_features,omitempty" json:"supported_features,omitempty"`
}

// AgentConfig represents configuration for a single agent
type AgentConfig struct {
	DID          string            `yaml:"did"`
	Name         string            `yaml:"name"`
	Description  string            `yaml:"description"`
	Endpoint     string            `yaml:"endpoint"`
	Type         string            `yaml:"type"`
	Capabilities AgentCapabilities `yaml:"capabilities"`
	KeyFile      string            `yaml:"key_file"`
}

// NetworkConfig represents network configuration
type NetworkConfig struct {
	Chain              string `yaml:"chain"`
	ConfirmationBlocks int    `yaml:"confirmation_blocks"`
	GasLimit           int    `yaml:"gas_limit"`
}

// Config represents the main configuration structure
type Config struct {
	Agents  map[string]AgentConfig `yaml:"agents"`
	Network NetworkConfig          `yaml:"network"`
}

// RegistrationConfig represents the registration configuration
type RegistrationConfig struct {
	Registration struct {
		Network  string `yaml:"network"`
		Contract struct {
			Address string `yaml:"address"`
			ABIPath string `yaml:"abi_path"`
		} `yaml:"contract"`
		Account struct {
			PrivateKey string `yaml:"private_key"`
		} `yaml:"account"`
		Gas struct {
			Limit int    `yaml:"limit"`
			Price string `yaml:"price"`
		} `yaml:"gas"`
		Transaction struct {
			WaitConfirmations int `yaml:"wait_confirmations"`
			TimeoutSeconds    int `yaml:"timeout_seconds"`
		} `yaml:"transaction"`
	} `yaml:"registration"`
	Defaults struct {
		CapabilitiesVersion string `yaml:"capabilities_version"`
		Active              bool   `yaml:"active"`
	} `yaml:"defaults"`
}

// EnvConfig holds environment variables
type EnvConfig struct {
	// Network Selection
	SageNetwork string

	// Local Network Configuration
	LocalContractAddress string
	LocalRPCEndpoint     string
	LocalChainID         int

	// Ethereum Configuration
	EthContractAddress string
	EthRPCEndpoint     string
	EthChainID         int

	// Sepolia Configuration
	SepoliaContractAddress string
	SepoliaRPCEndpoint     string
	SepoliaChainID         int

	// Kaia Configuration
	KaiaContractAddress string
	KaiaRPCEndpoint     string
	KaiaChainID         int

	// Kairos Configuration
	KairosContractAddress string
	KairosRPCEndpoint     string
	KairosChainID         int

	// API Keys
	GoogleAPIKey string

	// Server Ports
	RootAgentPort     int
	OrderingAgentPort int
	PlanningAgentPort int
	ClientPort        int
	WSPort            int

	// Registration
	RegistrationPrivateKey string
}

// LoadEnv loads environment variables
func LoadEnv() (*EnvConfig, error) {
	// Try to load .env file, ignore error if it doesn't exist
	_ = godotenv.Load()

	cfg := &EnvConfig{
		// Network Selection
		SageNetwork: getEnv("SAGE_NETWORK", "local"),

		// Local Network
		LocalContractAddress: getEnv("LOCAL_CONTRACT_ADDRESS", "0x5FbDB2315678afecb367f032d93F642f64180aa3"),
		LocalRPCEndpoint:     getEnv("LOCAL_RPC_ENDPOINT", "http://localhost:8545"),

		// Ethereum Mainnet
		EthContractAddress:  getEnv("ETH_CONTRACT_ADDRESS", ""),
		EthRPCEndpoint:      getEnv("ETH_RPC_ENDPOINT", ""),

		// Sepolia Testnet
		SepoliaContractAddress: getEnv("SEPOLIA_CONTRACT_ADDRESS", ""),
		SepoliaRPCEndpoint:     getEnv("SEPOLIA_RPC_ENDPOINT", ""),

		// Kaia Mainnet
		KaiaContractAddress: getEnv("KAIA_CONTRACT_ADDRESS", ""),
		KaiaRPCEndpoint:     getEnv("KAIA_RPC_ENDPOINT", "https://public-en-cypress.klaytn.net"),

		// Kairos Testnet
		KairosContractAddress: getEnv("KAIROS_CONTRACT_ADDRESS", ""),
		KairosRPCEndpoint:     getEnv("KAIROS_RPC_ENDPOINT", "https://public-en.kairos.node.kaia.io"),

		// API Keys and Registration
		GoogleAPIKey:        getEnv("GOOGLE_API_KEY", ""),
		RegistrationPrivateKey: getEnv("REGISTRATION_PRIVATE_KEY", ""),
	}

	// Parse integer values with defaults
	cfg.LocalChainID = getEnvInt("LOCAL_CHAIN_ID", 31337)
	cfg.EthChainID = getEnvInt("ETH_CHAIN_ID", 1)
	cfg.SepoliaChainID = getEnvInt("SEPOLIA_CHAIN_ID", 11155111)
	cfg.KaiaChainID = getEnvInt("KAIA_CHAIN_ID", 8217)
	cfg.KairosChainID = getEnvInt("KAIROS_CHAIN_ID", 1001)
	cfg.RootAgentPort = getEnvInt("ROOT_AGENT_PORT", 8080)
	cfg.OrderingAgentPort = getEnvInt("ORDERING_AGENT_PORT", 8083)
	cfg.PlanningAgentPort = getEnvInt("PLANNING_AGENT_PORT", 8084)
	cfg.ClientPort = getEnvInt("CLIENT_PORT", 8086)
	cfg.WSPort = getEnvInt("WS_PORT", 8085)

	return cfg, nil
}

// LoadAgentConfig loads the agent configuration from YAML
func LoadAgentConfig(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = "configs/agent_config.yaml"
	}

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Replace environment variables in the YAML
	configStr := expandEnvVars(string(data))

	var config Config
	if err := yaml.Unmarshal([]byte(configStr), &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// LoadRegistrationConfig loads the registration configuration
func LoadRegistrationConfig(configPath string) (*RegistrationConfig, error) {
	if configPath == "" {
		configPath = "configs/agent_registration.yaml"
	}

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read registration config: %w", err)
	}

	// Replace environment variables
	configStr := expandEnvVars(string(data))

	var config RegistrationConfig
	if err := yaml.Unmarshal([]byte(configStr), &config); err != nil {
		return nil, fmt.Errorf("failed to parse registration config: %w", err)
	}

	return &config, nil
}

// GetAgentByDID finds an agent configuration by DID
func (c *Config) GetAgentByDID(did string) (*AgentConfig, error) {
	for _, agent := range c.Agents {
		if agent.DID == did {
			return &agent, nil
		}
	}
	return nil, fmt.Errorf("agent with DID %s not found", did)
}

// GetAgentByType finds agents by type
func (c *Config) GetAgentByType(agentType string) []AgentConfig {
	var agents []AgentConfig
	for _, agent := range c.Agents {
		if agent.Type == agentType {
			agents = append(agents, agent)
		}
	}
	return agents
}

// CapabilitiesToJSON converts capabilities to JSON string
func (c *AgentCapabilities) ToJSON() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intVal int
		if _, err := fmt.Sscanf(value, "%d", &intVal); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func expandEnvVars(s string) string {
	// Replace ${VAR_NAME} with environment variable values
	return os.Expand(s, func(key string) string {
		return os.Getenv(key)
	})
}

// GetContractInfo returns contract address and RPC endpoint based on network
func GetContractInfo(network string, env *EnvConfig) (address, rpcEndpoint string, chainID int, error error) {
	// If network is empty or "auto", use the configured network from env
	if network == "" || network == "auto" {
		network = env.SageNetwork
	}

	switch strings.ToLower(network) {
	case "local", "localhost", "hardhat":
		if env.LocalContractAddress == "" {
			return "", "", 0, fmt.Errorf("LOCAL_CONTRACT_ADDRESS not set")
		}
		return env.LocalContractAddress, env.LocalRPCEndpoint, env.LocalChainID, nil
	case "ethereum", "eth", "mainnet":
		if env.EthContractAddress == "" {
			return "", "", 0, fmt.Errorf("ETH_CONTRACT_ADDRESS not set")
		}
		return env.EthContractAddress, env.EthRPCEndpoint, env.EthChainID, nil
	case "sepolia":
		if env.SepoliaContractAddress == "" {
			return "", "", 0, fmt.Errorf("SEPOLIA_CONTRACT_ADDRESS not set")
		}
		return env.SepoliaContractAddress, env.SepoliaRPCEndpoint, env.SepoliaChainID, nil
	case "kaia", "klaytn", "cypress":
		if env.KaiaContractAddress == "" {
			return "", "", 0, fmt.Errorf("KAIA_CONTRACT_ADDRESS not set")
		}
		return env.KaiaContractAddress, env.KaiaRPCEndpoint, env.KaiaChainID, nil
	case "kairos", "kaia-testnet":
		if env.KairosContractAddress == "" {
			return "", "", 0, fmt.Errorf("KAIROS_CONTRACT_ADDRESS not set")
		}
		return env.KairosContractAddress, env.KairosRPCEndpoint, env.KairosChainID, nil
	default:
		return "", "", 0, fmt.Errorf("unsupported network: %s (supported: local, ethereum, sepolia, kaia, kairos)", network)
	}
}