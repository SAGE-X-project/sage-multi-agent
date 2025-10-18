//go:build demo
// +build demo

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/sage-x-project/sage-multi-agent/types"
)

const (
	clientServerURL = "http://localhost:8085"
)

func main() {
	// Wait for server to start
	fmt.Println("Testing SAGE Protocol Implementation...")
	fmt.Println("========================================")
	time.Sleep(2 * time.Second)

	// Test 1: Check SAGE status
	fmt.Println("\n1. Checking SAGE status...")
	status, err := getSAGEStatus()
	if err != nil {
		log.Printf("Failed to get SAGE status: %v", err)
	} else {
		fmt.Printf("   SAGE Enabled: %v\n", status.Enabled)
		fmt.Printf("   Verifier Enabled: %v\n", status.VerifierEnabled)
		for agent, enabled := range status.AgentSigners {
			fmt.Printf("   Agent %s: %v\n", agent, enabled)
		}
	}

	// Test 2: Test SAGE signing and verification
	fmt.Println("\n2. Testing SAGE signing and verification...")
	testResult, err := testSAGE("root")
	if err != nil {
		log.Printf("Failed to test SAGE: %v", err)
	} else {
		fmt.Printf("   Test Success: %v\n", testResult.Success)
		if testResult.Error != "" {
			fmt.Printf("   Error: %s\n", testResult.Error)
		}
		if testResult.SignedBy != "" {
			fmt.Printf("   Signed by: %s\n", testResult.SignedBy)
		}
		if testResult.VerifiedBy != "" {
			fmt.Printf("   Verified by: %s\n", testResult.VerifiedBy)
		}
		fmt.Printf("   Stage: %s\n", testResult.Stage)
	}

	// Test 3: Enable SAGE
	fmt.Println("\n3. Enabling SAGE protocol...")
	enabled := true
	newStatus, err := setSAGEConfig(&types.SAGEConfigRequest{Enabled: &enabled})
	if err != nil {
		log.Printf("Failed to enable SAGE: %v", err)
	} else {
		fmt.Printf("   SAGE Enabled: %v\n", newStatus.Enabled)
	}

	// Test 4: Send prompt with SAGE enabled
	fmt.Println("\n4. Sending prompt with SAGE enabled...")
	response, err := sendPromptWithSAGE("Hello, this is a test message with SAGE protocol", true)
	if err != nil {
		log.Printf("Failed to send prompt: %v", err)
	} else {
		fmt.Printf("   Response: %s\n", response.Response)
		if response.SAGEVerification != nil {
			fmt.Printf("   SAGE Verified: %v\n", response.SAGEVerification.Verified)
			fmt.Printf("   Signature Valid: %v\n", response.SAGEVerification.SignatureValid)
			if response.SAGEVerification.AgentDID != "" {
				fmt.Printf("   Agent DID: %s\n", response.SAGEVerification.AgentDID)
			}
		}
	}

	// Test 5: Disable SAGE
	fmt.Println("\n5. Disabling SAGE protocol...")
	enabled = false
	newStatus, err = setSAGEConfig(&types.SAGEConfigRequest{Enabled: &enabled})
	if err != nil {
		log.Printf("Failed to disable SAGE: %v", err)
	} else {
		fmt.Printf("   SAGE Enabled: %v\n", newStatus.Enabled)
	}

	// Test 6: Send prompt without SAGE
	fmt.Println("\n6. Sending prompt without SAGE...")
	response, err = sendPromptWithSAGE("Hello, this is a test message without SAGE protocol", false)
	if err != nil {
		log.Printf("Failed to send prompt: %v", err)
	} else {
		fmt.Printf("   Response: %s\n", response.Response)
		if response.SAGEVerification != nil {
			fmt.Printf("   SAGE Verification present: %v\n", response.SAGEVerification.Verified)
		} else {
			fmt.Println("   No SAGE verification (as expected)")
		}
	}

	fmt.Println("\n========================================")
	fmt.Println("SAGE Protocol Testing Complete!")
}

func getSAGEStatus() (*types.SAGEStatus, error) {
	resp, err := http.Get(clientServerURL + "/api/sage/config")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var status types.SAGEStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}

	return &status, nil
}

func setSAGEConfig(config *types.SAGEConfigRequest) (*types.SAGEStatus, error) {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(clientServerURL+"/api/sage/config", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var status types.SAGEStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}

	return &status, nil
}

func testSAGE(agentType string) (*types.SAGETestResult, error) {
	reqBody := map[string]string{"agentType": agentType}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(clientServerURL+"/api/sage/test", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result types.SAGETestResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func sendPromptWithSAGE(prompt string, sageEnabled bool) (*types.PromptResponse, error) {
	req := types.PromptRequest{
		Prompt:      prompt,
		SAGEEnabled: sageEnabled,
		Metadata: &types.RequestMetadata{
			Timestamp: time.Now().Format(time.RFC3339),
		},
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(clientServerURL+"/send/prompt", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response types.PromptResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w\nBody: %s", err, string(body))
	}

	return &response, nil
}
