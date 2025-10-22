// agents/payment/agent.go
package payment

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"

	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// PaymentAgent performs in-proc call from Root and signs outbound HTTP to external payment.
type PaymentAgent struct {
	Name        string
	ExternalURL string

	myDID did.AgentDID
	myKey sagecrypto.KeyPair

	a2a *a2aclient.A2AClient
}

// NewPaymentAgent loads KeyPair from JWK and builds an A2A client.
// Env:
//
//	PAYMENT_JWK_FILE  (e.g., keys/payment.jwk)  [required]
//	PAYMENT_DID       (optional; if empty and key is secp256k1, DID is derived from 0xAddress)
//	PAYMENT_EXTERNAL_URL  (default: http://localhost:5500)
func NewPaymentAgent(name string) *PaymentAgent {
	ext := strings.TrimRight(envOr("PAYMENT_EXTERNAL_URL", "http://localhost:5500"), "/")

	jwkPath := envOr("PAYMENT_JWK_FILE", "")
	if jwkPath == "" {
		panic("PAYMENT_JWK_FILE is required (points to a private JWK file)")
	}
	jwkBytes, err := os.ReadFile(jwkPath)
	if err != nil {
		panic(fmt.Errorf("read PAYMENT_JWK_FILE: %w", err))
	}

	// Import JWK → sagecrypto.KeyPair
	jwkImp := formats.NewJWKImporter()
	kp, err := jwkImp.Import(jwkBytes, sagecrypto.KeyFormatJWK)
	if err != nil {
		panic(fmt.Errorf("import JWK: %w", err))
	}

	// DID resolution: prefer env, else derive from secp256k1 address
	didStr := strings.TrimSpace(os.Getenv("PAYMENT_DID"))
	if didStr == "" {
		// Best-effort DID from ethereum address if key is secp256k1
		if ecdsaPriv, ok := kp.PrivateKey().(*ecdsa.PrivateKey); ok {
			addr := ethcrypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex()
			didStr = fmt.Sprintf("did:sage:ethereum:%s", addr)
		} else {
			// Fallback using key id if present
			id := strings.TrimSpace(kp.ID())
			if id == "" {
				panic("PAYMENT_DID not set and cannot derive DID from key; set PAYMENT_DID")
			}
			didStr = "did:sage:generated:" + id
		}
	}
	agentDID := did.AgentDID(didStr)

	// Build A2A client (auto RFC9421 signing)
	httpCli := a2aclient.NewA2AClient(agentDID, kp, http.DefaultClient)

	return &PaymentAgent{
		Name:        name,
		ExternalURL: ext,
		myDID:       agentDID,
		myKey:       kp,
		a2a:         httpCli,
	}
}

// Process: internal call (from Root) → signed HTTP to external payment → return external response
func (p *PaymentAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	reqBody, _ := json.Marshal(msg)
	url := p.ExternalURL + "/process"

	// Add Content-Digest to detect body tampering by gateway.
	digest := a2autil.ComputeContentDigest(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return types.AgentMessage{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Digest", digest)

	resp, err := p.a2a.Do(ctx, req)
	if err != nil {
		return types.AgentMessage{}, fmt.Errorf("outbound http: %w", err)
	}
	defer resp.Body.Close()

	var out types.AgentMessage
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)

	if resp.StatusCode/100 != 2 {
		out = types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "external-payment",
			To:        msg.From,
			Type:      "error",
			Content:   fmt.Sprintf("external error: %d %s", resp.StatusCode, buf.String()),
			Timestamp: time.Now(),
		}
		return out, nil
	}

	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		out = types.AgentMessage{
			ID:        msg.ID + "-ext",
			From:      "external-payment",
			To:        msg.From,
			Type:      "response",
			Content:   buf.String(),
			Timestamp: time.Now(),
		}
	}
	return out, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
