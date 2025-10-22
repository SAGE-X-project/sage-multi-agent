package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/agents/root"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
)

func main() {
	port := flag.Int("port", 18080, "root agent port")
	// payment := flag.String("payment", "http://localhost:18083", "payment agent base URL (internal)")
	// planning := flag.String("planning", "http://localhost:18081", "planning agent base URL")
	// ordering := flag.String("ordering", "http://localhost:18082", "ordering agent base URL")
	keys := flag.String("keys", envOr("SAGE_KEYS_JSON", "generated_agent_keys.json"), "path to DID keys json for demo resolver")
	optional := flag.Bool("optional", false, "allow unsigned client->root requests (demo only)")
	flag.Parse()

	// Prepare DID middleware (client -> root HTTP)
	mw, err := a2autil.BuildDIDMiddlewareFromChain(*keys, *optional)
	if err != nil {
		log.Fatalf("didauth init: %v", err)
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	ra := root.NewRootAgent("root", *port)
	ra.SetPaymentInproc(payment.NewPaymentAgent("payment"))
	// ra.RegisterAgent("planning", "planning", "http://localhost:8084")
	// ra.RegisterAgent("ordering", "ordering", "http://localhost:8083")
	// If the root agent exposes a hook to install middleware, wire it here.
	// This avoids compile errors even if the method doesn't exist in your root package.
	type hasDIDSetter interface {
		SetDIDAuthMiddleware(*server.DIDAuthMiddleware)
	}
	if s, ok := any(ra).(hasDIDSetter); ok {
		s.SetDIDAuthMiddleware(mw)
		log.Printf("[root] DID middleware installed (keys=%s optional=%v)", *keys, *optional)
	} else {
		log.Printf("[root] DID middleware NOT installed (root agent has no SetDIDAuthMiddleware).")
		log.Printf("[root] Ensure client->root verification is handled inside the root agent's HTTP setup.")
	}

	if err := ra.Start(); err != nil {
		log.Fatalf("[root] server error: %v", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func itoa(i int) string { return fmt.Sprintf("%d", i) } // (kept if you still need string port elsewhere)
