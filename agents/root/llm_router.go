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
	if isMedicalInfoIntent(low) {
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

func normalizeDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "payment", "medical", "planning", "chat":
		return s
	case "pay", "purchase", "order", "checkout", "결제", "구매", "주문":
		return "payment"
	case "health", "doctor", "med", "의료", "건강":
		return "medical"
	case "plan", "schedule", "todo", "계획", "일정":
		return "planning"
	default:
		return "chat"
	}
}

// --- keyword helpers used only by router ---
func hasProductCue(s string) bool {
	return containsAny(s,
		"아이폰", "iphone", "맥북", "macbook", "아이패드", "ipad", "airpods", "apple watch",
		"갤럭시", "galaxy", "s24", "zflip", "zfold",
		"노트북", "laptop", "데스크탑", "desktop", "모니터", "monitor", "키보드", "keyboard",
		"마우스", "mouse", "tv", "텔레비전", "pro", "ultra", "max", "m2", "m3", "m4",
	)
}
func hasShippingCue(s string) bool {
	return containsAny(s, "배송", "배송지", "주소", "수령", "shipping", "deliver", "delivery", "address", "ship")
}
func hasAmountCue(s string) bool {
	return containsAny(s, "만원", "원 ", " krw", "가격", "price", "cost")
}
func hasRecipientCue(s string) bool {
	return containsAny(s, "받는사람", "수신자", "recipient", "to ")
}
func hasMethodCue(s string) bool {
	return containsAny(s, "카드", "card", "계좌", "bank", "wallet", "카카오페이", "토스", "현금", "무통장")
}
