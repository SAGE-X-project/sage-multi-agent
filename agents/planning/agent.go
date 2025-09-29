package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/adapters"
	"github.com/sage-x-project/sage-multi-agent/types"
)

// PlanningAgent handles travel and accommodation planning requests
type PlanningAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool
	sageManager *adapters.FlexibleSAGEManager // Changed to FlexibleSAGEManager
	hotels      []Hotel
}

// Hotel represents hotel information
type Hotel struct {
	Name     string  `json:"name"`
	Location string  `json:"location"`
	Price    float64 `json:"price"`
	Rating   float64 `json:"rating"`
	URL      string  `json:"url"`
}

// NewPlanningAgent creates a new planning agent instance
func NewPlanningAgent(name string, port int) *PlanningAgent {
	return &PlanningAgent{
		Name:        name,
		Port:        port,
		SAGEEnabled: true,
		hotels:      initializeHotels(),
	}
}

// initializeHotels creates sample hotel data
func initializeHotels() []Hotel {
	return []Hotel{
		{Name: "Grand Hotel Seoul", Location: "Myeongdong", Price: 250.0, Rating: 4.5, URL: "https://grandhotel.com/seoul"},
		{Name: "Plaza Hotel", Location: "Myeongdong", Price: 300.0, Rating: 4.7, URL: "https://plaza.com/seoul"},
		{Name: "Budget Inn", Location: "Myeongdong", Price: 80.0, Rating: 3.5, URL: "https://budgetinn.com/seoul"},
		{Name: "Luxury Suite", Location: "Gangnam", Price: 450.0, Rating: 4.9, URL: "https://luxury.com/seoul"},
		{Name: "City Center Hotel", Location: "Jongno", Price: 180.0, Rating: 4.2, URL: "https://citycenter.com/seoul"},
	}
}

// ProcessRequest processes planning requests
func (pa *PlanningAgent) ProcessRequest(ctx context.Context, request *types.AgentMessage) (*types.AgentMessage, error) {
	log.Printf("Planning Agent processing request: %s", request.Content)

	// Verify message if SAGE is enabled (flexible mode allows non-DID messages)
	if pa.SAGEEnabled && pa.sageManager != nil {
		valid, err := pa.sageManager.VerifyMessage(request)
		if err != nil {
			log.Printf("Request verification warning: %v", err)
			// Continue if it's a non-DID entity
		} else if !valid {
			log.Printf("Request verification failed - invalid signature")
			return nil, fmt.Errorf("request verification failed")
		}
	}

	// Process the planning request
	var responseContent string
	if strings.Contains(strings.ToLower(request.Content), "hotel") ||
	   strings.Contains(strings.ToLower(request.Content), "accommodation") {
		responseContent = pa.handleHotelRequest(request.Content)
	} else {
		responseContent = pa.handleGeneralPlanningRequest(request.Content)
	}

	// Create response
	response := &types.AgentMessage{
		ID:        fmt.Sprintf("resp-%s", request.ID),
		From:      pa.Name,
		To:        request.From,
		Content:   responseContent,
		Timestamp: request.Timestamp,
		Type:      "response",
		Metadata:  map[string]interface{}{"agent_type": "planning"},
	}

	// Process response with SAGE if enabled
	if pa.SAGEEnabled && pa.sageManager != nil {
		processedResponse, err := pa.sageManager.ProcessMessageWithSAGE(response)
		if err != nil {
			log.Printf("Failed to process response with SAGE: %v", err)
			// Continue without SAGE features for non-DID entities
			return response, nil
		}
		return processedResponse, nil
	}

	return response, nil
}

// handleHotelRequest handles hotel booking requests
func (pa *PlanningAgent) handleHotelRequest(content string) string {
	// Find hotels based on location
	location := pa.extractLocation(content)
	relevantHotels := pa.findHotelsByLocation(location)

	if len(relevantHotels) == 0 {
		return fmt.Sprintf("No hotels found in %s. Please try another location.", location)
	}

	// Format response with hotel recommendations
	var recommendations []string
	recommendations = append(recommendations, fmt.Sprintf("Hotel recommendations for %s:", location))

	for i, hotel := range relevantHotels {
		if i >= 3 { // Limit to top 3 recommendations
			break
		}
		recommendations = append(recommendations, fmt.Sprintf(
			"%d. %s - $%.2f/night, Rating: %.1f/5 - Book at: %s",
			i+1, hotel.Name, hotel.Price, hotel.Rating, hotel.URL,
		))
	}

	return strings.Join(recommendations, "\n")
}

// handleGeneralPlanningRequest handles general planning requests
func (pa *PlanningAgent) handleGeneralPlanningRequest(content string) string {
	return fmt.Sprintf("Planning Agent received your request: '%s'. I can help with hotel bookings, travel planning, and accommodation arrangements.", content)
}

// extractLocation extracts location from request content
func (pa *PlanningAgent) extractLocation(content string) string {
	// Common locations
	locations := []string{"myeongdong", "gangnam", "jongno", "hongdae", "itaewon"}

	contentLower := strings.ToLower(content)
	for _, loc := range locations {
		if strings.Contains(contentLower, loc) {
			return strings.Title(loc)
		}
	}

	// Default to Myeongdong if no specific location found
	if strings.Contains(contentLower, "seoul") || strings.Contains(contentLower, "hotel") {
		return "Myeongdong"
	}

	return "Seoul"
}

// findHotelsByLocation finds hotels in a specific location
func (pa *PlanningAgent) findHotelsByLocation(location string) []Hotel {
	var results []Hotel

	for _, hotel := range pa.hotels {
		if strings.EqualFold(hotel.Location, location) || location == "Seoul" {
			results = append(results, hotel)
		}
	}

	// Sort by rating (simple bubble sort for small dataset)
	for i := 0; i < len(results)-1; i++ {
		for j := 0; j < len(results)-i-1; j++ {
			if results[j].Rating < results[j+1].Rating {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}

	return results
}

// Start starts the planning agent server
func (pa *PlanningAgent) Start() error {
	// Initialize SAGE manager if enabled
	if pa.SAGEEnabled {
		sageManager, err := adapters.NewSAGEManagerWithKeyType(pa.Name, "secp256k1")
		if err != nil {
			log.Printf("Failed to initialize SAGE manager: %v", err)
			// Continue without SAGE
		} else {
			// Wrap with FlexibleSAGEManager to allow non-DID entities
			pa.sageManager = adapters.NewFlexibleSAGEManager(sageManager)
			pa.sageManager.SetAllowNonDID(true) // Allow non-DID messages by default
			log.Printf("Flexible SAGE manager initialized for %s (non-DID messages allowed)", pa.Name)
		}
	}

	// Create a new ServeMux for this agent
	mux := http.NewServeMux()
	mux.HandleFunc("/process", pa.handleProcessRequest)
	mux.HandleFunc("/status", pa.handleStatus)

	log.Printf("Planning Agent starting on port %d", pa.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", pa.Port), mux)
}

// handleProcessRequest handles incoming process requests
func (pa *PlanningAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request types.AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response, err := pa.ProcessRequest(r.Context(), &request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleStatus returns the agent status
func (pa *PlanningAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"name":         pa.Name,
		"sage_enabled": pa.SAGEEnabled,
		"type":         "planning",
		"hotels_count": len(pa.hotels),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}