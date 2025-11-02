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

// llmExtractPayment.go (replacement)
func (r *RootAgent) llmExtractPayment(ctx context.Context, lang, text string) (*llmPaymentExtract, bool) {
	r.ensureLLM()
	xo := &llmPaymentExtract{}

    // 1) Prefer LLM JSON extraction
	if r.llmClient != nil && strings.TrimSpace(text) != "" {
		sys := map[string]string{
			"ko": `역할: 결제/구매 정보 추출기.
출력은 JSON "하나"({ ... })만. 코드블록/설명 금지.
스키마:
{"fields":{"mode":"","method":"","shipping":"","to":"","merchant":"","item":"","model":"","amountKRW":0,"budgetKRW":0,"cardLast4":""}}
규칙:
- "recipient"를 반환한다면 "to"로 넣어라.
- 금액 단위: "150만 원" => 1500000, 억=100000000, 만=10000, 쉼표 제거, 소수점 버림.
- 구매/주문/결제 맥락이면 budgetKRW 우선, 송금/이체면 amountKRW 우선.
모르면 0 또는 ""로.`,
			"en": `Role: extract payment info. Output exactly ONE JSON only:
{"fields":{"mode":"","method":"","shipping":"","to":"","merchant":"","item":"","model":"","amountKRW":0,"budgetKRW":0,"cardLast4":""}}
If "recipient" key is used, copy it to "to". Amounts are integers in KRW.`,
		}[langOrDefault(lang)]

		if out, err := r.llmClient.Chat(ctx, sys, strings.TrimSpace(text)); err == nil && strings.TrimSpace(out) != "" {
			js := extractFirstJSONObject(out)
			if js != "" {
                // Allow recipient -> to normalization
				js = strings.ReplaceAll(js, `"recipient"`, `"to"`)
				_ = json.Unmarshal([]byte(js), xo)
			} else {
				r.logger.Printf("[llm][slots][warn] no json found")
			}
		} else if err != nil {
			r.logger.Printf("[llm][err] %v", err)
		}
	}

    // 2) Rule-based fallback/augmentation (fill only fields left empty by LLM)
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
	if strings.TrimSpace(xo.Fields.Item) == "" || strings.TrimSpace(xo.Fields.Model) == "" {
		if it, md := pickItemAndModel(text, xo.Fields.Merchant, xo.Fields.Shipping); it != "" {
			if xo.Fields.Item == "" {
				xo.Fields.Item = it
			}
			if xo.Fields.Model == "" {
				xo.Fields.Model = md
			}
		}
	}
    // Amount/budget augmentation
	if xo.Fields.AmountKRW <= 0 && xo.Fields.BudgetKRW <= 0 {
		if n := parseKRWFromText(text); n > 0 {
			if looksLikeTransfer(text) {
				xo.Fields.AmountKRW = n
			} else {
				xo.Fields.BudgetKRW = n
			}
		}
	}
    // Mode adjustment
	if strings.TrimSpace(xo.Fields.Mode) == "" {
        ps := paySlots{} // internal type
		xo.Fields.Mode = classifyPaymentMode(text, ps)
		if xo.Fields.Mode == "" {
			xo.Fields.Mode = "buy"
		}
	}

	// If shipping not specified, use recipient as default
	if strings.TrimSpace(xo.Fields.Shipping) == "" && strings.TrimSpace(xo.Fields.To) != "" {
		xo.Fields.Shipping = xo.Fields.To
	}

    // Fail if nothing extracted
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

// llmExtractMedical: extract medicalSlots from input and generate a one-sentence ask if needed
type medicalXO struct {
	Fields  medicalSlots `json:"fields"`
	Missing []string     `json:"missing,omitempty"`
	Ask     string       `json:"ask,omitempty"`
}

func (r *RootAgent) llmExtractMedical(ctx context.Context, lang, text string) (medicalXO, bool) {
	r.ensureLLM()
	var zero medicalXO
	if r.llmClient == nil || strings.TrimSpace(text) == "" {
		return zero, false
	}

	sys := map[string]string{
		"ko": `너는 의료 의도/인테이크 추출기야. 아래 JSON "하나"만 출력해.
{
  "fields": {
    "condition": "",   // 질환명(예: 당뇨병, 우울증 등)
    "topic": "",       // 예: 증상, 검사/진단, 약물/복용, 부작용, 식단, 운동, 관리, 예방
    "audience": "",    // 예: 본인, 가족, 임산부, 아동, 노인
    "duration": "",    // 예: 2주, 어제부터
    "age": "",         // 선택
    "medications": "", // 선택
    "symptoms": ""     // ★ 자유 텍스트 증상(있으면 최대 한두 문장)
  },
  "missing": [],       // 최소: condition, topic 또는 symptoms
  "ask": ""            // 누락 항목을 한 번에 물어보는 한국어 한 문장
}
설명/코드블록/리스트 금지. JSON만.`,
		"en": `Extract medical intent/intake. Output ONE JSON only:
{"fields":{"condition":"","topic":"","audience":"","duration":"","age":"","medications":"","symptoms":""},"missing":[],"ask":""}
If informational query (diet/exercise/management), "topic" should reflect that. Ask is ONE sentence.`,
	}[langOrDefault(lang)]

	out, err := r.llmClient.Chat(ctx, sys, strings.TrimSpace(text))
	if err != nil || strings.TrimSpace(out) == "" {
		return zero, false
	}

    raw := routerJSONRe.FindString(out) // JSON extraction regex used in the router
	if raw == "" {
		return zero, false
	}

	var xo medicalXO
	if err := json.Unmarshal([]byte(raw), &xo); err != nil {
		return zero, false
	}

	trim := func(s string) string { return strings.TrimSpace(s) }
	xo.Fields.Condition = trim(xo.Fields.Condition)
	xo.Fields.Topic = trim(xo.Fields.Topic)
	xo.Fields.Audience = trim(xo.Fields.Audience)
	xo.Fields.Duration = trim(xo.Fields.Duration)
	xo.Fields.Age = trim(xo.Fields.Age)
	xo.Fields.Medications = trim(xo.Fields.Medications)
	xo.Fields.Symptoms = trim(xo.Fields.Symptoms)

    // Adjust missing (ensure minimum requirements even if LLM leaves empty)
	if len(xo.Missing) == 0 {
		if xo.Fields.Condition == "" {
			xo.Missing = append(xo.Missing, "condition(질환)")
		}
		if xo.Fields.Symptoms == "" && xo.Fields.Topic == "" {
            // If neither symptoms nor topic, request symptoms
			xo.Missing = append(xo.Missing, "symptoms(개인 증상)")
		}
	}
	if xo.Ask == "" && len(xo.Missing) > 0 {
		xo.Ask = r.askForMissingMedicalWithLLM(ctx, lang, xo.Missing, text)
	}
	return xo, true
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

// pickItemAndModel extracts the closest object to verbs like "buy/order/pay/purchase",
// and splits the model part where numbers/versions/modifiers (Pro/Ultra/Plus/Max/SE/Ti/Super, etc.) appear.
// Excludes candidates that cannot be items, such as merchants or addresses.
func pickItemAndModel(text, merchant, shipping string) (item string, model string) {
	t := strings.TrimSpace(normalizeSpaces(text))

    // 0) Exclude address/merchant tokens
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

    // 1) Prefer quoted phrase: " … " / " … "
	if q := firstQuoted(t); q != "" && !isExcluded(q, exclude) {
		return splitItemModel(q)
	}

	// 1.5) Common product names (아이폰, 맥북, etc.)
	lowT := strings.ToLower(t)
	if strings.Contains(lowT, "아이폰") || strings.Contains(lowT, "iphone") {
		// Extract model if available (e.g., "16 pro", "15", etc.)
		if m := regexp.MustCompile(`(?:아이폰|iphone)\s+(\d+\s*(?:pro|max|mini|plus)?)`).FindStringSubmatch(lowT); len(m) >= 2 {
			return "아이폰", strings.TrimSpace(m[1])
		}
		return "아이폰", ""
	}
	if strings.Contains(lowT, "맥북") || strings.Contains(lowT, "macbook") {
		if m := regexp.MustCompile(`(?:맥북|macbook)\s+(\w+(?:\s+\w+)?)`).FindStringSubmatch(lowT); len(m) >= 2 {
			return "맥북", strings.TrimSpace(m[1])
		}
		return "맥북", ""
	}

    // 2) Accusative + purchase-verb pattern:  (NOUN{1,6}) (accusative particle) (buy|order|pay|purchase)
	// First, clean the text by removing amounts and recipients
	cleanText := regexp.MustCompile(`\d+[만억천백\d,\.]*\s*(?:원|krw|만원)(?:으로|로)?\s+`).ReplaceAllString(t, "")
	cleanText = regexp.MustCompile(`\s+(?:한테|에게)\s+[^\s]+\s+`).ReplaceAllString(cleanText, " ")
	cleanText = regexp.MustCompile(`\s+(?:카드|토스|카카오페이|현금|계좌)(?:로|으로)\s+`).ReplaceAllString(cleanText, " ")

	re := regexp.MustCompile(`([가-힣a-zA-Z0-9]+(?:\s+[가-힣a-zA-Z0-9]+){0,5}?)\s*(?:을|를)\s*(?:구매|주문|결제|사|사줘|사주세요|보내줘|보내|보내주세요)`)
	if m := re.FindStringSubmatch(cleanText); len(m) > 1 {
		cand := strings.TrimSpace(m[1])
		if cand != "" && !isExcluded(cand, exclude) {
			return splitItemModel(cand)
		}
	}

    // 3) Nearby noun phrase before purchase verbs
	re2 := regexp.MustCompile(`([^\s,]{1,80}(?:\s+[^\s,]{1,80}){0,5})\s*(?:로|으로|에서)?\s*(?:주문|구매|결제|사(?:요|자|줘|줄래|주세요)?)`)
	if m := re2.FindStringSubmatch(t); len(m) > 1 {
		cand := strings.TrimSpace(m[1])
        // Remove store phrase like "order on Coupang"
		cand = strings.TrimSuffix(cand, "에서")
		cand = strings.TrimSpace(cand)
		if cand != "" && !isExcluded(cand, exclude) {
			return splitItemModel(cand)
		}
	}

    // 4) Last resort: long noun phrase candidates (comma-separated; exclude merchant/address; prefer digits/caps mix)
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

    // (a) Treat the first occurrence of a digit/hyphen/model cue as model start
	idx := -1
	for i, tk := range toks {
		if hasDigitOrHyphen.MatchString(tk) || modelCue.MatchString(tk) {
			idx = i
			break
		}
	}
    if idx <= 0 {
        // If no model cue, treat the whole phrase as item
        return phrase, ""
    }
	item = strings.Join(toks[:idx], " ")
	model = strings.Join(toks[idx:], " ")
	return strings.TrimSpace(item), strings.TrimSpace(model)
}

func likelyProductPhrase(seg string) bool {
    // Very weak hint of product-likeness: has digits/uppercase/hyphen or a 2–6 word noun phrase
	if hasDigitOrHyphen.MatchString(seg) {
		return true
	}
	words := len(strings.Fields(seg))
	return words >= 2 && words <= 6
}

// Even if LLM adds pre/post text, keep only from the first '{' to the last '}'
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

// Examples: 150 x 10k KRW (1.5M), 1.5 x 10k KRW, 150,000 KRW → integer KRW
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
    // Support decimals (e.g., 1.5 x 10k KRW)
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
            // Normalize
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

    // Extract patterns like "Shipping address: <city> ..." / "Shipping address is <city> ..." / "Recipient <name>"
func inferAfterKeyword(s, keywordRegex string) string {
	re := regexp.MustCompile(`(?i)(` + keywordRegex + `)\s*[:=\s]\s*([^,，\n]+)`)
	if mm := re.FindStringSubmatch(s); len(mm) >= 3 {
		return strings.TrimSpace(mm[2])
	}
	return ""
}

func extractFirstJSONObject(s string) string {
	s = strings.TrimSpace(s)
    // Extract from the first '{' to the last '}'
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

    // 1.5 million won / 230 million won
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
    // 1,500,000 won
	reWon := regexp.MustCompile(`\b(\d+)\s*원`)
	if m := reWon.FindStringSubmatch(t); len(m) == 2 {
		if n, _ := strconv.ParseInt(m[1], 10, 64); n > 0 {
			return n
		}
	}
    // Last fallback: big integer
	reBig := regexp.MustCompile(`\b(\d{6,})\b`)
	if m := reBig.FindStringSubmatch(t); len(m) == 2 {
		if n, _ := strconv.ParseInt(m[1], 10, 64); n > 0 {
			return n
		}
	}
	return 0
}

func pickMethod(t string) string {
// Only representative keywords, briefly
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
	// Generic "card" keyword
	if strings.Contains(t, "카드") || strings.Contains(strings.ToLower(t), "card") {
		return "card"
	}
	return ""
}

func pickAddress(t string) string {
// Light address hints
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
// Patterns like "deliver to ~", "recipient is XXX"
	if i := strings.Index(t, "수령자"); i >= 0 {
		seg := strings.TrimSpace(t[i:])
		seg = strings.TrimPrefix(seg, "수령자")
		seg = strings.Trim(seg, " 는은이가: ")
		if len(strings.Fields(seg)) > 0 {
			seg = strings.Fields(seg)[0]
			return seg
		}
	}
	if i := strings.Index(t, "에게 배송"); i > 0 {
		pre := strings.TrimSpace(t[:i])
		parts := strings.Fields(pre)
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	// Pattern: "XXX한테" or "XXX에게"
	re := regexp.MustCompile(`([가-힣a-zA-Z0-9]+)\s*(?:한테|에게)`)
	if m := re.FindStringSubmatch(t); len(m) >= 2 {
		return strings.TrimSpace(m[1])
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
