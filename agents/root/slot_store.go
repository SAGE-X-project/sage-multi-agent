package root

import (
	"net/http"
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

// 대화(세션) 식별자 결정: 헤더 > 메타데이터 > From > 시나리오 > default
func getConvID(req *http.Request, msg *types.AgentMessage) string {
	// 1) 헤더 우선
	if v := strings.TrimSpace(req.Header.Get("X-Conv-ID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(req.Header.Get("X-Session-ID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(req.Header.Get("X-Client-Session")); v != "" {
		return v
	}

	// 2) 메시지 메타/필드
	if msg != nil {
		if msg.Metadata != nil {
			if v, ok := msg.Metadata["conversationId"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
			if v, ok := msg.Metadata["sessionId"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
			if v, ok := msg.Metadata["cid"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		if strings.TrimSpace(msg.From) != "" {
			return "from:" + strings.TrimSpace(msg.From)
		}
	}

	// 3) 시나리오(최후보)
	if v := strings.TrimSpace(req.Header.Get("X-Scenario")); v != "" {
		return "scenario:" + v
	}
	return "default"
}

func delPayCtx(id string) {
	payContextStore.mu.Lock()
	defer payContextStore.mu.Unlock()
	delete(payContextStore.m, id)
}

// paySlots는 기존 타입을 그대로 사용 (To, AmountKRW, Method, Item, Shipping, ...)

func mergePaymentSlots(old, now paySlots) paySlots {
	out := old
	if strings.TrimSpace(now.To) != "" {
		out.To = strings.TrimSpace(now.To)
	}
	if now.AmountKRW != 0 {
		out.AmountKRW = now.AmountKRW
	}
	if strings.TrimSpace(now.Method) != "" {
		out.Method = strings.TrimSpace(now.Method)
	}
	if strings.TrimSpace(now.Item) != "" {
		out.Item = strings.TrimSpace(now.Item)
	}
	if strings.TrimSpace(now.Shipping) != "" {
		out.Shipping = strings.TrimSpace(now.Shipping)
	}
	if strings.TrimSpace(now.CardLast4) != "" {
		out.CardLast4 = strings.TrimSpace(now.CardLast4)
	}
	if strings.TrimSpace(now.Merchant) != "" {
		out.Merchant = strings.TrimSpace(now.Merchant)
	}
	if strings.TrimSpace(now.Model) != "" {
		out.Model = strings.TrimSpace(now.Model)
	}
	if strings.TrimSpace(now.Note) != "" {
		out.Note = strings.TrimSpace(now.Note)
	}
	return out
}
