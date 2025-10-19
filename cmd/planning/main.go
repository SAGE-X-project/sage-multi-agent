package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/agents/planning"
)

func main() {
	port := flag.Int("port", 18081, "HTTP port for Planning Agent")
	sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
	flag.Parse()

	pa := planning.NewPlanningAgent("PlanningAgent", *port)
	pa.SAGEEnabled = *sage

	log.Printf("Planning Agent starting on port %d (SAGE=%v)", *port, *sage)
	if err := pa.Start(); err != nil {
		log.Fatalf("planning agent error: %v", err)
	}
}
