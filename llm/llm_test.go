package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNewFromEnv_OpenAI(t *testing.T) {
	// Save original env
	originalProvider := os.Getenv("LLM_PROVIDER")
	originalKey := os.Getenv("OPENAI_API_KEY")
	defer func() {
		os.Setenv("LLM_PROVIDER", originalProvider)
		os.Setenv("OPENAI_API_KEY", originalKey)
	}()

	// Test OpenAI provider
	os.Setenv("LLM_PROVIDER", "openai")
	os.Setenv("OPENAI_API_KEY", "sk-test123")

	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if client == nil {
		t.Fatal("Expected client, got nil")
	}

	oaiClient, ok := client.(*OpenAIClient)
	if !ok {
		t.Fatalf("Expected OpenAIClient, got %T", client)
	}

	if oaiClient.APIKey != "sk-test123" {
		t.Errorf("Expected API key 'sk-test123', got '%s'", oaiClient.APIKey)
	}

	if oaiClient.Model != "gpt-4o-mini" {
		t.Errorf("Expected model 'gpt-4o-mini', got '%s'", oaiClient.Model)
	}
}

func TestNewFromEnv_GeminiNative(t *testing.T) {
	// Save original env
	originalProvider := os.Getenv("LLM_PROVIDER")
	originalKey := os.Getenv("GEMINI_API_KEY")
	defer func() {
		os.Setenv("LLM_PROVIDER", originalProvider)
		os.Setenv("GEMINI_API_KEY", originalKey)
	}()

	// Test Gemini native provider
	os.Setenv("LLM_PROVIDER", "gemini-native")
	os.Setenv("GEMINI_API_KEY", "AIza-test123")

	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if client == nil {
		t.Fatal("Expected client, got nil")
	}

	geminiClient, ok := client.(*GeminiClient)
	if !ok {
		t.Fatalf("Expected GeminiClient, got %T", client)
	}

	if geminiClient.APIKey != "AIza-test123" {
		t.Errorf("Expected API key 'AIza-test123', got '%s'", geminiClient.APIKey)
	}

	if geminiClient.Model != "gemini-2.0-flash-exp" {
		t.Errorf("Expected model 'gemini-2.0-flash-exp', got '%s'", geminiClient.Model)
	}
}

func TestNewFromEnv_Anthropic(t *testing.T) {
	// Save original env
	originalProvider := os.Getenv("LLM_PROVIDER")
	originalKey := os.Getenv("ANTHROPIC_API_KEY")
	defer func() {
		os.Setenv("LLM_PROVIDER", originalProvider)
		os.Setenv("ANTHROPIC_API_KEY", originalKey)
	}()

	// Test Anthropic provider
	os.Setenv("LLM_PROVIDER", "anthropic")
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test123")

	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if client == nil {
		t.Fatal("Expected client, got nil")
	}

	anthropicClient, ok := client.(*AnthropicClient)
	if !ok {
		t.Fatalf("Expected AnthropicClient, got %T", client)
	}

	if anthropicClient.APIKey != "sk-ant-test123" {
		t.Errorf("Expected API key 'sk-ant-test123', got '%s'", anthropicClient.APIKey)
	}

	if anthropicClient.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("Expected model 'claude-3-5-sonnet-20241022', got '%s'", anthropicClient.Model)
	}
}

func TestNewFromEnv_MissingKey(t *testing.T) {
	// Save original env
	originalProvider := os.Getenv("LLM_PROVIDER")
	originalKey := os.Getenv("OPENAI_API_KEY")
	originalAllowNoKey := os.Getenv("LLM_ALLOW_NO_KEY")
	defer func() {
		os.Setenv("LLM_PROVIDER", originalProvider)
		os.Setenv("OPENAI_API_KEY", originalKey)
		os.Setenv("LLM_ALLOW_NO_KEY", originalAllowNoKey)
	}()

	// Test missing key
	os.Setenv("LLM_PROVIDER", "openai")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("LLM_API_KEY")
	os.Unsetenv("LLM_ALLOW_NO_KEY")

	_, err := NewFromEnv()
	if err != ErrLLMDisabled {
		t.Errorf("Expected ErrLLMDisabled, got: %v", err)
	}
}

func TestNewFromEnv_CustomTimeout(t *testing.T) {
	// Save original env
	originalProvider := os.Getenv("LLM_PROVIDER")
	originalKey := os.Getenv("OPENAI_API_KEY")
	originalTimeout := os.Getenv("LLM_TIMEOUT")
	defer func() {
		os.Setenv("LLM_PROVIDER", originalProvider)
		os.Setenv("OPENAI_API_KEY", originalKey)
		os.Setenv("LLM_TIMEOUT", originalTimeout)
	}()

	// Test custom timeout
	os.Setenv("LLM_PROVIDER", "openai")
	os.Setenv("OPENAI_API_KEY", "sk-test123")
	os.Setenv("LLM_TIMEOUT", "30s")

	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	oaiClient := client.(*OpenAIClient)
	if oaiClient.HTTP.Timeout != 30*time.Second {
		t.Errorf("Expected timeout 30s, got %v", oaiClient.HTTP.Timeout)
	}
}

func TestGeminiClient_Chat(t *testing.T) {
	// This is a unit test without actual API calls
	client := NewGeminiClient("test-key", "gemini-2.0-flash-exp", 12*time.Second)

	if client.APIKey != "test-key" {
		t.Errorf("Expected API key 'test-key', got '%s'", client.APIKey)
	}

	if client.Model != "gemini-2.0-flash-exp" {
		t.Errorf("Expected model 'gemini-2.0-flash-exp', got '%s'", client.Model)
	}

	if client.HTTP.Timeout != 12*time.Second {
		t.Errorf("Expected timeout 12s, got %v", client.HTTP.Timeout)
	}

	// Note: We don't test actual API calls without proper credentials
}

func TestAnthropicClient_Chat(t *testing.T) {
	// This is a unit test without actual API calls
	client := NewAnthropicClient("test-key", "claude-3-5-sonnet-20241022", 12*time.Second)

	if client.APIKey != "test-key" {
		t.Errorf("Expected API key 'test-key', got '%s'", client.APIKey)
	}

	if client.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("Expected model 'claude-3-5-sonnet-20241022', got '%s'", client.Model)
	}

	if client.HTTP.Timeout != 12*time.Second {
		t.Errorf("Expected timeout 12s, got %v", client.HTTP.Timeout)
	}

	if client.BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("Expected base URL 'https://api.anthropic.com/v1', got '%s'", client.BaseURL)
	}
}

func TestDetectLang(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello", "en"},
		{"안녕하세요", "ko"},
		{"Hello 안녕", "ko"},
		{"", "en"},
		{"12345", "en"},
		{"결제해줘", "ko"},
	}

	for _, tt := range tests {
		result := DetectLang(tt.input)
		if result != tt.expected {
			t.Errorf("DetectLang(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		inputs   []string
		expected string
	}{
		{[]string{"a", "b", "c"}, "a"},
		{[]string{"", "b", "c"}, "b"},
		{[]string{"", "", "c"}, "c"},
		{[]string{"", "", ""}, ""},
		{[]string{" a ", "b"}, "a"},
	}

	for _, tt := range tests {
		result := firstNonEmpty(tt.inputs...)
		if result != tt.expected {
			t.Errorf("firstNonEmpty(%v) = %q, expected %q", tt.inputs, result, tt.expected)
		}
	}
}

// Integration test example (requires actual API keys to run)
func TestIntegration_OpenAI(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set")
	}

	os.Setenv("LLM_PROVIDER", "openai")
	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	response, err := client.Chat(ctx, "You are a test assistant.", "Say 'test' once.")
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("OpenAI Response: %s", response)
}

// Integration test for Gemini Native (requires API key)
func TestIntegration_GeminiNative(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: GEMINI_API_KEY not set")
	}

	os.Setenv("LLM_PROVIDER", "gemini-native")
	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	response, err := client.Chat(ctx, "You are a test assistant.", "Say 'test' once.")
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("Gemini Response: %s", response)
}

// Integration test for Anthropic (requires API key)
func TestIntegration_Anthropic(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: ANTHROPIC_API_KEY not set")
	}

	os.Setenv("LLM_PROVIDER", "anthropic")
	client, err := NewFromEnv()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	response, err := client.Chat(ctx, "You are a test assistant.", "Say 'test' once.")
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("Anthropic Response: %s", response)
}
