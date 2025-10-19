// package payment

package payment

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	server "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	keys "github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/did"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sage-x-project/sage-multi-agent/types"
	rfc9421 "github.com/sage-x-project/sage/pkg/agent/core/rfc9421"
)

// Wallet represents a cryptocurrency wallet
type Wallet struct {
	Address string             `json:"address"`
	Owner   string             `json:"owner"`
	Balance map[string]float64 `json:"balance"` // currency -> amount
}

// Transaction represents a payment transaction
type Transaction struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"`
	TxHash   string  `json:"tx_hash"`
}

// statusWriter captures HTTP status for logging
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// PaymentAgent handles cryptocurrency payments.
// In this version, the agent forwards /process to an external payment agent.
// Set PAYMENT_EXTERNAL_URL (or PAYMENT_UPSTREAM) to http://host:port of that external agent.
type PaymentAgent struct {
	Name         string
	Port         int
	SAGEEnabled  bool
	wallets      map[string]*Wallet
	transactions map[string]*Transaction

	// inbound verification
	didV  verifier.DIDVerifier
	didMW *server.DIDAuthMiddleware

	// outbound signing
	myDID     did.AgentDID
	myKeyPair sagecrypto.KeyPair
	a2a       *a2aclient.A2AClient
	myECDSA   *ecdsa.PrivateKey

	// upstream external payment endpoint base (e.g., http://localhost:28083)
	upstreamBase string
}

// NewPaymentAgent constructs the PaymentAgent.
// Upstream endpoint is read from env (PAYMENT_EXTERNAL_URL or PAYMENT_UPSTREAM).
func NewPaymentAgent(name string, port int) *PaymentAgent {
	pa := &PaymentAgent{
		Name:         name,
		Port:         port,
		SAGEEnabled:  true,
		wallets:      map[string]*Wallet{},
		transactions: map[string]*Transaction{},
	}
	// Load identity from keys/payment.key (optional but required to sign)
	if didStr, kp, err := loadAgentIdentity("payment"); err != nil {
		log.Printf("[payment] load identity failed: %v (will send unsigned upstream)", err)
	} else {
		pa.myDID = did.AgentDID(didStr)
		pa.myKeyPair = kp
		pa.a2a = a2aclient.NewA2AClient(pa.myDID, pa.myKeyPair, nil)
		if ecdsaPriv, err := loadAgentECDSA("payment"); err == nil {
			pa.myECDSA = ecdsaPriv
		} else {
			log.Printf("[payment] ECDSA load failed: %v (fallback to A2A signer)", err)
		}
	}
	// Upstream base from environment
	if v := strings.TrimSpace(os.Getenv("PAYMENT_EXTERNAL_URL")); v != "" {
		pa.upstreamBase = strings.TrimRight(v, "/")
	} else if v := strings.TrimSpace(os.Getenv("PAYMENT_UPSTREAM")); v != "" {
		pa.upstreamBase = strings.TrimRight(v, "/")
	}
	return pa
}

// Start HTTP server with DID auth middleware; optional flag tracks SAGEEnabled dynamically
// Start HTTP server: wrap ONLY /process with DIDAuth; keep /status, /toggle-sage open
func (pa *PaymentAgent) Start() error {
	db, err := loadLocalKeys()
	if err != nil {
		log.Printf("[payment] load keys failed: %v", err)
	}

	if db != nil {
		pa.didMW = server.NewDIDAuthMiddleware(&fileEthereumClient{db: db})
		pa.didMW.SetOptional(false)
	}

	public := http.NewServeMux()
	public.HandleFunc("/status", pa.handleStatus)
	public.HandleFunc("/toggle-sage", pa.handleToggleSAGE)

	unsecured := http.HandlerFunc(pa.handleProcessRequest)

	mux := http.NewServeMux()
	mux.Handle("/", public)
	mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		// Optional verbose signature logging for troubleshooting (external agent)
		// Enable by setting PAYMENT_DEBUG_SIG=true
		debugSig := strings.EqualFold(os.Getenv("PAYMENT_DEBUG_SIG"), "true") || os.Getenv("PAYMENT_DEBUG_SIG") == "1"

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		if debugSig {
			// Read and restore body to compute digest safely
			raw, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			sum := sha256.Sum256(raw)
			sumB64 := base64.StdEncoding.EncodeToString(sum[:])
			sig := r.Header.Get("Signature")
			sigIn := r.Header.Get("Signature-Input")
			cd := r.Header.Get("Content-Digest")
			// Quick match check: RFC9421 digest header looks like sha-256=:<b64>:
			digestMatches := cd != "" && strings.Contains(cd, sumB64)
			missingSig := sig == "" || sigIn == ""
			log.Printf("[payment][sig-debug] IN %s %s len=%d Sig=%t SigInput=%t Content-Digest=%q body-sha256=:%s:",
				r.Method, r.URL.Path, len(raw), sig != "", sigIn != "", cd, sumB64)
			// Emit MITM warning always; emit signature-missing warning only when SAGE is ON
			if !digestMatches {
				log.Printf("[payment][mitm-warning] digest mismatch or missing; possible tampering (Content-Digest vs body sha256 differ)")
			}
			if pa.SAGEEnabled && missingSig {
				log.Printf("[payment][sig-warning] missing signature headers; request may be unsigned")
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		if pa.SAGEEnabled && pa.didMW != nil {
			// Secured path (DID verification). Use status-capturing writer
			pa.didMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				unsecured.ServeHTTP(w, r)
			})).ServeHTTP(sw, r)
			if debugSig {
				if sw.status >= 400 {
					log.Printf("[payment][sig-debug] OUT %s %s status=%d (verification failed)", r.Method, r.URL.Path, sw.status)
				} else {
					log.Printf("[payment][sig-debug] OUT %s %s status=%d", r.Method, r.URL.Path, sw.status)
				}
			}
			return
		}
		// Open path when SAGE is OFF
		unsecured.ServeHTTP(w, r)
	})

	log.Printf("Payment Agent starting on port %d (SAGE=%v, upstream=%s)",
		pa.Port, pa.SAGEEnabled, pa.upstreamBase)

	return http.ListenAndServe(fmt.Sprintf(":%d", pa.Port), mux)
}

// handleProcessRequest receives an AgentMessage and forwards it to the external payment agent.
// If SAGE is ON and the agent has a DID+key, the outbound request is DID-signed.
func (pa *PaymentAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var msg types.AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// No upstream? Respond with a simple stub to keep the flow working.
	if pa.upstreamBase == "" {
		stub := types.AgentMessage{
			ID:        "payment-stub-" + fmt.Sprint(time.Now().UnixNano()),
			From:      pa.Name,
			To:        msg.From,
			Content:   "[payment] upstream not configured; handled locally (stub)",
			Type:      "response",
			Timestamp: time.Now(),
			Metadata:  map[string]any{"agent_type": "payment", "mode": "local-stub"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stub)
		return
	}

	// Forward the same message to the external payment agent
	body, _ := json.Marshal(msg)
	url := pa.upstreamBase + "/process"

	reqOut, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	reqOut.Header.Set("Content-Type", "application/json")
	if reqOut.Host == "" { // ensure authority set before signing
		reqOut.Host = reqOut.URL.Host
	}
	// Ensure Content-Digest present for downstream verifier
	{
		sum := sha256.Sum256(body)
		reqOut.Header.Set("Content-Digest", fmt.Sprintf("sha-256=:%s:", base64.StdEncoding.EncodeToString(sum[:])))
	}

	// Optional packet trace
	tracePackets := strings.EqualFold(os.Getenv("SAGE_PACKET_TRACE"), "true") || os.Getenv("SAGE_PACKET_TRACE") == "1"
	traceBody := strings.EqualFold(os.Getenv("SAGE_TRACE_INCLUDE_BODY"), "true") || os.Getenv("SAGE_TRACE_INCLUDE_BODY") == "1"
	if tracePackets {
		if traceBody {
			b := string(body)
			if len(b) > 512 {
				b = b[:512] + "..."
			}
			log.Printf("[packet-trace][payment→external] POST %s len=%d Digest=%q Sig=%t SigInput=%t Body=%s",
				url, len(body), reqOut.Header.Get("Content-Digest"), reqOut.Header.Get("Signature") != "", reqOut.Header.Get("Signature-Input") != "", b)
		} else {
			log.Printf("[packet-trace][payment→external] POST %s len=%d Digest=%q Sig=%t SigInput=%t",
				url, len(body), reqOut.Header.Get("Content-Digest"), reqOut.Header.Get("Signature") != "", reqOut.Header.Get("Signature-Input") != "")
		}
	}

	var resp *http.Response
	var err error
	if pa.SAGEEnabled && pa.myECDSA != nil {
		// Sign outbound request explicitly with RFC9421 es256k
		rparams := &rfc9421.SignatureInputParams{
			// Drop @authority to avoid proxy Host variations
			CoveredComponents: []string{"\"@method\"", "\"@target-uri\"", "\"content-type\"", "\"content-digest\""},
			KeyID:             string(pa.myDID),
			Algorithm:         "es256k",
			Created:           time.Now().Unix(),
		}
		signer := rfc9421.NewHTTPVerifier()
		if err = signer.SignRequest(reqOut, "sig1", rparams, pa.myECDSA); err != nil {
			http.Error(w, "sign error: "+err.Error(), http.StatusBadGateway)
			return
		}
		resp, err = http.DefaultClient.Do(reqOut)
	} else if pa.SAGEEnabled && pa.a2a != nil {
		// Fallback: A2A signer
		resp, err = pa.a2a.Do(r.Context(), reqOut)
	} else {
		// Plain HTTP when SAGE is OFF
		resp, err = http.DefaultClient.Do(reqOut)
	}
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Relay upstream response as-is to the caller (client-api → root → payment → external → back)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// handleStatus provides basic runtime info
func (pa *PaymentAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"name":          pa.Name,
		"type":          "payment",
		"port":          pa.Port,
		"sage_enabled":  pa.SAGEEnabled,
		"upstream_base": pa.upstreamBase,
		"time":          time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleToggleSAGE flips optional flag on middleware and affects outbound signing mode
func (pa *PaymentAgent) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	pa.SAGEEnabled = req.Enabled
	if pa.didMW != nil {
		pa.didMW.SetOptional(!pa.SAGEEnabled)
	}
	log.Printf("[payment] SAGE %v (verify optional=%v)", pa.SAGEEnabled, !pa.SAGEEnabled)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": req.Enabled})
}

// ---------- Local DID verification helpers ----------

type localKeys struct {
	pub  map[did.AgentDID]map[did.KeyType]interface{}
	keys map[did.AgentDID][]did.AgentKey
}
type fileEthereumClient struct{ db *localKeys }

func (c *fileEthereumClient) ResolveAllPublicKeys(ctx context.Context, agentDID did.AgentDID) ([]did.AgentKey, error) {
	if keys, ok := c.db.keys[agentDID]; ok {
		return keys, nil
	}
	return nil, fmt.Errorf("no keys for DID: %s", agentDID)
}
func (c *fileEthereumClient) ResolvePublicKeyByType(ctx context.Context, agentDID did.AgentDID, keyType did.KeyType) (interface{}, error) {
	if m, ok := c.db.pub[agentDID]; ok {
		if pk, ok2 := m[keyType]; ok2 {
			return pk, nil
		}
	}
	return nil, fmt.Errorf("key type %v not found for %s", keyType, agentDID)
}
func loadLocalKeys() (*localKeys, error) {
	path := filepath.Join("keys", "all_keys.json")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	// FIX: proper separate JSON tags
	var data struct {
		Agents []struct {
			DID       string `json:"did"`
			PublicKey string `json:"publicKey"`
			Type      string `json:"type"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	db := &localKeys{pub: make(map[did.AgentDID]map[did.KeyType]interface{}), keys: make(map[did.AgentDID][]did.AgentKey)}
	for _, a := range data.Agents {
		d := did.AgentDID(a.DID)
		if _, ok := db.pub[d]; !ok {
			db.pub[d] = make(map[did.KeyType]interface{})
		}
		pk, err := parseSecp256k1ECDSAPublicKey(a.PublicKey)
		if err != nil {
			log.Printf("[payment] skip DID %s: %v", a.DID, err)
			continue
		}
		db.pub[d][did.KeyTypeECDSA] = pk
		db.keys[d] = []did.AgentKey{{
			Type:      did.KeyTypeECDSA,
			KeyData:   mustUncompressedECDSA(pk),
			Verified:  true,
			CreatedAt: time.Now(),
		}}
	}
	return db, nil
}
func parseSecp256k1ECDSAPublicKey(hexStr string) (*ecdsa.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid hex pubkey: %w", err)
	}
	pub, err := secp256k1.ParsePubKey(b)
	if err != nil {
		return nil, fmt.Errorf("parse pubkey: %w", err)
	}
	return pub.ToECDSA(), nil
}
func mustUncompressedECDSA(pk *ecdsa.PublicKey) []byte {
	byteLen := (pk.Curve.Params().BitSize + 7) / 8
	out := make([]byte, 1+2*byteLen)
	out[0] = 0x04
	pk.X.FillBytes(out[1 : 1+byteLen])
	pk.Y.FillBytes(out[1+byteLen:])
	return out
}
func loadAgentIdentity(name string) (string, sagecrypto.KeyPair, error) {
	path := filepath.Join("keys", fmt.Sprintf("%s.key", name))
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	// FIX: proper separate JSON tags
	var rec struct {
		DID        string `json:"did"`
		PrivateKey string `json:"privateKey"`
	}
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return "", nil, err
	}
	privBytes, err := hex.DecodeString(strings.TrimPrefix(rec.PrivateKey, "0x"))
	if err != nil {
		return "", nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	priv := secp256k1.PrivKeyFromBytes(privBytes)
	kp, err := keys.NewSecp256k1KeyPair(priv, "")
	if err != nil {
		return "", nil, err
	}
	return rec.DID, kp, nil
}

// loadAgentECDSA reads keys/<name>.key and returns *ecdsa.PrivateKey
func loadAgentECDSA(name string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join("keys", fmt.Sprintf("%s.key", name))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rec struct{ DID, PrivateKey string }
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimPrefix(rec.PrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	sk := secp256k1.PrivKeyFromBytes(b)
	return sk.ToECDSA(), nil
}
