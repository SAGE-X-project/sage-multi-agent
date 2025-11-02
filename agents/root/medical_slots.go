// Package root - medical query slot extraction & helpers.
// Split-out version: delete the medical section in root.go before adding this.
package root

import (
	"encoding/json"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// Unified medical slots (single source of truth).
type medicalSlots struct {
    Condition   string `json:"condition"`          // e.g., diabetes
    Topic       string `json:"topic"`              // e.g., symptoms/management/diet/exercise/tests/treatment/prevention/medication/side effects/general info
    Audience    string `json:"audience,omitempty"` // e.g., self/family/pregnant/child/elderly
    Duration    string `json:"duration,omitempty"` // e.g., 2 weeks, since yesterday
    Age         string `json:"age,omitempty"`
    Medications string `json:"medications,omitempty"` // current medications
    // For LLM extraction only (kept separately in context): the Symptoms below are mirrored into medCtx.Symptoms.
    Symptoms string `json:"symptoms,omitempty"`
}

// Metadata first + light body hints + JSON fallback.
// If either 'condition' or 'topic' is missing, include in the missing list.
func extractMedicalSlots(msg *types.AgentMessage) (s medicalSlots, missing []string) {
	getS := func(keys ...string) string {
		if msg.Metadata == nil {
			return ""
		}
		for _, k := range keys {
			if v, ok := msg.Metadata[k]; ok {
				if str, ok2 := v.(string); ok2 && strings.TrimSpace(str) != "" {
					return strings.TrimSpace(str)
				}
			}
		}
		return ""
	}
	s.Condition = getS("medical.condition", "condition", "질환", "병")
	s.Topic = getS("medical.topic", "topic", "주제")
	s.Audience = getS("medical.audience", "audience", "대상")
	s.Duration = getS("medical.duration", "duration", "기간")
	s.Age = getS("medical.age", "age", "나이")
	s.Medications = getS("medical.meds", "meds", "복용약", "medications")

    // JSON body fallback
	if strings.HasPrefix(strings.TrimSpace(msg.Content), "{") {
		var m map[string]any
		if json.Unmarshal([]byte(msg.Content), &m) == nil {
			for k, ptr := range map[string]*string{
				"medical.condition": &s.Condition, "condition": &s.Condition,
				"medical.topic": &s.Topic, "topic": &s.Topic,
				"medical.audience": &s.Audience, "audience": &s.Audience,
				"medical.duration": &s.Duration, "duration": &s.Duration,
				"medical.age": &s.Age, "age": &s.Age,
				"medical.meds": &s.Medications, "meds": &s.Medications, "medications": &s.Medications,
			} {
				if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
					*ptr = strings.TrimSpace(v)
				}
			}
		}
	}

    // Light keyword hints
	low := strings.ToLower(strings.TrimSpace(msg.Content))
	if s.Condition == "" {
		switch {
		case containsAny(low, "당뇨", "혈당", "diabetes"):
			s.Condition = "당뇨병"
		case containsAny(low, "우울", "depress"):
			s.Condition = "우울증"
		case containsAny(low, "불안", "anxiety"):
			s.Condition = "불안장애"
		case containsAny(low, "고혈압", "hypertension"):
			s.Condition = "고혈압"
		case containsAny(low, "콜레스테롤", "고지혈", "cholesterol"):
			s.Condition = "고지혈증"
		}
	}
	if s.Topic == "" {
		switch {
		case containsAny(low, "증상", "symptom"):
			s.Topic = "증상"
		case containsAny(low, "검사", "diagnos", "test"):
			s.Topic = "검사/진단"
		case containsAny(low, "약", "복용", "약물", "med"):
			s.Topic = "약물/복용"
		case containsAny(low, "부작용", "side effect"):
			s.Topic = "부작용"
		case containsAny(low, "식단", "diet", "영양"):
			s.Topic = "식단"
		case containsAny(low, "운동", "exercise"):
			s.Topic = "운동"
		case containsAny(low, "관리", "관리법", "관리방법", "management"):
			s.Topic = "관리"
		}
	}

    // Minimum requirements
    if s.Condition == "" {
        missing = append(missing, "condition(질환)")
    }
	if s.Topic == "" {
		missing = append(missing, "topic(주제)")
	}
	return
}

func fillMsgMetaFromMedical(msg *types.AgentMessage, s medicalSlots, lang string) {
	if msg.Metadata == nil {
		msg.Metadata = map[string]any{}
	}
	if s.Condition != "" {
		msg.Metadata["medical.condition"] = s.Condition
	}
	if s.Topic != "" {
		msg.Metadata["medical.topic"] = s.Topic
	}
	if s.Audience != "" {
		msg.Metadata["medical.audience"] = s.Audience
	}
	if s.Duration != "" {
		msg.Metadata["medical.duration"] = s.Duration
	}
	if s.Age != "" {
		msg.Metadata["medical.age"] = s.Age
	}
	if s.Medications != "" {
		msg.Metadata["medical.meds"] = s.Medications
	}
	msg.Metadata["lang"] = lang
}
