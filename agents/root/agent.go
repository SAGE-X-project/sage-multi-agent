// Package root: RootAgent does ONLY in-proc routing to sub agents.
// It never signs outbound HTTP. A2A signatures are applied ONLY by sub agents
// when they talk to their external services.
package root

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/agents/planning"
	"github.com/sage-x-project/sage-multi-agent/types"
)

type RootAgent struct {
	name string
	port int

	mux    *http.ServeMux
	server *http.Server

	// IN-PROC agents
	planning *planning.PlanningAgent
	ordering *ordering.OrderingAgent
	payment  *payment.PaymentAgent

	logger *log.Logger
}

func NewRootAgent(name string, port int, p *planning.PlanningAgent, o *ordering.OrderingAgent, pay *payment.PaymentAgent) *RootAgent {
	mux := http.NewServeMux()
	ra := &RootAgent{
		name:     name,
		port:     port,
		mux:      mux,
		planning: p,
		ordering: o,
		payment:  pay,
		logger:   log.Default(),
	}
	ra.mountRoutes()
	return ra
}

func (r *RootAgent) Start() error {
	addr := fmt.Sprintf(":%d", r.port)
	r.server = &http.Server{Addr: addr, Handler: r.mux}
	r.logger.Printf("[root] listening on %s", addr)
	return r.server.ListenAndServe()
}

func (r *RootAgent) mountRoutes() {
	// health
	r.mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"name": r.name,
			"type": "root",
			"port": r.port,
			"agents": map[string]any{
				"planning": r.planning != nil,
				"ordering": r.ordering != nil,
				"payment":  r.payment != nil,
			},
			"time": time.Now().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// toggle SAGE on/off at sub-agents (affects ONLY outbound signing of each sub)
	r.mux.HandleFunc("/toggle-sage", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if r.planning != nil {
			r.planning.SAGEEnabled = in.Enabled
		}
		if r.ordering != nil {
			r.ordering.SAGEEnabled = in.Enabled
		}
		if r.payment != nil {
			r.payment.SAGEEnabled = in.Enabled
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": in.Enabled})
	})

	// main in-proc processing
	r.mux.HandleFunc("/process", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var msg types.AgentMessage
		if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		agent := r.pickAgent(&msg)
		if agent == "" {
			http.Error(w, "no agent available", http.StatusBadGateway)
			return
		}

		var out types.AgentMessage
		var err error
		switch agent {
		case "planning":
			out, err = r.planning.Process(req.Context(), msg)
		case "ordering":
			out, err = r.ordering.Process(req.Context(), msg)
		case "payment":
			out, err = r.payment.Process(req.Context(), msg)
		default:
			http.Error(w, "unknown agent", http.StatusBadGateway)
			return
		}
		if err != nil {
			http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

func (r *RootAgent) pickAgent(msg *types.AgentMessage) string {
	// 1) metadata.domain
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["domain"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				s = strings.ToLower(strings.TrimSpace(s))
				switch s {
				case "planning", "ordering", "payment":
					return s
				}
			}
		}
	}
	// 2) content keyword heuristic
	c := strings.ToLower(msg.Content)
	switch {
	case containsAny(c, "pay", "payment", "송금", "결제", "wallet", "transfer", "crypto"):
		if r.payment != nil {
			return "payment"
		}
	case containsAny(c, "order", "주문", "buy", "purchase", "product", "catalog"):
		if r.ordering != nil {
			return "ordering"
		}
	default:
		if r.planning != nil {
			return "planning"
		}
	}
	if r.planning != nil {
		return "planning"
	}
	if r.ordering != nil {
		return "ordering"
	}
	if r.payment != nil {
		return "payment"
	}
	return ""
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(s, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
