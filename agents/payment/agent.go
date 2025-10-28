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
// Frontend-driven behavior (per-request):
//   - Signing (SAGE): metadata["sageEnabled"] = true|false
//   - HPKE:           metadata["hpkeEnabled"] = true|false (effective only when SAGE is true)
// Notes:
//   - If HPKE is requested and no session exists, the agent performs a lazy HPKE handshake.
//   - If HPKE is not requested, plaintext is sent even if a previous session exists.
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

// ctx key for per-request SAGE signing override
type ctxKey int
const ctxKeySAGEOverride ctxKey = iota

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

// forwardExternal sends msg to the external payment over HTTP via Gateway.
// It applies per-request SAGE/HPKE overrides derived from msg.Metadata.
func (p *PaymentAgent) forwardExternal(ctx context.Context, msg *types.AgentMessage) (*types.AgentMessage, error) {
    body, _ := json.Marshal(msg)

    // Per-request overrides (from metadata)
    useSAGE := p.SAGEEnabled
    // Default to current session state so process-start HPKE (demo_SAGE.sh) works
    wantHPKE := p.IsHPKEEnabled()
    if msg.Metadata != nil {
        if v, ok := msg.Metadata["sageEnabled"]; ok {
            useSAGE = parseBoolLike(v, useSAGE)
        }
        if v, ok := msg.Metadata["hpkeEnabled"]; ok {
            wantHPKE = parseBoolLike(v, false)
        }
    }
    // Policy: HPKE requires SAGE signing
    if !useSAGE {
        wantHPKE = false
    }
    // Prepare context override for SAGE signing (apply to both handshake and data send)
    ctxReq := ctx
    if useSAGE != p.SAGEEnabled {
        ctxReq = context.WithValue(ctxReq, ctxKeySAGEOverride, useSAGE)
    }
    // If HPKE requested but no session yet, initialize lazily (handshake uses the same ctx override)
    if wantHPKE && !p.IsHPKEEnabled() {
        keys := hpkeKeysPath()
        if err := p.EnableHPKE(ctxReq, HPKEConfig{Enable: true, KeysFile: keys}); err != nil {
            log.Printf("[payment] HPKE init failed (lazy): %v", err)
        }
    }

    // When HPKE is requested, encrypt; otherwise force plaintext even if a session exists
    var kid string
    if wantHPKE {
        if ct, k, used, err := p.encryptIfHPKE(body); used {
            if err != nil {
                return nil, fmt.Errorf("hpke: %w", err)
            }
            body = ct
            kid = k
            log.Printf("[encrypt] hpke kid=%s bytes=%d", k, len(ct))
        } else {
            log.Printf("[payment] HPKE requested but no session; sending plaintext bytes=%d", len(body))
        }
    } else {
        log.Printf("[payment] HPKE disabled by request (plaintext) bytes=%d", len(body))
    }

    // Always send via transport (A2A transport applies headers; HPKE data uses hpke_kid)
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
    // Send using the same context override
    resp, err := p.txPlain.Send(ctxReq, sm)
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
    useSign := p.SAGEEnabled
    if v := ctx.Value(ctxKeySAGEOverride); v != nil {
        if b, ok := v.(bool); ok {
            useSign = b
        }
    }
    if useSign {
        if p.A2A == nil {
            if err := p.initSigning(); err != nil {
                return nil, err
            }
        }
        return p.A2A.Do(ctx, req)
    }
    return p.httpClient.Do(req)
}

// Resolve HPKE keys file path in the same way demo scripts do (env-driven)
func hpkeKeysPath() string {
    if v := strings.TrimSpace(os.Getenv("HPKE_KEYS")); v != "" { return v }
    if v := strings.TrimSpace(os.Getenv("PAYMENT_HPKE_KEYS")); v != "" { return v }
    if v := strings.TrimSpace(os.Getenv("HPKE_KEYS_PATH")); v != "" { return v }
    return "generated_agent_keys.json"
}

// parseBoolLike converts assorted types/strings to bool for metadata flags
func parseBoolLike(v any, def bool) bool {
    switch t := v.(type) {
    case bool:
        return t
    case string:
        low := strings.ToLower(strings.TrimSpace(t))
        return low == "1" || low == "true" || low == "on" || low == "yes"
    case float64:
        return t != 0
    default:
        return def
    }
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
