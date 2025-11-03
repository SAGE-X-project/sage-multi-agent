package bootstrap

import (
	"os"
	"strconv"
	"strings"
)

// LoadConfigFromEnv loads agent configuration from environment variables
func LoadConfigFromEnv(agentName string) *AgentConfig {
	prefix := strings.ToUpper(agentName)

	keyDir := getEnvStr(prefix+"_KEY_DIR", "keys")
	signingKeyFile := getEnvStr(prefix+"_JWK_FILE", "")
	kemKeyFile := getEnvStr(prefix+"_KEM_JWK_FILE", "")
	did := getEnvStr(prefix+"_DID", "")

	// Global blockchain config
	ethRPC := getEnvStr("ETH_RPC_URL", "http://127.0.0.1:8545")
	registryAddr := getEnvStr("SAGE_REGISTRY_ADDRESS", "")
	autoRegister := getEnvBool(prefix+"_AUTO_REGISTER", getEnvBool("AUTO_REGISTER", false))
	fundingKey := getEnvStr("FUNDING_PRIVATE_KEY", "")

	return &AgentConfig{
		Name:              agentName,
		KeyDir:            keyDir,
		SigningKeyFile:    signingKeyFile,
		KEMKeyFile:        kemKeyFile,
		DID:               did,
		ETHRPCUrl:         ethRPC,
		RegistryAddress:   registryAddr,
		AutoRegister:      autoRegister,
		FundingPrivateKey: fundingKey,
	}
}

// Environment variable helpers

func getEnvStr(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	default:
		return defaultValue
	}
}
