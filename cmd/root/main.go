package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/agents/root"
)

func main() {
	// Flags
	port := flag.Int("port", 18080, "HTTP port for Root Agent")
	orderingURL := flag.String("ordering-url", "http://localhost:18082", "Ordering agent endpoint URL")
	planningURL := flag.String("planning-url", "http://localhost:18081", "Planning agent endpoint URL")
	paymentURL := flag.String("payment-url", "http://localhost:18083", "Payment agent endpoint URL")
	sage := flag.Bool("sage", true, "enable SAGE features (sign outbound, verify inbound)")
	flag.Parse()

	// Create root agent
	ra := root.NewRootAgent("RootAgent", *port)
	ra.SAGEEnabled = *sage

	// Register sub-agent endpoints
	ra.RegisterAgent("planning", "PlanningAgent", *planningURL)
	ra.RegisterAgent("ordering", "OrderingAgent", *orderingURL)
	ra.RegisterAgent("payment", "PaymentAgent", *paymentURL)

	log.Printf("Root Agent starting on port %d (planning=%s ordering=%s payment=%s, SAGE=%v)",
		*port, *planningURL, *orderingURL, *paymentURL, *sage)

	if err := ra.Start(); err != nil {
		log.Fatalf("root agent error: %v", err)
	}
}
