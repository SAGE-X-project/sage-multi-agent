package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

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

	// External URLs (Root routes by keyword; leave empty to use in-proc fallback for planning/medical)
	planningExternal := flag.String("planning-external", getenvStr("PLANNINGL_URL", ""), "external planning base (optional)")
	MEDICALExternal := flag.String("medical-external", getenvStr("MEDICAL_URL", "http://localhost:5500/medical"), "external medical base (optional)")
	paymentExternal := flag.String("payment-external", getenvStr("PAYMENT_URL", "http://localhost:5500/payment"), "external payment base (gateway)")

	// Root signing (RFC 9421 via A2A)
	rootJWK := flag.String("jwk", getenvStr("ROOT_JWK_FILE", ""), "private JWK for outbound signing (root)")
	rootDID := flag.String("did", getenvStr("ROOT_DID", ""), "DID override for root")
	sage := flag.Bool("sage", getenvBool("ROOT_SAGE_ENABLED", true), "enable outbound signing at root")

	// Root HPKE bootstrap (optional). You can also enable/disable later via /hpke/config API.
	hpke := flag.Bool("hpke", getenvBool("ROOT_HPKE", false), "initialize HPKE to external at startup (root)")
	hpkeKeys := flag.String("hpke-keys", getenvStr("ROOT_HPKE_KEYS", "merged_agent_keys.json"), "path to DID mapping JSON")
	hpkeTargets := flag.String("hpke-targets", getenvStr("ROOT_HPKE_TARGETS", "payment"), "comma-separated targets: payment,medical,planning")

	// === LLM config for Root pre-ask (added) ===
	llmEnable := flag.Bool("llm", getenvBool("LLM_ENABLED", true), "enable LLM prompts (root pre-ask)")
	llmURL := flag.String("llm-url", getenvStr("LLM_BASE_URL", "http://localhost:11434"), "LLM base URL (Zamiai/Ollama/etc.)")
	llmKey := flag.String("llm-key", getenvStr("LLM_API_KEY", ""), "LLM API key (if required)")
	llmModel := flag.String("llm-model", getenvStr("LLM_MODEL", "gemma2:2b"), "LLM model name/id")
	llmLang := flag.String("llm-lang", getenvStr("LLM_LANG_DEFAULT", "auto"), "default language (auto|ko|en)")
	llmTimeout := flag.Int("llm-timeout", getenvInt("LLM_TIMEOUT_MS", 80000), "LLM timeout in milliseconds")

	flag.Parse()

	// ---- Export env BEFORE constructing Root (Root reads env on NewRootAgent) ----
	if *planningExternal != "" {
		_ = os.Setenv("PLANNING_EXTERNAL_URL", *planningExternal)
	}
	if *MEDICALExternal != "" {
		_ = os.Setenv("MEDICAL_URL", *MEDICALExternal)
	}
	if *paymentExternal != "" {
		_ = os.Setenv("PAYMENT_URL", *paymentExternal)
	}
	_ = os.Setenv("ROOT_SAGE_ENABLED", fmt.Sprintf("%v", *sage))
	if *rootJWK != "" {
		_ = os.Setenv("ROOT_JWK_FILE", *rootJWK)
	}
	if *rootDID != "" {
		_ = os.Setenv("ROOT_DID", *rootDID)
	}

	// === Export LLM env for Root pre-ask (added) ===
	_ = os.Setenv("LLM_ENABLED", fmt.Sprintf("%v", *llmEnable))
	if *llmURL != "" {
		_ = os.Setenv("LLM_BASE_URL", *llmURL)
	}
	if *llmKey != "" {
		_ = os.Setenv("LLM_API_KEY", *llmKey)
	}
	if *llmModel != "" {
		_ = os.Setenv("LLM_MODEL", *llmModel)
	}
	if *llmLang != "" {
		_ = os.Setenv("LLM_LANG_DEFAULT", *llmLang)
	}
	_ = os.Setenv("LLM_TIMEOUT_MS", strconv.Itoa(*llmTimeout))

	// ---- Root ----
	r := root.NewRootAgent(*rootName, *rootPort)

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
		"[boot] root:%d  ext{planning=%s medical=%s payment=%s}  SAGE=%v  llm={enable:%v url:%q model:%q lang:%q timeout:%dms}",
		*rootPort,
		os.Getenv("PLANNING_EXTERNAL_URL"),
		os.Getenv("MEDICAL_URL"),
		os.Getenv("PAYMENT_URL"),
		*sage,
		*llmEnable, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_LANG_DEFAULT"), *llmTimeout,
	)
	if err := r.Start(); err != nil {
		log.Fatal(err)
	}
}
