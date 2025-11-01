package root

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sage-x-project/sage-multi-agent/types"
)

var payContextStore struct {
	mu sync.Mutex
	m  map[string]*payCtx
}

// payCtx에 Stage/Token 추가
type payCtx struct {
	Slots     paySlots
	Stage     string // "collect" | "await_confirm"
	Token     string
	UpdatedAt time.Time
}

func init() { payContextStore.m = make(map[string]*payCtx) }

// 기존 get/put/del 그대로 두되 Stage/Token 유지
func getPayCtx(id string) paySlots {
	payContextStore.mu.Lock()
	defer payContextStore.mu.Unlock()
	if c, ok := payContextStore.m[id]; ok {
		return c.Slots
	}
	return paySlots{}
}

func putPayCtx(id string, s paySlots) {
	payContextStore.mu.Lock()
	defer payContextStore.mu.Unlock()
	now := time.Now()
	if c, ok := payContextStore.m[id]; ok {
		c.Slots = s
		c.UpdatedAt = now
	} else {
		payContextStore.m[id] = &payCtx{Slots: s, UpdatedAt: now}
	}
}

func putPayCtxFull(id string, s paySlots, stage, token string) {
	payContextStore.mu.Lock()
	defer payContextStore.mu.Unlock()
	now := time.Now()
	if c, ok := payContextStore.m[id]; ok {
		c.Slots = s
		c.Stage = stage
		c.Token = token
		c.UpdatedAt = now
	} else {
		payContextStore.m[id] = &payCtx{Slots: s, Stage: stage, Token: token, UpdatedAt: now}
	}
}

func getStageToken(id string) (stage, token string) {
	payContextStore.mu.Lock()
	defer payContextStore.mu.Unlock()
	if c, ok := payContextStore.m[id]; ok {
		return c.Stage, c.Token
	}
	return "", ""
}

func delPayCtx(id string) {
	payContextStore.mu.Lock()
	defer payContextStore.mu.Unlock()
	delete(payContextStore.m, id)
}

// 현재 결제 슬롯이 비어있는지 여부
func payCtxNotEmpty(s paySlots) bool {
	return strings.TrimSpace(s.Method) != "" ||
		strings.TrimSpace(s.Shipping) != "" ||
		strings.TrimSpace(s.Merchant) != "" ||
		strings.TrimSpace(s.Item) != "" ||
		strings.TrimSpace(s.Model) != "" ||
		strings.TrimSpace(s.To) != "" ||
		strings.TrimSpace(s.Recipient) != "" ||
		s.AmountKRW > 0 || s.BudgetKRW > 0
}

// stage 이름만 추출 (getStageToken이 (stage, token) 반환하므로 보조로 둠)
func getStageName(id string) string {
	stage, _ := getStageToken(id)
	return stage
}

// sticky payment: (1) 슬롯이 일부라도 채워졌거나 (2) stage가 collect/await_confirm 이면
// 사용자가 "의료/플래닝"을 강하게 말하지 않는 한 payment로 고정
func shouldForcePayment(cid, userText string) bool {
	s := getPayCtx(cid)
	stage, _ := getStageToken(cid)

	if payCtxNotEmpty(s) || stage == "collect" || stage == "await_confirm" {
		low := strings.ToLower(strings.TrimSpace(userText))
		// 아래 두 함수는 이미 프로젝트에 있을 가능성이 큼.
		// 없다면 간단한 휴리스틱으로 만들어도 됨.
		if !isMedicalActionIntent(low) && !isPlanningActionIntent(low) {
			return true
		}
	}
	return false
}

// ==== Medical minimal context (per conversation) ====
type medCtx struct {
	Slots      medicalSlots
	Symptoms   string   // 자유 텍스트 증상
	Await      string   // "", "symptoms", "condition"
	Transcript []string // 유저 원문 히스토리(턴별 Content)
	FirstQ     string   // 첫 질문 원문(선택)
}

var medStore sync.Map

func getMedCtx(cid string) medCtx {
	if v, ok := medStore.Load(cid); ok {
		if s, ok2 := v.(medCtx); ok2 {
			return s
		}
	}
	return medCtx{}
}
func putMedCtx(cid string, s medCtx) { medStore.Store(cid, s) }
func delMedCtx(cid string)           { medStore.Delete(cid) }

func mergeMedCtx(a, b medCtx) medCtx {
	ts := func(s string) string { return strings.TrimSpace(s) }

	// slots
	if v := ts(b.Slots.Condition); v != "" {
		a.Slots.Condition = v
	}
	if v := ts(b.Slots.Topic); v != "" {
		a.Slots.Topic = v
	}
	if v := ts(b.Slots.Audience); v != "" {
		a.Slots.Audience = v
	}
	if v := ts(b.Slots.Duration); v != "" {
		a.Slots.Duration = v
	}
	if v := ts(b.Slots.Age); v != "" {
		a.Slots.Age = v
	}
	if v := ts(b.Slots.Medications); v != "" {
		a.Slots.Medications = v
	}

	if v := ts(b.Slots.Symptoms); v != "" {
		a.Symptoms = v
	}
	if v := ts(b.Symptoms); v != "" {
		a.Symptoms = v
	}

	// transcript/await/firstQ는 호출부에서 관리
	return a
}
func hasMedCtx(cid string) bool {
	_, ok := medStore.Load(cid)
	return ok
}

func extractMedicalCore(msg *types.AgentMessage) medCtx {
	var s medCtx

	// 1) metadata 우선
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["medical.condition"].(string); ok && strings.TrimSpace(v) != "" {
			s.Slots.Condition = strings.TrimSpace(v)
		} else if v, ok := msg.Metadata["condition"].(string); ok && strings.TrimSpace(v) != "" {
			s.Slots.Condition = strings.TrimSpace(v)
		}
		if v, ok := msg.Metadata["medical.symptoms"].(string); ok && strings.TrimSpace(v) != "" {
			s.Symptoms = strings.TrimSpace(v)
		} else if v, ok := msg.Metadata["symptoms"].(string); ok && strings.TrimSpace(v) != "" {
			s.Symptoms = strings.TrimSpace(v)
		}
	}

	// 2) JSON 본문 폴백
	c := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(c, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(c), &m) == nil {
			if v, ok := m["medical.condition"].(string); ok && strings.TrimSpace(v) != "" {
				s.Slots.Condition = strings.TrimSpace(v)
			}
			if v, ok := m["condition"].(string); ok && strings.TrimSpace(v) != "" {
				s.Slots.Condition = strings.TrimSpace(v)
			}
			if v, ok := m["medical.symptoms"].(string); ok && strings.TrimSpace(v) != "" {
				s.Symptoms = strings.TrimSpace(v)
			}
			if v, ok := m["symptoms"].(string); ok && strings.TrimSpace(v) != "" {
				s.Symptoms = strings.TrimSpace(v)
			}
		}
	}

	// 3) condition 힌트 — 없을 때만
	if s.Slots.Condition == "" {
		low := strings.ToLower(c)
		switch {
		case containsAny(low, "당뇨", "혈당", "diabetes"):
			s.Slots.Condition = "당뇨병"
		case containsAny(low, "고혈압", "hypertension"):
			s.Slots.Condition = "고혈압"
		case containsAny(low, "우울", "depress"):
			s.Slots.Condition = "우울증"
		case containsAny(low, "불안", "anxiety"):
			s.Slots.Condition = "불안장애"
		case containsAny(low, "콜레스테롤", "고지혈", "cholesterol"):
			s.Slots.Condition = "고지혈증"
		}
	}
	return s
}

func medicalMissing(s medCtx) (missing []string) {
	if strings.TrimSpace(s.Slots.Condition) == "" {
		missing = append(missing, "condition(질환)")
	}
	if strings.TrimSpace(s.Symptoms) == "" {
		missing = append(missing, "symptoms(개인 증상)")
	}
	return
}

func isInfoTopic(t string) bool {
	t = strings.TrimSpace(t)
	return containsAny(t, "관리", "식단", "운동", "약물", "복용", "검사", "치료", "예방", "일반", "정보", "가이드", "방법")
}

// ---- 증상 유도 질문 (ONE sentence) ----
// 예: askForSymptomsLLM 실패 시 폴백
func (r *RootAgent) askForSymptomsLLM(ctx context.Context, lang, condition, userText string) string {
	r.ensureLLM()
	if r.llmClient != nil {
		sys := map[string]string{
			"ko": "너는 의료 정보 수집 도우미야. 사용자의 개인 증상을 '한 문장'으로 정중히 물어봐. 리스트/코드/예시 금지.",
			"en": "You are a medical intake assistant. Ask for user's personal symptoms in ONE polite sentence.",
		}[langOrDefault(lang)]
		usr := fmt.Sprintf("Condition=%s\nUserText=%s\nOutput: ONE-sentence ask in %s",
			strings.TrimSpace(condition), strings.TrimSpace(userText), langOrDefault(lang))
		if out, err := r.llmClient.Chat(ctx, sys, usr); err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		} else {
			log.Println("[llm][err]", err)
		}

	}
	// ---- 폴백(상황형) ----
	if langOrDefault(lang) == "ko" {
		if strings.TrimSpace(condition) != "" {
			return fmt.Sprintf("%s 관련해서 지금 느끼는 주요 증상·지속 기간·복용 중인 약을 한 문장으로 알려주세요.", condition)
		}
		return "지금 느끼는 주요 증상과 언제부터인지, 복용 중인 약이 있으면 포함해 한 문장으로 알려주세요."
	}
	if strings.TrimSpace(condition) != "" {
		return fmt.Sprintf("For %s, please describe your main symptoms, how long they've lasted, and any meds in one sentence.", condition)
	}
	return "Please describe your main symptoms, since when, and any medications in one sentence."
}

// ---- (둘 다 비었을 때) 질병+증상 함께 요청 ----
func (r *RootAgent) askForCondAndSymptomsLLM(ctx context.Context, lang, userText string) string {
	r.ensureLLM()
	if r.llmClient == nil {
		if langOrDefault(lang) == "ko" {
			return "어떤 질병에 대한 상담인지와, 현재 겪는 개인 증상을 한 번에 한 문장으로 알려주세요."
		}
		return "Please tell me which condition this is about and your personal symptoms in one short sentence."
	}
	sys := map[string]string{
		"ko": "너는 의료 정보 수집 도우미야. '질병명과 개인 증상'을 한 번에 한 문장으로 요청해. 예시/리스트/코드 금지.",
		"en": "You collect medical info. Ask for 'condition + personal symptoms' together in ONE sentence. No examples/lists/code.",
	}[langOrDefault(lang)]
	usr := fmt.Sprintf("UserText=%s\nOutput: ONE-sentence ask in %s", compact(userText, 160), langOrDefault(lang))
	out, err := r.llmClient.Chat(ctx, sys, usr)
	if err != nil || strings.TrimSpace(out) == "" {
		if langOrDefault(lang) == "ko" {
			return "상담할 질병명과 개인 증상을 한 번에 알려주세요."
		}
		return "Please share the condition and your personal symptoms together in one short sentence."
	}
	o := strings.TrimSpace(out)
	o = strings.Trim(o, "`\"'")
	if i := strings.IndexAny(o, "\r\n"); i >= 0 {
		o = strings.TrimSpace(o[:i])
	}
	return o
}

var amountRe = regexp.MustCompile(`(?i)(\d[\d,\.]*)\s*(원|krw|만원|usd|usdc|eth|btc)`)

func isPaymentActionIntent(c string) bool {
	// 질문투면 라우팅 보류(강한 지시어 있으면 허용)
	if isQuestionLike(c) && !isOrderish(c) && !containsAny(c, "보내", "송금", "이체", "지불해", "pay", "send", "transfer") {
		return false
	}
	if isOrderish(c) || containsAny(c, "보내", "송금", "이체", "결제해", "지불해", "pay", "send", "transfer") {
		return true
	}
	// 슬롯 힌트 2개 이상이면 결제 의도
	hits := 0
	if amountRe.FindStringIndex(c) != nil {
		hits++
	}
	if hasMethodCue(c) {
		hits++
	}
	if hasRecipientCue(c) {
		hits++
	}
	return hits >= 2
}

func isMedicalActionIntent(c string) bool {
	c = strings.ToLower(strings.TrimSpace(c))

	// 대표 질환/영역
	if containsAny(c,
		"당뇨", "혈당", "고혈당", "저혈당", "당화혈색소", "insulin", "metformin",
		"정신", "우울", "불안", "조현", "bipolar", "adhd", "치매", "수면",
		"우울증", "공황", "강박", "ptsd",
		"hypertension", "고혈압", "고지혈", "cholesterol",
	) {
		return true
	}

	// 의료정보 톤
	if containsAny(c,
		"증상", "원인", "치료", "약", "복용", "부작용", "관리", "생활습관",
		"가이드라인", "권고안", "주의사항", "금기", "진단", "검사",
		"symptom", "treatment", "side effect", "guideline", "diagnosis",
	) && containsAny(c, "알려줘", "설명", "정보", "방법", "how", "what", "guide") {
		return true
	}
	if containsAny(c, "증상", "지속", "어지럽", "두통", "통증", "메스꺼움", "구토", "발열", "기침", "호흡곤란", "피곤",
		"dizzy", "headache", "pain", "nausea", "vomit", "fever", "cough", "shortness of breath", "fatigue") {
		return true
	}
	// "~~ 먹어도 돼?" 같은 질문
	if containsAny(c, "먹어도 돼", "괜찮아", "해도 돼", "해도돼", "임신", "모유", "술", "운동") &&
		containsAny(c, "약", "복용", "병", "질환", "증상") {
		return true
	}

	return false
}

func isPlanningActionIntent(c string) bool {
	if containsAny(c, "계획해", "플랜 짜줘", "일정 짜줘", "plan", "schedule", "스케줄 만들어", "할일 정리") {
		return true
	}
	// '계획/일정' 키워드가 있고 질문투가 아니면 라우팅
	return containsAny(c, "계획", "일정", "플랜", "todo") && !isQuestionLike(c)
}
