package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	servermw "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func main() {
	port := flag.Int("port", 19083, "port")
	keysFile := flag.String("keys", "keys/all_keys.json", "public keys file for inbound DID verify")
	requireSig := flag.Bool("require", true, "require signature (false = optional)")
	flag.Parse()

	// Build DID middleware (local file verifier)
	var mw *servermw.DIDAuthMiddleware
	if v, err := newLocalDIDVerifier(*keysFile); err != nil {
		log.Printf("[external-payment] DID verifier init failed: %v (running without verify)", err)
	} else {
		mw = servermw.NewDIDAuthMiddlewareWithVerifier(v)
		mw.SetOptional(!*requireSig)
	}

	open := http.NewServeMux()
	open.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "external-payment",
			"type":         "payment",
			"sage_enabled": true,
			"time":         time.Now().Format(time.RFC3339),
		})
	})

	protected := http.NewServeMux()
	protected.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		// read body
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		r.Body.Close()

		bodyStr := strings.ReplaceAll(buf.String(), "\n", "\\n")
		// Content-Digest check (tamper detection)
		if *requireSig {
			// SAGE ON : Read RFC-9421 and verify signatrue
			sig := r.Header.Get("Signature")
			sigIn := r.Header.Get("Signature-Input")
			want := r.Header.Get("Content-Digest")
			got := a2autil.ComputeContentDigest(buf.Bytes())

			log.Printf("[RX][STRICT] sig=%q sig-input=%q digest(want)=%q got=%q", sig, sigIn, want, got)
			log.Printf("[RX][STRICT] body=%s", bodyStr)

			if want != "" && want != got {
				log.Printf(
					"⚠️ MITM DETECTED: Content-Digest mismatch\n  method=%s path=%s remote=%s req_id=%s\n  digest_recv=%q\n  digest_calc=%q\n  sig_input=%q\n  sig=%q\n  body_sample=%q",
					r.Method, r.URL.Path, r.RemoteAddr, requestID(r), want, got, sigIn, sig, safeSample(bodyStr, 256),
				)
				http.Error(w, "content-digest mismatch (tampered in transit)", http.StatusUnauthorized)
				return
			}
		} else {
			// SAGE OFF
			log.Printf("[RX][LENIENT] SAGE disabled; accepting without signature checks")
			log.Printf("[RX][LENIENT] body=%s", bodyStr)
		}

		var in types.AgentMessage
		if err := json.Unmarshal(buf.Bytes(), &in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// Do "payment" work (demo: echo)
		out := types.AgentMessage{
			ID:        in.ID + "-ok",
			From:      "external-payment",
			To:        in.From,
			Type:      "response",
			Content:   fmt.Sprintf("External payment processed at %s (echo): %s", time.Now().Format(time.RFC3339), strings.TrimSpace(in.Content)),
			Timestamp: time.Now(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	var handler http.Handler = open
	if mw != nil {
		wrapped := mw.Wrap(protected)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/status":
				open.ServeHTTP(w, r)
			default:
				wrapped.ServeHTTP(w, r)
			}
		})
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[boot] external-payment on %s (requireSig=%v)", addr, *requireSig)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// ------- local DID verification (file-based) -------

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

func newLocalDIDVerifier(keysPath string) (verifier.DIDVerifier, error) {
	if stat, err := os.Stat(keysPath); err != nil || stat.IsDir() {
		return nil, fmt.Errorf("keys file not found: %s", keysPath)
	}
	db, err := loadLocalKeys(keysPath)
	if err != nil {
		return nil, err
	}
	client := &fileEthereumClient{db: db}
	selector := verifier.NewDefaultKeySelector(client)
	sigVerifier := verifier.NewRFC9421Verifier()
	return verifier.NewDefaultDIDVerifier(client, selector, sigVerifier), nil
}

func loadLocalKeys(path string) (*localKeys, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var data struct {
		Agents []struct{ DID, PublicKey, Type string } `json:"agents"`
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
			log.Printf("[ext] skip DID %s: %v", a.DID, err)
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
	pub, err := did.UnmarshalPublicKey(b, "secp256k1")
	if err != nil {
		return nil, fmt.Errorf("unmarshal pubkey: %w", err)
	}
	if ecdsaPK, ok := pub.(*ecdsa.PublicKey); ok {
		return ecdsaPK, nil
	}
	return nil, fmt.Errorf("unexpected key type %T", pub)
}

func mustUncompressedECDSA(pk *ecdsa.PublicKey) []byte {
	byteLen := (pk.Curve.Params().BitSize + 7) / 8
	out := make([]byte, 1+2*byteLen)
	out[0] = 0x04
	pk.X.FillBytes(out[1 : 1+byteLen])
	pk.Y.FillBytes(out[1+byteLen:])
	return out
}

func requestID(r *http.Request) string {
	if v := r.Header.Get("X-Request-Id"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Correlation-Id"); v != "" {
		return v
	}
	return "-"
}
func safeSample(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
