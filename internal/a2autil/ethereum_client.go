// Package a2autil provides an Ethereum key resolver for A2A verifier flows.
// It is backed by the on-chain V4 registry client, with an optional file-based
// cache/fallback for DIDs → keys loaded from a JSON file.
package a2autil

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	// go-ethereum crypto helpers
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	// A2A verifier interface
	"github.com/sage-x-project/sage-a2a-go/pkg/verifier"

	// SAGE DID types and Ethereum V4 client
	"github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// ---------- Input file formats (same as before) ----------

type fileKey struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`            // uncompressed hex (0x04... 65B)
	PrivateKey string `json:"privateKey,omitempty"` // not used here
	Address    string `json:"address"`
	Type       string `json:"type,omitempty"` // "secp256k1" expected
}

// Some tools save []fileKey at top-level; some save {"agents":[...]}.
type fileEnvelope struct {
	Agents []fileKey `json:"agents"`
}

// ---------- Chain-backed client with optional file fallback ----------

// EthereumClient implements verifier.EthereumClient.
// Primary source: on-chain V4 registry via sage/pkg/agent/did/ethereum.
// Fallback: local JSON file (same format you already use) if chain lookup fails.
type EthereumClient struct {
	onchain *dideth.EthereumClientV4
	byDID   map[did.AgentDID]fileKey // optional fallback store
}

// NewEthereumClientFromFile builds the chain-backed client and loads an
// optional keys file used only as a fallback/cache source.
func NewEthereumClientFromFile(cfg *did.RegistryConfig, keysPath string) (*EthereumClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil registry config")
	}

	// Create on-chain V4 client
	v4, err := dideth.NewEthereumClientV4(cfg)
	if err != nil {
		return nil, fmt.Errorf("init on-chain client: %w", err)
	}

	idx := make(map[did.AgentDID]fileKey)
	if strings.TrimSpace(keysPath) != "" {
		arr, err := readKeysFile(keysPath)
		if err != nil {
			return nil, err
		}
		for _, k := range arr {
			// Derive DID from address if missing
			if strings.TrimSpace(k.DID) == "" && strings.TrimSpace(k.Address) != "" {
				k.DID = "did:sage:ethereum:" + k.Address
			}
			if strings.TrimSpace(k.DID) != "" {
				idx[did.AgentDID(k.DID)] = k
			}
		}
	}

	return &EthereumClient{
		onchain: v4,
		byDID:   idx,
	}, nil
}

// ResolveAllPublicKeys tries chain first, then falls back to a single ECDSA key
// from the local file if available.
func (c *EthereumClient) ResolveAllPublicKeys(ctx context.Context, agentDID did.AgentDID) ([]did.AgentKey, error) {
	// 1) Try chain
	if c.onchain != nil {
		if keys, err := c.onchain.ResolveAllPublicKeys(ctx, agentDID); err == nil && len(keys) > 0 {
			return keys, nil
		}
	}

	// 2) Fallback to file
	k, ok := c.byDID[agentDID]
	if !ok {
		return nil, fmt.Errorf("unknown DID (chain+file): %s", agentDID)
	}
	raw, err := hexToUncompressed(k.PublicKey)
	if err != nil {
		return nil, err
	}
	return []did.AgentKey{
		{
			Type:    did.KeyTypeECDSA,
			KeyData: raw,
			// You can mark Verified=true if your verifier expects it.
			// Verified: true,
		},
	}, nil
}

// ResolvePublicKeyByType returns a public key object. Chain first; file fallback.
// For the fallback we return *ecdsa.PublicKey (secp256k1).
func (c *EthereumClient) ResolvePublicKeyByType(ctx context.Context, agentDID did.AgentDID, keyType did.KeyType) (interface{}, error) {
	// Only ECDSA is supported in fallback. Chain client can support Ed25519 etc.
	if c.onchain != nil {
		if pub, err := c.onchain.ResolvePublicKeyByType(ctx, agentDID, keyType); err == nil && pub != nil {
			return pub, nil
		}
	}
	if keyType != did.KeyTypeECDSA {
		return nil, fmt.Errorf("fallback only supports ECDSA (requested type=%d)", keyType)
	}

	k, ok := c.byDID[agentDID]
	if !ok {
		return nil, fmt.Errorf("unknown DID (chain+file): %s", agentDID)
	}
	raw, err := hexToUncompressed(k.PublicKey)
	if err != nil {
		return nil, err
	}

	// Try decompress (33B) first; if it's uncompressed (65B) use UnmarshalPubkey.
	if pub, err := ethcrypto.DecompressPubkey(raw); err == nil {
		return pub, nil
	}
	pub2, err := ethcrypto.UnmarshalPubkey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal pubkey: %w", err)
	}
	return pub2, nil
}

// Ensure interface compliance with the verifier.
var _ verifier.EthereumClient = (*EthereumClient)(nil)

// ---------- Local helpers ----------

func readKeysFile(path string) ([]fileKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keys file: %w", err)
	}
	var env fileEnvelope
	if err := json.Unmarshal(b, &env); err == nil && len(env.Agents) > 0 {
		return env.Agents, nil
	}
	var arr []fileKey
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, fmt.Errorf("parse keys: %w", err)
	}
	return arr, nil
}

// hexToUncompressed normalizes a hex pubkey into 65B uncompressed (0x04||X||Y).
func hexToUncompressed(h string) ([]byte, error) {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "0x")
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("pubkey hex: %w", err)
	}
	switch len(b) {
	case 65: // 0x04||X||Y
		if b[0] != 0x04 {
			return nil, fmt.Errorf("unexpected 65B pubkey without 0x04 prefix")
		}
		return b, nil
	case 64: // X||Y
		return append([]byte{0x04}, b...), nil
	case 33: // compressed
		pub, err := ethcrypto.DecompressPubkey(b)
		if err != nil {
			return nil, err
		}
		return ethcrypto.FromECDSAPub(pub), nil
	default:
		return nil, fmt.Errorf("unexpected pubkey length: %d", len(b))
	}
}

// As a convenience, expose the underlying on-chain client (e.g., for tests).
func (c *EthereumClient) UnderlyingOnchain() *dideth.EthereumClientV4 {
	return c.onchain
}

// Optionally expose the fallback public key as *ecdsa.PublicKey for a DID.
func (c *EthereumClient) FallbackECDSAPublicKey(agentDID did.AgentDID) (*ecdsa.PublicKey, error) {
	k, ok := c.byDID[agentDID]
	if !ok {
		return nil, fmt.Errorf("no fallback key for DID: %s", agentDID)
	}
	raw, err := hexToUncompressed(k.PublicKey)
	if err != nil {
		return nil, err
	}
	if pub, err := ethcrypto.DecompressPubkey(raw); err == nil {
		return pub, nil
	}
	return ethcrypto.UnmarshalPubkey(raw)
}
