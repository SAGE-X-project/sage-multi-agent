package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/llm"
)

// Simulate Root Agent's LLM routing logic
func testLLMRouting(client llm.Client, text string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sys := `You are an intent classifier.
Return a single JSON object with fields: domain in ["payment","medical","planning","ordering","chat"], lang in ["ko","en"].
Pick the most likely domain.`

	pr := map[string]any{"text": text}
	jb, _ := json.Marshal(pr)

	out, err := client.Chat(ctx, sys, string(jb))
	if err != nil {
		return "", ""
	}

	// Parse JSON response
	type routeOut struct {
		Domain string `json:"domain"`
		Lang   string `json:"lang"`
	}

	// Try to extract JSON from response
	jsonStart := strings.Index(out, "{")
	jsonEnd := strings.LastIndex(out, "}")
	if jsonStart == -1 || jsonEnd == -1 {
		return "", ""
	}

	jsonStr := out[jsonStart : jsonEnd+1]
	var result routeOut
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return "", ""
	}

	return result.Domain, result.Lang
}

func main() {
	fmt.Println("=== SAGE Root Agent LLM Routing Test ===\n")

	// Setup
	os.Setenv("LLM_PROVIDER", "gemini-native")
	os.Setenv("GEMINI_API_KEY", os.Getenv("GEMINI_API_KEY"))

	client, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}

	// Test cases
	tests := []struct {
		input          string
		expectedDomain string
		expectedLang   string
	}{
		// Payment domain tests
		{
			input:          "김철수님께 10만원 송금해줘",
			expectedDomain: "payment",
			expectedLang:   "ko",
		},
		{
			input:          "Send $100 to John",
			expectedDomain: "payment",
			expectedLang:   "en",
		},
		{
			input:          "카드로 결제하고 싶어요",
			expectedDomain: "payment",
			expectedLang:   "ko",
		},
		// Medical domain tests
		{
			input:          "머리가 아파요",
			expectedDomain: "medical",
			expectedLang:   "ko",
		},
		{
			input:          "I have a headache",
			expectedDomain: "medical",
			expectedLang:   "en",
		},
		{
			input:          "감기 증상이 있어요",
			expectedDomain: "medical",
			expectedLang:   "ko",
		},
		// Planning domain tests
		{
			input:          "부산 여행 계획 세워줘",
			expectedDomain: "planning",
			expectedLang:   "ko",
		},
		{
			input:          "Plan a trip to Seoul",
			expectedDomain: "planning",
			expectedLang:   "en",
		},
		{
			input:          "내일 일정 정리해줘",
			expectedDomain: "planning",
			expectedLang:   "ko",
		},
		// Ordering domain tests
		{
			input:          "피자 주문하고 싶어요",
			expectedDomain: "ordering",
			expectedLang:   "ko",
		},
		{
			input:          "Order a pizza",
			expectedDomain: "ordering",
			expectedLang:   "en",
		},
		// Chat domain tests
		{
			input:          "안녕하세요",
			expectedDomain: "chat",
			expectedLang:   "ko",
		},
		{
			input:          "Hello, how are you?",
			expectedDomain: "chat",
			expectedLang:   "en",
		},
		{
			input:          "날씨가 좋네요",
			expectedDomain: "chat",
			expectedLang:   "ko",
		},
	}

	passed := 0
	failed := 0

	fmt.Println("Running routing tests...")
	fmt.Println(strings.Repeat("-", 80))

	for i, tt := range tests {
		fmt.Printf("\n[Test %d/%d]\n", i+1, len(tests))
		fmt.Printf("Input: %s\n", tt.input)
		fmt.Printf("Expected: domain=%s, lang=%s\n", tt.expectedDomain, tt.expectedLang)

		domain, lang := testLLMRouting(client, tt.input)
		fmt.Printf("Got:      domain=%s, lang=%s\n", domain, lang)

		domainMatch := domain == tt.expectedDomain
		langMatch := lang == tt.expectedLang

		if domainMatch && langMatch {
			fmt.Println("✅ PASS")
			passed++
		} else {
			fmt.Println("❌ FAIL")
			if !domainMatch {
				fmt.Printf("   Domain mismatch: expected '%s', got '%s'\n", tt.expectedDomain, domain)
			}
			if !langMatch {
				fmt.Printf("   Language mismatch: expected '%s', got '%s'\n", tt.expectedLang, lang)
			}
			failed++
		}

		// Rate limiting
		time.Sleep(2 * time.Second)
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("\nTest Results:\n")
	fmt.Printf("✅ Passed: %d/%d (%.1f%%)\n", passed, len(tests), float64(passed)/float64(len(tests))*100)
	fmt.Printf("❌ Failed: %d/%d (%.1f%%)\n", failed, len(tests), float64(failed)/float64(len(tests))*100)

	if failed == 0 {
		fmt.Println("\n🎉 All tests passed!")
	} else {
		fmt.Printf("\n⚠️  Some tests failed. Accuracy: %.1f%%\n", float64(passed)/float64(len(tests))*100)
	}
}
