// cmd/payment/main.go
// Boot a payment HTTP server module (was cmd/payment). It exposes /status and /process.
// HPKE enables automatically if PAYMENT_JWK_FILE and PAYMENT_KEM_JWK_FILE are set.
//

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/sage-x-project/sage-multi-agent/agents/payment"
)

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
		switch v {
		case "1", "true", "TRUE", "on", "yes":
			return true
		case "0", "false", "FALSE", "off", "no":
			return false
		}
	}
	return def
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
		// also try relative to repo root if running from subdir
		if abs, err := filepath.Abs(p); err == nil {
			if _, err2 := os.Stat(abs); err2 == nil {
				return abs
			}
		}
	}
	return ""
}

func main() {
	// clearer logs
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[payment] ")

	// flags (ENV as defaults)
	port := flag.Int("port", getenvInt("EXTERNAL_PAYMENT_PORT", 19083), "HTTP port for payment server")
	requireSig := flag.Bool("require", getenvBool("PAYMENT_REQUIRE_SIGNATURE", true), "require RFC9421 signature")
	signJWK := flag.String("sign-jwk", getenvStr("PAYMENT_JWK_FILE", ""), "Ed25519 signing JWK path (enables HPKE server)")
	kemJWK := flag.String("kem-jwk", getenvStr("PAYMENT_KEM_JWK_FILE", ""), "X25519 KEM JWK path (enables HPKE server)")
	keysFile := flag.String("keys", getenvStr("HPKE_KEYS_FILE", ""), "DID mapping file (merged_agent_keys.json/generated_agent_keys.json)")

	// === LLM config (added) ===
	llmEnable := flag.Bool("llm", getenvBool("LLM_ENABLED", true), "enable LLM prompts")
	llmURL := flag.String("llm-url", getenvStr("LLM_BASE_URL", "http://localhost:11434"), "LLM base URL (Zamiai/Ollama/etc.)")
	llmKey := flag.String("llm-key", getenvStr("LLM_API_KEY", ""), "LLM API key (if required)")
	llmModel := flag.String("llm-model", getenvStr("LLM_MODEL", "gemma2:2b"), "LLM model name/id")
	llmLang := flag.String("llm-lang", getenvStr("LLM_LANG_DEFAULT", "auto"), "default language (auto|ko|en)")
	llmTimeout := flag.Int("llm-timeout", getenvInt("LLM_TIMEOUT_MS", 80000), "LLM timeout in milliseconds")

	flag.Parse()

	// ---- Auto-detect defaults if flags/env are empty ----
	if *signJWK == "" {
		*signJWK = firstExisting("keys/external.jwk")
	}
	if *kemJWK == "" {
		*kemJWK = firstExisting("keys/kem/external.x25519.jwk")
	}
	if *keysFile == "" {
		*keysFile = firstExisting("merged_agent_keys.json", "generated_agent_keys.json", "keys/merged_agent_keys.json")
	}

	// ---- Export envs so the agent (lazy enable) can always find them ----
	if *signJWK != "" {
		_ = os.Setenv("PAYMENT_JWK_FILE", *signJWK)
	}
	if *kemJWK != "" {
		_ = os.Setenv("PAYMENT_KEM_JWK_FILE", *kemJWK)
	}
	if *keysFile != "" {
		_ = os.Setenv("HPKE_KEYS_FILE", *keysFile)
	}

	// === Export LLM env for agent (added) ===
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

	log.Printf("[boot] requireSig=%v  sign-jwk=%q  kem-jwk=%q  keys=%q  llm={enable:%v url:%q model:%q lang:%q timeout:%dms}",
		*requireSig, os.Getenv("PAYMENT_JWK_FILE"), os.Getenv("PAYMENT_KEM_JWK_FILE"), os.Getenv("HPKE_KEYS_FILE"),
		*llmEnable, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_LANG_DEFAULT"), *llmTimeout)

	agent, err := payment.NewPaymentAgent(*requireSig)
	if err != nil {
		log.Fatalf("payment agent init: %v", err)
	}

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{Addr: addr, Handler: agent.Handler()}
	log.Printf("listening on %s (HPKE auto by env; lazy-enable supported)", addr)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
