package payment

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io/ioutil" // 추가
	"net/http"
	"os"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/sage-x-project/sage-a2a-go/pkg/signer"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
	agentcrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// --- (추가) demo keys json 포맷 ---
type demoKey struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PrivateKey string `json:"privateKey"` // hex (without 0x or with 0x 모두 허용)
	Address    string `json:"address"`    // 0x....
	PublicKey  string `json:"publicKey"`  // optional
}

func loadKeyFromFile(path, name string) (*ecdsa.PrivateKey, did.AgentDID, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read keys: %w", err)
	}
	var all []demoKey
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, "", fmt.Errorf("parse keys: %w", err)
	}
	for _, k := range all {
		if k.Name == name {
			priv, err := ethcrypto.HexToECDSA(trim0x(k.PrivateKey))
			if err != nil {
				return nil, "", fmt.Errorf("private key parse: %w", err)
			}
			// DID 우선순위: 파일의 DID 있으면 사용, 없으면 주소로 구성
			didStr := k.DID
			if didStr == "" {
				addr := ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()
				didStr = fmt.Sprintf("did:sage:ethereum:%s", addr)
			}
			return priv, did.AgentDID(didStr), nil
		}
	}
	return nil, "", fmt.Errorf("key '%s' not found in %s", name, path)
}
func trim0x(s string) string {
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		return s[2:]
	}
	return s
}

// --- (추가) ecdsa -> agentcrypto.KeyPair 어댑터 ---
type ecdsaKeyPair struct{ priv *ecdsa.PrivateKey }

func (k *ecdsaKeyPair) Type() agentcrypto.KeyType     { return agentcrypto.KeyTypeSecp256k1 }
func (k *ecdsaKeyPair) PublicKey() crypto.PublicKey   { return &k.priv.PublicKey }
func (k *ecdsaKeyPair) PrivateKey() crypto.PrivateKey { return k.priv }
func (k *ecdsaKeyPair) ID() string                    { return "" } // 데모에선 미사용
func (k *ecdsaKeyPair) Sign(msg []byte) ([]byte, error) {
	h := ethcrypto.Keccak256(msg)
	return ethcrypto.Sign(h, k.priv) // 65바이트 (recovery 포함)
}
func (k *ecdsaKeyPair) Verify(message, signature []byte) error {
	// 데모에선 검증 호출 안 쓰므로 no-op로 두거나 필요 시 구현
	return nil
}

// --- 기존 PaymentAgent struct는 그대로 ---
type PaymentAgent struct {
	Name        string
	ExternalURL string
	myDID       did.AgentDID
	myKey       agentcrypto.KeyPair
	httpSign    *a2autil.SignedHTTPClient
}

// 🔧 여기만 최소 수정
func NewPaymentAgent(name string) *PaymentAgent {
	ext := os.Getenv("PAYMENT_EXTERNAL_URL")
	if ext == "" {
		ext = "http://localhost:5500"
	}

	// (중요) 등록된 키를 파일에서 로드
	keyFile := envOr("PAYMENT_KEY_FILE", "generated_agent_keys.json")
	keyName := envOr("PAYMENT_KEY_NAME", "payment")

	priv, didVal, err := loadKeyFromFile(keyFile, keyName)
	if err != nil {
		// 안전망: 실패 시 종료보단 로그 + 에페메럴 경고(원하지 않으면 Fatal로 바꿔도 됨)
		panic(fmt.Errorf("payment key load failed: %w", err))
	}
	kp := &ecdsaKeyPair{priv: priv}

	s := signer.NewDefaultA2ASigner()
	hc := a2autil.NewSignedHTTPClient(didVal, kp, s, http.DefaultClient)

	return &PaymentAgent{
		Name:        name,
		ExternalURL: ext,
		myDID:       didVal,
		myKey:       kp,
		httpSign:    hc,
	}
}

// 그대로
func (p *PaymentAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	reqBody, _ := json.Marshal(msg)
	url := p.ExternalURL + "/process"

	resp, err := p.httpSign.PostJSON(ctx, url, reqBody)
	if err != nil {
		return types.AgentMessage{}, fmt.Errorf("outbound http: %w", err)
	}
	defer resp.Body.Close()

	var out types.AgentMessage
	buf := new(bytes.Buffer)
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

// (작은 유틸)
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
