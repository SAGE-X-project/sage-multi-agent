// Package root - LLM-based structured extractors for payment / medical / planning.
// This file ONLY touches LLM prompts and JSON schemas. Non-LLM parts remain unchanged.
package root

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

/* ------------------------- PAYMENT ------------------------- */

// payFields are LLM-facing normalized fields including richer transfer needs.
// Root's existing paySlots remains unchanged; extra fields are for LLM Q&A only.

type llmPaymentExtract struct {
	Fields struct {
		Mode      string `json:"mode"`
		Method    string `json:"method"`
		Shipping  string `json:"shipping"`
		To        string `json:"to"`
		Merchant  string `json:"merchant"`
		Item      string `json:"item"`
		Model     string `json:"model"`
		AmountKRW int64  `json:"amountKRW"`
		BudgetKRW int64  `json:"budgetKRW"`
		CardLast4 string `json:"cardLast4"`
	} `json:"fields"`
}

func (r *RootAgent) llmExtractPayment(ctx context.Context, lang, text string) (*llmPaymentExtract, bool) {
	r.ensureLLM()
	xo := &llmPaymentExtract{}

	if r.llmClient != nil {
		sys := map[string]string{
			"ko": `역할: 결제/구매 정보 추출기.
출력은 JSON 하나({ ... })만. 코드블록/설명 금지.
필드:
- fields.mode: "buy"/"transfer" 등 (구매 맥락이면 buy)
- fields.method: 국민카드/신한카드/카카오페이/계좌이체/현금 등 (원문 보존)
- fields.shipping: 주소/지역(자유형)
- fields.recipient 또는 to: 수령자 이름
- fields.merchant: 쿠팡/네이버/11번가/지마켓/SSG/애플스토어 등
- fields.item, fields.model: 상품명/모델명 (자유 추출, 어떤 상품이든)
- fields.amountKRW, fields.budgetKRW: 정수 KRW. "150만 원" → 1500000.
규칙:
- 억=100000000, 만=10000. 쉼표 제거. 소수점 버림.
- "구매/주문/결제" 맥락이면 budgetKRW 우선, "송금"이면 amountKRW 우선.
- 모르는 건 0 또는 "".`,
			"en": `Role: payment info extractor. Output exactly one JSON. Fields: mode, method, shipping, recipient|to, merchant, item, model, amountKRW, budgetKRW. Korean units OK.`,
		}[lang]
		if out, err := r.llmClient.Chat(ctx, sys, text); err == nil {
			if js := extractFirstJSONObject(out); js != "" {
				_ = json.Unmarshal([]byte(js), xo)
			}
		}
	}

	// 금액 보정
	if xo.Fields.AmountKRW <= 0 && xo.Fields.BudgetKRW <= 0 {
		if n := parseKRWFromText(text); n > 0 {
			if looksLikeTransfer(text) {
				xo.Fields.AmountKRW = n
			} else {
				xo.Fields.BudgetKRW = n
			}
		}
	}
	if strings.TrimSpace(xo.Fields.Method) == "" {
		if m := pickMethod(text); m != "" {
			xo.Fields.Method = m
		}
	}
	if strings.TrimSpace(xo.Fields.Shipping) == "" {
		if a := pickAddress(text); a != "" {
			xo.Fields.Shipping = a
		}
	}
	if strings.TrimSpace(xo.Fields.To) == "" {
		if n := pickRecipient(text); n != "" {
			xo.Fields.To = n
		}
	}
	if strings.TrimSpace(xo.Fields.Merchant) == "" {
		if m := pickMerchant(text); m != "" {
			xo.Fields.Merchant = m
		}
	}
	if strings.TrimSpace(xo.Fields.Mode) == "" {
		ps := paySlots{}
		xo.Fields.Mode = classifyPaymentMode(text, ps)
		if xo.Fields.Mode == "" {
			xo.Fields.Mode = "buy"
		}
	}
	// item/model: 일반화 (예: “맥북 프로 16”, “아이폰 15 프로”, “에어팟 프로 2” 등 자유 추출)
	if strings.TrimSpace(xo.Fields.Item) == "" {
		if it, md := pickItemAndModel(text, xo.Fields.Merchant, xo.Fields.Shipping); it != "" {
			xo.Fields.Item = it
			if xo.Fields.Model == "" {
				xo.Fields.Model = md
			}
		}
	}

	if strings.TrimSpace(xo.Fields.Method) == "" &&
		strings.TrimSpace(xo.Fields.Shipping) == "" &&
		strings.TrimSpace(xo.Fields.To) == "" &&
		strings.TrimSpace(xo.Fields.Merchant) == "" &&
		strings.TrimSpace(xo.Fields.Item) == "" &&
		xo.Fields.AmountKRW <= 0 && xo.Fields.BudgetKRW <= 0 {
		return nil, false
	}
	return xo, true
}

/* ------------------------- MEDICAL ------------------------- */

type medicalExtractOut struct {
	Fields  medicalSlots
	Missing []string
	Ask     string
}

func (r *RootAgent) llmExtractMedical(ctx context.Context, lang, text string) (medicalExtractOut, bool) {
	out := medicalExtractOut{}
	// JSON
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		var m map[string]any
		if json.Unmarshal([]byte(text), &m) == nil {
			s := medicalSlots{}
			for k, ptr := range map[string]*string{
				"medical.condition": &s.Condition,
				"condition":         &s.Condition,
				"medical.topic":     &s.Topic,
				"topic":             &s.Topic,
				"medical.audience":  &s.Audience,
				"audience":          &s.Audience,
				"medical.duration":  &s.Duration,
				"duration":          &s.Duration,
				"medical.age":       &s.Age,
				"age":               &s.Age,
				"medical.meds":      &s.Medications,
				"meds":              &s.Medications,
				"medications":       &s.Medications,
			} {
				if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
					*ptr = strings.TrimSpace(v)
				}
			}
			out.Fields = s
			out.Missing = []string{}
			if s.Condition == "" {
				out.Missing = append(out.Missing, "condition(질환)")
			}
			if s.Topic == "" {
				out.Missing = append(out.Missing, "topic(주제)")
			}
			if len(out.Missing) > 0 {
				out.Ask = r.askForMissingMedicalWithLLM(ctx, lang, out.Missing, text)
			}
			return out, true
		}
	}
	// Heuristics (very light)
	s := medicalSlots{}
	t := strings.ToLower(strings.TrimSpace(text))
	if containsAny(t, "당뇨", "diabetes") {
		s.Condition = "당뇨병"
	}
	if containsAny(t, "식단", "diet") {
		s.Topic = "식단"
	}
	out.Fields = s
	out.Missing = []string{}
	if s.Condition == "" {
		out.Missing = append(out.Missing, "condition(질환)")
	}
	if s.Topic == "" {
		out.Missing = append(out.Missing, "topic(주제)")
	}
	if len(out.Missing) > 0 {
		out.Ask = r.askForMissingMedicalWithLLM(ctx, lang, out.Missing, text)
	}
	return out, s.Condition != "" || s.Topic != ""
}

/* ------------------------- PLANNING ------------------------- */

type planningExtractOut struct {
	Fields  planningSlots
	Missing []string
	Ask     string
}

func (r *RootAgent) llmExtractPlanning(ctx context.Context, lang, text string) (planningExtractOut, bool) {
	out := planningExtractOut{}
	// JSON
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		var m map[string]any
		if json.Unmarshal([]byte(text), &m) == nil {
			s := planningSlots{}
			if v, ok := m["task"].(string); ok && strings.TrimSpace(v) != "" {
				s.Task = strings.TrimSpace(v)
			}
			if v, ok := m["timeframe"].(string); ok && strings.TrimSpace(v) != "" {
				s.Timeframe = strings.TrimSpace(v)
			}
			if v, ok := m["context"].(string); ok && strings.TrimSpace(v) != "" {
				s.Context = strings.TrimSpace(v)
			}
			out.Fields = s
			if s.Task == "" {
				out.Missing = append(out.Missing, "task/goal(계획 대상)")
			}
			if len(out.Missing) > 0 {
				out.Ask = r.askForMissingPlanningWithLLM(ctx, lang, out.Missing, text)
			}
			return out, true
		}
	}
	// Heuristics
	s := planningSlots{}
	t := strings.ToLower(strings.TrimSpace(text))
	if containsAny(t, "계획", "일정", "plan", "schedule", "플랜", "할일") {
		// rough: treat entire text as task when short
		if len([]rune(text)) <= 60 {
			s.Task = strings.TrimSpace(text)
		} else {
			s.Task = "계획 수립"
		}
	}
	out.Fields = s
	if s.Task == "" {
		out.Missing = append(out.Missing, "task/goal(계획 대상)")
	}
	if len(out.Missing) > 0 {
		out.Ask = r.askForMissingPlanningWithLLM(ctx, lang, out.Missing, text)
	}
	return out, s.Task != ""
}

// pickItemAndModel는 문장 내에서 "구매/주문/결제/사다" 등 동사와 가장 가까운 객체를 아이템 후보로 추출하고,
// 숫자/버전/수식어(프로/울트라/플러스/맥스/SE/Ultra/Pro/Max/Plus/Ti/Super 등)가 섞인 부분을 모델로 분리한다.
// merchant나 주소처럼 아이템이 될 수 없는 후보는 제외한다.
func pickItemAndModel(text, merchant, shipping string) (item string, model string) {
	t := strings.TrimSpace(normalizeSpaces(text))

	// 0) 주소/상점 토큰은 제외
	exclude := map[string]struct{}{}
	if merchant = strings.TrimSpace(merchant); merchant != "" {
		exclude[merchant] = struct{}{}
	}
	if shipping = strings.TrimSpace(shipping); shipping != "" {
		for _, tok := range strings.Fields(shipping) {
			exclude[tok] = struct{}{}
		}
	}
	commonMerchants := []string{"쿠팡", "네이버", "지마켓", "11번가", "SSG", "롯데ON", "무신사", "이마트", "티몬", "위메프", "배민", "마켓컬리"}
	for _, m := range commonMerchants {
		exclude[m] = struct{}{}
	}

	// 1) 따옴표 안 구절 우선: “ … ” / " … "
	if q := firstQuoted(t); q != "" && !isExcluded(q, exclude) {
		return splitItemModel(q)
	}

	// 2) 목적격 + 구매동사 패턴:  (NOUN{1,6}) (을|를) (구매|주문|결제|사|사줘|사주세요)
	re := regexp.MustCompile(`([^\s,]{1,80}(?:\s+[^\s,]{1,80}){0,5})\s*(?:을|를)\s*(?:구매|주문|결제|사|사줘|사주세요)`)
	if m := re.FindStringSubmatch(t); len(m) > 1 {
		cand := strings.TrimSpace(m[1])
		if cand != "" && !isExcluded(cand, exclude) {
			return splitItemModel(cand)
		}
	}

	// 3) 구매동사 앞쪽 명사구 근접 탐색
	re2 := regexp.MustCompile(`([^\s,]{1,80}(?:\s+[^\s,]{1,80}){0,5})\s*(?:로|으로|에서)?\s*(?:주문|구매|결제|사(?:요|자|줘|줄래|주세요)?)`)
	if m := re2.FindStringSubmatch(t); len(m) > 1 {
		cand := strings.TrimSpace(m[1])
		// "쿠팡에서 주문" 같은 상점 문구 제거
		cand = strings.TrimSuffix(cand, "에서")
		cand = strings.TrimSpace(cand)
		if cand != "" && !isExcluded(cand, exclude) {
			return splitItemModel(cand)
		}
	}

	// 4) 마지막 수단: 긴 명사구 후보(쉼표로 구분된 항목 중 상점/주소 제외, 숫자·대문자 혼합 우선)
	for _, seg := range strings.Split(t, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" || isExcluded(seg, exclude) {
			continue
		}
		if likelyProductPhrase(seg) {
			return splitItemModel(seg)
		}
	}

	return "", ""
}

func normalizeSpaces(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

func firstQuoted(s string) string {
	qs := []struct{ l, r string }{{"“", "”"}, {"\"", "\""}, {"‘", "’"}, {"'", "'"}}
	for _, q := range qs {
		l, r := q.l, q.r
		if li := strings.Index(s, l); li >= 0 {
			if ri := strings.Index(s[li+len(l):], r); ri >= 0 {
				return strings.TrimSpace(s[li+len(l) : li+len(l)+ri])
			}
		}
	}
	return ""
}

func isExcluded(phrase string, exclude map[string]struct{}) bool {
	for tok := range exclude {
		if tok == "" {
			continue
		}
		if strings.Contains(phrase, tok) {
			return true
		}
	}
	return false
}

var modelCue = regexp.MustCompile(`(?i)\b(ultra|pro|max|plus|mini|air|se|ti|super|fe|fold|flip|note|s\d{1,2}|z\d{1,2})\b`)
var hasDigitOrHyphen = regexp.MustCompile(`[0-9]|-`)

func splitItemModel(phrase string) (item string, model string) {
	phrase = strings.TrimSpace(phrase)
	toks := strings.Fields(phrase)
	if len(toks) == 0 {
		return "", ""
	}

	// (a) 숫자/하이픈/모델 큐가 처음 나타나는 위치를 모델 시작으로 간주
	idx := -1
	for i, tk := range toks {
		if hasDigitOrHyphen.MatchString(tk) || modelCue.MatchString(tk) {
			idx = i
			break
		}
	}
	if idx <= 0 {
		// 모델 단서가 없으면 전체를 item
		return phrase, ""
	}
	item = strings.Join(toks[:idx], " ")
	model = strings.Join(toks[idx:], " ")
	return strings.TrimSpace(item), strings.TrimSpace(model)
}

func likelyProductPhrase(seg string) bool {
	// 상품스러움의 매우 약한 힌트: 숫자/영문대문자/하이픈 포함 또는 2~6어절의 명사구
	if hasDigitOrHyphen.MatchString(seg) {
		return true
	}
	words := len(strings.Fields(seg))
	return words >= 2 && words <= 6
}

// LLM이 앞뒤에 설명을 붙여도 첫 '{'~마지막 '}'만 남김
func trimToJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func normalizeMethod(m string) string {
	x := strings.ToLower(strings.TrimSpace(m))
	switch {
	case x == "card" || strings.Contains(x, "카드") || strings.Contains(x, "체크") || strings.Contains(x, "신용"):
		return "card"
	case x == "bank" || strings.Contains(x, "계좌") || strings.Contains(x, "이체") || strings.Contains(x, "송금"):
		return "bank"
	case strings.Contains(x, "kakao"):
		return "kakaopay"
	case strings.Contains(x, "naver"):
		return "naverpay"
	case strings.Contains(x, "toss"):
		return "toss"
	case strings.Contains(x, "cash") || strings.Contains(x, "현금"):
		return "cash"
	default:
		return x
	}
}

// "150만원", "1.5만", "150,000원", "150만 원" → KRW 정수
var reKRW = regexp.MustCompile(`(?i)(\d[\d,\.]*)\s*(만원|만|원|krw)`)

func parseKRW(s string) int64 {
	m := reKRW.FindStringSubmatch(strings.ReplaceAll(s, " ", ""))
	if len(m) < 3 {
		return 0
	}
	num := strings.ReplaceAll(m[1], ",", "")
	factor := int64(1)
	unit := strings.ToLower(m[2])
	if unit == "만원" || unit == "만" {
		factor = 10000
	}
	// 소수점 지원 (1.5만)
	if strings.Contains(num, ".") {
		if f, err := strconv.ParseFloat(num, 64); err == nil {
			return int64(f * float64(factor))
		}
	}
	if n, err := strconv.ParseInt(num, 10, 64); err == nil {
		return n * factor
	}
	return 0
}

var knownMerchants = []string{"쿠팡", "네이버", "11번가", "지마켓", "G마켓", "옥션", "위메프", "SSG", "이마트", "하이마트", "애플스토어", "Apple Store"}

func inferMerchant(s string) string {
	for _, k := range knownMerchants {
		if strings.Contains(s, k) {
			// 통일
			if k == "G마켓" {
				return "지마켓"
			}
			if k == "Apple Store" {
				return "애플스토어"
			}
			return k
		}
	}
	return ""
}

// "배송지: 서울..." / "배송지는 서울 ..." / "수령자 황수진" 같은 패턴 추출
func inferAfterKeyword(s, keywordRegex string) string {
	re := regexp.MustCompile(`(?i)(` + keywordRegex + `)\s*[:=\s]\s*([^,，\n]+)`)
	if mm := re.FindStringSubmatch(s); len(mm) >= 3 {
		return strings.TrimSpace(mm[2])
	}
	return ""
}

func extractFirstJSONObject(s string) string {
	s = strings.TrimSpace(s)
	// 가장 앞의 '{'부터 마지막 '}'까지 단순 추출
	l := strings.IndexByte(s, '{')
	r := strings.LastIndexByte(s, '}')
	if l >= 0 && r > l {
		return s[l : r+1]
	}
	return s
}

func looksLikeTransfer(t string) bool {
	t = strings.ToLower(t)
	return strings.Contains(t, "송금") || strings.Contains(t, "보내") || strings.Contains(t, "이체")
}

func parseKRWFromText(t string) int64 {
	t = strings.ReplaceAll(t, ",", "")
	t = strings.TrimSpace(t)

	// 150만 원 / 2.3억
	reUnit := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(억|만)\s*원?`)
	if m := reUnit.FindStringSubmatch(t); len(m) == 3 {
		f, _ := strconv.ParseFloat(m[1], 64)
		unit := int64(1)
		switch m[2] {
		case "억":
			unit = 100_000_000
		case "만":
			unit = 10_000
		}
		return int64(f * float64(unit))
	}
	// 1500000원
	reWon := regexp.MustCompile(`\b(\d+)\s*원`)
	if m := reWon.FindStringSubmatch(t); len(m) == 2 {
		if n, _ := strconv.ParseInt(m[1], 10, 64); n > 0 {
			return n
		}
	}
	// 마지막 보루: 큰 정수
	reBig := regexp.MustCompile(`\b(\d{6,})\b`)
	if m := reBig.FindStringSubmatch(t); len(m) == 2 {
		if n, _ := strconv.ParseInt(m[1], 10, 64); n > 0 {
			return n
		}
	}
	return 0
}

func pickMethod(t string) string {
	// 대표 키워드만 간단히
	banks := []string{"국민", "신한", "우리", "하나", "농협", "롯데", "삼성", "현대"}
	w := strings.ReplaceAll(t, "카 드", "카드")
	w = strings.ReplaceAll(w, "카드", " 카드")
	for _, b := range banks {
		if strings.Contains(w, b+" 카드") || strings.Contains(w, b+"카드") {
			return strings.TrimSpace(b + " 카드")
		}
	}
	wallets := []string{"카카오페이", "네이버페이", "토스", "국민페이"}
	for _, p := range wallets {
		if strings.Contains(t, p) {
			return p
		}
	}
	if strings.Contains(t, "계좌이체") {
		return "계좌이체"
	}
	if strings.Contains(t, "현금") {
		return "현금"
	}
	return ""
}

func pickAddress(t string) string {
	// 가벼운 주소 힌트
	keys := []string{"서울", "수원", "경기", "광진구", "능동로", "동", "로", "길", "호"}
	hit := 0
	for _, k := range keys {
		if strings.Contains(t, k) {
			hit++
		}
	}
	if hit >= 2 { // 두 개 이상 키워드 포함시 주소로 간주
		return compact(t, 80) // 그냥 원문 일부 전달(미니멈)
	}
	return ""
}

func pickRecipient(t string) string {
	// "~에게/한테 배송", "수령자는 XXX"
	if i := strings.Index(t, "수령자"); i >= 0 {
		seg := strings.TrimSpace(t[i:])
		seg = strings.TrimPrefix(seg, "수령자")
		seg = strings.Trim(seg, " 는은이가: ")
		seg = strings.Fields(seg)[0]
		return seg
	}
	if i := strings.Index(t, "에게 배송"); i > 0 {
		pre := strings.TrimSpace(t[:i])
		parts := strings.Fields(pre)
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return ""
}

func pickMerchant(t string) string {
	m := []string{"쿠팡", "네이버", "지마켓", "G마켓", "11번가", "SSG", "옥션", "티몬", "위메프", "하이마트", "애플스토어"}
	for _, v := range m {
		if strings.Contains(t, v) {
			return v
		}
	}
	return ""
}
