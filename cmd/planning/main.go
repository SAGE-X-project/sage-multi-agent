package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/sage-x-project/sage-multi-agent/agents/planning"
)

func envPort(names []string, def int) int {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			if p, err := strconv.Atoi(v); err == nil && p > 0 {
				return p
			}
		}
	}
	return def
}

func main() {
	// Default port resolves from PLANNING_PORT or PLANNING_AGENT_PORT, else 18081
	defPort := envPort([]string{"PLANNING_PORT", "PLANNING_AGENT_PORT"}, 18081)

	port := flag.Int("port", defPort, "HTTP port for Planning Agent")
	sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
	flag.Parse()

	agent := planning.NewPlanningAgent("planning", *port)
	agent.SAGEEnabled = *sage

	log.Printf("[planning] starting on :%d (SAGE=%v)", *port, *sage)
	if err := agent.Start(); err != nil {
		log.Fatal(err)
	}
}
