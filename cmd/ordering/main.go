package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
)

func main() {
	port := flag.Int("port", 18082, "HTTP port for Ordering Agent")
	sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
	flag.Parse()

	oa := ordering.NewOrderingAgent("OrderingAgent", *port)
	oa.SAGEEnabled = *sage

	log.Printf("Ordering Agent starting on port %d (SAGE=%v)", *port, *sage)
	if err := oa.Start(); err != nil {
		log.Fatalf("ordering agent error: %v", err)
	}
}
