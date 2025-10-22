package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
)

func main() {
	port := flag.String("port", "19083", "listen port")
	keys := flag.String("keys", envOr("SAGE_KEYS_JSON", "generated_agent_keys.json"), "path to DID keys json for demo resolver")
	optional := flag.Bool("optional", false, "allow unsigned requests (demo only)")
	flag.Parse()

	// DID verifier middleware (file-backed resolver for demo)
	mw, err := a2autil.BuildDIDMiddlewareFromChain(*keys, *optional)
	if err != nil {
		log.Fatalf("didauth init: %v", err)
	}

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"external-payment","sage_enabled":true}`))
	})

	// /process — must be signed (RFC9421) and include Content-Digest that matches body
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1) read whole body (so we can verify Content-Digest)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body error", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		// 2) verify Content-Digest against body (prevents tamper)
		gotDigest := r.Header.Get("Content-Digest")
		if gotDigest == "" {
			http.Error(w, "missing Content-Digest", http.StatusBadRequest)
			return
		}
		wantDigest := a2autil.ComputeContentDigest(body)
		if gotDigest != wantDigest {
			http.Error(w, "content-digest mismatch", http.StatusBadRequest)
			return
		}

		// 3) decode JSON
		var in types.AgentMessage
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// 4) fake processing result
		out := types.AgentMessage{
			ID:        in.ID + "-ok",
			From:      "external-payment",
			To:        in.From,
			Type:      "response",
			Content:   "payment processed (demo)",
			Timestamp: time.Now(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	// attach DID middleware (if optional=false, unsigned → 401)
	mux.Handle("/process", mw.Wrap(inner))

	addr := ":" + *port
	log.Printf("[external-payment] listening on %s (keys=%s optional=%v)", addr, *keys, *optional)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
