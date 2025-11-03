// Package llm - Gemini Native API client implementation
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

// GeminiClient implements the Client interface using Gemini's native REST API
// https://ai.google.dev/api/rest
type GeminiClient struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// Gemini API request/response structures
type geminiRequest struct {
	Contents         []geminiContent       `json:"contents"`
	GenerationConfig *geminiGenConfig      `json:"generationConfig,omitempty"`
	SafetySettings   []geminiSafetySetting `json:"safetySettings,omitempty"`
}

type geminiContent struct {
	Role  string        `json:"role"` // "user" or "model"
	Parts []geminiPart  `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	TopP            float64 `json:"topP,omitempty"`
	TopK            int     `json:"topK,omitempty"`
}

type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content       geminiContent `json:"content"`
	FinishReason  string        `json:"finishReason"`
	SafetyRatings []struct {
		Category    string `json:"category"`
		Probability string `json:"probability"`
	} `json:"safetyRatings"`
}

// NewGeminiClient creates a new Gemini native API client
func NewGeminiClient(apiKey, model string, timeout time.Duration) *GeminiClient {
	if model == "" {
		model = "gemini-2.0-flash-exp"
	}
	if timeout == 0 {
		timeout = 12 * time.Second
	}

	return &GeminiClient{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// Chat implements the Client interface for Gemini native API
func (c *GeminiClient) Chat(ctx context.Context, system, user string) (string, error) {
	// Gemini doesn't have a dedicated "system" role, so we prepend system instruction to user message
	fullPrompt := user
	if strings.TrimSpace(system) != "" {
		fullPrompt = fmt.Sprintf("System Instructions: %s\n\nUser: %s", system, user)
	}

	fmt.Printf("[gemini] Starting API call - Model: %s, Prompt length: %d\n", c.Model, len(fullPrompt))

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{Text: fullPrompt},
				},
			},
		},
		GenerationConfig: &geminiGenConfig{
			Temperature:     0.7,
			MaxOutputTokens: 1024,
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gemini: marshal request: %w", err)
	}

	// Endpoint: https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={apiKey}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.BaseURL, c.Model, c.APIKey)

	// Retry loop (max 2 attempts)
	for attempt := 0; attempt < 2; attempt++ {
		fmt.Printf("[gemini] Attempt %d/%d - Creating HTTP request to: %s\n", attempt+1, 2, endpoint)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
		if err != nil {
			fmt.Printf("[gemini] ERROR: Failed to create request: %v\n", err)
			return "", fmt.Errorf("gemini: create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		fmt.Printf("[gemini] Sending HTTP POST request...\n")
		res, err := c.HTTP.Do(httpReq)
		if err != nil {
			fmt.Printf("[gemini] ERROR: HTTP request failed: %v\n", err)
			if attempt == 0 {
				fmt.Printf("[gemini] Retrying in 3 seconds...\n")
				time.Sleep(3 * time.Second)
				continue
			}
			return "", fmt.Errorf("gemini: http request: %w", err)
		}
		defer res.Body.Close()

		fmt.Printf("[gemini] Received response - Status: %d %s\n", res.StatusCode, res.Status)
		body, _ := io.ReadAll(res.Body)
		fmt.Printf("[gemini] Response body length: %d bytes\n", len(body))

		// Handle rate limiting (429)
		if res.StatusCode == http.StatusTooManyRequests {
			fmt.Printf("[gemini] WARNING: Rate limit (429), body: %s\n", string(body))
			if attempt == 0 {
				fmt.Printf("[gemini] Retrying in 4 seconds...\n")
				time.Sleep(4 * time.Second)
				continue
			}
			return "", fmt.Errorf("gemini: 429 rate limit: %s", string(body))
		}

		// Handle non-2xx responses
		if res.StatusCode/100 != 2 {
			fmt.Printf("[gemini] ERROR: Non-2xx status code: %d, body: %s\n", res.StatusCode, string(body))
			if attempt == 0 {
				fmt.Printf("[gemini] Retrying in 2 seconds...\n")
				time.Sleep(2 * time.Second)
				continue
			}
			return "", fmt.Errorf("gemini: %d %s", res.StatusCode, string(body))
		}

		// Parse response
		fmt.Printf("[gemini] Parsing JSON response...\n")
		var out geminiResponse
		if err := json.Unmarshal(body, &out); err != nil {
			fmt.Printf("[gemini] ERROR: JSON decode failed: %v, raw body: %s\n", err, string(body))
			return "", fmt.Errorf("gemini: decode failed: %w; raw=%s", err, string(body))
		}

		// Check for API error
		if out.Error != nil {
			fmt.Printf("[gemini] ERROR: API error in response: code=%d msg=%s\n", out.Error.Code, out.Error.Message)
			if attempt == 0 {
				fmt.Printf("[gemini] Retrying in 2 seconds...\n")
				time.Sleep(2 * time.Second)
				continue
			}
			return "", fmt.Errorf("gemini: %d %s", out.Error.Code, out.Error.Message)
		}

		// Extract text from response
		if len(out.Candidates) == 0 {
			fmt.Printf("[gemini] WARNING: Empty candidates in response\n")
			if attempt == 0 {
				fmt.Printf("[gemini] Retrying in 1 second...\n")
				time.Sleep(1 * time.Second)
				continue
			}
			return "", errors.New("gemini: empty candidates")
		}

		candidate := out.Candidates[0]
		if len(candidate.Content.Parts) == 0 {
			fmt.Printf("[gemini] WARNING: Empty parts in candidate\n")
			if attempt == 0 {
				fmt.Printf("[gemini] Retrying in 1 second...\n")
				time.Sleep(1 * time.Second)
				continue
			}
			return "", errors.New("gemini: empty parts")
		}

		result := strings.TrimSpace(candidate.Content.Parts[0].Text)
		fmt.Printf("[gemini] SUCCESS: Received response, length: %d chars\n", len(result))
		return result, nil
	}

	fmt.Printf("[gemini] ERROR: All retry attempts exhausted\n")
	return "", errors.New("gemini: retry exhausted")
}
