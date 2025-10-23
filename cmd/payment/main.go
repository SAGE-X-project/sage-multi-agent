// cmd/payment/main.go
// Debug server wrapper for the in-proc PaymentAgent.
// PaymentAgent signs outbound HTTP to the external payment service (usually via the gateway)
// ONLY when SAGE is enabled. In production the RootAgent calls PaymentAgent.Process in-proc.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sage-x-project/sage-multi-agent/agents/payment"
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
	port := flag.Int("port", getenvInt("PAYMENT_AGENT_PORT", 18083), "HTTP port")
	external := flag.String("external", getenvStr("PAYMENT_EXTERNAL_URL", "http://localhost:5500"), "External payment base (gateway)")
	jwk := flag.String("jwk", getenvStr("PAYMENT_JWK_FILE", ""), "Private JWK for outbound signing (required if SAGE enabled)")
	did := flag.String("did", getenvStr("PAYMENT_DID", ""), "DID override (optional)")
	sage := flag.Bool("sage", getenvBool("PAYMENT_SAGE_ENABLED", true), "Enable outbound signing (SAGE)")
	flag.Parse()

	// Export to env so agent picks them via envOr()
	_ = os.Setenv("PAYMENT_EXTERNAL_URL", *external)
	if *jwk != "" {
		_ = os.Setenv("PAYMENT_JWK_FILE", *jwk)
	}
	if *did != "" {
		_ = os.Setenv("PAYMENT_DID", *did)
	}

	agent := payment.NewPaymentAgent("PaymentAgent")
	agent.SAGEEnabled = *sage

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "PaymentAgent",
			"type":         "payment-debug",
			"external_url": agent.ExternalURL,
			"sage_enabled": agent.SAGEEnabled,
			"time":         time.Now().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/toggle-sage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		agent.SAGEEnabled = in.Enabled
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": in.Enabled})
	})
	mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var msg types.AgentMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		out, err := agent.Process(r.Context(), msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[payment-debug] listening on %s (SAGE=%v, external=%s)", addr, agent.SAGEEnabled, agent.ExternalURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}
