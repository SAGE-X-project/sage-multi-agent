package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/agents/payment"
)

func main() {
	port := flag.Int("port", 18083, "HTTP port for Payment Agent")
	sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
	flag.Parse()

	pa := payment.NewPaymentAgent("PaymentAgent", *port)
	pa.SAGEEnabled = *sage

	log.Printf("Payment Agent starting on port %d (SAGE=%v)", *port, *sage)
	if err := pa.Start(); err != nil {
		log.Fatalf("payment agent error: %v", err)
	}
}
