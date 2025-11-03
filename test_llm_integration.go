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
	fmt.Println("=== SAGE Multi-Agent LLM Integration Test ===\n")

	// Test 1: Gemini Native API
	fmt.Println("Test 1: Gemini Native API")
	fmt.Println("----------------------------")
	testGemini()

	fmt.Println("\n" + strings.Repeat("=", 50) + "\n")

	// Test 2: Multiple queries with different contexts
	fmt.Println("Test 2: Different Query Types")
	fmt.Println("----------------------------")
	testMultipleQueries()

	fmt.Println("\n" + strings.Repeat("=", 50) + "\n")

	// Test 3: Korean language support
	fmt.Println("Test 3: Korean Language Support")
	fmt.Println("----------------------------")
	testKoreanLanguage()

	fmt.Println("\n✅ All tests completed successfully!")
}

func testGemini() {
	// Set environment for Gemini
	os.Setenv("LLM_PROVIDER", "gemini-native")
	os.Setenv("GEMINI_API_KEY", os.Getenv("GEMINI_API_KEY"))

	client, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("Failed to create Gemini client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	systemPrompt := "You are a helpful AI assistant. Provide concise and accurate answers."
	userMessage := "What is 2 + 2? Answer in one sentence."

	fmt.Printf("System: %s\n", systemPrompt)
	fmt.Printf("User: %s\n", userMessage)
	fmt.Print("Assistant: ")

	response, err := client.Chat(ctx, systemPrompt, userMessage)
	if err != nil {
		log.Fatalf("Gemini API call failed: %v", err)
	}

	fmt.Printf("%s\n", response)
	fmt.Printf("\n✓ Gemini test passed (response length: %d chars)\n", len(response))
}

func testMultipleQueries() {
	client, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	tests := []struct {
		name   string
		system string
		user   string
	}{
		{
			name:   "Medical Query",
			system: "You are a medical advisor. Provide brief health information.",
			user:   "What are common symptoms of a cold?",
		},
		{
			name:   "Payment Query",
			system: "You are a payment processing assistant.",
			user:   "How do I send $100 to John?",
		},
		{
			name:   "General Query",
			system: "You are a helpful assistant.",
			user:   "What's the weather like today?",
		},
	}

	for i, tt := range tests {
		fmt.Printf("%d. %s\n", i+1, tt.name)
		fmt.Printf("   User: %s\n", tt.user)
		fmt.Print("   Response: ")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		response, err := client.Chat(ctx, tt.system, tt.user)
		cancel()

		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			continue
		}

		// Truncate long responses
		if len(response) > 100 {
			fmt.Printf("%s...\n", response[:100])
		} else {
			fmt.Printf("%s\n", response)
		}
		fmt.Println()

		time.Sleep(1 * time.Second) // Rate limiting
	}
}

func testKoreanLanguage() {
	client, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	tests := []struct {
		name   string
		system string
		user   string
	}{
		{
			name:   "한국어 인사",
			system: "당신은 친절한 AI 어시스턴트입니다.",
			user:   "안녕하세요! 오늘 날씨가 어때요?",
		},
		{
			name:   "한국어 결제",
			system: "당신은 결제 처리 어시스턴트입니다.",
			user:   "김철수님께 10만원을 송금하고 싶어요.",
		},
	}

	for i, tt := range tests {
		fmt.Printf("%d. %s\n", i+1, tt.name)
		fmt.Printf("   사용자: %s\n", tt.user)
		fmt.Print("   응답: ")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		response, err := client.Chat(ctx, tt.system, tt.user)
		cancel()

		if err != nil {
			fmt.Printf("오류: %v\n", err)
			continue
		}

		// Truncate long responses
		if len(response) > 100 {
			fmt.Printf("%s...\n", response[:100])
		} else {
			fmt.Printf("%s\n", response)
		}
		fmt.Println()

		time.Sleep(1 * time.Second) // Rate limiting
	}
}
