package payment

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
	"golang.org/x/crypto/sha3"

	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// PaymentAgent runs IN-PROC and (optionally) calls an EXTERNAL payment service.
// Only outbound (agent -> external) is A2A-signed when SAGE is ON.
type PaymentAgent struct {
	Name        string
	SAGEEnabled bool

	ExternalURL string // Gateway/relay or external payment (e.g. http://localhost:5500)

	myDID did.AgentDID
	myKey sagecrypto.KeyPair
	a2a   *a2aclient.A2AClient

	httpClient *http.Client
}

func NewPaymentAgent(name string) *PaymentAgent {
	return &PaymentAgent{
		Name:        name,
		SAGEEnabled: envBool("PAYMENT_SAGE_ENABLED", true),
		ExternalURL: strings.TrimRight(envOr("PAYMENT_EXTERNAL_URL", "http://localhost:5500"), "/"),
		httpClient:  http.DefaultClient,
	}
}

// IN-PROC entrypoint (Root -> Payment)
func (p *PaymentAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	// Always try external if configured
	if p.ExternalURL != "" {
		out, err := p.forwardExternal(ctx, &msg)
		if err == nil {
			return *out, nil
		}
		// If external fails, return error (payment usually must go out)
		return types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "payment",
			To:        msg.From,
			Type:      "error",
			Content:   fmt.Sprintf("payment upstream error: %v", err),
			Timestamp: time.Now(),
		}, nil
	}

	// No external configured: respond gracefully
	return types.AgentMessage{
		ID:        "resp-" + msg.ID,
		From:      p.Name,
		To:        msg.From,
		Type:      "response",
		Content:   "Payment external endpoint not configured.",
		Timestamp: time.Now(),
		Metadata:  map[string]any{"agent_type": "payment"},
	}, nil
}

func (p *PaymentAgent) forwardExternal(ctx context.Context, msg *types.AgentMessage) (*types.AgentMessage, error) {
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ExternalURL+"/process", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Digest", a2autil.ComputeContentDigest(body))

	resp, err := p.do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	var out types.AgentMessage
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)

	if resp.StatusCode/100 != 2 {
		return &types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "external-payment",
			To:        msg.From,
			Type:      "error",
			Content:   fmt.Sprintf("external error: %d %s", resp.StatusCode, strings.TrimSpace(buf.String())),
			Timestamp: time.Now(),
		}, nil
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		out = types.AgentMessage{
			ID:        msg.ID + "-ext",
			From:      "external-payment",
			To:        msg.From,
			Type:      "response",
			Content:   strings.TrimSpace(buf.String()),
			Timestamp: time.Now(),
		}
	}
	return &out, nil
}

func (p *PaymentAgent) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if p.SAGEEnabled {
		if p.a2a == nil {
			if err := p.initSigning(); err != nil {
				return nil, err
			}
		}
		return p.a2a.Do(ctx, req)
	}
	return p.httpClient.Do(req)
}

func (p *PaymentAgent) initSigning() error {
	jwk := strings.TrimSpace(os.Getenv("PAYMENT_JWK_FILE"))
	if jwk == "" {
		return fmt.Errorf("PAYMENT_JWK_FILE required for SAGE signing")
	}
	raw, err := os.ReadFile(jwk)
	if err != nil {
		return fmt.Errorf("read PAYMENT_JWK_FILE: %w", err)
	}
	imp := formats.NewJWKImporter()
	kp, err := imp.Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return fmt.Errorf("import payment JWK: %w", err)
	}

	var check formats.JWK
	if err := json.Unmarshal(raw, &check); err != nil {
		log.Printf("[SIGNER][JWK] unmarshal err=%v", err)
	}

	xb, errX := base64.RawURLEncoding.DecodeString(check.X)
	yb, errY := base64.RawURLEncoding.DecodeString(check.Y)
	db, errD := base64.RawURLEncoding.DecodeString(check.D)

	log.Printf("[SIGNER][JWK] kid=%s alg=%s crv=%s use=%s", check.Kid, check.Alg, check.Crv, check.Use)
	if errX != nil || errY != nil {
		log.Printf("[SIGNER][JWK] coord decode error x=%v y=%v", errX, errY)
	}
	log.Printf("[SIGNER][JWK] X=%x", xb)
	log.Printf("[SIGNER][JWK] Y=%x", yb)
	if errD == nil {
		log.Printf("[SIGNER][JWK] D=%x  (TEST ONLY! NEVER LOG IN PROD)", db)
	} else {
		log.Printf("[SIGNER][JWK] D=<none/err=%v>", errD)
	}

	// 비압축 공개키 0x04 || X || Y
	uncompressed := make([]byte, 1+32+32)
	uncompressed[0] = 0x04
	copy(uncompressed[1:33], xb)
	copy(uncompressed[33:], yb)
	log.Printf("[SIGNER][JWK] pub(uncompressed)=0x%s", hex.EncodeToString(uncompressed))

	// 압축 공개키 0x02/0x03 || X   (Y 짝/홀로 prefix 결정)
	prefix := byte(0x02)
	if (yb[len(yb)-1] & 1) == 1 {
		prefix = 0x03
	}
	compressed := append([]byte{prefix}, xb...)
	log.Printf("[SIGNER][JWK] pub(compressed)=0x%s", hex.EncodeToString(compressed))

	// (선택) 이더리움 주소 추출: keccak256(uncompressed[1:])의 마지막 20바이트
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	sum := h.Sum(nil)
	addr := sum[12:]
	log.Printf("[SIGNER][JWK] eth_address=0x%s  (compare with DID suffix)", hex.EncodeToString(addr))

	// (선택) *ecdsa.PublicKey로도 만들어서 좌표/커브 확인
	pub := &ecdsa.PublicKey{
		Curve: secp256k1.S256(),
		X:     new(big.Int).SetBytes(xb),
		Y:     new(big.Int).SetBytes(yb),
	}
	log.Printf("[SIGNER][JWK] ecdsa pub OK? X=%x Y=%x curve=%T", pub.X, pub.Y, pub.Curve)
	didStr := strings.TrimSpace(os.Getenv("PAYMENT_DID"))
	if didStr == "" {
		if ecdsaPriv, ok := kp.PrivateKey().(*ecdsa.PrivateKey); ok {
			addr := ethcrypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex()
			didStr = "did:sage:ethereum:" + addr

		} else if id := strings.TrimSpace(kp.ID()); id != "" {
			didStr = "did:sage:generated:" + id
		} else {
			return fmt.Errorf("PAYMENT_DID not set and cannot derive from key")
		}
	}

	p.myKey = kp
	p.myDID = did.AgentDID(didStr)
	p.a2a = a2aclient.NewA2AClient(p.myDID, p.myKey, http.DefaultClient)
	return nil
}

// ---- utils ----

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envBool(k string, d bool) bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(k))); v != "" {
		return v == "1" || v == "true" || v == "on" || v == "yes"
	}
	return d
}
