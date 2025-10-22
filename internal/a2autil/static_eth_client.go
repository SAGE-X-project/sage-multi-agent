package a2autil

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// ---- input file formats ----

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

// StaticEthereumClient implements verifier.EthereumClient from a JSON file.
type StaticEthereumClient struct {
	byDID map[did.AgentDID]fileKey
}

func NewStaticEthereumClientFromFile(path string) (*StaticEthereumClient, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keys file: %w", err)
	}

	var env fileEnvelope
	var arr []fileKey

	// Try envelope
	if err := json.Unmarshal(b, &env); err == nil && len(env.Agents) > 0 {
		arr = env.Agents
	} else {
		// Try array
		if err := json.Unmarshal(b, &arr); err != nil {
			return nil, fmt.Errorf("parse keys: %w", err)
		}
	}

	idx := make(map[did.AgentDID]fileKey)
	for _, k := range arr {
		if strings.TrimSpace(k.DID) == "" {
			// derive DID from address if missing
			if k.Address != "" {
				k.DID = "did:sage:ethereum:" + k.Address
			}
		}
		if k.DID == "" {
			continue
		}
		idx[did.AgentDID(k.DID)] = k
	}
	return &StaticEthereumClient{byDID: idx}, nil
}

// ResolveAllPublicKeys returns one ECDSA key (secp256k1) from the file.
func (c *StaticEthereumClient) ResolveAllPublicKeys(ctx context.Context, agentDID did.AgentDID) ([]did.AgentKey, error) {
	k, ok := c.byDID[agentDID]
	if !ok {
		return nil, fmt.Errorf("unknown DID: %s", agentDID)
	}
	raw, err := hexToUncompressed(k.PublicKey)
	if err != nil {
		return nil, err
	}
	// did.AgentKey is expected to have Type + KeyData fields
	return []did.AgentKey{
		{
			Type:    did.KeyTypeECDSA,
			KeyData: raw,
			// if there is a Verified field in your did.AgentKey, it can be set true here.
		},
	}, nil
}

// ResolvePublicKeyByType returns crypto.PublicKey for requested type.
func (c *StaticEthereumClient) ResolvePublicKeyByType(ctx context.Context, agentDID did.AgentDID, keyType did.KeyType) (interface{}, error) {
	if keyType != did.KeyTypeECDSA {
		return nil, fmt.Errorf("only ECDSA (secp256k1) supported in static client")
	}
	k, ok := c.byDID[agentDID]
	if !ok {
		return nil, fmt.Errorf("unknown DID: %s", agentDID)
	}
	raw, err := hexToUncompressed(k.PublicKey)
	if err != nil {
		return nil, err
	}
	pub, err := crypto.DecompressPubkey(raw) // accepts 33B compressed; our raw is 65B uncompressed → handle below
	if err != nil {
		// If it's already 65B uncompressed (0x04...), convert to *ecdsa.PublicKey directly
		pub2, err2 := crypto.UnmarshalPubkey(raw)
		if err2 != nil {
			return nil, fmt.Errorf("unmarshal pubkey: %v / %v", err, err2)
		}
		return pub2, nil
	}
	return pub, nil
}

func hexToUncompressed(h string) ([]byte, error) {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "0x")
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("pubkey hex: %w", err)
	}
	// If 64 bytes (X||Y), add 0x04 prefix; if 33 bytes (compressed), decompress; if 65 bytes (0x04||X||Y) ok.
	switch len(b) {
	case 65:
		if b[0] != 0x04 {
			return nil, fmt.Errorf("unexpected 65B pubkey without 0x04 prefix")
		}
		return b, nil
	case 64:
		return append([]byte{0x04}, b...), nil
	case 33:
		// let go-ethereum decompress
		pub, err := crypto.DecompressPubkey(b)
		if err != nil {
			return nil, err
		}
		return crypto.FromECDSAPub(pub), nil
	default:
		return nil, fmt.Errorf("unexpected pubkey length: %d", len(b))
	}
}

// Ensure interface compliance
var _ verifier.EthereumClient = (*StaticEthereumClient)(nil)
