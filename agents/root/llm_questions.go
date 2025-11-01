// Package root - LLM one-liners for asking missing info and short medical answers.
// Only LLM prompts live here. Non-LLM behavior is untouched.
package root

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/llm"
)

// askForMissingPaymentWithLLM always recomputes dynamic missing via prompt,
// ignoring rigid keyword lists to keep behavior adaptive.
// It asks ONE natural sentence tailored to purchase vs transfer.

// LLM이 매번 다른 표현으로 "부족 정보 질문"을 생성하도록 하는 버전
// LLM이 매번 다른 표현으로 "부족 정보 질문"을 생성하도록 하는 버전 (기존 함수 교체)
// ==== RootAgent.askForMissingPaymentWithLLM (DROP-IN REPLACEMENT) ====
func (r *RootAgent) askForMissingPaymentWithLLM(
	ctx context.Context, lang string, s paySlots, missing []string, userText string,
) string {
	r.ensureLLM()

	koMap := map[string]string{
		"method": "결제수단", "budgetKRW": "예산(원)", "shipping": "배송지",
		"recipient": "수령자", "to": "수령자", "merchant": "상점", "item": "상품", "model": "모델",
	}
	humanMissing := make([]string, 0, len(missing))
	for _, k := range missing {
		if lang == "ko" {
			if v, ok := koMap[k]; ok {
				humanMissing = append(humanMissing, v)
			} else {
				humanMissing = append(humanMissing, k)
			}
		} else {
			humanMissing = append(humanMissing, k)
		}
	}

	type kv struct{ K, V string }
	known := []kv{}
	if v := firstNonEmpty(s.Model, s.Item); v != "" {
		known = append(known, kv{"item", v})
	}
	if s.Method != "" {
		known = append(known, kv{"method", s.Method})
	}
	if s.Merchant != "" {
		known = append(known, kv{"merchant", s.Merchant})
	}
	if s.Shipping != "" {
		known = append(known, kv{"shipping", s.Shipping})
	}
	if s.To != "" {
		known = append(known, kv{"recipient", s.To})
	}
	if s.BudgetKRW > 0 {
		known = append(known, kv{"budgetKRW", formatKRW(s.BudgetKRW)})
	}

	sys := map[string]string{
		"ko": `역할: 결제/구매 보조 에이전트.
규칙:
- "한 문장"만 출력. 이모지/리스트/JSON/코드 금지.
- 부족한 항목들을 "한 번에" 알려달라고 정중히 요청하라.
- 이미 아는 정보는 언급하지 말고 부족 항목만 요약해 나열하라.
- 한국어로 출력.`,
		"en": `Role: payment assistant.
Rules:
- Output exactly ONE sentence (no lists/JSON/code).
- Politely ask the user to provide ALL missing fields at once.
- Do not repeat known info; briefly list missing fields only.`,
	}[lang]

	var b strings.Builder
	if lang == "ko" {
		fmt.Fprintf(&b, "부족 항목: %s\n", strings.Join(humanMissing, "·"))
		if len(known) > 0 {
			b.WriteString("확보된 키워드: ")
			for i, kv := range known {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(kv.K + "=" + kv.V)
			}
			b.WriteString("\n")
		}
		if userText != "" {
			fmt.Fprintf(&b, "사용자 최근 입력: %s\n", compact(userText, 140))
		}
		b.WriteString("출력: 위 부족 항목을 한 번에 알려달라고 정중히 요청하는 한국어 한 문장\n")
	} else {
		fmt.Fprintf(&b, "Missing: %s\n", strings.Join(missing, ", "))
		if len(known) > 0 {
			b.WriteString("Known: ")
			for i, kv := range known {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(kv.K + "=" + kv.V)
			}
			b.WriteString("\n")
		}
		if userText != "" {
			fmt.Fprintf(&b, "User last input: %s\n", compact(userText, 140))
		}
		b.WriteString("Output: ONE sentence politely asking for all missing fields at once\n")
	}

	out := ""
	if r.llmClient != nil {
		if s, err := r.llmClient.Chat(ctx, sys, b.String()); err == nil {
			out = s
		} else {
			log.Println("[llm][err]", err)
		}
	}
	out = strings.TrimSpace(out)

	if out == "" {
		if lang == "ko" {
			out = "가능하시다면 " + strings.Join(humanMissing, "·") + "을(를) 한 번에 알려주시면 바로 이어서 진행할게요."
		} else {
			out = "Please share all missing details at once so I can proceed: " + strings.Join(missing, ", ") + "."
		}
	}
	if lang == "ko" && !strings.HasSuffix(out, "요.") && !strings.HasSuffix(out, "요?") && !strings.HasSuffix(out, "?") {
		out = strings.TrimRight(out, ".") + "요."
	} else if lang != "ko" && !strings.HasSuffix(out, ".") && !strings.HasSuffix(out, "?") {
		out = strings.TrimRight(out, ".") + "."
	}
	return out
}

// 추가: 확인 단계에서 애매하면 LLM로 yes/no/unclear 분류
// 기존 함수 교체: yes/no/unclear 분류를 더 튼튼하게
func (r *RootAgent) llmConfirmIntent(ctx context.Context, lang, user string) string {
	r.ensureLLM()
	if r.llmClient == nil {
		return "unclear"
	}
	sys := map[string]string{
		"ko": "분류기: 아래 입력에 대해 '예' 또는 '아니오' 또는 '애매' 중 하나만 정확히 출력해.",
		"en": "Classifier: output exactly one of yes/no/unclear for the input below.",
	}[lang]
	out, err := r.llmClient.Chat(ctx, sys, strings.TrimSpace(user))
	if err != nil {
		return "unclear"
	}
	o := strings.ToLower(strings.TrimSpace(out))
	o = strings.Trim(o, "`\"'") // 따옴표/백틱 제거
	if i := strings.IndexAny(o, "\r\n"); i >= 0 {
		o = strings.TrimSpace(o[:i])
	} // 첫 줄만

	switch o {
	case "yes", "y", "ok", "okay", "확인", "진행", "네", "예":
		return "yes"
	case "no", "n", "취소", "중단", "아니오", "아니":
		return "no"
	default:
		return "unclear"
	}
}

// askForMissingMedicalWithLLM: ONE-line ask covering all missing essentials.
func (r *RootAgent) askForMissingMedicalWithLLM(ctx context.Context, lang string, missing []string, userText string) string {
	r.ensureLLM()
	if r.llmClient == nil {
		if langOrDefault(lang) == "ko" {
			return "의료 정보를 제공하려면 " + strings.Join(missing, ", ") + "를 알려주세요."
		}
		return "To help, please share: " + strings.Join(missing, ", ") + "."
	}

	sys := map[string]string{
		"ko": "너는 의료 정보 수집 도우미야. 부족한 항목만 '한국어 한 문장'으로 자연스럽게 물어봐. 리스트/예시/코드 금지.",
		"en": "You are a medical info collector. Ask ONLY the missing items in ONE short sentence. No lists/examples/code.",
	}[langOrDefault(lang)]
	usr := "Missing=" + strings.Join(missing, ", ") + "\nUserText=" + strings.TrimSpace(userText)

	out, err := r.llmClient.Chat(ctx, sys, usr)
	if err != nil || strings.TrimSpace(out) == "" {
		if langOrDefault(lang) == "ko" {
			return "의료 정보를 제공하려면 " + strings.Join(missing, ", ") + "를 알려주세요."
		}
		return "To help, please share: " + strings.Join(missing, ", ") + "."
	}
	return strings.TrimSpace(out)
}

// askForMissingPlanningWithLLM: ONE-line ask covering all missing essentials.
// 계획(PLANNING) 누락 슬롯 질문기
func (r *RootAgent) askForMissingPlanningWithLLM(ctx context.Context, lang string, missing []string, userText string) string {
	r.ensureLLM()

	// 언어별 폴백(LLM 비활성/실패 시)
	koFallback := fmt.Sprintf("계획을 세우려면 %s 정보가 필요해요.", strings.Join(missing, ", "))
	enFallback := fmt.Sprintf("To make the plan, I need %s.", strings.Join(missing, ", "))
	fallback := map[string]string{"ko": koFallback, "en": enFallback}[lang]

	if r.llmClient == nil {
		return fallback
	}

	// 시스템 프롬프트
	var sys string
	if lang == "ko" {
		sys = "너는 일정/계획 도우미야. 부족한 정보만 한국어 '한 문장'으로 짧고 자연스럽게 물어봐. 예시/코드블록/리스트 금지."
	} else {
		sys = "You are a planning assistant. Ask ONLY the missing info in ONE short, natural sentence. No examples, no lists, no code blocks."
	}

	usr := fmt.Sprintf(
		"Agent=planning\nLanguage=%s\nMissing=%s\nUser said: %s",
		lang, strings.Join(missing, ", "), strings.TrimSpace(userText),
	)

	if out, err := r.llmClient.Chat(ctx, sys, usr); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	return fallback
}

// llmMedicalAnswer: short, conservative, non-advisory informational response.

func (r *RootAgent) llmMedicalAnswer(ctx context.Context, lang string, userText string, s medicalSlots) string {
	r.ensureLLM()
	// 안전 가드: 의학적 조언/진단 아님 + 응급 징후 안내 + 간결/근거지향
	sys := map[string]string{
		"ko": `너는 의료 정보 어시스턴트야. 
- 의학적 조언/진단을 대체하지 않는다고 명확히 말해. 
- 핵심만 5줄 이내로, 불릿 없이 짧은 문장으로.
- 생활관리 팁은 보수적으로. 약/검사는 전문의 상담 권고.
- 응급 경고 신호(심한 증상/의식저하/자살사고 등) 시 즉시 119/응급실 권고.
- 정보 제공 목적임을 마지막에 다시 한번 밝힘.`,
		"en": `You are a medical information assistant.
- Clarify this is not medical advice/diagnosis.
- Keep it under ~5 short lines, no bullets.
- Be conservative; advise to see a professional for meds/tests.
- For red flags (severe symptoms, altered consciousness, suicidal thoughts), advise ER immediately.
- End by restating informational purpose.`,
	}[lang]

	usr := fmt.Sprintf(
		"Language=%s\nCondition=%s\nTopic=%s\nAudience=%s\nDuration=%s\nAge=%s\nMedications=%s\nQuestion=%s",
		lang, s.Condition, s.Topic, s.Audience, s.Duration, s.Age, s.Medications, strings.TrimSpace(userText),
	)

	out, err := r.llmClient.Chat(ctx, sys, usr)
	if err != nil || strings.TrimSpace(out) == "" {
		if lang == "ko" {
			return "의료 정보 제공용 답변을 생성하지 못했어요. 증상이 지속되거나 심해지면 의료진과 상의하세요."
		}
		return "I couldn't generate a medical info response. Please consult a healthcare professional."
	}
	return strings.TrimSpace(out)
}

// GeneratePaymentReceipt: short, friendly one-liner receipt (kept as LLM-first with fallback).
func GeneratePaymentReceipt(ctx context.Context, c llm.Client, lang, to string, amountKRW int64, method, item, memo string) string {
	if lang != "en" && lang != "ko" {
		lang = "ko"
	}
	sys := map[string]string{
		"en": "You are a payment agent. Produce a short, natural confirmation in ONE line. Include KRW with thousand separators, recipient and method if available, and a short fake order ID like ORD-5F3A. No code blocks.",
		"ko": "당신은 결제 에이전트입니다. 한 줄로 자연스럽게 결제 완료 문장을 출력하세요. 금액은 천단위 콤마, 수신자/결제수단이 있으면 포함, 간단한 가짜 주문번호(예: ORD-5F3A)를 넣으세요. 코드블록 금지.",
	}[lang]

	usr := fmt.Sprintf(
		"Recipient: %s\nAmount (KRW): %d\nMethod: %s\nItem: %s\nMemo: %s\nStyle: concise, friendly.",
		strings.TrimSpace(to), amountKRW, strings.TrimSpace(method), strings.TrimSpace(item), strings.TrimSpace(memo),
	)

	if c != nil {
		if out, err := c.Chat(ctx, sys, usr); err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}

	// Fallback if LLM disabled/unavailable
	amt := formatKRW(amountKRW)
	orderID := fmt.Sprintf("ORD-%04d", time.Now().Unix()%10000)
	if lang == "en" {
		parts := []string{"Payment completed", fmt.Sprintf("%s KRW", amt)}
		if strings.TrimSpace(item) != "" {
			parts = append(parts, fmt.Sprintf("(item: %s)", strings.TrimSpace(item)))
		}
		if strings.TrimSpace(to) != "" {
			parts = append(parts, fmt.Sprintf("to %s", strings.TrimSpace(to)))
		}
		if strings.TrimSpace(method) != "" {
			parts = append(parts, fmt.Sprintf("via %s", strings.TrimSpace(method)))
		}
		if strings.TrimSpace(memo) != "" {
			parts = append(parts, fmt.Sprintf("- %s", strings.TrimSpace(memo)))
		}
		parts = append(parts, fmt.Sprintf("[%s]", orderID))
		return strings.Join(parts, " ") + " ✅"
	}
	parts := []string{"결제가 완료되었습니다", fmt.Sprintf("%s원", amt)}
	if strings.TrimSpace(item) != "" {
		parts = append(parts, fmt.Sprintf("(상품: %s)", strings.TrimSpace(item)))
	}
	if strings.TrimSpace(to) != "" {
		parts = append(parts, fmt.Sprintf("→ %s", strings.TrimSpace(to)))
	}
	if strings.TrimSpace(method) != "" {
		parts = append(parts, fmt.Sprintf("/ %s", strings.TrimSpace(method)))
	}
	if strings.TrimSpace(memo) != "" {
		parts = append(parts, fmt.Sprintf("- %s", strings.TrimSpace(memo)))
	}
	parts = append(parts, fmt.Sprintf("[%s]", orderID))
	return strings.Join(parts, " ") + " ✅"
}

// formatKRW formats an integer in KRW with thousand separators.
func formatKRW(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := ""
	if n < 0 {
		neg = "-"
		s = s[1:]
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return neg + string(out)
}

func compact(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= limit {
		return s
	}
	rs := []rune(s)
	return string(rs[:limit]) + "…"
}
