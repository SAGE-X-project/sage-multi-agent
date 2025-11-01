package root

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

// ==== Medical minimal context (per conversation) ====
// ==== Medical context (per conversation) ====
type medCtx struct {
	Condition   string // 질병명 (ex. 당뇨병)
	Symptoms    string // 개인 증상 (자유 텍스트)
	Topic       string // "증상", "검사/진단", "약물/복용", "부작용", "식단", "운동", "관리" 등
	Audience    string // "본인", "가족", "임산부", "아동", "노인"
	Duration    string // "2주", "어제부터" 등
	Age         string // 선택
	Medications string // 선택
	Await       string // "", "symptoms", "condition" (다음 턴 힌트)
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
	// b의 비어있지 않은 값만 a에 덮어쓰기
	if strings.TrimSpace(b.Condition) != "" {
		a.Condition = strings.TrimSpace(b.Condition)
	}
	if strings.TrimSpace(b.Symptoms) != "" {
		a.Symptoms = strings.TrimSpace(b.Symptoms)
	}
	if strings.TrimSpace(b.Topic) != "" {
		a.Topic = strings.TrimSpace(b.Topic)
	}
	if strings.TrimSpace(b.Audience) != "" {
		a.Audience = strings.TrimSpace(b.Audience)
	}
	if strings.TrimSpace(b.Duration) != "" {
		a.Duration = strings.TrimSpace(b.Duration)
	}
	if strings.TrimSpace(b.Age) != "" {
		a.Age = strings.TrimSpace(b.Age)
	}
	if strings.TrimSpace(b.Medications) != "" {
		a.Medications = strings.TrimSpace(b.Medications)
	}
	if strings.TrimSpace(b.Await) != "" {
		a.Await = strings.TrimSpace(b.Await)
	}
	return a
}

// ---- minimal extractor: condition + symptoms (필요시 확장) ----
func extractMedicalCore(msg *types.AgentMessage) medCtx {
	var s medCtx

	// 1) metadata 우선
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["medical.condition"].(string); ok && strings.TrimSpace(v) != "" {
			s.Condition = strings.TrimSpace(v)
		} else if v, ok := msg.Metadata["condition"].(string); ok && strings.TrimSpace(v) != "" {
			s.Condition = strings.TrimSpace(v)
		}
		if v, ok := msg.Metadata["medical.symptoms"].(string); ok && strings.TrimSpace(v) != "" {
			s.Symptoms = strings.TrimSpace(v)
		} else if v, ok := msg.Metadata["symptoms"].(string); ok && strings.TrimSpace(v) != "" {
			s.Symptoms = strings.TrimSpace(v)
		}

		// (선택) 추가 필드도 메타데이터에서 받으면 채워줌
		if v, ok := msg.Metadata["medical.topic"].(string); ok {
			s.Topic = strings.TrimSpace(v)
		}
		if v, ok := msg.Metadata["medical.audience"].(string); ok {
			s.Audience = strings.TrimSpace(v)
		}
		if v, ok := msg.Metadata["medical.duration"].(string); ok {
			s.Duration = strings.TrimSpace(v)
		}
		if v, ok := msg.Metadata["medical.age"].(string); ok {
			s.Age = strings.TrimSpace(v)
		}
		if v, ok := msg.Metadata["medical.meds"].(string); ok {
			s.Medications = strings.TrimSpace(v)
		}
	}

	// 2) JSON 본문 폴백 (top-level 또는 fields{} 내 키 지원)
	c := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(c, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(c), &m) == nil {
			getStr := func(mm map[string]any, keys ...string) string {
				for _, k := range keys {
					if v, ok := mm[k].(string); ok && strings.TrimSpace(v) != "" {
						return strings.TrimSpace(v)
					}
				}
				return ""
			}
			// top-level
			if v := getStr(m, "medical.condition", "condition"); v != "" {
				s.Condition = v
			}
			if v := getStr(m, "medical.symptoms", "symptoms"); v != "" {
				s.Symptoms = v
			}
			if v := getStr(m, "medical.topic", "topic"); v != "" {
				s.Topic = v
			}
			if v := getStr(m, "medical.audience", "audience"); v != "" {
				s.Audience = v
			}
			if v := getStr(m, "medical.duration", "duration"); v != "" {
				s.Duration = v
			}
			if v := getStr(m, "medical.age", "age"); v != "" {
				s.Age = v
			}
			if v := getStr(m, "medical.medications", "medications", "meds"); v != "" {
				s.Medications = v
			}

			// fields{}
			if f, ok := m["fields"].(map[string]any); ok {
				if v := getStr(f, "condition"); v != "" {
					s.Condition = v
				}
				if v := getStr(f, "symptoms"); v != "" {
					s.Symptoms = v
				}
				if v := getStr(f, "topic"); v != "" {
					s.Topic = v
				}
				if v := getStr(f, "audience"); v != "" {
					s.Audience = v
				}
				if v := getStr(f, "duration"); v != "" {
					s.Duration = v
				}
				if v := getStr(f, "age"); v != "" {
					s.Age = v
				}
				if v := getStr(f, "medications"); v != "" {
					s.Medications = v
				}
			}
		}
	}

	// 3) condition 힌트(키워드) — 없을 때만
	if s.Condition == "" {
		low := strings.ToLower(c)
		switch {
		case containsAny(low, "당뇨", "혈당", "diabetes"):
			s.Condition = "당뇨병"
		case containsAny(low, "고혈압", "hypertension"):
			s.Condition = "고혈압"
		case containsAny(low, "우울", "depress"):
			s.Condition = "우울증"
		case containsAny(low, "불안", "anxiety"):
			s.Condition = "불안장애"
		case containsAny(low, "콜레스테롤", "고지혈", "cholesterol"):
			s.Condition = "고지혈증"
		}
	}
	return s
}

func medicalMissing(s medCtx) (missing []string) {
	if strings.TrimSpace(s.Condition) == "" {
		missing = append(missing, "condition(질병)")
	}
	if strings.TrimSpace(s.Symptoms) == "" {
		missing = append(missing, "symptoms(개인 증상)")
	}
	return
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
