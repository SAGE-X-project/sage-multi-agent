package payment

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	prototx "github.com/sage-x-project/sage-multi-agent/protocol"
	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// PaymentAgent runs IN-PROC and (optionally) calls an EXTERNAL payment service.
type PaymentAgent struct {
	Name        string
	SAGEEnabled bool

	ExternalURL string

	myDID did.AgentDID
	myKey sagecrypto.KeyPair
	A2A   *a2aclient.A2AClient

	httpClient *http.Client

    // Always route through transport (for plaintext/HPKE data transfer)
	txPlain transport.MessageTransport
}

func NewPaymentAgent(name string) *PaymentAgent {
	p := &PaymentAgent{
		Name:        name,
		SAGEEnabled: envBool("PAYMENT_SAGE_ENABLED", true),
		ExternalURL: strings.TrimRight(envOr("PAYMENT_EXTERNAL_URL", "http://localhost:5500"), "/"),
		httpClient:  http.DefaultClient,
	}
    // Use transport for plaintext/HPKE data as well (internally calls p.Do() → A2A signing)
	p.txPlain = prototx.NewA2ATransport(p, p.ExternalURL, false)
	return p
}

// IN-PROC entrypoint (Root -> Payment)
func (p *PaymentAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	if p.ExternalURL != "" {
		out, err := p.forwardExternal(ctx, &msg)
		if err == nil {
			return *out, nil
		}
		return types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "payment",
			To:        msg.From,
			Type:      "error",
			Content:   fmt.Sprintf("payment upstream error: %v", err),
			Timestamp: time.Now(),
		}, nil
	}
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

    // When HPKE is enabled, encrypt (must call)
	var kid string
	if ct, k, used, err := p.encryptIfHPKE(body); used {
		if err != nil {
			return nil, fmt.Errorf("hpke: %w", err)
		}
		body = ct
		kid = k
		log.Printf("[encrypt] hpke kid=%s bytes=%d", k, len(ct))
	} else {
		log.Printf("[payment] HPKE not used (plaintext) bytes=%d", len(body))
	}

    // Always send via transport
	sm := &transport.SecureMessage{
		ID:      uuid.NewString(),
		Payload: body,
		DID:     string(p.myDID),
		Metadata: map[string]string{
			"ctype": "application/json",
		},
		Role: "agent",
	}
	if kid != "" {
        sm.Metadata["hpke_kid"] = kid // A2ATransport will set HPKE headers automatically
	}

	resp, err := p.txPlain.Send(ctx, sm)
	if err != nil {
		return nil, fmt.Errorf("transport send: %w", err)
	}
	if !resp.Success {
		reason := strings.TrimSpace(string(resp.Data))
		if reason == "" && resp.Error != nil {
			reason = resp.Error.Error()
		}
		if reason == "" {
			reason = "unknown upstream error"
		}
		return &types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "external-payment",
			To:        msg.From,
			Type:      "error",
			Content:   "external error: " + reason,
			Timestamp: time.Now(),
		}, nil
	}
	if kid != "" {
		if pt, _, derr := p.decryptIfHPKEResponse(kid, resp.Data); derr != nil {
            // Server may return a plaintext error; wrap into message on failure
            return &types.AgentMessage{
				ID:        msg.ID + "-exterr",
				From:      "external-payment",
				To:        msg.From,
				Type:      "error",
				Content:   "external error: " + derr.Error(),
				Timestamp: time.Now(),
			}, nil
		} else {
			resp.Data = pt
		}
	}

	var out types.AgentMessage
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		out = types.AgentMessage{
			ID:        msg.ID + "-ext",
			From:      "external-payment",
			To:        msg.From,
			Type:      "response",
			Content:   strings.TrimSpace(string(resp.Data)),
			Timestamp: time.Now(),
		}
	}
	return &out, nil
}

func (p *PaymentAgent) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if p.SAGEEnabled {
		if p.A2A == nil {
			if err := p.initSigning(); err != nil {
				return nil, err
			}
		}
		return p.A2A.Do(ctx, req)
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
	p.A2A = a2aclient.NewA2AClient(p.myDID, p.myKey, http.DefaultClient)
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
