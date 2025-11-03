// Package llm - Anthropic Claude API client implementation
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicClient implements the Client interface using Anthropic's Claude API
// https://docs.anthropic.com/en/api/messages
type AnthropicClient struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// Anthropic API request/response structures
type anthropicRequest struct {
	Model       string              `json:"model"`
	Messages    []anthropicMessage  `json:"messages"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature float64             `json:"temperature,omitempty"`
	System      string              `json:"system,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID      string              `json:"id"`
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []anthropicContent  `json:"content"`
	Model   string              `json:"model"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// NewAnthropicClient creates a new Anthropic Claude API client
func NewAnthropicClient(apiKey, model string, timeout time.Duration) *AnthropicClient {
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}
	if timeout == 0 {
		timeout = 12 * time.Second
	}

	return &AnthropicClient{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: "https://api.anthropic.com/v1",
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// Chat implements the Client interface for Anthropic Claude API
func (c *AnthropicClient) Chat(ctx context.Context, system, user string) (string, error) {
	reqBody := anthropicRequest{
		Model: c.Model,
		Messages: []anthropicMessage{
			{
				Role:    "user",
				Content: user,
			},
		},
		MaxTokens:   1024,
		Temperature: 0.7,
	}

	// Add system instruction if provided
	if strings.TrimSpace(system) != "" {
		reqBody.System = system
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal request: %w", err)
	}

	endpoint := c.BaseURL + "/messages"

	// Retry loop (max 2 attempts)
	for attempt := 0; attempt < 2; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
		if err != nil {
			return "", fmt.Errorf("anthropic: create request: %w", err)
		}

		// Required headers for Anthropic API
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", c.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")

		res, err := c.HTTP.Do(httpReq)
		if err != nil {
			if attempt == 0 {
				time.Sleep(3 * time.Second)
				continue
			}
			return "", fmt.Errorf("anthropic: http request: %w", err)
		}
		defer res.Body.Close()

		body, _ := io.ReadAll(res.Body)

		// Handle rate limiting (429)
		if res.StatusCode == http.StatusTooManyRequests {
			if attempt == 0 {
				time.Sleep(4 * time.Second)
				continue
			}
			return "", fmt.Errorf("anthropic: 429 rate limit: %s", string(body))
		}

		// Handle non-2xx responses
		if res.StatusCode/100 != 2 {
			if attempt == 0 {
				time.Sleep(2 * time.Second)
				continue
			}
			return "", fmt.Errorf("anthropic: %d %s", res.StatusCode, string(body))
		}

		// Parse response
		var out anthropicResponse
		if err := json.Unmarshal(body, &out); err != nil {
			return "", fmt.Errorf("anthropic: decode failed: %w; raw=%s", err, string(body))
		}

		// Check for API error
		if out.Error != nil {
			if attempt == 0 {
				time.Sleep(2 * time.Second)
				continue
			}
			return "", fmt.Errorf("anthropic: %s - %s", out.Error.Type, out.Error.Message)
		}

		// Extract text from response
		if len(out.Content) == 0 {
			if attempt == 0 {
				time.Sleep(1 * time.Second)
				continue
			}
			return "", errors.New("anthropic: empty content")
		}

		// Find first text content block
		for _, content := range out.Content {
			if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
				return strings.TrimSpace(content.Text), nil
			}
		}

		if attempt == 0 {
			time.Sleep(1 * time.Second)
			continue
		}
		return "", errors.New("anthropic: no text content found")
	}

	return "", errors.New("anthropic: retry exhausted")
}
