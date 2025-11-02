package did

import (
	"os"
	"strings"
)

// newResolverFromEnvWithDefaults creates a resolver from environment variables with fallback defaults.
// This matches the behavior of internal/a2autil/middleware.go
func newResolverFromEnvWithDefaults() (*Resolver, error) {
	rpc := strings.TrimSpace(os.Getenv("ETH_RPC_URL"))
	if rpc == "" {
		rpc = "http://127.0.0.1:8545"
	}

	contract := strings.TrimSpace(os.Getenv("SAGE_REGISTRY_ADDRESS"))
	if contract == "" {
		contract = "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"
	}

	priv := strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY"))
	if priv == "" {
		priv = "0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a"
	}
	priv = strings.TrimPrefix(priv, "0x") // normalize

	return NewResolver(Config{
		RPCEndpoint:     rpc,
		ContractAddress: contract,
		PrivateKey:      priv,
	})
}
