package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/sage-x-project/sage-a2a-go/pkg/signer"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"

	"github.com/sage-x-project/sage/pkg/agent/crypto/keys"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func main() {
	port := flag.Int("port", 8086, "client api port")
	rootBase := flag.String("root", "http://localhost:18080", "root base")
	flag.Parse()

	// Demo: generate ephemeral key (replace with load-from-file in your tree).
	kp, _ := keys.GenerateSecp256k1KeyPair()
	clientDID := did.AgentDID("did:sage:ethereum:0xclient-demo")

	signer := signer.NewDefaultA2ASigner()
	httpSigner := a2autil.NewSignedHTTPClient(clientDID, kp, signer, http.DefaultClient)

	mux := http.NewServeMux()

	// UX config
	mux.HandleFunc("/api/sage/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"enabled":true}`))
	})

	// POST /api/payment {prompt:string}
	mux.HandleFunc("/api/payment", func(w http.ResponseWriter, r *http.Request) {
		var in types.PromptRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		msg := types.AgentMessage{
			ID:        fmt.Sprintf("api-%d", time.Now().UnixNano()),
			From:      "client",
			To:        "root",
			Type:      "request",
			Content:   in.Prompt,
			Timestamp: time.Now(),
			Metadata:  map[string]any{"domain": "payment"},
		}
		b, _ := json.Marshal(msg)

		req, _ := http.NewRequest(http.MethodPost, *rootBase+"/process", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpSigner.Do(r.Context(), req)
		if err != nil {
			http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		data, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(data)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[client] on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
