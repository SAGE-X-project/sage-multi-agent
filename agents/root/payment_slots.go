// Package root - payment slot extraction & helpers.
// Split-out version: delete the payment section in root.go before adding this.
package root

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// Normalized payment slots used across Root → Payment agent.
type paySlots struct {
	Mode      string // "transfer" | "purchase"
	Recipient string
	To        string
	AmountKRW int64
	BudgetKRW int64
	Method    string
	Item      string
	Model     string
	Merchant  string
	Shipping  string
	CardLast4 string
	Note      string
}

// 병합(우변 우선)
func mergePaySlots(a, b paySlots) paySlots {
	out := a
	if strings.TrimSpace(b.Mode) != "" {
		out.Mode = strings.TrimSpace(b.Mode)
	}
	if strings.TrimSpace(b.To) != "" {
		out.To = strings.TrimSpace(b.To)
	}
	if b.AmountKRW > 0 {
		out.AmountKRW = b.AmountKRW
	}
	if b.BudgetKRW > 0 {
		out.BudgetKRW = b.BudgetKRW
	}
	if strings.TrimSpace(b.Method) != "" {
		out.Method = strings.TrimSpace(b.Method)
	}
	if strings.TrimSpace(b.Item) != "" {
		out.Item = strings.TrimSpace(b.Item)
	}
	if strings.TrimSpace(b.Model) != "" {
		out.Model = strings.TrimSpace(b.Model)
	}
	if strings.TrimSpace(b.Merchant) != "" {
		out.Merchant = strings.TrimSpace(b.Merchant)
	}
	if strings.TrimSpace(b.Shipping) != "" {
		out.Shipping = strings.TrimSpace(b.Shipping)
	}
	if strings.TrimSpace(b.CardLast4) != "" {
		out.CardLast4 = strings.TrimSpace(b.CardLast4)
	}
	if strings.TrimSpace(b.Note) != "" {
		out.Note = strings.TrimSpace(b.Note)
	}
	return out
}

func computeMissingPayment(s paySlots) []string {
	var m []string
	if strings.TrimSpace(s.Method) == "" {
		m = append(m, "method")
	}
	if strings.TrimSpace(firstNonEmpty(s.Recipient, s.To)) == "" {
		m = append(m, "recipient")
	}
	if strings.TrimSpace(s.Shipping) == "" {
		m = append(m, "shipping")
	}
	if s.AmountKRW <= 0 && s.BudgetKRW <= 0 {
		m = append(m, "budgetKRW")
	}
	return m
}

// 미리보기
func buildPaymentPreview(lang string, s paySlots) string {
	item := strings.TrimSpace(firstNonEmpty(s.Item, s.Model))
	if item == "" {
		item = "-"
	}
	method := strings.TrimSpace(s.Method)
	if method == "" {
		method = "-"
	}
	ship := strings.TrimSpace(s.Shipping)
	if ship == "" {
		ship = "-"
	}
	merchant := strings.TrimSpace(s.Merchant)
	if merchant == "" {
		merchant = "-"
	}
	budget := "-"
	if s.BudgetKRW > 0 {
		if lang == "ko" {
			budget = withComma(s.BudgetKRW) + "원"
		} else {
			budget = "₩" + withComma(s.BudgetKRW)
		}
	}
	memo := "-" // 비어있으면 하이픈 고정

	if lang == "ko" {
		return fmt.Sprintf("구매 미리보기\n- 상품: %s\n- 결제: %s\n- 배송: %s\n- 예산: %s\n- 상점: %s\n 진행 하시겠습니까?",
			item, method, ship, budget, merchant)
	}
	return fmt.Sprintf("Purchase preview\n- item: %s\n- method: %s\n- shipping: %s\n- budget: %s\n- merchant: %s\n- memo: %s",
		item, method, ship, budget, merchant, memo)
}

func withComma(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return "-" + withComma(-n)
	}
	var out []byte
	cnt := 0
	for i := len(s) - 1; i >= 0; i-- {
		out = append(out, s[i])
		cnt++
		if cnt%3 == 0 && i != 0 {
			out = append(out, ',')
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func (r *RootAgent) buildConfirmPromptLLM(ctx context.Context, lang string, s paySlots) string {
	r.ensureLLM()
	if r.llmClient == nil {
		// 기존 고정 프롬프트 폴백
		if lang == "ko" {
			return "이대로 진행할까요? (예/아니오)"
		}
		return "Proceed with this? (yes/no)"
	}

	sys := map[string]string{
		"ko": `역할: 결제/구매 보조 에이전트.
규칙:
- "한 문장" 또는 "아주 짧은" 확인 질문 1개만 제시한다.
- 예/아니오(또는 네/아니오)로 답할 수 있게 묻는다.
- JSON/리스트/코드블록 금지. 자연어 한 줄만.
- 매번 표현을 살짝 바꿔라(동의어/어순), styleSeed를 참고해 변주.
- 한국어로 출력.`,
		"en": `Role: payment/purchase assistant.
Rules:
- Ask exactly ONE short confirmation question.
- Must be answerable with yes/no.
- No JSON/list/code. Plain natural language only.
- Vary phrasing slightly each time; use styleSeed for variation.
- Output in English.`,
	}[lang]

	styleSeed := fmt.Sprintf("%d", time.Now().UnixNano()%7919)

	// 슬랏 요약 키워드만 제공(자연어는 LLM이)
	var b strings.Builder
	if lang == "ko" {
		fmt.Fprintf(&b, "키워드 요약: ")
		first := true
		if v := firstNonEmpty(s.Model, s.Item); v != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "상품=%s", v)
		}
		if s.Method != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "결제=%s", s.Method)
		}
		if s.Merchant != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "상점=%s", s.Merchant)
		}
		if s.Shipping != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "배송=%s", s.Shipping)
		}
		if s.To != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "수령자=%s", s.To)
		}
		if s.BudgetKRW > 0 {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "예산=%s", formatKRW(s.BudgetKRW))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "styleSeed: %s\n", styleSeed)
		fmt.Fprintf(&b, "출력: '예/아니오'로 답할 수 있는 짧은 한국어 한 문장만\n")
	} else {
		fmt.Fprintf(&b, "keywords: ")
		first := true
		if v := firstNonEmpty(s.Model, s.Item); v != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "item=%s", v)
		}
		if s.Method != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "method=%s", s.Method)
		}
		if s.Merchant != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "merchant=%s", s.Merchant)
		}
		if s.Shipping != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "shipping=%s", s.Shipping)
		}
		if s.To != "" {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "recipient=%s", s.To)
		}
		if s.BudgetKRW > 0 {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "budget=%s", formatKRW(s.BudgetKRW))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "styleSeed: %s\n", styleSeed)
		fmt.Fprintf(&b, "Output: ONE short yes/no English question only\n")
	}

	out, err := r.llmClient.Chat(ctx, sys, b.String())
	if err != nil {
		if lang == "ko" {
			return "이대로 진행할까요? (예/아니오)"
		}
		return "Proceed with this? (yes/no)"
	}
	return strings.TrimSpace(out)
}

// 예/아니오 판별
// 교체: 더 넓은 한국어/축약/명령형 긍정/부정 분류
func parseYesNo(s string) (yes bool, no bool) {
	t := strings.TrimSpace(strings.ToLower(s))

	// 강한 긍정 (예/네/ㅇㅇ/ㄱㄱ/진행/구매/결제/확정/바로/고고/진행해/진행해줘/구매해줘/결제해줘 등)
	pos := []string{
		"예", "네", "응", "ㅇㅇ", "ㅇㅋ", "ok", "okay", "그래", "좋아", "진행", "진행해", "진행해줘", "진행하세요",
		"구매", "구매해", "구매해줘", "사줘", "사 주세요", "결제", "결제해", "결제해줘", "바로", "확정", "고고", "ㄱㄱ",
	}
	// 강한 부정
	neg := []string{
		"아니오", "아니", "싫어", "ㄴㄴ", "no", "취소", "취소해", "그만", "중단", "보류", "대기",
	}

	for _, p := range pos {
		if strings.Contains(t, strings.ToLower(p)) {
			return true, false
		}
	}
	for _, n := range neg {
		if strings.Contains(t, strings.ToLower(n)) {
			return false, true
		}
	}
	return false, false
}

// 본문/메타/JSON/정규식을 종합해 슬롯 채우기.
// root.go의 llmExtractPayment가 먼저 시도되고, 실패 시 이 규칙 파서가 보조.
func extractPaymentSlots(msg *types.AgentMessage) (s paySlots, missing []string, ok bool) {
	// meta
	if msg.Metadata != nil {
		if v, ok2 := msg.Metadata["payment.mode"].(string); ok2 {
			s.Mode = v
		}
		getS := func(keys ...string) string {
			for _, k := range keys {
				if v, ok := msg.Metadata[k]; ok {
					if str, ok2 := v.(string); ok2 && strings.TrimSpace(str) != "" {
						return strings.TrimSpace(str)
					}
				}
			}
			return ""
		}
		getI := func(keys ...string) int64 {
			for _, k := range keys {
				if v, ok := msg.Metadata[k]; ok {
					switch t := v.(type) {
					case float64:
						return int64(t)
					case int:
						return int64(t)
					case int64:
						return t
					case string:
						if n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(t), ",", ""), 10, 64); err == nil {
							return n
						}
					}
				}
			}
			return 0
		}
		s.To = getS("payment.to", "to", "recipient")
		s.Method = getS("payment.method", "method")
		s.Item = getS("payment.item", "item", "제품", "상품")
		s.Model = getS("payment.model", "model", "옵션")
		s.Merchant = getS("payment.merchant", "merchant", "store")
		s.Shipping = getS("payment.shipping", "shipping")
		s.CardLast4 = getS("payment.cardLast4", "cardLast4")
		s.Note = getS("payment.note", "note", "memo")
		s.AmountKRW = getI("payment.amountKRW", "amountKRW", "amount")
		s.BudgetKRW = getI("payment.budgetKRW", "budgetKRW", "budget")
	}

	// JSON 본문
	content := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(content, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(content), &m) == nil {
			setIf := func(dst *string, key string) {
				if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
					*dst = strings.TrimSpace(v)
				}
			}
			setIf(&s.To, "to")
			setIf(&s.Method, "method")
			setIf(&s.Item, "item")
			setIf(&s.Model, "model")
			setIf(&s.Merchant, "merchant")
			setIf(&s.Shipping, "shipping")
			setIf(&s.CardLast4, "cardLast4")
			if s.AmountKRW == 0 {
				switch v := m["amountKRW"].(type) {
				case float64:
					s.AmountKRW = int64(v)
				case string:
					if n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(v), ",", ""), 10, 64); err == nil {
						s.AmountKRW = n
					}
				}
			}
			if s.BudgetKRW == 0 {
				switch v := m["budgetKRW"].(type) {
				case float64:
					s.BudgetKRW = int64(v)
				case string:
					if n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(v), ",", ""), 10, 64); err == nil {
						s.BudgetKRW = n
					}
				}
			}
		}
	}

	// 금액 표현 (원/만 원)
	low := strings.ToLower(content)
	if s.AmountKRW == 0 {
		if m := regexp.MustCompile(`([0-9][0-9,\.]*)\s*(원|krw)`).FindStringSubmatch(low); len(m) >= 2 {
			raw := strings.ReplaceAll(strings.ReplaceAll(m[1], ",", ""), ".", "")
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				s.AmountKRW = n
			}
		}
	}
	if s.AmountKRW == 0 {
		if m := regexp.MustCompile(`([0-9][0-9,\.]*)\s*만\s*원?`).FindStringSubmatch(content); len(m) == 2 {
			raw := strings.ReplaceAll(strings.ReplaceAll(m[1], ",", ""), ".", "")
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				s.AmountKRW = n * 10000
			}
		}
	}

	// 결제수단 힌트
	if s.Method == "" {
		switch {
		case containsAny(low, "신용카드", "체크카드", " card"):
			s.Method = "card"
		case containsAny(low, "카카오페이"):
			s.Method = "kakaopay"
		case containsAny(low, "네이버페이"):
			s.Method = "naverpay"
		case containsAny(low, "토스"):
			s.Method = "toss"
		case containsAny(low, "계좌", "이체", "송금", "bank", "transfer"):
			s.Method = "bank"
		case containsAny(low, "현금", " cash"):
			s.Method = "cash"
		}
	}

	// 상품/상점 힌트
	if s.Item == "" && (containsAny(low, "macbook") || strings.Contains(content, "맥북")) {
		s.Item = "맥북"
	}
	if s.Item == "" && (containsAny(low, "iphone") || strings.Contains(content, "아이폰")) {
		s.Item = "아이폰"
	}
	if s.Merchant == "" && containsAny(low, "쿠팡", "coupang", "apple store", "애플 스토어", "네이버", "지마켓", "11번가", "amazon") {
		if strings.Contains(content, "쿠팡") {
			s.Merchant = "쿠팡"
		}
	}

	// 카드 끝4
	if s.CardLast4 == "" && s.Method == "card" {
		if m := regexp.MustCompile(`(?:끝|last)\s*4\s*(?:자리|digits?)?\s*[:\-]?\s*([0-9]{4})`).FindStringSubmatch(low); len(m) == 2 {
			s.CardLast4 = m[1]
		}
	}

	missing = computeMissingPayment(s)
	ok = true
	return
}
