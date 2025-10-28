package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/agents/planning"
	"github.com/sage-x-project/sage-multi-agent/agents/root"
)

// env-backed defaults
func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func getenvStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "on", "yes":
			return true
		case "0", "false", "off", "no":
			return false
		}
	}
	return def
}

func main() {
    // Distinct process prefix for clearer logs
    log.SetFlags(log.LstdFlags)
    log.SetPrefix("[root] ")
	// ---- Root flags (env-backed defaults) ----
	rootName := flag.String("name", getenvStr("ROOT_AGENT_NAME", "root"), "root agent name")
	rootPort := flag.Int("port", getenvInt("ROOT_AGENT_PORT", 18080), "root agent port")

	// External URLs (Root routes by keyword; leave empty to use in-proc fallback for planning/ordering)
	planningExternal := flag.String("planning-external", getenvStr("PLANNING_EXTERNAL_URL", ""), "external planning base (optional)")
	orderingExternal := flag.String("ordering-external", getenvStr("ORDERING_EXTERNAL_URL", ""), "external ordering base (optional)")
	paymentExternal := flag.String("payment-external", getenvStr("PAYMENT_EXTERNAL_URL", "http://localhost:5500"), "external payment base (gateway)")

	// Root signing (RFC 9421 via A2A)
	rootJWK := flag.String("jwk", getenvStr("ROOT_JWK_FILE", ""), "private JWK for outbound signing (root)")
	rootDID := flag.String("did", getenvStr("ROOT_DID", ""), "DID override for root")
	sage := flag.Bool("sage", getenvBool("ROOT_SAGE_ENABLED", true), "enable outbound signing at root")

	// Root HPKE bootstrap (optional). You can also enable/disable later via /hpke/config API.
	hpke := flag.Bool("hpke", getenvBool("ROOT_HPKE", false), "initialize HPKE to external at startup (root)")
	hpkeKeys := flag.String("hpke-keys", getenvStr("ROOT_HPKE_KEYS", "merged_agent_keys.json"), "path to DID mapping JSON")
	hpkeTargets := flag.String("hpke-targets", getenvStr("ROOT_HPKE_TARGETS", "payment"), "comma-separated targets: payment,ordering,planning")

	flag.Parse()

	// ---- Export env BEFORE constructing Root (Root reads env on NewRootAgent) ----
	if *planningExternal != "" {
		_ = os.Setenv("PLANNING_EXTERNAL_URL", *planningExternal)
	}
	if *orderingExternal != "" {
		_ = os.Setenv("ORDERING_EXTERNAL_URL", *orderingExternal)
	}
	if *paymentExternal != "" {
		_ = os.Setenv("PAYMENT_EXTERNAL_URL", *paymentExternal)
	}
	_ = os.Setenv("ROOT_SAGE_ENABLED", fmt.Sprintf("%v", *sage))
	if *rootJWK != "" {
		_ = os.Setenv("ROOT_JWK_FILE", *rootJWK)
	}
	if *rootDID != "" {
		_ = os.Setenv("ROOT_DID", *rootDID)
	}

	// ---- In-proc agents (fallback only; Root owns network crypto) ----
	pl := planning.NewPlanningAgent("planning")
	or := ordering.NewOrderingAgent("ordering")

	// ---- Root ----
	r := root.NewRootAgent(*rootName, *rootPort, pl, or)

	// Optional: initialize HPKE sessions for targets at startup
	if *hpke {
		keys := strings.TrimSpace(*hpkeKeys)
		targets := strings.Split(*hpkeTargets, ",")
		for _, t := range targets {
			tgt := strings.TrimSpace(strings.ToLower(t))
			if tgt == "" {
				continue
			}
			if err := r.EnableHPKE(context.Background(), tgt, keys); err != nil {
				log.Printf("[root] HPKE init FAILED target=%s: %v", tgt, err)
			} else {
				log.Printf("[root] HPKE init OK target=%s (keys=%s)", tgt, keys)
			}
		}
	} else {
		log.Printf("[root] HPKE disabled at startup")
	}

	log.Printf(
		"[boot] root:%d  ext{planning=%s ordering=%s payment=%s}  SAGE=%v",
		*rootPort,
		os.Getenv("PLANNING_EXTERNAL_URL"),
		os.Getenv("ORDERING_EXTERNAL_URL"),
		os.Getenv("PAYMENT_EXTERNAL_URL"),
		*sage,
	)
	if err := r.Start(); err != nil {
		log.Fatal(err)
	}
}
