package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/api"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func main() {
	port := flag.Int("port", 8086, "client api port")
	rootBase := flag.String("root", "http://localhost:18080", "root base URL")

	clientJWK := flag.String("client-jwk", "", "optional: path to JWK (private) for signing client->root")
	clientDID := flag.String("client-did", "", "optional: DID to use for client signing")
	flag.Parse()

	var a2a *a2aclient.A2AClient
	if *clientJWK != "" {
		raw, err := os.ReadFile(*clientJWK)
		if err != nil {
			log.Fatalf("read client-jwk: %v", err)
		}
		imp := formats.NewJWKImporter()
		kp, err := imp.Import(raw, sagecrypto.KeyFormatJWK)
		if err != nil {
			log.Fatalf("import client-jwk: %v", err)
		}
		didStr := strings.TrimSpace(*clientDID)
		if didStr == "" {
			if id := strings.TrimSpace(kp.ID()); id != "" {
				didStr = "did:sage:generated:" + id
			} else {
				didStr = "did:sage:client"
			}
		}
		a2a = a2aclient.NewA2AClient(did.AgentDID(didStr), kp, http.DefaultClient)
		log.Printf("[client] A2A signing enabled (DID=%s)", didStr)
	}

	apiServer := api.NewClientAPIWithA2A(*rootBase, "", http.DefaultClient, a2a)

	mux := http.NewServeMux()
	// Single public endpoint. Routing is done by Root.
	mux.HandleFunc("/api/request", apiServer.HandleRequest)
	mux.HandleFunc("/api/sage/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	addr := ":" + strconv.Itoa(*port)
	log.Printf("[boot] client api on %s -> root=%s", addr, *rootBase)
	log.Fatal(http.ListenAndServe(addr, mux))
}
