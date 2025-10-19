package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/sage-x-project/sage-multi-agent/agents/payment"
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
	// Default port resolves from PAYMENT_PORT or PAYMENT_AGENT_PORT, else 18083
	defPort := envPort([]string{"PAYMENT_PORT", "PAYMENT_AGENT_PORT"}, 18083)

	port := flag.Int("port", defPort, "HTTP port for Payment Agent")
	external := flag.String("external", "", "Upstream external payment base URL (e.g. http://localhost:5500)")
	sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
	flag.Parse()

	// The payment package reads PAYMENT_EXTERNAL_URL or PAYMENT_UPSTREAM
	if *external != "" {
		os.Setenv("PAYMENT_EXTERNAL_URL", *external)
	}

	agent := payment.NewPaymentAgent("payment", *port)
	agent.SAGEEnabled = *sage

	log.Printf("[payment] starting on :%d (SAGE=%v, upstream=%s)", *port, *sage, os.Getenv("PAYMENT_EXTERNAL_URL"))
	if err := agent.Start(); err != nil {
		log.Fatal(err)
	}
}
