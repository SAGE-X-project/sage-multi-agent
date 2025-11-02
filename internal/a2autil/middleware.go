package a2autil

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	// a2a-go: DID verifier, key selector, RFC9421 verifier interfaces/implementations
	"github.com/sage-x-project/sage-a2a-go/pkg/registry"
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// DIDAuth wraps the a2a-go server middleware so callers don't depend on a2a-go directly.
type DIDAuth struct {
	Mw *server.DIDAuthMiddleware
}

// ETH_RPC_URL               (default: http://127.0.0.1:8545)
// SAGE_REGISTRY_ADDRESS  (default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
// SAGE_OPERATOR_PRIVATE_KEY (default: 0xac0974...ff80)  // hex; 0x prefix allowed
func BuildDIDMiddleware(optional bool) (*server.DIDAuthMiddleware, error) {

	// Read envs with hard defaults.
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

	// 1) Create registry client for DID resolution
	registryClient, err := registry.NewRegistrationClient(&registry.ClientConfig{
		RPCURL:          rpc,
		RegistryAddress: contract,
		PrivateKey:      priv,
	})
	if err != nil {
		panic(err)
	}

	// 2) Create Ethereum client for public key resolution (need SAGE client directly)
	keyClient, err := dideth.NewEthereumClient(&did.RegistryConfig{
		RPCEndpoint:     rpc,
		ContractAddress: contract,
	})
	if err != nil {
		panic(err)
	}

	// Use SAGE DID client from registry wrapper + direct Ethereum client
	sageDIDClient := registryClient.GetSAGEClient()

	mw := server.NewDIDAuthMiddleware(sageDIDClient, keyClient)
	mw.SetOptional(optional)
	return mw, nil
}

// ComputeContentDigest makes RFC9421-compatible Content-Digest header (sha-256).
func ComputeContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	b64 := base64.StdEncoding.EncodeToString(sum[:])
	return fmt.Sprintf("sha-256=:%s:", b64)
}
