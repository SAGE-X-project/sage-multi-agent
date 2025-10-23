// cmd/planning/main.go
// Debug server wrapper for the in-proc PlanningAgent.
// NOTE: In production the RootAgent calls PlanningAgent.Process in-proc.
// This server is only for manual testing.

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

	"github.com/sage-x-project/sage-multi-agent/agents/planning"
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

func main() {
	port := flag.Int("port", getenvInt("PLANNING_AGENT_PORT", 18081), "HTTP port")
	flag.Parse()

	agent := planning.NewPlanningAgent("PlanningAgent")

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "PlanningAgent",
			"type": "planning-debug",
			"time": time.Now().Format(time.RFC3339),
		})
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[planning-debug] listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
