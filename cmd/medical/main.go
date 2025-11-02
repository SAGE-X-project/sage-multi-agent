// cmd/medical/main.go
// Boot a medical HTTP server module (mirrors cmd/payment).
// Exposes /status and /process. HPKE auto-enables if
// MEDICAL_JWK_FILE and MEDICAL_KEM_JWK_FILE are set.

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/sage-x-project/sage-multi-agent/agents/medical"
)

func getenvInt(keys []string, def int) int {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return def
}
func getenvStr(keys []string, def string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return def
}
func getenvBool(keys []string, def bool) bool {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			switch v {
			case "1", "true", "TRUE", "on", "yes":
				return true
			case "0", "false", "FALSE", "off", "no":
				return false
			}
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
	log.SetPrefix("[medical] ")

	// ---------- Flags (ENV as defaults) ----------
	// Port: prefer EXTERNAL_MEDICAL_PORT, then MEDICAL_AGENT_PORT, default 19082
	port := flag.Int("port", getenvInt([]string{"EXTERNAL_MEDICAL_PORT", "MEDICAL_AGENT_PORT"}, 19082), "HTTP port for medical server")

	// Signature requirement (you can map SAGE_MODE -> this in scripts)
	requireSig := flag.Bool("require", getenvBool([]string{"MEDICAL_REQUIRE_SIGNATURE"}, true), "require RFC9421 signature")

	// HPKE key paths (server signing + KEM)
	signJWK := flag.String("sign-jwk", getenvStr([]string{"MEDICAL_JWK_FILE"}, ""), "Ed25519 signing JWK path (enables HPKE server)")
	kemJWK := flag.String("kem-jwk", getenvStr([]string{"MEDICAL_KEM_JWK_FILE"}, ""), "X25519 KEM JWK path (enables HPKE server)")
	keysFile := flag.String("keys", getenvStr([]string{"HPKE_KEYS_FILE"}, ""), "DID mapping file (merged_agent_keys.json/generated_agent_keys.json)")

	// LLM config (bridged into env for llm.NewFromEnv)
	llmEnable := flag.Bool("llm", getenvBool([]string{"LLM_ENABLED"}, true), "enable LLM prompts")
	llmURL := flag.String("llm-url", getenvStr([]string{"LLM_BASE_URL", "GEMINI_API_URL"}, "http://localhost:11434"), "LLM base URL (OpenAI-compatible)")
	llmKey := flag.String("llm-key", getenvStr([]string{"LLM_API_KEY", "GEMINI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY"}, ""), "LLM API key (if required)")
	llmModel := flag.String("llm-model", getenvStr([]string{"LLM_MODEL", "GEMINI_MODEL"}, "gemma2:2b"), "LLM model name/id")
	llmLang := flag.String("llm-lang", getenvStr([]string{"LLM_LANG_DEFAULT"}, "auto"), "default language (auto|ko|en)")
	llmTimeout := flag.Int("llm-timeout", getenvInt([]string{"LLM_TIMEOUT_MS"}, 8000), "LLM timeout in milliseconds")

	flag.Parse()

	// ---------- Auto-detect default key paths if not provided ----------
	if *signJWK == "" {
		*signJWK = firstExisting(
			"keys/medical.jwk",
			"keys/external-medical.jwk",
			"keys/external.jwk", // fallback
		)
	}
	if *kemJWK == "" {
		*kemJWK = firstExisting(
			"keys/kem/medical.x25519.jwk",
			"keys/kem/external-medical.x25519.jwk",
			"keys/kem/external.x25519.jwk", // fallback
		)
	}
	if *keysFile == "" {
		*keysFile = firstExisting(
			"merged_agent_keys.json",
			"generated_agent_keys.json",
			"keys/merged_agent_keys.json",
		)
	}

	// ---------- Export env so the agent (lazy HPKE/LLM) can find them ----------
	if *signJWK != "" {
		_ = os.Setenv("MEDICAL_JWK_FILE", *signJWK)
	}
	if *kemJWK != "" {
		_ = os.Setenv("MEDICAL_KEM_JWK_FILE", *kemJWK)
	}
	if *keysFile != "" {
		_ = os.Setenv("HPKE_KEYS_FILE", *keysFile)
	}

	// LLM env (shared convention with payment)
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
		*requireSig, os.Getenv("MEDICAL_JWK_FILE"), os.Getenv("MEDICAL_KEM_JWK_FILE"), os.Getenv("HPKE_KEYS_FILE"),
		*llmEnable, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"), os.Getenv("LLM_LANG_DEFAULT"), *llmTimeout)

	// ---------- Start agent HTTP server ----------
	agent, err := medical.NewMedicalAgent(*requireSig)
	if err != nil {
		log.Fatalf("medical agent init: %v", err)
	}

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{Addr: addr, Handler: agent.Handler()}
	log.Printf("listening on %s (HPKE auto by env; lazy-enable supported)", addr)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
