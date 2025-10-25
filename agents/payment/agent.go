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
