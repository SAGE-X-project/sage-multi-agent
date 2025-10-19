package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
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
	// Default port resolves from ORDERING_PORT or ORDERING_AGENT_PORT, else 18082
	defPort := envPort([]string{"ORDERING_PORT", "ORDERING_AGENT_PORT"}, 18082)

	port := flag.Int("port", defPort, "HTTP port for Ordering Agent")
	sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
	flag.Parse()

	agent := ordering.NewOrderingAgent("ordering", *port)
	agent.SAGEEnabled = *sage

	log.Printf("[ordering] starting on :%d (SAGE=%v)", *port, *sage)
	if err := agent.Start(); err != nil {
		log.Fatal(err)
	}
}
