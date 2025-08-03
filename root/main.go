package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sage-multi-agent/sage"
	"github.com/sage-multi-agent/websocket"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/googleai"
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type rootAgentProcessor struct {
	// LLM client for decision making
	llm *googleai.GoogleAI
	// Subagent clients
	orderingClient      *client.A2AClient
	planningClient      *client.A2AClient
	// WebSocket server for logs
	wsServer *websocket.LogServer
}

func (p *rootAgentProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handle taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	// Extract text from the incoming message
	text := extractText(message)
	if text == "" {
		errMsg := "input message must contain text"
		log.Error("Message processing failed: %s", errMsg)

		// Return error message directly
		errorMessage := protocol.NewMessage(
			protocol.MessageRoleAgent,
			[]protocol.Part{protocol.NewTextPart(errMsg)},
		)
		return &taskmanager.MessageProcessingResult{
			Result: &errorMessage,
		}, nil
	}

	logMsg := fmt.Sprintf("RootAgent received new request: %s", text)
	log.Info(logMsg)
	if p.wsServer != nil {
		p.wsServer.BroadcastLog(logMsg)
	}

	// Use Gemini or rule-based routing to decide which subagent to route the task to
	subagent, err := p.routeTaskToSubagent(ctx, text)
	if err != nil {
		log.Error("Error routing task: %v", err)
		errMsg := fmt.Sprintf("Failed to process your request: %v", err)
		errMessage := protocol.NewMessage(
			protocol.MessageRoleAgent,
			[]protocol.Part{protocol.NewTextPart(errMsg)},
		)
		return &taskmanager.MessageProcessingResult{
			Result: &errMessage,
		}, nil
	}

	var result string

	// Forward the task to the appropriate subagent
	switch subagent {
	case "ordering":
		logMsg := "Routing to ordering agent."
		log.Info(logMsg)
		if p.wsServer != nil {
			p.wsServer.BroadcastLog(logMsg)
		}
		result, err = p.callOrderingAgent(ctx, text)
	case "planning":
		logMsg := "Routing to planning agent."
		log.Info(logMsg)
		if p.wsServer != nil {
			p.wsServer.BroadcastLog(logMsg)
		}
		result, err = p.callPlanningAgent(ctx, text)
	default:
		// Handle using the root agent's own logic if no specific subagent was identified
		log.Info("No specific subagent identified, handling with root agent.")
		result = fmt.Sprintf("I'm not sure how to process your request: '%s'. You can try asking me to order something, or plan something.", text)
		err = nil
	}

	if err != nil {
		log.Errorf("Error from subagent: %v", err)
		errMsg := fmt.Sprintf("Failed to get response from subagent: %v", err)
		errMessage := protocol.NewMessage(
			protocol.MessageRoleAgent,
			[]protocol.Part{protocol.NewTextPart(errMsg)},
		)
		return &taskmanager.MessageProcessingResult{
			Result: &errMessage,
		}, nil
	}

	// Create response message
	responseMessage := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]protocol.Part{protocol.NewTextPart(result)},
	)

	return &taskmanager.MessageProcessingResult{
		Result: &responseMessage,
	}, nil
}

// callOrderingAgent forwards a task to the ordering agent.
func (p *rootAgentProcessor) callOrderingAgent(ctx context.Context, text string) (string, error) {
	// Create the message to send
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart(text)},
	)

	// Send the message to the ordering agent
	params := protocol.SendMessageParams{
		Message: message,
	}
	result, err := p.orderingClient.SendMessage(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to send message to ordering agent: %w", err)
	}

	// Handle the response based on its type
	switch result.Result.GetKind() {
	case protocol.KindMessage:
		msg := result.Result.(*protocol.Message)
		return extractText(*msg), nil
	case protocol.KindTask:
		task := result.Result.(*protocol.Task)
		if task.Status.Message != nil {
			return extractText(*task.Status.Message), nil
		}
		return "", fmt.Errorf("no response message from ordering agent")
	default:
		return "", fmt.Errorf("unexpected response type from ordering agent: %T", result.Result)
	}
}

// callPlanningAgent forwards a task to the planning agent.
func (p *rootAgentProcessor) callPlanningAgent(ctx context.Context, text string) (string, error) {
	// Create the message to send
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart(text)},
	)

	// Send the message to the ordering agent
	params := protocol.SendMessageParams{
		Message: message,
	}
	result, err := p.planningClient.SendMessage(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to send message to planning agent: %w", err)
	}

	// Handle the response based on its type
	switch result.Result.GetKind() {
	case protocol.KindMessage:
		msg := result.Result.(*protocol.Message)
		return extractText(*msg), nil
	case protocol.KindTask:
		task := result.Result.(*protocol.Task)
		if task.Status.Message != nil {
			return extractText(*task.Status.Message), nil
		}
		return "", fmt.Errorf("no response message from ordering agent")
	default:
		return "", fmt.Errorf("unexpected response type from ordering agent: %T", result.Result)
	}
}


// getAgentCard returns the agent's metadata.
func getAgentCard() server.AgentCard {
	return server.AgentCard{
		Name:        "Multi-Agent Router",
		Description: "An agent that routes tasks to appropriate subagents.",
		URL:         "http://localhost:8080",
		Version:     "1.0.0",
		Capabilities: server.AgentCapabilities{
			Streaming:              boolPtr(false),
			PushNotifications:      boolPtr(false),
			StateTransitionHistory: boolPtr(true),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []server.AgentSkill{
			{
				ID:          "route",
				Name:        "Task Routing",
				Description: stringPtr("Routes tasks to the appropriate specialized agent."),
				Tags:        []string{"routing", "multi-agent", "orchestration"},
				Examples: []string{
					"Order a pizza to my home",
					"Plan a trip to Tokyo",
				},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
		},
	}
}

func (p *rootAgentProcessor) routeTaskToSubagent(ctx context.Context, text string) (string, error) {
	if p.llm == nil {
		// Simple rule-based routing if LLM is not available
		text = strings.ToLower(text)
		if strings.Contains(text, "order") || strings.Contains(text, "buy") ||
			strings.Contains(text, "purchase") || strings.Contains(text, "shopping") {
			return "ordering", nil
		} else if strings.Contains(text, "plan") || strings.Contains(text, "schedule") ||
			strings.Contains(text, "organize") || strings.Contains(text, "trip") {
			return "planning", nil
		}
		return "", nil
	}

	// Use Gemini LLM to determine which subagent should handle the request
	prompt := fmt.Sprintf(
		"Based on the following user request, determine which agent should handle it:\n\n"+
			"User request: %s\n\n"+
			"Available agents:\n"+
			"1. 'ordering' - Ordering agent that can order, buy, or purchase things\n"+
			"2. 'planning' - Planning agent that can plan trips\n"+
			"Respond with ONLY one word: 'ordering', 'planning', or 'none' if no agent is applicable.",
		text,
	)

	completion, err := p.llm.Call(ctx, prompt, llms.WithTemperature(0))
	if err != nil {
		return "", fmt.Errorf("LLM error: %v", err)
	}

	// Extract and normalize the agent name
	subagent := strings.ToLower(strings.TrimSpace(completion))
	if subagent == "none" {
		return "", nil
	}

	// Validate that the response is one of our expected agents
	validAgents := map[string]bool{
		"ordering":      true,
		"planning":      true,
	}

	if _, ok := validAgents[subagent]; !ok {
		return "", nil
	}

	return subagent, nil
}

// extractText extracts the text content from a message.
func extractText(message protocol.Message) string {
	var result strings.Builder
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			result.WriteString(textPart.Text)
		}
	}
	return result.String()
}

// stringPtr is a helper function to get a pointer to a string.
func stringPtr(s string) *string {
	return &s
}

// boolPtr is a helper function to get a pointer to a bool.
func boolPtr(b bool) *bool {
	return &b
}


func main() {
	port := flag.Int("port", 8080, "Port to listen on for the root agent")
	// set the url of gateway server
	orderingAgentURL := flag.String("ordering-url", "http://localhost:8083", "URL for the ordering agent")
	planningAgentURL := flag.String("planning-url", "http://localhost:8084", "URL for the planning agent")
	wsPort := flag.Int("ws-port", 8085, "Port to listen on for WebSocket connections")
	flag.Parse()

	// Create the processor
	processor := &rootAgentProcessor{}

	// Initialize subagent clients
	var err error
	// TODO: valid  DID
	if orderingReqHandler, err := sage.NewSageHttpRequestHandler("did:sage:ethereum:0x742d35Cc6634C0532925a3b844Bc9e7595f7F1a");err != nil {
		log.Fatal("Failed to create ordering request handler: %v", err)
	} else if processor.orderingClient, err = client.NewA2AClient(*orderingAgentURL,
		client.WithHTTPReqHandler(orderingReqHandler),
	); err != nil {
		log.Fatal("Failed to create ordering agent client: %v", err)
	}
	
	// TODO : valid DID
	if planningReqHandler, err := sage.NewSageHttpRequestHandler("did:sage:ethereum:0x842d35Cc6634C0532925a3b844Bc9e7595f7F1a");err != nil {
		log.Fatal("Failed to create ordering request handler: %v", err)
	} else if processor.planningClient, err = client.NewA2AClient(*planningAgentURL,
		client.WithHTTPReqHandler(planningReqHandler),
	); err != nil {
		log.Fatal("Failed to create planning agent client: %v", err)
	}

	// Try to initialize the LLM if an API key is available
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey != "" {
		ctx := context.Background()
		processor.llm, err = googleai.New(
			ctx,
			googleai.WithAPIKey(apiKey),
			googleai.WithDefaultModel("gemini-1.5-flash"),
		)
		if err != nil {
			log.Warn("Failed to initialize Gemini LLM: %v. Will use rule-based routing.", err)
		} else {
			log.Info("Successfully initialized Gemini LLM for task routing.")
		}
	} else {
		log.Info("No GOOGLE_API_KEY environment variable found. Using rule-based routing.")
	}

	// Create WebSocket server
	wsServer := websocket.NewLogServer(*wsPort)
	processor.wsServer = wsServer
	if err := processor.wsServer.Start(); err != nil {
		log.Fatal("Failed to start WebSocket server: %v", err)
	}

	// Create task manager with our processor
	taskManager, err := taskmanager.NewMemoryTaskManager(processor)
	if err != nil {
		log.Fatal("Failed to create task manager: %v", err)
	}

	// Create the A2A server
	agentCard := getAgentCard()
	a2aServer, err := server.NewA2AServer(agentCard, taskManager)
	if err != nil {
		log.Fatal("Failed to create A2A server: %v", err)
	}

	// Set up a channel to listen for termination signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine
	go func() {
	addr := fmt.Sprintf(":%d", *port)
	log.Info("Starting Root Agent server on %s", addr)
	if err := a2aServer.Start(addr); err != nil {
		log.Fatal("Failed to start A2A server: %v", err)
	}
	}()

	// Wait for termination signal
	sig := <-sigChan
	log.Info("Received signal %v, shutting down...", sig)

	// Stop the WebSocket server
	if processor.wsServer != nil {
		if err := processor.wsServer.Stop(); err != nil {
			log.Error("Failed to stop WebSocket server: %v", err)
		}
	}
}