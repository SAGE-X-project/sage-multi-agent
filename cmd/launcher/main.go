package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sage-x-project/sage-multi-agent/agents/ordering"
	"github.com/sage-x-project/sage-multi-agent/agents/payment"
	"github.com/sage-x-project/sage-multi-agent/agents/planning"
	"github.com/sage-x-project/sage-multi-agent/agents/root"
)

func main() {
	// Define command-line flags
	rootPort := flag.Int("root-port", 8080, "Port for Root Agent")
	planningPort := flag.Int("planning-port", 8081, "Port for Planning Agent")
	orderingPort := flag.Int("ordering-port", 8082, "Port for Ordering Agent")
	paymentPort := flag.Int("payment-port", 8083, "Port for Payment Agent")
	sageEnabled := flag.Bool("sage", true, "Enable SAGE protocol")
	flag.Parse()

	fmt.Println("Starting SAGE Multi-Agent System")
	fmt.Println("================================")
	fmt.Printf("SAGE Protocol: %s\n", map[bool]string{true: "ENABLED", false: "DISABLED"}[*sageEnabled])
	fmt.Println()

	// Create wait group for goroutines
	var wg sync.WaitGroup

	// Channel to capture errors
	errChan := make(chan error, 4)

	// Start Root Agent
	rootAgent := root.NewRootAgent("RootAgent", *rootPort)
	rootAgent.SAGEEnabled = *sageEnabled
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Printf("Starting Root Agent on port %d...\n", *rootPort)
		if err := rootAgent.Start(); err != nil {
			errChan <- fmt.Errorf("Root Agent failed: %v", err)
		}
	}()

	// Wait a moment for root agent to start
	time.Sleep(2 * time.Second)

	// Start Planning Agent
	planningAgent := planning.NewPlanningAgent("PlanningAgent", *planningPort)
	planningAgent.SAGEEnabled = *sageEnabled
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Printf("Starting Planning Agent on port %d...\n", *planningPort)
		if err := planningAgent.Start(); err != nil {
			errChan <- fmt.Errorf("Planning Agent failed: %v", err)
		}
	}()

	// Start Ordering Agent
	orderingAgent := ordering.NewOrderingAgent("OrderingAgent", *orderingPort)
	orderingAgent.SAGEEnabled = *sageEnabled
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Printf("Starting Ordering Agent on port %d...\n", *orderingPort)
		if err := orderingAgent.Start(); err != nil {
			errChan <- fmt.Errorf("Ordering Agent failed: %v", err)
		}
	}()

	// Start Payment Agent
	paymentAgent := payment.NewPaymentAgent("PaymentAgent", *paymentPort)
	paymentAgent.SAGEEnabled = *sageEnabled
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Printf("Starting Payment Agent on port %d...\n", *paymentPort)
		if err := paymentAgent.Start(); err != nil {
			errChan <- fmt.Errorf("Payment Agent failed: %v", err)
		}
	}()

	// Wait for startup
	time.Sleep(2 * time.Second)

	fmt.Println()
	fmt.Println("Multi-Agent System Started Successfully!")
	fmt.Println("========================================")
	fmt.Printf("Root Agent:     http://localhost:%d\n", *rootPort)
	fmt.Printf("Planning Agent: http://localhost:%d\n", *planningPort)
	fmt.Printf("Ordering Agent: http://localhost:%d\n", *orderingPort)
	fmt.Printf("Payment Agent:  http://localhost:%d\n", *paymentPort)
	fmt.Println()
	fmt.Println("API Endpoints:")
	fmt.Println("  POST /process      - Process agent request")
	fmt.Println("  GET  /status       - Get agent status")
	fmt.Println("  POST /toggle-sage  - Toggle SAGE protocol (Root Agent only)")
	fmt.Println("  GET  /ws           - WebSocket connection (Root Agent only)")
	fmt.Println()
	fmt.Println("Press Ctrl+C to shutdown...")

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for shutdown signal or error
	select {
	case sig := <-sigChan:
		fmt.Printf("\nReceived signal: %v\n", sig)
		fmt.Println("Shutting down agents...")
	case err := <-errChan:
		log.Printf("Error occurred: %v\n", err)
		fmt.Println("Shutting down due to error...")
	}

	// Graceful shutdown would go here
	// For now, we'll just exit
	fmt.Println("Multi-Agent System shutdown complete.")
	os.Exit(0)
}