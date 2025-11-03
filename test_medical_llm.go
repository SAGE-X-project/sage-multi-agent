package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/llm"
)

func main() {
	fmt.Println("=== SAGE Medical Agent LLM Test ===\n")

	// Setup
	os.Setenv("LLM_PROVIDER", "gemini-native")
	os.Setenv("GEMINI_API_KEY", os.Getenv("GEMINI_API_KEY"))

	client, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}

	// Test medical advice generation
	tests := []struct {
		name      string
		lang      string
		condition string
		symptoms  string
	}{
		{
			name:      "Korean - Headache",
			lang:      "ko",
			condition: "두통",
			symptoms:  "머리가 아프고 어지러워요",
		},
		{
			name:      "English - Cold",
			lang:      "en",
			condition: "cold",
			symptoms:  "I have a runny nose and sore throat",
		},
		{
			name:      "Korean - Stomach ache",
			lang:      "ko",
			condition: "복통",
			symptoms:  "배가 아프고 소화가 안돼요",
		},
		{
			name:      "English - Fever",
			lang:      "en",
			condition: "fever",
			symptoms:  "I have a high fever and body aches",
		},
		{
			name:      "Korean - Cough",
			lang:      "ko",
			condition: "기침",
			symptoms:  "기침이 계속 나와요",
		},
	}

	passed := 0
	failed := 0

	fmt.Println("Running Medical LLM tests...")
	fmt.Println(strings.Repeat("-", 80))

	for i, tt := range tests {
		fmt.Printf("\n[Test %d/%d] %s\n", i+1, len(tests), tt.name)
		fmt.Printf("Condition: %s\n", tt.condition)
		fmt.Printf("Symptoms: %s\n", tt.symptoms)

		// Simulate Medical Agent's LLM call
		systemPrompt := `You are a medical advisor. Reply in ONE short sentence only. No disclaimers.`

		userPrompt := fmt.Sprintf("Condition: %s\nSymptoms: %s\n", tt.condition, tt.symptoms)
		if tt.lang == "ko" {
			userPrompt += "Output: 한 문장 한국어 답변만.\n"
		} else {
			userPrompt += "Output: ONE-sentence answer only.\n"
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		response, err := client.Chat(ctx, systemPrompt, userPrompt)
		cancel()

		if err != nil {
			fmt.Printf("❌ FAIL: LLM error: %v\n", err)
			failed++
		} else if strings.TrimSpace(response) == "" {
			fmt.Printf("❌ FAIL: Empty response\n")
			failed++
		} else {
			fmt.Printf("Response: %s\n", response)
			fmt.Printf("✅ PASS (response length: %d chars)\n", len(response))
			passed++
		}

		time.Sleep(2 * time.Second) // Rate limiting
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("\nTest Results:\n")
	fmt.Printf("✅ Passed: %d/%d (%.1f%%)\n", passed, len(tests), float64(passed)/float64(len(tests))*100)
	fmt.Printf("❌ Failed: %d/%d (%.1f%%)\n", failed, len(tests), float64(failed)/float64(len(tests))*100)

	if failed == 0 {
		fmt.Println("\n🎉 All tests passed!")
	} else {
		fmt.Printf("\n⚠️  Some tests failed\n")
	}
}
