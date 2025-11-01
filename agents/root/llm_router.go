// Package root - LLM Router (domain classification).
// Keeps strong guards minimal; prefers LLM; falls back to guards on low confidence.
package root

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/llm"
)

type routeOut struct {
	Domain string // "payment" | "medical" | "planning" | ""
	Lang   string // "ko" | "en"
}

var routerJSONRe = regexp.MustCompile(`(?s)\{.*\}`)

// llmRoute uses the LLM to classify domain, with minimal hard guards as safety rails.
func (r *RootAgent) llmRoute(ctx context.Context, text string) (routeOut, bool) {
	// 1) 규칙 우선 빠른 판정
	low := strings.ToLower(strings.TrimSpace(text))
	if isPaymentActionIntent(low) {
		return routeOut{Domain: "payment", Lang: llm.DetectLang(text)}, true
	}
	if isMedicalActionIntent(low) {
		return routeOut{Domain: "medical", Lang: llm.DetectLang(text)}, true
	}
	if isPlanningActionIntent(low) {
		return routeOut{Domain: "planning", Lang: llm.DetectLang(text)}, true
	}

	// 2) LLM 사용 (있을 때만)
	r.ensureLLM()
	if r.llmClient == nil {
		return routeOut{}, false
	}

	sys := `You are an intent classifier.
Return a single JSON object with fields: domain in ["payment","medical","planning","chat"], lang in ["ko","en"].
Pick the most likely domain.`
	pr := map[string]any{"text": text}
	jb, _ := json.Marshal(pr)
	out, err := r.llmClient.Chat(ctx, sys, string(jb))
	if err != nil || strings.TrimSpace(out) == "" {
		return routeOut{}, false
	}
	// 관대한 파서
	var m struct{ Domain, Lang string }
	_ = json.Unmarshal([]byte(out), &m)
	m.Domain = strings.ToLower(strings.TrimSpace(m.Domain))
	if m.Domain == "chat" {
		m.Domain = ""
	}
	lg := strings.ToLower(strings.TrimSpace(m.Lang))
	if lg != "ko" && lg != "en" {
		lg = llm.DetectLang(text)
	}
	if m.Domain == "payment" || m.Domain == "medical" || m.Domain == "planning" || m.Domain == "" {
		return routeOut{Domain: m.Domain, Lang: lg}, true
	}
	return routeOut{}, false
}

func hasRecipientCue(s string) bool {
	return containsAny(s, "받는사람", "수신자", "recipient", "to ")
}
func hasMethodCue(s string) bool {
	return containsAny(s, "카드", "card", "계좌", "bank", "wallet", "카카오페이", "토스", "현금", "무통장")
}
