// cmd/payment/main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
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

func normalizeBase(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	if _, err := url.Parse(u); err != nil {
		return ""
	}
	return u
}

func main() {
	// 기본 포트: PAYMENT_PORT / PAYMENT_AGENT_PORT / 18083
	defPort := envPort([]string{"PAYMENT_PORT", "PAYMENT_AGENT_PORT"}, 18083)

	name := flag.String("name", "payment", "Payment agent name")
	port := flag.Int("port", defPort, "HTTP port for Payment Agent")
	external := flag.String("external", "", "Upstream payment base URL (e.g. http://localhost:5500)")
	upstream := flag.String("upstream", "", "Alias of --external")

	// (선택) inbound DID 검증 — 기본은 꺼짐(루트→내부는 보통 in-proc)
	sage := flag.Bool("sage", false, "Enable inbound DID verification (demo)")
	keys := flag.String("keys", envOr("SAGE_KEYS_JSON", "generated_agent_keys.json"), "path to DID keys json for demo resolver")
	optional := flag.Bool("optional", false, "allow unsigned requests when --sage (demo only)")

	flag.Parse()

	// --upstream 별칭
	if *upstream != "" && *external == "" {
		*external = *upstream
	}
	// 업스트림 주소를 env로 주입 (payment.NewPaymentAgent가 이 값을 읽음)
	if *external != "" {
		base := normalizeBase(*external)
		if base == "" {
			log.Fatalf("[payment] invalid --external/--upstream value: %q", *external)
		}
		os.Setenv("PAYMENT_EXTERNAL_URL", base)
	}

	// 내부 에이전트 생성 (패키지 시그니처에 맞춤)
	agent := payment.NewPaymentAgent(*name)

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "internal-payment",
			"agent_name":   *name,
			"sage_enabled": *sage,
			"upstream":     os.Getenv("PAYMENT_EXTERNAL_URL"),
		})
	})

	// /process : Root가 호출하는 엔드포인트.
	// 바디(JSON)를 types.AgentMessage로 파싱 → agent.Process 호출 → JSON 반환
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in types.AgentMessage
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		// 타임아웃 있는 컨텍스트로 외부 결제 호출
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		out, err := agent.Process(ctx, in)
		if err != nil {
			http.Error(w, "process error: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	var handler http.Handler = inner
	if *sage {
		// external-payment와 동일한 방식의 DID 미들웨어 (데모용)
		mw, err := a2autil.BuildDIDMiddlewareFromChain(*keys, *optional)
		if err != nil {
			log.Fatalf("didauth init: %v", err)
		}
		handler = mw.Wrap(inner)
	}
	mux.Handle("/process", handler)

	addr := ":" + strconv.Itoa(*port)
	log.Printf("[payment] starting on %s (name=%s SAGE=%v upstream=%s)",
		addr, *name, *sage, os.Getenv("PAYMENT_EXTERNAL_URL"))
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
