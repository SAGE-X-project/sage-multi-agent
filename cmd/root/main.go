package main

import (
    "context"
    "flag"
    "log"

    // Add "os" if needed
    "github.com/sage-x-project/sage-multi-agent/agents/ordering"
    "github.com/sage-x-project/sage-multi-agent/agents/payment"
    "github.com/sage-x-project/sage-multi-agent/agents/planning"
    "github.com/sage-x-project/sage-multi-agent/agents/root"
)

func main() {
	// Root flags
	rootName := flag.String("name", "root", "root agent name")
	rootPort := flag.Int("port", 18080, "root agent port")

    // External (gateway) flag is informational — PaymentAgent uses env defaults
    paymentExternal := flag.String("payment-external", "http://localhost:5500", "external payment base (gateway)")

    // HPKE flags (default OFF)
    hpke := flag.Bool("hpke", false, "Enable HPKE to external from PaymentAgent")
    hpkeKeys := flag.String("hpke-keys", "generated_agent_keys.json", "Path to generated agent keys JSON")

    // Must call before using flags
    flag.Parse()

	// IN-PROC agents
	pl := planning.NewPlanningAgent("planning")
	or := ordering.NewOrderingAgent("ordering")
	pa := payment.NewPaymentAgent("payment")

    // Enable HPKE (optional)
    if *hpke {
        if err := pa.EnableHPKE(context.Background(), payment.HPKEConfig{
            Enable:   true,
            KeysFile: *hpkeKeys,
        }); err != nil {
            log.Printf("[root] HPKE init FAILED: %v", err)
        } else {
            log.Printf("[root] HPKE init OK (keys=%s)", *hpkeKeys)
        }
    } else {
        log.Printf("[root] HPKE disabled")
    }

	// Root
	r := root.NewRootAgent(*rootName, *rootPort, pl, or, pa)

	log.Printf("[boot] root:%d  payment→external=%s", *rootPort, *paymentExternal)
	if err := r.Start(); err != nil {
		log.Fatal(err)
	}
}
