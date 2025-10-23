package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/agents/planning"
	"github.com/sage-x-project/sage-multi-agent/agents/root"
)

func main() {
	// Root flags
	rootName := flag.String("name", "root", "root agent name")
	rootPort := flag.Int("port", 18080, "root agent port")

	// Payment (outbound to gateway/external)
	paymentExternal := flag.String("payment-external", "http://localhost:5500", "external payment base (gateway)")
	// paymentJWK := flag.String("payment-jwk", "", "payment agent JWK (private) file path [required]")
	// paymentDID := flag.String("payment-did", "", "payment agent DID (optional; derived from key if empty)")

	// flag.Parse()

	// if *paymentJWK == "" {
	// 	log.Fatal("missing -payment-jwk (required)")
	// }

	// // Inject ENV for payment agent
	// _ = os.Setenv("PAYMENT_EXTERNAL_URL", *paymentExternal)
	// _ = os.Setenv("PAYMENT_JWK_FILE", *paymentJWK)
	// if *paymentDID != "" {
	// 	_ = os.Setenv("PAYMENT_DID", *paymentDID)
	// }

	// Build in-proc agents
	pl := planning.NewPlanningAgent("planning")
	or := ordering.NewOrderingAgent("ordering")
	pa := payment.NewPaymentAgent("payment")

	// Root
	r := root.NewRootAgent(*rootName, *rootPort, pl, or, pa)

	log.Printf("[boot] root:%d  payment→external=%s", *rootPort, *paymentExternal)
	if err := r.Start(); err != nil {
		log.Fatal(err)
	}
}
