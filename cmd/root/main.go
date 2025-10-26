package main

import (
	"context"
	"flag"
	"log"

	// 필요하면 "os" 추가
	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/agents/planning"
	"github.com/sage-x-project/sage-multi-agent/agents/root"
)

func main() {
	// Root flags
	rootName := flag.String("name", "root", "root agent name")
	rootPort := flag.Int("port", 18080, "root agent port")

	// External (게이트웨이) 표시용만 사용 — 실제로는 PaymentAgent가 env 기본값을 씀
	paymentExternal := flag.String("payment-external", "http://localhost:5500", "external payment base (gateway)")

	// HPKE 플래그 (기본 OFF)
	hpke := flag.Bool("hpke", false, "Enable HPKE to external from PaymentAgent")
	hpkeKeys := flag.String("hpke-keys", "generated_agent_keys.json", "Path to generated agent keys JSON")

	// 반드시 호출!
	flag.Parse()

	// IN-PROC agents
	pl := planning.NewPlanningAgent("planning")
	or := ordering.NewOrderingAgent("ordering")
	pa := payment.NewPaymentAgent("payment")

	// HPKE 켜기 (선택)
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
