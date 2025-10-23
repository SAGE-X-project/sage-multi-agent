package ordering

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"

	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
	"github.com/sage-x-project/sage/pkg/agent/did"
)

// OrderingAgent handles ordering IN-PROC,
// and (optionally) forwards to an EXTERNAL ordering service.
// Only outbound (agent -> external) is signed when SAGE is ON.
type OrderingAgent struct {
	Name        string
	SAGEEnabled bool

	ExternalURL string // e.g. http://external-ordering:19082 (empty => local only)

	myDID did.AgentDID
	myKey sagecrypto.KeyPair
	a2a   *a2aclient.A2AClient

	httpClient *http.Client

	products []Product
	orders   map[string]*Order
}

type Product struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
	Description string  `json:"description"`
}
type Order struct {
	ID              string   `json:"id"`
	Products        []string `json:"products"`
	Total           float64  `json:"total"`
	ShippingAddress string   `json:"shipping_address"`
	Status          string   `json:"status"`
}

func NewOrderingAgent(name string) *OrderingAgent {
	return &OrderingAgent{
		Name:        name,
		SAGEEnabled: envBool("ORDERING_SAGE_ENABLED", true),
		ExternalURL: strings.TrimRight(envOr("ORDERING_EXTERNAL_URL", ""), "/"),
		httpClient:  http.DefaultClient,
		products:    initProducts(),
		orders:      make(map[string]*Order),
	}
}

// IN-PROC entrypoint (Root -> Ordering)
func (oa *OrderingAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	// Prefer external if configured
	if oa.ExternalURL != "" {
		if out, err := oa.forwardExternal(ctx, &msg); err == nil {
			return *out, nil
		}
	}

	// Local behavior
	c := strings.ToLower(msg.Content)
	var content string
	switch {
	case strings.Contains(c, "order") || strings.Contains(c, "buy"):
		content = oa.handleOrderRequest(msg.Content)
	case strings.Contains(c, "catalog") || strings.Contains(c, "products"):
		content = oa.handleCatalogRequest()
	case strings.Contains(c, "status"):
		content = oa.handleOrderStatusRequest(msg.Content)
	default:
		content = "Ordering Agent ready to help! I can process orders, show product catalog, and check order status."
	}
	return types.AgentMessage{
		ID:        "resp-" + msg.ID,
		From:      oa.Name,
		To:        msg.From,
		Type:      "response",
		Content:   content,
		Timestamp: time.Now(),
		Metadata:  map[string]any{"agent_type": "ordering"},
	}, nil
}

// ---- Outbound (agent -> external) ----

func (oa *OrderingAgent) forwardExternal(ctx context.Context, msg *types.AgentMessage) (*types.AgentMessage, error) {
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oa.ExternalURL+"/process", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Digest", a2autil.ComputeContentDigest(body))

	resp, err := oa.do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	var out types.AgentMessage
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)

	if resp.StatusCode/100 != 2 {
		return &types.AgentMessage{
			ID:        msg.ID + "-exterr",
			From:      "external-ordering",
			To:        msg.From,
			Type:      "error",
			Content:   fmt.Sprintf("external error: %d %s", resp.StatusCode, strings.TrimSpace(buf.String())),
			Timestamp: time.Now(),
		}, nil
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		out = types.AgentMessage{
			ID:        msg.ID + "-ext",
			From:      "external-ordering",
			To:        msg.From,
			Type:      "response",
			Content:   strings.TrimSpace(buf.String()),
			Timestamp: time.Now(),
		}
	}
	return &out, nil
}

func (oa *OrderingAgent) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if oa.SAGEEnabled {
		if oa.a2a == nil {
			if err := oa.initSigning(); err != nil {
				return nil, err
			}
		}
		return oa.a2a.Do(ctx, req)
	}
	return oa.httpClient.Do(req)
}

func (oa *OrderingAgent) initSigning() error {
	jwk := strings.TrimSpace(os.Getenv("ORDERING_JWK_FILE"))
	if jwk == "" {
		return fmt.Errorf("ORDERING_JWK_FILE required for SAGE signing")
	}
	raw, err := os.ReadFile(jwk)
	if err != nil {
		return fmt.Errorf("read ORDERING_JWK_FILE: %w", err)
	}
	imp := formats.NewJWKImporter()
	kp, err := imp.Import(raw, sagecrypto.KeyFormatJWK)
	if err != nil {
		return fmt.Errorf("import ordering JWK: %w", err)
	}

	didStr := strings.TrimSpace(os.Getenv("ORDERING_DID"))
	if didStr == "" {
		if ecdsaPriv, ok := kp.PrivateKey().(*ecdsa.PrivateKey); ok {
			addr := ethcrypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex()
			didStr = "did:sage:ethereum:" + addr
		} else if id := strings.TrimSpace(kp.ID()); id != "" {
			didStr = "did:sage:generated:" + id
		} else {
			return fmt.Errorf("ORDERING_DID not set and cannot derive from key")
		}
	}

	oa.myKey = kp
	oa.myDID = did.AgentDID(didStr)
	oa.a2a = a2aclient.NewA2AClient(oa.myDID, oa.myKey, http.DefaultClient)
	return nil
}

// ---- Local helpers (unchanged UX) ----

func initProducts() []Product {
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

func (oa *OrderingAgent) handleOrderRequest(content string) string {
	p := oa.findProductByContent(content)
	if p == nil {
		return "Product not found. Please specify a valid product from our catalog."
	}
	if p.Stock <= 0 {
		return fmt.Sprintf("Sorry, %s is currently out of stock.", p.Name)
	}
	orderID := fmt.Sprintf("ORD-%d", len(oa.orders)+1000)
	order := &Order{
		ID:              orderID,
		Products:        []string{p.ID},
		Total:           p.Price,
		ShippingAddress: "123 Main St, Seoul, Korea",
		Status:          "confirmed",
	}
	oa.orders[orderID] = order
	p.Stock--
	return fmt.Sprintf("Order Confirmed!\nOrder ID: %s\nProduct: %s\nPrice: $%.2f\nShipping to: %s\nStatus: %s",
		orderID, p.Name, p.Price, order.ShippingAddress, order.Status)
}

func (oa *OrderingAgent) handleCatalogRequest() string {
	var lines []string
	lines = append(lines, "Product Catalog:")
	cats := make(map[string][]Product)
	for _, p := range oa.products {
		cats[p.Category] = append(cats[p.Category], p)
	}
	for cat, ps := range cats {
		lines = append(lines, fmt.Sprintf("\n%s:", strings.Title(cat)))
		for _, p := range ps {
			stock := "In Stock"
			if p.Stock <= 0 {
				stock = "Out of Stock"
			}
			lines = append(lines, fmt.Sprintf("- %s: $%.2f (%s)", p.Name, p.Price, stock))
		}
	}
	return strings.Join(lines, "\n")
}

func (oa *OrderingAgent) handleOrderStatusRequest(content string) string {
	for id, o := range oa.orders {
		if strings.Contains(content, id) {
			return fmt.Sprintf("Order %s Status: %s\nShipping to: %s", id, o.Status, o.ShippingAddress)
		}
	}
	return "Order not found. Please provide a valid order ID."
}

func (oa *OrderingAgent) findProductByContent(content string) *Product {
	c := strings.ToLower(content)
	for i := range oa.products {
		p := &oa.products[i]
		if strings.Contains(c, strings.ToLower(p.Name)) || strings.Contains(c, strings.ToLower(p.Category)) {
			return p
		}
	}
	if strings.Contains(c, "sunglasses") {
		for i := range oa.products {
			if oa.products[i].Category == "sunglasses" && oa.products[i].Stock > 0 {
				return &oa.products[i]
			}
		}
	}
	return nil
}

// ---- utils ----

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envBool(k string, d bool) bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(k))); v != "" {
		return v == "1" || v == "true" || v == "on" || v == "yes"
	}
	return d
}
