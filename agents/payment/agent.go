package payment

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

// PaymentAgent handles cryptocurrency and payment requests
type PaymentAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool
	sageManager *adapters.FlexibleSAGEManager // Changed to FlexibleSAGEManager
	wallets     map[string]*Wallet
	transactions map[string]*Transaction
}

// Wallet represents a cryptocurrency wallet
type Wallet struct {
	Address string            `json:"address"`
	Owner   string            `json:"owner"`
	Balance map[string]float64 `json:"balance"` // currency -> amount
}

// Transaction represents a payment transaction
type Transaction struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"`
	TxHash   string  `json:"tx_hash"`
}

// NewPaymentAgent creates a new payment agent instance
func NewPaymentAgent(name string, port int) *PaymentAgent {
	return &PaymentAgent{
		Name:         name,
		Port:         port,
		SAGEEnabled:  true,
		wallets:      initializeWallets(),
		transactions: make(map[string]*Transaction),
	}
}

// initializeWallets creates sample wallet data
func initializeWallets() map[string]*Wallet {
	wallets := make(map[string]*Wallet)

	// User wallet
	wallets["user"] = &Wallet{
		Address: "0x1234567890abcdef1234567890abcdef12345678",
		Owner:   "User",
		Balance: map[string]float64{
			"ETH":  5.0,
			"USDC": 1000.0,
			"USDT": 1000.0,
		},
	}

	// Merchant wallet
	wallets["merchant"] = &Wallet{
		Address: "0xabcdef1234567890abcdef1234567890abcdef12",
		Owner:   "Merchant",
		Balance: map[string]float64{
			"ETH":  10.0,
			"USDC": 5000.0,
			"USDT": 5000.0,
		},
	}

	// Agent wallet
	wallets["agent"] = &Wallet{
		Address: "0x9876543210fedcba9876543210fedcba98765432",
		Owner:   "PaymentAgent",
		Balance: map[string]float64{
			"ETH":  1.0,
			"USDC": 100.0,
			"USDT": 100.0,
		},
	}

	return wallets
}

// ProcessRequest processes payment requests
func (pa *PaymentAgent) ProcessRequest(ctx context.Context, request *types.AgentMessage) (*types.AgentMessage, error) {
	log.Printf("Payment Agent processing request: %s", request.Content)

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

	// Process the payment request
	var responseContent string
	contentLower := strings.ToLower(request.Content)

	if strings.Contains(contentLower, "buy") || strings.Contains(contentLower, "purchase") {
		responseContent = pa.handlePurchaseRequest(request.Content)
	} else if strings.Contains(contentLower, "send") || strings.Contains(contentLower, "transfer") {
		responseContent = pa.handleTransferRequest(request.Content)
	} else if strings.Contains(contentLower, "balance") {
		responseContent = pa.handleBalanceRequest()
	} else if strings.Contains(contentLower, "transaction") || strings.Contains(contentLower, "status") {
		responseContent = pa.handleTransactionStatusRequest(request.Content)
	} else {
		responseContent = pa.handleGeneralRequest(request.Content)
	}

	// Create response
	response := &types.AgentMessage{
		ID:        fmt.Sprintf("resp-%s", request.ID),
		From:      pa.Name,
		To:        request.From,
		Content:   responseContent,
		Timestamp: request.Timestamp,
		Type:      "response",
		Metadata:  map[string]interface{}{"agent_type": "payment"},
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

// handlePurchaseRequest processes cryptocurrency purchase requests
func (pa *PaymentAgent) handlePurchaseRequest(content string) string {
	// Extract amount and currency from request
	amount, currency := pa.extractPaymentDetails(content)

	if amount <= 0 {
		return "Please specify a valid amount to purchase."
	}

	if currency == "" {
		currency = "USDC" // Default to USDC
	}

	// Check user balance (assuming USD payment)
	userWallet := pa.wallets["user"]
	requiredUSD := amount * pa.getExchangeRate(currency)

	if userWallet.Balance["USDC"] < requiredUSD {
		return fmt.Sprintf("Insufficient funds. You need $%.2f USDC but have $%.2f",
			requiredUSD, userWallet.Balance["USDC"])
	}

	// Create transaction
	txID := fmt.Sprintf("TX-%d", len(pa.transactions)+1000)
	tx := &Transaction{
		ID:       txID,
		From:     userWallet.Address,
		To:       pa.wallets["merchant"].Address,
		Amount:   amount,
		Currency: currency,
		Status:   "confirmed",
		TxHash:   fmt.Sprintf("0x%s", pa.generateTxHash()),
	}

	pa.transactions[txID] = tx

	// Update balances
	userWallet.Balance["USDC"] -= requiredUSD
	userWallet.Balance[currency] += amount

	return fmt.Sprintf("Purchase Confirmed!\nTransaction ID: %s\nPurchased: %.2f %s\nCost: $%.2f USDC\nTx Hash: %s\nNew %s Balance: %.2f",
		txID, amount, currency, requiredUSD, tx.TxHash, currency, userWallet.Balance[currency])
}

// handleTransferRequest processes transfer requests
func (pa *PaymentAgent) handleTransferRequest(content string) string {
	amount, currency := pa.extractPaymentDetails(content)

	if amount <= 0 {
		return "Please specify a valid amount to transfer."
	}

	if currency == "" {
		currency = "USDC"
	}

	userWallet := pa.wallets["user"]

	if userWallet.Balance[currency] < amount {
		return fmt.Sprintf("Insufficient %s balance. You have %.2f %s",
			currency, userWallet.Balance[currency], currency)
	}

	// Extract recipient address (default to merchant)
	recipientAddress := pa.wallets["merchant"].Address

	// Create transaction
	txID := fmt.Sprintf("TX-%d", len(pa.transactions)+1000)
	tx := &Transaction{
		ID:       txID,
		From:     userWallet.Address,
		To:       recipientAddress,
		Amount:   amount,
		Currency: currency,
		Status:   "pending",
		TxHash:   fmt.Sprintf("0x%s", pa.generateTxHash()),
	}

	pa.transactions[txID] = tx

	// Update balance
	userWallet.Balance[currency] -= amount
	tx.Status = "confirmed"

	return fmt.Sprintf("Transfer Initiated!\nTransaction ID: %s\nFrom: %s\nTo: %s\nAmount: %.2f %s\nStatus: %s\nTx Hash: %s",
		txID, tx.From[:10]+"...", tx.To[:10]+"...", amount, currency, tx.Status, tx.TxHash)
}

// handleBalanceRequest returns wallet balance
func (pa *PaymentAgent) handleBalanceRequest() string {
	userWallet := pa.wallets["user"]

	var balances []string
	balances = append(balances, "Your Wallet Balance:")
	balances = append(balances, fmt.Sprintf("Address: %s", userWallet.Address))

	for currency, balance := range userWallet.Balance {
		value := balance * pa.getExchangeRate(currency)
		balances = append(balances, fmt.Sprintf("- %s: %.4f (~$%.2f USD)", currency, balance, value))
	}

	return strings.Join(balances, "\n")
}

// handleTransactionStatusRequest checks transaction status
func (pa *PaymentAgent) handleTransactionStatusRequest(content string) string {
	// Look for transaction ID in content
	for txID, tx := range pa.transactions {
		if strings.Contains(content, txID) {
			return fmt.Sprintf("Transaction %s:\nStatus: %s\nAmount: %.2f %s\nFrom: %s\nTo: %s\nTx Hash: %s",
				txID, tx.Status, tx.Amount, tx.Currency,
				tx.From[:10]+"...", tx.To[:10]+"...", tx.TxHash)
		}
	}

	// Return recent transactions if no specific ID
	if len(pa.transactions) > 0 {
		var recent []string
		recent = append(recent, "Recent Transactions:")

		count := 0
		for txID, tx := range pa.transactions {
			if count >= 3 {
				break
			}
			recent = append(recent, fmt.Sprintf("- %s: %.2f %s (%s)",
				txID, tx.Amount, tx.Currency, tx.Status))
			count++
		}

		return strings.Join(recent, "\n")
	}

	return "No transactions found."
}

// handleGeneralRequest handles general payment requests
func (pa *PaymentAgent) handleGeneralRequest(content string) string {
	return "Payment Agent ready! I can help with cryptocurrency purchases, transfers, and balance inquiries. Supported currencies: ETH, USDC, USDT."
}

// extractPaymentDetails extracts amount and currency from request
func (pa *PaymentAgent) extractPaymentDetails(content string) (float64, string) {
	var amount float64
	var currency string

	// Try to extract amount (look for numbers)
	contentLower := strings.ToLower(content)

	// Common amounts
	if strings.Contains(contentLower, "100") {
		amount = 100
	} else if strings.Contains(contentLower, "50") {
		amount = 50
	} else if strings.Contains(contentLower, "10") {
		amount = 10
	} else if strings.Contains(contentLower, "1000") {
		amount = 1000
	}

	// Extract currency
	if strings.Contains(contentLower, "eth") || strings.Contains(contentLower, "ethereum") {
		currency = "ETH"
	} else if strings.Contains(contentLower, "usdc") || strings.Contains(contentLower, "stable") {
		currency = "USDC"
	} else if strings.Contains(contentLower, "usdt") || strings.Contains(contentLower, "tether") {
		currency = "USDT"
	}

	// Default amount if not found
	if amount == 0 && (strings.Contains(contentLower, "buy") || strings.Contains(contentLower, "purchase")) {
		amount = 100 // Default $100
	}

	return amount, currency
}

// getExchangeRate returns exchange rate for currency
func (pa *PaymentAgent) getExchangeRate(currency string) float64 {
	rates := map[string]float64{
		"ETH":  2500.0, // $2500 per ETH
		"USDC": 1.0,    // $1 per USDC
		"USDT": 1.0,    // $1 per USDT
	}

	if rate, exists := rates[currency]; exists {
		return rate
	}
	return 1.0
}

// generateTxHash generates a mock transaction hash
func (pa *PaymentAgent) generateTxHash() string {
	const charset = "0123456789abcdef"
	hash := make([]byte, 64)
	for i := range hash {
		hash[i] = charset[i%16]
	}
	return string(hash)
}

// Start starts the payment agent server
func (pa *PaymentAgent) Start() error {
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

	log.Printf("Payment Agent starting on port %d", pa.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", pa.Port), mux)
}

// handleProcessRequest handles incoming process requests
func (pa *PaymentAgent) handleProcessRequest(w http.ResponseWriter, r *http.Request) {
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
func (pa *PaymentAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"name":              pa.Name,
		"sage_enabled":      pa.SAGEEnabled,
		"type":              "payment",
		"wallets_count":     len(pa.wallets),
		"transactions_count": len(pa.transactions),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}