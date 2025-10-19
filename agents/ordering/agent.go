package ordering

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	servermw "github.com/sage-x-project/sage-a2a-go/pkg/server"
	verifier "github.com/sage-x-project/sage-a2a-go/pkg/verifier"
	"github.com/sage-x-project/sage-multi-agent/types"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// OrderingAgent handles product ordering and shopping requests
type OrderingAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool
	products    []Product
	orders      map[string]*Order

	// Inbound DID verification
	didVerifier verifier.DIDVerifier
	didMW       *servermw.DIDAuthMiddleware
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

	response := &types.AgentMessage{
		ID:        fmt.Sprintf("resp-%s", request.ID),
		From:      oa.Name,
		To:        request.From,
		Content:   responseContent,
		Timestamp: request.Timestamp,
		Type:      "response",
		Metadata:  map[string]interface{}{"agent_type": "ordering"},
	}
	return response, nil
}

// handleOrderRequest processes product orders
func (oa *OrderingAgent) handleOrderRequest(content string) string {
	product := oa.findProductByContent(content)
	if product == nil {
		return "Product not found. Please specify a valid product from our catalog."
	}
	if product.Stock <= 0 {
		return fmt.Sprintf("Sorry, %s is currently out of stock.", product.Name)
	}

	orderID := fmt.Sprintf("ORD-%d", len(oa.orders)+1000)
	order := &Order{
		ID:              orderID,
		Products:        []string{product.ID},
		Total:           product.Price,
		ShippingAddress: "123 Main St, Seoul, Korea",
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
	for orderID, order := range oa.orders {
		if strings.Contains(content, orderID) {
			return fmt.Sprintf("Order %s Status: %s\nShipping to: %s",
				orderID, order.Status, order.ShippingAddress)
		}
	}
	return "Order not found. Please provide a valid order ID."
}

// handleGeneralRequest handles general ordering requests
func (oa *OrderingAgent) handleGeneralRequest(_ string) string {
	return "Ordering Agent ready to help! I can process orders, show product catalog, and check order status."
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
	if strings.Contains(contentLower, "sunglasses") {
		for i := range oa.products {
			if oa.products[i].Category == "sunglasses" && oa.products[i].Stock > 0 {
				return &oa.products[i]
			}
		}
	}
	return nil
}

// Start starts the ordering agent server, with DID middleware only on protected routes.
func (oa *OrderingAgent) Start() error {
	if v, err := newLocalDIDVerifier(); err != nil {
		log.Printf("[ordering] DID verifier init failed: %v", err)
	} else {
		oa.didVerifier = v
	}

	// open (no auth)
	open := http.NewServeMux()
	open.HandleFunc("/status", oa.handleStatus)
	open.HandleFunc("/toggle-sage", oa.handleToggleSAGE)

	// protected (auth)
	protected := http.NewServeMux()
	protected.HandleFunc("/process", oa.handleProcessRequest)

	var handler http.Handler = open
	if oa.didVerifier != nil {
		mw := servermw.NewDIDAuthMiddlewareWithVerifier(oa.didVerifier)
		mw.SetOptional(!oa.SAGEEnabled)
		wrapped := mw.Wrap(protected)

		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/status", "/toggle-sage":
				open.ServeHTTP(w, r)
			default:
				wrapped.ServeHTTP(w, r)
			}
		})
		oa.didMW = mw
	}

	log.Printf("Ordering Agent starting on port %d", oa.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", oa.Port), handler)
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
	_ = json.NewEncoder(w).Encode(response)
}

// handleStatus returns the agent status
func (oa *OrderingAgent) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status := map[string]interface{}{
		"name":           oa.Name,
		"sage_enabled":   oa.SAGEEnabled,
		"type":           "ordering",
		"products_count": len(oa.products),
		"orders_count":   len(oa.orders),
		"time":           time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleToggleSAGE toggles SAGE mode and updates DID middleware "optional"
func (oa *OrderingAgent) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	oa.SAGEEnabled = req.Enabled
	if oa.didMW != nil {
		oa.didMW.SetOptional(!oa.SAGEEnabled)
	}
	log.Printf("[ordering] SAGE %v (verify optional=%v)", oa.SAGEEnabled, !oa.SAGEEnabled)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": req.Enabled})
}

// ---------- Local DID verification helpers (file-based) ----------

type localKeys struct {
	pub  map[did.AgentDID]map[did.KeyType]interface{}
	keys map[did.AgentDID][]did.AgentKey
}

type fileEthereumClient struct{ db *localKeys }

func (c *fileEthereumClient) ResolveAllPublicKeys(ctx context.Context, agentDID did.AgentDID) ([]did.AgentKey, error) {
	if keys, ok := c.db.keys[agentDID]; ok {
		return keys, nil
	}
	return nil, fmt.Errorf("no keys for DID: %s", agentDID)
}
func (c *fileEthereumClient) ResolvePublicKeyByType(ctx context.Context, agentDID did.AgentDID, keyType did.KeyType) (interface{}, error) {
	if m, ok := c.db.pub[agentDID]; ok {
		if pk, ok2 := m[keyType]; ok2 {
			return pk, nil
		}
	}
	return nil, fmt.Errorf("key type %v not found for %s", keyType, agentDID)
}

func newLocalDIDVerifier() (verifier.DIDVerifier, error) {
	db, err := loadLocalKeys()
	if err != nil {
		return nil, err
	}
	client := &fileEthereumClient{db: db}
	selector := verifier.NewDefaultKeySelector(client)
	sigVerifier := verifier.NewRFC9421Verifier()
	return verifier.NewDefaultDIDVerifier(client, selector, sigVerifier), nil
}

func loadLocalKeys() (*localKeys, error) {
	path := filepath.Join("keys", "all_keys.json")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var data struct {
		Agents []struct{ DID, PublicKey, Type string } `json:"agents"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	db := &localKeys{pub: make(map[did.AgentDID]map[did.KeyType]interface{}), keys: make(map[did.AgentDID][]did.AgentKey)}
	for _, a := range data.Agents {
		d := did.AgentDID(a.DID)
		if _, ok := db.pub[d]; !ok {
			db.pub[d] = make(map[did.KeyType]interface{})
		}
		pk, err := parseSecp256k1ECDSAPublicKey(a.PublicKey)
		if err != nil {
			log.Printf("[ordering] skip DID %s: %v", a.DID, err)
			continue
		}
		db.pub[d][did.KeyTypeECDSA] = pk
		db.keys[d] = []did.AgentKey{{
			Type:      did.KeyTypeECDSA,
			KeyData:   mustUncompressedECDSA(pk),
			Verified:  true,
			CreatedAt: time.Now(),
		}}
	}
	return db, nil
}

func parseSecp256k1ECDSAPublicKey(hexStr string) (*ecdsa.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid hex pubkey: %w", err)
	}
	pub, err := did.UnmarshalPublicKey(b, "secp256k1")
	if err != nil {
		return nil, fmt.Errorf("unmarshal pubkey: %w", err)
	}
	if ecdsaPK, ok := pub.(*ecdsa.PublicKey); ok {
		return ecdsaPK, nil
	}
	return nil, fmt.Errorf("unexpected key type %T", pub)
}

func mustUncompressedECDSA(pk *ecdsa.PublicKey) []byte {
	byteLen := (pk.Curve.Params().BitSize + 7) / 8
	out := make([]byte, 1+2*byteLen)
	out[0] = 0x04
	pk.X.FillBytes(out[1 : 1+byteLen])
	pk.Y.FillBytes(out[1+byteLen:])
	return out
}
