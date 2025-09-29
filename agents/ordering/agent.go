package ordering

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

// OrderingAgent handles product ordering and shopping requests
type OrderingAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool
	sageManager *adapters.FlexibleSAGEManager // Changed to FlexibleSAGEManager
	products    []Product
	orders      map[string]*Order
}

// Product represents a product in the catalog
type Product struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
	Description string  `json:"description"`
}

// Order represents a customer order
type Order struct {
	ID              string   `json:"id"`
	Products        []string `json:"products"`
	Total           float64  `json:"total"`
	ShippingAddress string   `json:"shipping_address"`
	Status          string   `json:"status"`
}

// NewOrderingAgent creates a new ordering agent instance
func NewOrderingAgent(name string, port int) *OrderingAgent {
	return &OrderingAgent{
		Name:        name,
		Port:        port,
		SAGEEnabled: true,
		products:    initializeProducts(),
		orders:      make(map[string]*Order),
	}
}

// initializeProducts creates sample product catalog
func initializeProducts() []Product {
	return []Product{
		{ID: "P001", Name: "Ray-Ban Aviator", Category: "sunglasses", Price: 150.0, Stock: 50, Description: "Classic aviator sunglasses"},
		{ID: "P002", Name: "Oakley Sport", Category: "sunglasses", Price: 200.0, Stock: 30, Description: "Sports sunglasses with UV protection"},
		{ID: "P003", Name: "Gucci Fashion", Category: "sunglasses", Price: 450.0, Stock: 10, Description: "Designer sunglasses"},
		{ID: "P004", Name: "Samsung Galaxy S24", Category: "phone", Price: 999.0, Stock: 25, Description: "Latest flagship smartphone"},
		{ID: "P005", Name: "iPhone 15 Pro", Category: "phone", Price: 1199.0, Stock: 20, Description: "Premium smartphone"},
		{ID: "P006", Name: "MacBook Pro", Category: "laptop", Price: 2499.0, Stock: 15, Description: "Professional laptop"},
		{ID: "P007", Name: "Dell XPS", Category: "laptop", Price: 1799.0, Stock: 18, Description: "High-performance laptop"},
	}
}

// ProcessRequest processes ordering requests
func (oa *OrderingAgent) ProcessRequest(ctx context.Context, request *types.AgentMessage) (*types.AgentMessage, error) {
	log.Printf("Ordering Agent processing request: %s", request.Content)

	// Verify message if SAGE is enabled (flexible mode allows non-DID messages)
	if oa.SAGEEnabled && oa.sageManager != nil {
		valid, err := oa.sageManager.VerifyMessage(request)
		if err != nil {
			log.Printf("Request verification warning: %v", err)
			// Continue if it's a non-DID entity
		} else if !valid {
			log.Printf("Request verification failed - invalid signature")
			return nil, fmt.Errorf("request verification failed")
		}
	}

	// Process the ordering request
	var responseContent string
	contentLower := strings.ToLower(request.Content)

	if strings.Contains(contentLower, "order") || strings.Contains(contentLower, "buy") {
		responseContent = oa.handleOrderRequest(request.Content)
	} else if strings.Contains(contentLower, "catalog") || strings.Contains(contentLower, "products") {
		responseContent = oa.handleCatalogRequest()
	} else if strings.Contains(contentLower, "status") {
		responseContent = oa.handleOrderStatusRequest(request.Content)
	} else {
		responseContent = oa.handleGeneralRequest(request.Content)
	}

	// Create response
	response := &types.AgentMessage{
		ID:        fmt.Sprintf("resp-%s", request.ID),
		From:      oa.Name,
		To:        request.From,
		Content:   responseContent,
		Timestamp: request.Timestamp,
		Type:      "response",
		Metadata:  map[string]interface{}{"agent_type": "ordering"},
	}

	// Process response with SAGE if enabled
	if oa.SAGEEnabled && oa.sageManager != nil {
		processedResponse, err := oa.sageManager.ProcessMessageWithSAGE(response)
		if err != nil {
			log.Printf("Failed to process response with SAGE: %v", err)
			// Continue without SAGE features for non-DID entities
			return response, nil
		}
		return processedResponse, nil
	}

	return response, nil
}

// handleOrderRequest processes product orders
func (oa *OrderingAgent) handleOrderRequest(content string) string {
	// Extract product from request
	product := oa.findProductByContent(content)

	if product == nil {
		return "Product not found. Please specify a valid product from our catalog."
	}

	if product.Stock <= 0 {
		return fmt.Sprintf("Sorry, %s is currently out of stock.", product.Name)
	}

	// Create order
	orderID := fmt.Sprintf("ORD-%d", len(oa.orders)+1000)
	order := &Order{
		ID:              orderID,
		Products:        []string{product.ID},
		Total:           product.Price,
		ShippingAddress: "123 Main St, Seoul, Korea", // Default address
		Status:          "confirmed",
	}

	oa.orders[orderID] = order
	product.Stock--

	return fmt.Sprintf("Order Confirmed!\nOrder ID: %s\nProduct: %s\nPrice: $%.2f\nShipping to: %s\nStatus: %s",
		orderID, product.Name, product.Price, order.ShippingAddress, order.Status)
}

// handleCatalogRequest returns product catalog
func (oa *OrderingAgent) handleCatalogRequest() string {
	var catalog []string
	catalog = append(catalog, "Product Catalog:")

	categories := make(map[string][]Product)
	for _, product := range oa.products {
		categories[product.Category] = append(categories[product.Category], product)
	}

	for category, products := range categories {
		catalog = append(catalog, fmt.Sprintf("\n%s:", strings.Title(category)))
		for _, p := range products {
			stockStatus := "In Stock"
			if p.Stock <= 0 {
				stockStatus = "Out of Stock"
			}
			catalog = append(catalog, fmt.Sprintf("- %s: $%.2f (%s)", p.Name, p.Price, stockStatus))
		}
	}

	return strings.Join(catalog, "\n")
}

// handleOrderStatusRequest checks order status
func (oa *OrderingAgent) handleOrderStatusRequest(content string) string {
	// Extract order ID from content
	for orderID, order := range oa.orders {
		if strings.Contains(content, orderID) {
			return fmt.Sprintf("Order %s Status: %s\nShipping to: %s",
				orderID, order.Status, order.ShippingAddress)
		}
	}

	return "Order not found. Please provide a valid order ID."
}

// handleGeneralRequest handles general ordering requests
func (oa *OrderingAgent) handleGeneralRequest(content string) string {
	return fmt.Sprintf("Ordering Agent ready to help! I can process orders, show product catalog, and check order status.")
}

// findProductByContent finds a product based on request content
func (oa *OrderingAgent) findProductByContent(content string) *Product {
	contentLower := strings.ToLower(content)

	for i := range oa.products {
		product := &oa.products[i]
		if strings.Contains(contentLower, strings.ToLower(product.Name)) ||
		   strings.Contains(contentLower, strings.ToLower(product.Category)) {
			return product
		}
	}

	// Check for generic terms
	if strings.Contains(contentLower, "sunglasses") {
		for i := range oa.products {
			if oa.products[i].Category == "sunglasses" && oa.products[i].Stock > 0 {
				return &oa.products[i]
			}
		}
	}

	return nil
}

// Start starts the ordering agent server
func (oa *OrderingAgent) Start() error {
	// Initialize SAGE manager if enabled
	if oa.SAGEEnabled {
		sageManager, err := adapters.NewSAGEManagerWithKeyType(oa.Name, "secp256k1")
		if err != nil {
			log.Printf("Failed to initialize SAGE manager: %v", err)
			// Continue without SAGE
		} else {
			// Wrap with FlexibleSAGEManager to allow non-DID entities
			oa.sageManager = adapters.NewFlexibleSAGEManager(sageManager)
			oa.sageManager.SetAllowNonDID(true) // Allow non-DID messages by default
			log.Printf("Flexible SAGE manager initialized for %s (non-DID messages allowed)", oa.Name)
		}
	}

	// Create a new ServeMux for this agent
	mux := http.NewServeMux()
	mux.HandleFunc("/process", oa.handleProcessRequest)
	mux.HandleFunc("/status", oa.handleStatus)

	log.Printf("Ordering Agent starting on port %d", oa.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", oa.Port), mux)
}

// handleProcessRequest handles incoming process requests
func (oa *OrderingAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request types.AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response, err := oa.ProcessRequest(r.Context(), &request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleStatus returns the agent status
func (oa *OrderingAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"name":          oa.Name,
		"sage_enabled":  oa.SAGEEnabled,
		"type":          "ordering",
		"products_count": len(oa.products),
		"orders_count":  len(oa.orders),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}