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
	fmt.Println("=== SAGE Agent LLM Helper Functions Test ===\n")

	// Setup
	os.Setenv("LLM_PROVIDER", "gemini-native")
	os.Setenv("GEMINI_API_KEY", os.Getenv("GEMINI_API_KEY"))

	client, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}

	// Test 1: Payment Receipt Generation
	fmt.Println("Test 1: Payment Receipt Generation")
	fmt.Println(strings.Repeat("-", 60))
	testPaymentReceipt(client)

	fmt.Println("\n" + strings.Repeat("=", 60) + "\n")

	// Test 2: Payment Clarification Questions
	fmt.Println("Test 2: Payment Clarification Questions")
	fmt.Println(strings.Repeat("-", 60))
	testPaymentClarify(client)

	fmt.Println("\n✅ All helper function tests completed!")
}

func testPaymentReceipt(client llm.Client) {
	tests := []struct {
		name      string
		lang      string
		to        string
		amountKRW int64
		method    string
		item      string
		memo      string
	}{
		{
			name:      "Korean Receipt - Card Payment",
			lang:      "ko",
			to:        "김철수",
			amountKRW: 100000,
			method:    "card",
			item:      "피자 세트",
			memo:      "맛있게 먹겠습니다",
		},
		{
			name:      "English Receipt - Transfer",
			lang:      "en",
			to:        "John Doe",
			amountKRW: 50000,
			method:    "transfer",
			item:      "Book purchase",
			memo:      "Thanks",
		},
		{
			name:      "Korean Receipt - Simple",
			lang:      "ko",
			to:        "이영희",
			amountKRW: 25000,
			method:    "card",
			item:      "",
			memo:      "",
		},
	}

	for i, tt := range tests {
		fmt.Printf("\n%d. %s\n", i+1, tt.name)
		fmt.Printf("   Details: to=%s, amount=%d원, method=%s, item=%s\n",
			tt.to, tt.amountKRW, tt.method, tt.item)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		receipt := llm.GeneratePaymentReceipt(ctx, client, tt.lang, tt.to, tt.amountKRW, tt.method, tt.item, tt.memo)
		cancel()

		fmt.Printf("   Receipt: %s\n", receipt)

		if receipt == "" {
			fmt.Println("   ❌ Failed to generate receipt")
		} else {
			fmt.Println("   ✅ Receipt generated")
		}

		time.Sleep(2 * time.Second) // Rate limiting
	}
}

func testPaymentClarify(client llm.Client) {
	tests := []struct {
		name       string
		lang       string
		missing    []string
		userText   string
	}{
		{
			name:     "Korean - Missing Recipient and Amount",
			lang:     "ko",
			missing:  []string{"수신자", "금액"},
			userText: "카드로 결제하고 싶어요",
		},
		{
			name:     "English - Missing All Info",
			lang:     "en",
			missing:  []string{"recipient", "amount", "payment method"},
			userText: "I want to send money",
		},
		{
			name:     "Korean - Missing Amount Only",
			lang:     "ko",
			missing:  []string{"금액"},
			userText: "김철수님께 송금하려고 합니다",
		},
	}

	for i, tt := range tests {
		fmt.Printf("\n%d. %s\n", i+1, tt.name)
		fmt.Printf("   User text: %s\n", tt.userText)
		fmt.Printf("   Missing: %s\n", strings.Join(tt.missing, ", "))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		question := llm.GeneratePaymentClarify(ctx, client, tt.lang, tt.missing, tt.userText)
		cancel()

		fmt.Printf("   Question: %s\n", question)

		if question == "" {
			fmt.Println("   ❌ Failed to generate question")
		} else {
			fmt.Println("   ✅ Question generated")
		}

		time.Sleep(2 * time.Second) // Rate limiting
	}
}
