package a2autil

import (
	"fmt"
	"os"
	"strings"

	// a2a-go: DID verifier, key selector, RFC9421 verifier 인터페이스/구현
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// DIDAuth wraps the a2a-go server middleware so callers don't depend on a2a-go directly.
type DIDAuth struct {
	Mw *server.DIDAuthMiddleware
}

// ETH_RPC_URL               (default: http://127.0.0.1:8545)
// SAGE_REGISTRY_V4_ADDRESS  (default: 0x5FbDB2315678afecb367f032d93F642f64180aa3)
// SAGE_OPERATOR_PRIVATE_KEY (default: 0xac0974...ff80)  // hex; 0x prefix allowed
func BuildDIDMiddleware(optional bool) (*server.DIDAuthMiddleware, error) {

	// Read envs with hard defaults.
	rpc := strings.TrimSpace(os.Getenv("ETH_RPC_URL"))
	if rpc == "" {
		rpc = "http://127.0.0.1:8545"
	}
	contract := strings.TrimSpace(os.Getenv("SAGE_REGISTRY_V4_ADDRESS"))
	if contract == "" {
		contract = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	}
	priv := strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY"))
	if priv == "" {
		priv = "0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a"
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

	ethClient, err := dideth.NewEthereumClientV4(cfg)
	if err != nil {
		return nil, fmt.Errorf("init on-chain client: %w", err)
	}

	mw := server.NewDIDAuthMiddleware(ethClient)
	mw.SetOptional(optional)
	return mw, nil
}
