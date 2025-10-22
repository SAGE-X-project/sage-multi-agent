package a2autil

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// StaticDIDVerifier satisfies a2a-go's DIDVerifier with a static DID->pubkey map.
type StaticDIDVerifier struct {
	pubkeys map[did.AgentDID]crypto.PublicKey
	httpv   *verifier.RFC9421Verifier
}

func NewStaticDIDVerifier(pubkeys map[did.AgentDID]crypto.PublicKey) *StaticDIDVerifier {
	return &StaticDIDVerifier{pubkeys: pubkeys, httpv: verifier.NewRFC9421Verifier()}
}

func (v *StaticDIDVerifier) ResolvePublicKey(ctx context.Context, d did.AgentDID, keyType *did.KeyType) (crypto.PublicKey, error) {
	pk, ok := v.pubkeys[d]
	if !ok {
		return nil, fmt.Errorf("unknown DID: %s", d)
	}
	return pk, nil
}
func (v *StaticDIDVerifier) VerifyHTTPSignature(ctx context.Context, req *http.Request, d did.AgentDID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if req.Header.Get("Signature-Input") == "" || req.Header.Get("Signature") == "" {
		return fmt.Errorf("missing signature headers")
	}
	pk, err := v.ResolvePublicKey(ctx, d, nil)
	if err != nil {
		return err
	}
	return v.httpv.VerifyHTTPRequest(req, pk)
}
func (v *StaticDIDVerifier) VerifyHTTPSignatureWithKeyID(ctx context.Context, req *http.Request) (did.AgentDID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	si := req.Header.Get("Signature-Input")
	if si == "" {
		return "", fmt.Errorf("missing Signature-Input")
	}
	re := regexp.MustCompile(`keyid="([^"]+)"`)
	m := re.FindStringSubmatch(si)
	if len(m) < 2 {
		return "", fmt.Errorf("keyid not found")
	}
	keyID := m[1]
	if !strings.HasPrefix(keyID, "did:sage:") {
		return "", fmt.Errorf("invalid DID: %s", keyID)
	}
	d := did.AgentDID(keyID)
	return d, v.VerifyHTTPSignature(ctx, req, d)
}

// Load from your keys/all_keys.json {agents:[{did,publicKey},...]}
type AllKeys struct {
	Agents []struct {
		DID       string `json:"did"`
		PublicKey string `json:"publicKey"`
	} `json:"agents"`
}

func BuildStaticKeyMap(allKeysJSON []byte) (map[did.AgentDID]crypto.PublicKey, error) {
	var ak AllKeys
	if err := json.Unmarshal(allKeysJSON, &ak); err != nil {
		return nil, err
	}
	out := make(map[did.AgentDID]crypto.PublicKey)
	for _, a := range ak.Agents {
		pub, err := PubkeyFromUncompressedHex(a.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("pub for %s: %w", a.DID, err)
		}
		out[did.AgentDID(a.DID)] = pub
	}
	return out, nil
}

// PubkeyFromUncompressedHex parses a secp256k1 uncompressed public key hex string
// into *ecdsa.PublicKey. The expected format is 65 bytes:
//
//	0x04 || X(32 bytes) || Y(32 bytes)
//
// It also accepts 64-byte raw XY (without 0x04) and prepends the prefix.
func PubkeyFromUncompressedHex(hexStr string) (*ecdsa.PublicKey, error) {
	// Strip optional "0x" prefix
	h := strings.TrimPrefix(strings.TrimSpace(hexStr), "0x")

	// Decode hex to bytes
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}

	// Allow raw 64-byte XY (no 0x04). If so, prepend the uncompressed prefix.
	if len(b) == 64 {
		b = append([]byte{0x04}, b...)
	}

	// Validate uncompressed format: 65 bytes with leading 0x04
	if len(b) != 65 || b[0] != 0x04 {
		return nil, fmt.Errorf("want 65 bytes (0x04 + X32 + Y32), got %d (head=0x%02x)", len(b), func() byte {
			if len(b) > 0 {
				return b[0]
			}
			return 0x00
		}())
	}

	// Convert to *ecdsa.PublicKey using go-ethereum
	pk, err := ethcrypto.UnmarshalPubkey(b)
	if err != nil {
		return nil, fmt.Errorf("unmarshal pubkey: %w", err)
	}
	return pk, nil
}
