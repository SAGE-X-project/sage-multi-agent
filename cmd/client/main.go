package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/api"
	"github.com/sage-x-project/sage-multi-agent/internal/bootstrap"
	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[client] ")

	port := flag.Int("port", getenvInt("CLIENT_API_PORT", 8086), "client api port")
	rootBase := flag.String("root", getenvStr("ROOT_AGENT_URL", "http://localhost:18080"), "root base URL")

	clientJWK := flag.String("client-jwk", getenvStr("CLIENT_JWK_FILE", ""), "optional: path to JWK (private) for signing client->root")
	clientDID := flag.String("client-did", getenvStr("CLIENT_DID", ""), "optional: DID to use for client signing")
	flag.Parse()

	// ---- Bootstrap: Ensure keys exist before starting ----
	log.Println("[client] Initializing agent keys...")
	bootstrapCfg := bootstrap.LoadConfigFromEnv("client")

	// Override with command-line flags if provided
	if *clientJWK != "" {
		bootstrapCfg.SigningKeyFile = *clientJWK
	}
	if *clientDID != "" {
		bootstrapCfg.DID = *clientDID
	}

	agentKeys, err := bootstrap.EnsureAgentKeys(context.Background(), bootstrapCfg)
	if err != nil {
		log.Fatalf("[client] Failed to initialize keys: %v", err)
	}

	log.Printf("[client] Agent initialized with DID: %s", agentKeys.DID)

	// Update flags with bootstrapped values
	if *clientJWK == "" && bootstrapCfg.SigningKeyFile != "" {
		*clientJWK = bootstrapCfg.SigningKeyFile
	}
	if *clientDID == "" {
		*clientDID = agentKeys.DID
	}

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
