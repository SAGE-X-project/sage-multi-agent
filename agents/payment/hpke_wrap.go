package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
	"github.com/sage-x-project/sage/pkg/agent/hpke"
	"github.com/sage-x-project/sage/pkg/agent/session"

	prototx "github.com/sage-x-project/sage-multi-agent/protocol"
)

type HPKEConfig struct {
	Enable   bool
	KeysFile string
}

type hpkeState struct {
	cli  *hpke.Client
	sMgr *session.Manager
	kid  string
}

var hpkeStates sync.Map

type agentKeyRow struct {
	Name string `json:"name"`
	DID  string `json:"did"`
}

func (p *PaymentAgent) EnableHPKE(ctx context.Context, cfg HPKEConfig) error {
	if !cfg.Enable {
		return nil
	}
	// A2A 서명에 쓰는 키/디드를 재사용
	if p.myKey == nil || strings.TrimSpace(string(p.myDID)) == "" {
		if err := p.initSigning(); err != nil {
			return fmt.Errorf("HPKE: initSigning failed: %w", err)
		}
	}

	keysFile := strings.TrimSpace(cfg.KeysFile)
	if keysFile == "" {
		keysFile = "merged_agent_keys.json"
	}
	nameToDID, err := loadDIDsFromKeys(keysFile)
	if err != nil {
		return fmt.Errorf("HPKE: load keys: %w", err)
	}
	clientDID := strings.TrimSpace(nameToDID["payment"])
	serverDID := strings.TrimSpace(nameToDID["external"])
	if clientDID == "" {
		return fmt.Errorf("HPKE: client DID not found for name 'payment' in %s", keysFile)
	}
	if serverDID == "" {
		return fmt.Errorf("HPKE: server DID not found for name 'external' in %s", keysFile)
	}

	// Resolver
	rpc := firstNonEmpty(os.Getenv("ETH_RPC_URL"), "http://127.0.0.1:8545")
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_V4_ADDRESS"), "0x5FbDB2315678afecb367f032d93F642f64180aa3")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	cfgV4 := &sagedid.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv,
		GasPrice:           0,
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}
	ethV4, err := dideth.NewEthereumClientV4(cfgV4)
	if err != nil {
		return fmt.Errorf("HPKE: init resolver: %w", err)
	}

	// 세션 매니저
	sMgr := session.NewManager()

	// Handshake 전송체: A2ATransport (hpkeHandshake=true)
	t := prototx.NewA2ATransport(p, p.ExternalURL, true)

	// HPKE 클라이언트
	cli := hpke.NewClient(t, ethV4, p.myKey, clientDID, hpke.DefaultInfoBuilder{}, sMgr)

	// Initialize
	ctxInit, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctxID := "ctx-" + uuid.NewString()
	kid, err := cli.Initialize(ctxInit, ctxID, clientDID, serverDID)
	if err != nil {
		return fmt.Errorf("HPKE Initialize: %w", err)
	}
	if kid == "" {
		return fmt.Errorf("HPKE Initialize returned empty kid")
	}

	log.Printf("[payment] HPKE initialized kid=%s clientDID=%s serverDID=%s", kid, clientDID, serverDID)

	hpkeStates.Store(p, &hpkeState{cli: cli, sMgr: sMgr, kid: kid})
	return nil
}

func (p *PaymentAgent) encryptIfHPKE(plaintext []byte) ([]byte, string, bool, error) {
	v, ok := hpkeStates.Load(p)
	if !ok {
		return nil, "", false, nil
	}
	st := v.(*hpkeState)

	sess, ok := st.sMgr.GetByKeyID(st.kid)
	if !ok {
		return nil, "", true, fmt.Errorf("HPKE: session not found for kid=%s", st.kid)
	}
	ct, err := sess.Encrypt(plaintext)
	if err != nil {
		return nil, "", true, fmt.Errorf("HPKE encrypt: %w", err)
	}
	return ct, st.kid, true, nil
}

func (p *PaymentAgent) decryptIfHPKEResponse(kid string, data []byte) ([]byte, bool, error) {
	if kid == "" {
		// HPKE 미사용
		return data, false, nil
	}
	v, ok := hpkeStates.Load(p)
	if !ok {
		return nil, true, fmt.Errorf("HPKE: state missing")
	}
	st := v.(*hpkeState)

	sess, ok := st.sMgr.GetByKeyID(kid)
	if !ok {
		return nil, true, fmt.Errorf("HPKE: session not found for kid=%s", kid)
	}
	pt, err := sess.Decrypt(data)
	if err != nil {
		return nil, true, fmt.Errorf("HPKE decrypt response: %w", err)
	}
	return pt, true, nil
}

func loadDIDsFromKeys(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []agentKeyRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		if n := strings.TrimSpace(r.Name); n != "" && strings.TrimSpace(r.DID) != "" {
			m[n] = r.DID
		}
	}
	return m, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
