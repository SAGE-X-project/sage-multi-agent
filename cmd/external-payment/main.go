// cmd/external-payment/main.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"

	// DID / Resolver
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
	sagehttp "github.com/sage-x-project/sage/pkg/agent/transport/http"

	// HPKE
	"github.com/sage-x-project/sage/pkg/agent/hpke"
	"github.com/sage-x-project/sage/pkg/agent/session"
	"github.com/sage-x-project/sage/pkg/agent/transport"

	// Keys
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
)

// External Payment Server:
// - Verifies inbound RFC9421 (via DID middleware) over the HTTP body.
// - If request is HPKE-wrapped (Content-Type: application/sage+hpke OR X-SAGE-HPKE: v1):
//   - With X-KID and existing session  -> decrypt, handle JSON, encrypt response.
//   - Without X-KID or unknown KID     -> treat as HPKE handshake (transport.SecureMessage) and respond with HandleMessage().
//
// - Otherwise, handle plaintext JSON.
//
// Modifications:
//   - Server signing key is loaded from EXTERNAL_JWK_FILE (JWK; Ed25519 expected).
//   - Server HPKE KEM key is loaded from EXTERNAL_KEM_JWK_FILE (JWK; X25519 expected).
//   - Server DID is loaded from merged keys JSON (MERGED_KEYS_FILE; default ./merged_agent_keys.json) by EXTERNAL_AGENT_NAME (default "external").
//     Fallback: EXTERNAL_DID env when not found.
var (
	hpkeSrvMgr *session.Manager
	hpkeSrv    *hpke.Server
)

type agentKeyRow struct {
	Name string `json:"name"`
	DID  string `json:"did"`
}

// ---------- env helpers ----------
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Load Ed25519 signing key from EXTERNAL_JWK_FILE (JWK).
func loadServerSigningKeyFromEnv() sagecrypto.KeyPair {
	path := strings.TrimSpace(os.Getenv("EXTERNAL_JWK_FILE"))
	if path == "" {
		log.Fatal("missing EXTERNAL_JWK_FILE for server signing key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read EXTERNAL_JWK_FILE (%s): %v", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		log.Fatalf("import EXTERNAL_JWK_FILE (%s) as JWK: %v", path, err)
	}
	return kp
}

// Load X25519 KEM key from EXTERNAL_KEM_JWK_FILE (JWK).
func loadServerKEMFromEnv() sagecrypto.KeyPair {
	path := strings.TrimSpace(os.Getenv("EXTERNAL_KEM_JWK_FILE"))
	if path == "" {
		log.Fatal("missing EXTERNAL_KEM_JWK_FILE for server KEM key (JWK)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read EXTERNAL_KEM_JWK_FILE (%s): %v", path, err)
	}
	kp, err := formats.NewJWKImporter().Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		log.Fatalf("import EXTERNAL_KEM_JWK_FILE (%s) as JWK: %v", path, err)
	}
	return kp
}

// Create a MultiChain resolver from env (ETH_RPC_URL / SAGE_REGISTRY_V4_ADDRESS / SAGE_EXTERNAL_KEY).
func buildResolver() sagedid.Resolver {
	rpc := firstNonEmpty(os.Getenv("ETH_RPC_URL"), "http://127.0.0.1:8545")
	contract := firstNonEmpty(os.Getenv("SAGE_REGISTRY_V4_ADDRESS"), "0x5FbDB2315678afecb367f032d93F642f64180aa3")
	priv := strings.TrimPrefix(strings.TrimSpace(os.Getenv("SAGE_EXTERNAL_KEY")), "0x")

	cfgV4 := &sagedid.RegistryConfig{
		RPCEndpoint:        rpc,
		ContractAddress:    contract,
		PrivateKey:         priv, // optional (read-only resolve OK)
		GasPrice:           0,
		MaxRetries:         24,
		ConfirmationBlocks: 0,
	}
	ethV4, err := dideth.NewEthereumClientV4(cfgV4)
	if err != nil {
		log.Fatalf("HPKE: init resolver failed: %v", err)
	}

	return ethV4
}

// ---------- main ----------

func main() {
	port := flag.Int("port", 19083, "port")
	requireSig := flag.Bool("require", true, "require RFC9421 signature (false = optional)")
	flag.Parse()

    // HPKE server bootstrap (unchanged)
	hpkeSrvMgr = session.NewManager()
	srvSigningKey := loadServerSigningKeyFromEnv()
	resolver := buildResolver()
	kemKey := loadServerKEMFromEnv()

	nameToDID, err := loadDIDsFromKeys("merged_agent_keys.json")
	if err != nil {
		log.Fatalf("HPKE: load keys: %v", err)
	}
	serverDID := strings.TrimSpace(nameToDID["external"])
	if serverDID == "" {
		log.Fatal("HPKE: server DID not found for name 'external' in merged_agent_keys.json")
	}

	hpkeSrv = hpke.NewServer(
		srvSigningKey,
		hpkeSrvMgr,
		serverDID,
		resolver,
		&hpke.ServerOpts{KEM: kemKey},
	)

    // ⬇️ Handshake-only: transport/http server adapter
	hsrv := sagehttp.NewHTTPServer(func(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
        // Delegate handshake to the HPKE server
		return hpkeSrv.HandleMessage(ctx, msg)
	})

// ⬇️ Data-mode only: our app handler (transport.MessageHandler signature)
	appHandler := func(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
        // msg.Payload must be plaintext JSON (AgentMessage); HPKE data was decrypted above
		var in types.AgentMessage
		if err := json.Unmarshal(msg.Payload, &in); err != nil {
            // Return errors as transport.Response, but upper layer writes raw body response.
            // Since this is not the handshake, this Response is only used internally (no data).
			return &transport.Response{
				Success:   false,
				MessageID: msg.ID,
				TaskID:    msg.TaskID,
				Error:     fmt.Errorf("bad json: %w", err),
			}, nil
		}

        // Keep existing echo logic
		out := types.AgentMessage{
			ID:        in.ID + "-ok",
			From:      "external-payment",
			To:        in.From,
			Type:      "response",
			Content:   fmt.Sprintf("External payment processed at %s (echo): %s", time.Now().Format(time.RFC3339), strings.TrimSpace(in.Content)),
			Timestamp: time.Now(),
		}
		b, _ := json.Marshal(out)
		return &transport.Response{
			Success:   true,
			MessageID: msg.ID,
			TaskID:    msg.TaskID,
            Data:      b, // In data mode, return raw body as-is
		}, nil
	}

// DID middleware (unchanged)
	mw, err := a2autil.BuildDIDMiddleware(!*requireSig)
	if err != nil {
		log.Printf("[external-payment] DID middleware init failed: %v (running without verify)", err)
		mw = nil
	}
	if mw != nil {
		mw.SetErrorHandler(newCompactDIDErrorHandler(log.Default()))
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
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

        // Common: headers used to reconstruct transport.SecureMessage-like context
        // Client A2ATransport sends only raw payload in data mode; reconstruct here for appHandler
		did := strings.TrimSpace(r.Header.Get("X-SAGE-DID"))
		mid := strings.TrimSpace(r.Header.Get("X-SAGE-Message-ID"))
		ctxID := strings.TrimSpace(r.Header.Get("X-SAGE-Context-ID"))
		taskID := strings.TrimSpace(r.Header.Get("X-SAGE-Task-ID"))

        // === HPKE decision ===
		if isHPKE(r) {
			kid := strings.TrimSpace(r.Header.Get("X-KID"))

            // [Handshake] No KID → delegate to transport/http server handler (SecureMessage JSON)
			if kid == "" {
                // Client (handshake) sends SecureMessage JSON → pass through to MessagesHandler
				r.Body = io.NopCloser(bytes.NewReader(body))
				hsrv.MessagesHandler().ServeHTTP(w, r)
				return
			}

            // [Data mode/HPKE] KID present → decrypt with session → appHandler → re-encrypt → raw body response
			sess, ok := hpkeSrvMgr.GetByKeyID(kid)
			if !ok {
                // If unknown KID, it might be a handshake body; try delegating once more
				r.Body = io.NopCloser(bytes.NewReader(body))
				hsrv.MessagesHandler().ServeHTTP(w, r)
				return
			}
			pt, err := sess.Decrypt(body)
			if err != nil {
				http.Error(w, "hpke decrypt failed", http.StatusBadRequest)
				return
			}

            // Wrap as transport.SecureMessage and call app handler
			sm := &transport.SecureMessage{
				ID:        mid,
				ContextID: ctxID,
				TaskID:    taskID,
				Payload:   pt,
				DID:       did,
				Metadata:  map[string]string{"hpke": "true"},
				Role:      "agent",
			}
			resp, _ := appHandler(r.Context(), sm)
			if !resp.Success {
				http.Error(w, "application error", http.StatusBadRequest)
				return
			}

            // After re-encrypting, respond with raw body (client A2ATransport uses respBody as-is)
			ct, err := sess.Encrypt(resp.Data)
			if err != nil {
				http.Error(w, "hpke encrypt failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/sage+hpke")
			w.Header().Set("X-SAGE-HPKE", "v1")
			w.Header().Set("X-KID", kid)
			w.Header().Set("Content-Digest", a2autil.ComputeContentDigest(ct))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(ct)
			return
		}

        // [Data mode/plain] → wrap into transport.SecureMessage → appHandler → raw body response
		sm := &transport.SecureMessage{
			ID:        mid,
			ContextID: ctxID,
			TaskID:    taskID,
            Payload:   body, // plaintext payload
			DID:       did,
			Metadata:  map[string]string{"hpke": "false"},
			Role:      "agent",
		}
		resp, _ := appHandler(r.Context(), sm)
		if !resp.Success {
			http.Error(w, "application error", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
        _, _ = w.Write(resp.Data) // In data mode, write raw body as-is
	})

	var handler http.Handler = open
	if mw != nil {
		wrapped := mw.Wrap(protected)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/status" {
				open.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[boot] external-payment on %s (requireSig=%v)", addr, *requireSig)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// ---------- helpers ----------
func isHPKE(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/sage+hpke") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-SAGE-HPKE")), "v1") {
		return true
	}
	return false
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

// DID middleware compact error shape
func newCompactDIDErrorHandler(l *log.Logger) func(w http.ResponseWriter, r *http.Request, err error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		re := rootError(err)
		if l != nil {
			l.Printf("⚠️ [did-auth] %s", re.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "unauthorized",
			"reason": re.Error(),
		})
	}
}

func rootError(err error) error {
	e := err
	for {
		u := errors.Unwrap(e)
		if u == nil {
			return e
		}
		e = u
	}
}
