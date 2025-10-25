package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
)

func main() {
	port := flag.Int("port", 19083, "port")
	keysFile := flag.String("keys", "keys/all_keys.json", "public keys file for inbound DID verify")
	requireSig := flag.Bool("require", true, "require signature (false = optional)")
	flag.Parse()

	// Build DID middleware (local file verifier)
	mw, err := a2autil.BuildDIDMiddleware(*keysFile, true)
	if err != nil {
		log.Printf("[external-payment] DID middleware init failed: %v (running without verify)", err)
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
