package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// Simple CLI tool that sends a prompt to the Client Server.
// It can toggle SAGE by setting X-SAGE-Enabled header and choose a domain path.
func main() {
	server := flag.String("server", "http://localhost:18086", "Client server base URL")
	domain := flag.String("domain", "prompt", "domain: prompt|planning|ordering|payment")
	prompt := flag.String("prompt", "Plan a trip to Seoul and book a hotel in Myeongdong", "text prompt")
	scenario := flag.String("scenario", "", "optional scenario label (e.g., mitm)")
	sage := flag.Bool("sage", true, "set X-SAGE-Enabled header to sign between Client → Root")
	flag.Parse()

	var path string
	switch strings.ToLower(*domain) {
	case "planning":
		path = "/api/planning"
	case "ordering":
		path = "/api/ordering"
	case "payment":
		path = "/api/payment"
	default:
		path = "/send/prompt"
	}

	reqBody := types.PromptRequest{Prompt: *prompt}
	b, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(*server, "/")+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SAGE-Enabled", fmt.Sprintf("%v", *sage))
	if *scenario != "" {
		req.Header.Set("X-Scenario", *scenario)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, string(data))
		os.Exit(2)
	}

	// Try to decode structured response; fallback to raw
	var pr types.PromptResponse
	if err := json.Unmarshal(data, &pr); err == nil && pr.Response != "" {
		fmt.Println("=== Response ===")
		fmt.Println(pr.Response)
		fmt.Println("\n=== SAGE Verification ===")
		if pr.SAGEVerification != nil {
			fmt.Printf("Verified: %v  SignatureValid: %v  ts=%d\n", pr.SAGEVerification.Verified, pr.SAGEVerification.SignatureValid, pr.SAGEVerification.Timestamp)
		}
		if len(pr.Logs) > 0 {
			fmt.Println("\n=== Logs ===")
			for _, l := range pr.Logs {
				fmt.Printf("[%s] %s -> %s : %s\n", l.Type, l.From, l.To, l.Content)
			}
		}
	} else {
		fmt.Println(string(data))
	}
}
