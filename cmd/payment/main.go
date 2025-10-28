// cmd/payment/main.go
// Boot a payment HTTP server module (was cmd/payment). It exposes /status and /process.
// HPKE enables automatically if EXTERNAL_JWK_FILE and EXTERNAL_KEM_JWK_FILE are set.
//
// 한국어 설명:
// - 기존 payment 메인을 대체하는 실행 파일입니다.
// - 환경변수 또는 플래그로 서명키/HPKE KEM 키 경로를 지정하면 HPKE가 자동 활성화됩니다.
// - Root는 이 서버의 /process 로 A2A 전송(서명/HPKE 포함)을 수행합니다.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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

func main() {
    // Distinct process prefix for clearer logs
    log.SetFlags(log.LstdFlags)
    log.SetPrefix("[payment] ")
	// Server port & signature requirement
	port := flag.Int("port", getenvInt("EXTERNAL_PAYMENT_PORT", 19083), "HTTP port for payment server")
	requireSig := flag.Bool("require", getenvBool("PAYMENT_REQUIRE_SIGNATURE", true), "require RFC9421 signature")

	// Convenience flags for key paths (also set envs so the module can pick them up)
	signJWK := flag.String("sign-jwk", getenvStr("EXTERNAL_JWK_FILE", ""), "Ed25519 signing JWK path (enables HPKE server)")
	kemJWK := flag.String("kem-jwk", getenvStr("EXTERNAL_KEM_JWK_FILE", ""), "X25519 KEM JWK path (enables HPKE server)")
	keysFile := flag.String("keys", getenvStr("HPKE_KEYS_FILE", "merged_agent_keys.json"), "DID mapping file (merged_agent_keys.json)")

	flag.Parse()

	// Export envs so payment module picks them up
	if *signJWK != "" {
		_ = os.Setenv("EXTERNAL_JWK_FILE", *signJWK)
	}
	if *kemJWK != "" {
		_ = os.Setenv("EXTERNAL_KEM_JWK_FILE", *kemJWK)
	}
	if *keysFile != "" {
		// The module uses a fixed filename "merged_agent_keys.json"; set env for tooling/scripts.
		_ = os.Setenv("HPKE_KEYS_FILE", *keysFile)
	}

	agent, err := payment.NewPaymentAgent(*requireSig)
	if err != nil {
		log.Fatalf("payment agent init: %v", err)
	}

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:    addr,
		Handler: agent.Handler(),
	}
	log.Printf("[payment] listening on %s (requireSig=%v, HPKE auto by env EXTERNAL_JWK_FILE/EXTERNAL_KEM_JWK_FILE)", addr, *requireSig)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
