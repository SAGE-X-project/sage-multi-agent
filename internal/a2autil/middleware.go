package a2autil

import (
	"fmt"
	"os"
	"strings"

	// a2a-go: DID verifier, key selector, RFC9421 verifier 인터페이스/구현
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// DIDAuth wraps the a2a-go server middleware so callers don't depend on a2a-go directly.
type DIDAuth struct {
	Mw *server.DIDAuthMiddleware
}

// BuildDIDMiddlewareFromChain constructs a DID-auth middleware that prefers the
// on-chain Ethereum V4 registry resolver, with a file-based fallback.
//
// Fixed environment variables (defaults applied if missing):
//
//	ETH_RPC_URL               (default: http://127.0.0.1:8545)
//	SAGE_REGISTRY_V4_ADDRESS  (default: 0x5FbDB2315678afecb367f032d93F642f64180aa3)
//	SAGE_OPERATOR_PRIVATE_KEY (default: 0xac0974...ff80)  // hex; 0x prefix allowed
//
// If the chain client fails to initialize (e.g., RPC unreachable), it falls back
// to a static file resolver at keysJSONPath (generated_agent_keys.json format).
func BuildDIDMiddleware(keysJSONPath string, optional bool) (*server.DIDAuthMiddleware, error) {
	if strings.TrimSpace(keysJSONPath) == "" {
		keysJSONPath = "generated_agent_keys.json"
	}

	// Read envs with hard defaults.
	rpc := strings.TrimSpace(os.Getenv("ETH_RPC_URL"))
	if rpc == "" {
		rpc = "http://127.0.0.1:8545"
	}
	contract := strings.TrimSpace(os.Getenv("SAGE_REGISTRY_V4_ADDRESS"))
	if contract == "" {
		contract = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	}
	priv := strings.TrimSpace(os.Getenv("SAGE_OPERATOR_PRIVATE_KEY"))
	if priv == "" {
		priv = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	}
	priv = strings.TrimPrefix(priv, "0x") // normalize

	cfg := &did.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv, // optional for read-only resolve; required for tx-signed ops
		GasPrice:           0,    // use node suggestion
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}

	ethClient, ferr := NewEthereumClientFromFile(cfg, keysJSONPath)
	if ferr != nil {
		return nil, fmt.Errorf("chain init failed (%v)cle", ferr)
	}

	mw := server.NewDIDAuthMiddleware(ethClient)
	mw.SetOptional(optional)
	return mw, nil
}
