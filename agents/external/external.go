package external

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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	servermw "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"

	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// ExternalPaymentServer is a standalone HTTP service that verifies RFC9421 signatures
// and Content-Digest before processing /process requests from internal PaymentAgent.
type ExternalPaymentServer struct {
	Name         string
	Port         int
	RequireSAGE  bool // if true, signature is required; if false, middleware is optional
	keysFilePath string

	didVerifier verifier.DIDVerifier
	didMW       *servermw.DIDAuthMiddleware

	httpSrv *http.Server
}

// NewExternalPaymentServer creates a new external payment server.
func NewExternalPaymentServer(name string, port int, keysFilePath string, requireSAGE bool) (*ExternalPaymentServer, error) {
	s := &ExternalPaymentServer{
		Name:         name,
		Port:         port,
		RequireSAGE:  requireSAGE,
		keysFilePath: keysFilePath,
	}
	if err := s.initVerifier(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ExternalPaymentServer) initVerifier() error {
	db, err := loadLocalKeys(s.keysFilePath)
	if err != nil {
		return fmt.Errorf("load keys: %w", err)
	}
	client := &fileEthereumClient{db: db}
	selector := verifier.NewDefaultKeySelector(client)
	sigVerifier := verifier.NewRFC9421Verifier()
	s.didVerifier = verifier.NewDefaultDIDVerifier(client, selector, sigVerifier)

	mw := servermw.NewDIDAuthMiddlewareWithVerifier(s.didVerifier)
	mw.SetOptional(!s.RequireSAGE)
	s.didMW = mw
	return nil
}

// Start runs the HTTP server with /status (open) and /process (protected) routes.
func (s *ExternalPaymentServer) Start(ctx context.Context) error {
	open := http.NewServeMux()
	open.HandleFunc("/status", s.handleStatus)
	open.HandleFunc("/toggle-sage", s.handleToggleSAGE) // POST {"enabled":true|false}

	protected := http.NewServeMux()
	protected.HandleFunc("/process", s.handleProcess)

	var root http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status", "/toggle-sage":
			open.ServeHTTP(w, r)
		default:
			s.didMW.Wrap(protected).ServeHTTP(w, r)
		}
	})

	s.httpSrv = &http.Server{
		Addr:              fmt.Sprintf(":%d", s.Port),
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("[external-payment] starting on :%d (requireSAGE=%v) keys=%s\n", s.Port, s.RequireSAGE, s.keysFilePath)

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		_ = s.httpSrv.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		return err
	}
}

// --- Handlers ---

func (s *ExternalPaymentServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"name":          s.Name,
		"type":          "external-payment",
		"port":          s.Port,
		"require_sage":  s.RequireSAGE,
		"keys_registry": s.keysFilePath,
		"time":          time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *ExternalPaymentServer) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.RequireSAGE = req.Enabled
	if s.didMW != nil {
		s.didMW.SetOptional(!s.RequireSAGE)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"requireSAGE": s.RequireSAGE,
	})
}

func (s *ExternalPaymentServer) handleProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body (and keep a copy for verifying content-digest)
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r.Body); err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}
	body := buf.Bytes()

	// Verify Content-Digest (sha-256)
	if err := verifyContentDigestHeader(r.Header.Get("Content-Digest"), body); err != nil {
		http.Error(w, "content-digest mismatch: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Parse message
	var msg types.AgentMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// Extract DID from context (middleware sets it only if signature present & valid)
	agentDID, _ := servermw.GetAgentDIDFromContext(r.Context())

	// Simulate payment-side business logic
	out := types.AgentMessage{
		ID:        msg.ID + "-ok",
		From:      s.Name,   // external payment service name
		To:        msg.From, // reply to original sender
		Type:      "response",
		Timestamp: time.Now(),
		Content:   fmt.Sprintf("OK: received %q (from=%s)", msg.Content, agentDID),
		Metadata: map[string]any{
			"verified_did": string(agentDID),
			"note":         "RFC9421 signature + Content-Digest verified",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// --- Content-Digest verification (sha-256) ---

func verifyContentDigestHeader(headerVal string, body []byte) error {
	if strings.TrimSpace(headerVal) == "" {
		return fmt.Errorf("missing Content-Digest header")
	}
	// Accept formats like:
	//   "sha-256=:BASE64:”
	//   "sha-256=<base64>"
	//   "sha-256=:<base64>:, other=..." (we parse first token)
	part := strings.Split(headerVal, ",")[0]
	part = strings.TrimSpace(part)

	if !strings.HasPrefix(strings.ToLower(part), "sha-256") {
		return fmt.Errorf("unsupported digest alg (want sha-256)")
	}
	// Extract the value after '=' and strip optional surrounding colons
	eq := strings.Index(part, "=")
	if eq < 0 || eq == len(part)-1 {
		return fmt.Errorf("malformed Content-Digest")
	}
	val := strings.TrimSpace(part[eq+1:])
	val = strings.Trim(val, ":") // drop surrounding colons if present

	expectedB64 := val
	// Compute actual
	sum := sha256.Sum256(body)
	actualB64 := base64.StdEncoding.EncodeToString(sum[:])

	if expectedB64 != actualB64 {
		return fmt.Errorf("expected %s got %s", expectedB64, actualB64)
	}
	return nil
}

// --- Local DID verification helpers (file-based) ---

type localKeys struct {
	pub  map[did.AgentDID]map[did.KeyType]interface{}
	keys map[did.AgentDID][]did.AgentKey
}

type fileEthereumClient struct{ db *localKeys }

func (c *fileEthereumClient) ResolveAllPublicKeys(ctx context.Context, agentDID did.AgentDID) ([]did.AgentKey, error) {
	if k, ok := c.db.keys[agentDID]; ok {
		return k, nil
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

func loadLocalKeys(path string) (*localKeys, error) {
	if path == "" {
		// default
		path = filepath.Join("keys", "all_keys.json")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var data struct {
		Agents []struct {
			DID       string `json:"did"`
			PublicKey string `json:"publicKey"` // uncompressed 0x04...
			Type      string `json:"type"`      // expect "secp256k1"
		} `json:"agents"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	db := &localKeys{
		pub:  make(map[did.AgentDID]map[did.KeyType]interface{}),
		keys: make(map[did.AgentDID][]did.AgentKey),
	}
	for _, a := range data.Agents {
		d := did.AgentDID(a.DID)
		if _, ok := db.pub[d]; !ok {
			db.pub[d] = make(map[did.KeyType]interface{})
		}
		pk, err := parseSecp256k1ECDSAPublicKey(a.PublicKey)
		if err != nil {
			// skip invalid entries but keep server running
			fmt.Printf("[external-payment] skip DID %s: %v\n", a.DID, err)
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
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(hexStr), "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid hex pubkey: %w", err)
	}
	pub, err := did.UnmarshalPublicKey(raw, "secp256k1")
	if err != nil {
		return nil, fmt.Errorf("unmarshal pubkey: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("unexpected key type %T", pub)
	}
	return ec, nil
}

func mustUncompressedECDSA(pk *ecdsa.PublicKey) []byte {
	byteLen := (pk.Curve.Params().BitSize + 7) / 8
	out := make([]byte, 1+2*byteLen)
	out[0] = 0x04
	pk.X.FillBytes(out[1 : 1+byteLen])
	pk.Y.FillBytes(out[1+byteLen:])
	return out
}
