// cmd/ordering/main.go
// Boot an ordering HTTP server module. It exposes /status and /process.
// SAGE enables automatically if ORDERING_JWK_FILE is set.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/types"
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

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch v {
		case "1", "true", "TRUE", "on", "yes":
			return true
		case "0", "false", "FALSE", "off", "no":
			return false
		}
	}
	return def
}

func main() {
	// clearer logs
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[ordering] ")

	// flags (ENV as defaults)
	port := flag.Int("port", getenvInt("EXTERNAL_ORDERING_PORT", 19084), "HTTP port for ordering server")
	sageEnabled := flag.Bool("sage", getenvBool("ORDERING_SAGE_ENABLED", true), "enable SAGE signing")
	jwkFile := flag.String("jwk", getenvStr("ORDERING_JWK_FILE", ""), "JWK file for SAGE signing")
	did := flag.String("did", getenvStr("ORDERING_DID", ""), "DID for ordering agent")

	flag.Parse()

	// Export to env for agent
	if *jwkFile != "" {
		_ = os.Setenv("ORDERING_JWK_FILE", *jwkFile)
	}
	if *did != "" {
		_ = os.Setenv("ORDERING_DID", *did)
	}
	_ = os.Setenv("ORDERING_SAGE_ENABLED", fmt.Sprintf("%v", *sageEnabled))

	log.Printf("[boot] port=%d sage=%v jwk=%q did=%q",
		*port, *sageEnabled, *jwkFile, *did)

	agent := ordering.NewOrderingAgent("ordering-agent-1")

	// HTTP handlers
	mux := http.NewServeMux()

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "healthy",
			"agent":        agent.Name,
			"sage_enabled": agent.SAGEEnabled,
		})
	})

	mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var msg types.AgentMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		response, err := agent.Process(ctx, msg)
		if err != nil {
			log.Printf("process error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{Addr: addr, Handler: mux}
	log.Printf("listening on %s (SAGE auto by env; lazy-enable supported)", addr)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
