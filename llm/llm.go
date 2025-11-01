// Package llm provides a small, pluggable OpenAI-compatible chat client
// with sane env defaults and local (no-key) allowance.
// Only LLM-related code lives here. Non-LLM parts are not touched.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

var ErrLLMDisabled = errors.New("llm client disabled (missing key or base url)")

// Client is the minimal interface used by RootAgent/PaymentAgent.
type Client interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

// JaminAIClient is an OpenAI-compatible HTTP client.
type JaminAIClient struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
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
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Choices []chatChoice `json:"choices"`
	Error   *struct {
		Message string      `json:"message"`
		Type    string      `json:"type,omitempty"`
		Code    interface{} `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	FinishReason string      `json:"finish_reason"`
	Message      chatMessage `json:"message"`
}

// NewFromEnv creates a client using relaxed env precedence.
// Base URL precedence: JAMINAI_API_URL > LLM_BASE_URL > LLM_URL > default Google OpenAI-compatible.
// Key precedence: JAMINAI_API_KEY > LLM_API_KEY > GEMINI_API_KEY > GOOGLE_API_KEY > OPENAI_API_KEY.
// Local hosts (localhost/127.0.0.1) allow empty key or LLM_ALLOW_NO_KEY=true.
func NewFromEnv() (Client, error) {
	base := firstNonEmpty(
		os.Getenv("JAMINAI_API_URL"),
		os.Getenv("LLM_BASE_URL"),
		os.Getenv("LLM_URL"),
	)
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta/openai"
	}
	base = normalizeBase(base)

	key := firstNonEmpty(
		os.Getenv("JAMINAI_API_KEY"),
		os.Getenv("LLM_API_KEY"),
		os.Getenv("GEMINI_API_KEY"),
		os.Getenv("GOOGLE_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
	)

	model := firstNonEmpty(
		os.Getenv("JAMINAI_MODEL"),
		os.Getenv("LLM_MODEL"),
	)
	if model == "" {
		model = "gemini-2.5-flash"
	}

	// Allow converting LLM_TIMEOUT_MS → JAMINAI_TIMEOUT seconds.
	if v := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_MS")); v != "" {
		if d, err := time.ParseDuration(v + "ms"); err == nil {
			_ = os.Setenv("JAMINAI_TIMEOUT", fmt.Sprintf("%.0fs", d.Seconds()))
		}
	}
	timeout := 12 * time.Second
	if v := strings.TrimSpace(os.Getenv("JAMINAI_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}

	allowNoKey := strings.EqualFold(os.Getenv("LLM_ALLOW_NO_KEY"), "true") ||
		strings.Contains(base, "localhost") || strings.Contains(base, "127.0.0.1")

	if key == "" && !allowNoKey {
		return nil, ErrLLMDisabled
	}

	return &JaminAIClient{
		BaseURL: strings.TrimRight(base, "/"),
		APIKey:  key,
		Model:   model,
		HTTP:    &http.Client{Timeout: timeout},
	}, nil
}

// Chat sends a synchronous chat.completions request.
func (c *JaminAIClient) Chat(ctx context.Context, system, user string) (string, error) {
	reqBody := chatReq{
		Model:       c.Model,
		Messages:    []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		MaxTokens:   320,
		Temperature: 0.7,
	}
	b, _ := json.Marshal(reqBody)

	endpoint := c.BaseURL + "/chat/completions"
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	var out chatResp
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("llm decode failed: %w; raw=%s", err, strings.TrimSpace(string(body)))
	}
	if out.Error != nil {
		return "", errors.New(strings.TrimSpace(out.Error.Message))
	}
	if len(out.Choices) == 0 {
		return "", errors.New("llm: empty choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// ---------- Domain helpers (safe fallbacks inside) ----------

// GeneratePaymentClarify returns ONE short question asking only missing fields.
// Always returns non-empty string (falls back if LLM fails).
func GeneratePaymentClarify(ctx context.Context, c Client, lang string, missing []string, userText string) string {
	// fallback labels
	if len(missing) == 0 {
		missing = []string{"수신자", "금액(원)", "결제수단"}
	}
	miss := strings.Join(missing, ", ")

	if c != nil {
		sys := "You generate ONE short clarification question focused only on the missing fields for checkout/transfer."
		user := fmt.Sprintf("User text: %q\nMissing fields: %s\nReturn exactly ONE concise question. No list, no explanation.", strings.TrimSpace(userText), miss)
		if lang == "ko" {
			sys = "결제/송금 맥락에서 누락된 항목만 간결하게 한 문장으로 물어봐. 설명/목록 없이 질문 한 문장만."
			user = fmt.Sprintf("사용자 입력: %q\n누락 항목: %s\n질문 한 문장만 출력.", strings.TrimSpace(userText), miss)
		}
		if out, err := c.Chat(ctx, sys, user); err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}

	// fallback
	if lang == "ko" {
		return "부족한 항목만 한 문장으로 알려주세요: " + miss
	}
	return "Please provide the missing info in one short sentence: " + miss
}

// GeneratePaymentReceipt returns one-line receipt text.
// Always returns non-empty string (falls back if LLM fails).
func GeneratePaymentReceipt(ctx context.Context, c Client, lang string, to string, amountKRW int64, method, item, memo string) string {
	amt := withComma(amountKRW)
	if amt == "" {
		amt = "0"
	}
	if c != nil {
		sys := "Generate exactly ONE single-line human-friendly receipt/confirmation sentence."
		user := fmt.Sprintf("to=%s, amount=%s KRW, method=%s, item=%s, memo=%s. One short line.",
			nz(to), amt, nz(method), nz(item), nz(memo))
		if lang == "ko" {
			sys = "영수증 확인 문장을 한국어로 한 줄만 생성한다. 간결하게."
			user = fmt.Sprintf("수신자=%s, 금액=%s원, 결제수단=%s, 제품=%s, 메모=%s. 한 줄만 출력.",
				nz(to), amt, nz(method), nz(item), nz(memo))
		}
		if out, err := c.Chat(ctx, sys, user); err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}
	// fallback
	if lang == "ko" {
		return fmt.Sprintf("%s님에 대한 결제 %s원(%s) 처리 완료%s.",
			nz(to), amt, nz(method), optionalSuffix(" - "+nz(item)))
	}
	return fmt.Sprintf("Payment %s KRW via %s to %s completed%s.",
		amt, nz(method), nz(to), optionalSuffix(" - "+nz(item)))
}

// ---------- shared helpers (LLM-side only) ----------

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// normalizeBase adds /v1 for local OpenAI-compatible servers if necessary.
func normalizeBase(u string) string {
	s := strings.TrimRight(strings.TrimSpace(u), "/")
	if s == "" {
		return s
	}
	isLocal := strings.Contains(s, "localhost") || strings.Contains(s, "127.0.0.1")
	if isLocal {
		if !strings.HasSuffix(s, "/v1") && !strings.Contains(s, "/openai/v1") {
			s += "/v1"
		}
	}
	return s
}

// DetectLang returns "ko" if Hangul is detected, otherwise "en".
func DetectLang(s string) string {
	for _, r := range s {
		if unicode.Is(unicode.Hangul, r) {
			return "ko"
		}
	}
	return "en"
}

func nz(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

func optionalSuffix(s string) string {
	if strings.TrimSpace(s) == "" || strings.TrimSpace(s) == "-" {
		return ""
	}
	return s
}

func withComma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
