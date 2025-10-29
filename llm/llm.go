// Package llm provides a tiny, pluggable chat-completion client with bilingual (ko/en) support.
// Env:
//
//	JAMINAI_API_URL, JAMINAI_API_KEY, JAMINAI_MODEL
//
// Public helpers:
//   - DetectLang(text) -> "ko" or "en"
//   - BuildPaymentAskPromptWithLang(lang, missing, original)
//   - BuildPaymentReceiptPromptWithLang(lang, to, amountKRW, method, item, memo)
//
// Backward-compat wrappers (still available):
//   - BuildPaymentAskPrompt(missing, original)           // auto language detection
//   - BuildPaymentReceiptPrompt(to, amountKRW, ...)      // defaults to ko
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

var ErrLLMDisabled = errors.New("llm client disabled (missing env)")

// Client is a minimal chat interface.
type Client interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

// JaminAIClient implements Client using an OpenAI-compatible chat endpoint.
type JaminAIClient struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewFromEnv creates a client if envs are present; otherwise returns ErrLLMDisabled.
func NewFromEnv() (Client, error) {
	base := strings.TrimSpace(os.Getenv("JAMINAI_API_URL"))
	key := strings.TrimSpace(os.Getenv("JAMINAI_API_KEY"))
	model := strings.TrimSpace(os.Getenv("JAMINAI_MODEL"))
	if base == "" || key == "" {
		return nil, ErrLLMDisabled
	}
	if model == "" {
		model = "jaminai-chat"
	}
	return &JaminAIClient{
		BaseURL: strings.TrimRight(base, "/"),
		APIKey:  key,
		Model:   model,
		HTTP: &http.Client{
			Timeout: 12 * time.Second,
		},
	}, nil
}

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Choices []chatChoice   `json:"choices"`
	Error   *providerError `json:"error,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	FinishReason string      `json:"finish_reason"`
	Message      chatMessage `json:"message"`
}

type providerError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func (c *JaminAIClient) Chat(ctx context.Context, system, user string) (string, error) {
	reqBody := chatReq{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens:   320,
		Temperature: 0.7,
	}
	b, _ := json.Marshal(reqBody)
	endpoint := c.BaseURL + "/v1/chat/completions"

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var out chatResp
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", errors.New(strings.TrimSpace(out.Error.Message))
	}
	if len(out.Choices) == 0 {
		return "", errors.New("llm: empty choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// DetectLang returns "ko" if the text contains any Hangul, otherwise "en".
func DetectLang(s string) string {
	for _, r := range s {
		if unicode.Is(unicode.Hangul, r) {
			return "ko"
		}
	}
	return "en"
}

// BuildPaymentAskPromptWithLang builds a one-line follow-up question in ko/en.
func BuildPaymentAskPromptWithLang(lang string, missing []string, original string) (system, user string) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang != "ko" && lang != "en" {
		lang = DetectLang(original)
	}
	if lang == "en" {
		system = "You are a helpful assistant. Ask a brief, natural follow-up question to collect the missing payment details. Prefer one sentence; a short second clause with example options is okay. Keep it conversational. Reply in English only."
		user = fmt.Sprintf(
			"User input: %s\nMissing fields to collect: %s\nHint: You may include short options in parentheses (e.g., recipient/card/bank/wallet), but keep it concise.",
			strings.TrimSpace(original), strings.Join(missing, ", "),
		)
		return
	}
	// ko
	system = "당신은 친절한 비서입니다. 부족한 결제 정보를 자연스럽게 묻는 짧은 추가 질문을 하세요. 한 문장을 권장하지만, 필요한 경우 짧은 예시(괄호)를 덧붙여도 됩니다. 한국어로만 답하세요."
	user = fmt.Sprintf(
		"사용자 입력: %s\n수집해야 할 필드: %s\n힌트: 괄호로 간단한 선택지(예: 지갑주소/카드/계좌)를 덧붙여도 되지만, 전반적으로 간결하게 유지하세요.",
		strings.TrimSpace(original), strings.Join(missing, ", "),
	)
	return
}

// BuildPaymentReceiptPromptWithLang builds a natural 'payment completed' line in ko/en.
func BuildPaymentReceiptPromptWithLang(lang, to string, amountKRW int64, method, item, memo string) (system, user string) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang != "ko" && lang != "en" {
		lang = "ko"
	}
	if lang == "en" {
		system = "You are a payment agent. Produce a short, natural confirmation that sounds like a completed purchase. Prefer one line; emoji is optional. Include KRW amount (with thousand separators), recipient and method when available, and a short fake order ID (e.g., ORD-5F3A). No code blocks, no long explanations."
		user = fmt.Sprintf(
			"Recipient: %s\nAmount (KRW): %d\nMethod: %s\nItem: %s\nMemo: %s\nNote: Keep it friendly and concise; the exact phrasing is flexible.",
			strings.TrimSpace(to), amountKRW, strings.TrimSpace(method), strings.TrimSpace(item), strings.TrimSpace(memo),
		)
		return
	}
	// ko
	system = "당신은 결제 에이전트입니다. 결제가 완료된 듯 자연스러운 짧은 확인 문장을 출력하세요. 한 줄을 권장하되, 이모지는 선택 사항입니다. 금액은 천단위 콤마, 수신자/결제수단이 있으면 포함하고, 간단한 가짜 주문번호(예: ORD-5F3A)도 넣어주세요. 코드블록/장문 설명은 금지."
	user = fmt.Sprintf(
		"수신자: %s\n금액(KRW): %d\n결제수단: %s\n상품: %s\n메모: %s\n메모: 말투와 표현은 유연하게, 단 짧고 자연스럽게.",
		strings.TrimSpace(to), amountKRW, strings.TrimSpace(method), strings.TrimSpace(item), strings.TrimSpace(memo),
	)
	return
}

// Backward-compat wrappers
func BuildPaymentAskPrompt(missing []string, original string) (string, string) {
	return BuildPaymentAskPromptWithLang(DetectLang(original), missing, original)
}

func BuildPaymentReceiptPrompt(to string, amountKRW int64, method string, item string, memo string) (string, string) {
	return BuildPaymentReceiptPromptWithLang("ko", to, amountKRW, method, item, memo)
}
