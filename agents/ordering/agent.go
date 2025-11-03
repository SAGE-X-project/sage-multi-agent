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

	// Use internal agent framework for key management
	"github.com/sage-x-project/sage-a2a-go/pkg/agent/framework/keys"
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
)

// OrderingAgent handles e-commerce orders and delivery requests IN-PROC,
// and (optionally) forwards to an EXTERNAL ordering service.
// Only the outbound call (agent -> external) is A2A-signed when SAGE is ON.
type OrderingAgent struct {
	Name        string
	SAGEEnabled bool

	ExternalURL string // e.g. http://external-ordering:19084 (empty => local only)

	// Outbound signing
	myDID sagedid.AgentDID
	myKey keys.KeyPair
	a2a   *a2aclient.A2AClient

	httpClient *http.Client

	products []Product
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
	OrderID         string    `json:"orderId"`
	ProductID       string    `json:"productId"`
	ProductName     string    `json:"productName"`
	Quantity        int       `json:"quantity"`
	TotalPrice      float64   `json:"totalPrice"`
	ShippingAddress string    `json:"shippingAddress"`
	Status          string    `json:"status"`
	EstimatedDelivery string  `json:"estimatedDelivery"`
	CreatedAt       time.Time `json:"createdAt"`
}

func NewOrderingAgent(name string) *OrderingAgent {
	return &OrderingAgent{
		Name:        name,
		SAGEEnabled: envBool("ORDERING_SAGE_ENABLED", true),
		ExternalURL: strings.TrimRight(envOr("ORDERING_EXTERNAL_URL", ""), "/"),
		httpClient:  http.DefaultClient,
		products:    initProducts(),
	}
}

// IN-PROC entrypoint (Root -> Ordering)
func (oa *OrderingAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	// If external URL is set, prefer forwarding to external service.
	if oa.ExternalURL != "" {
		if out, err := oa.forwardExternal(ctx, &msg); err == nil {
			return *out, nil
		}
		// On external error, gracefully fall back to local logic.
	}

	// Local logic (same UX as before)
	content := strings.ToLower(msg.Content)
	var responseContent string

	if strings.Contains(content, "주문") || strings.Contains(content, "order") ||
	   strings.Contains(content, "구매") || strings.Contains(content, "배송") {
		responseContent = oa.handleOrderRequest(msg.Content)
	} else if strings.Contains(content, "상품") || strings.Contains(content, "product") ||
	          strings.Contains(content, "목록") || strings.Contains(content, "list") {
		responseContent = oa.handleProductListRequest()
	} else {
		responseContent = oa.handleGeneralOrderingRequest(msg.Content)
	}

	return types.AgentMessage{
		ID:        "resp-" + msg.ID,
		From:      oa.Name,
		To:        msg.From,
		Type:      "response",
		Content:   responseContent,
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
	// Optional body integrity (tamper detection)
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
	// Initialize signing client lazily (only when SAGE ON + actually needed)
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
	kp, err := keys.LoadFromJWKFile(jwk)
	if err != nil {
		return fmt.Errorf("load ordering key: %w", err)
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
	oa.myDID = sagedid.AgentDID(didStr)
	oa.a2a = a2aclient.NewA2AClient(oa.myDID, oa.myKey, http.DefaultClient)
	return nil
}

// ---- Local helpers (unchanged UX) ----

func initProducts() []Product {
	return []Product{
		{ID: "PRD001", Name: "iPhone 15 Pro", Category: "Electronics", Price: 1500000, Stock: 50, Description: "Latest iPhone model with advanced features"},
		{ID: "PRD002", Name: "MacBook Pro 16", Category: "Electronics", Price: 3500000, Stock: 30, Description: "Professional laptop for developers"},
		{ID: "PRD003", Name: "AirPods Pro", Category: "Electronics", Price: 350000, Stock: 100, Description: "Wireless earbuds with noise cancellation"},
		{ID: "PRD004", Name: "Samsung Galaxy S24", Category: "Electronics", Price: 1200000, Stock: 45, Description: "Flagship Android smartphone"},
		{ID: "PRD005", Name: "Sony WH-1000XM5", Category: "Electronics", Price: 450000, Stock: 60, Description: "Premium noise-canceling headphones"},
		{ID: "PRD006", Name: "iPad Air", Category: "Electronics", Price: 850000, Stock: 70, Description: "Versatile tablet for work and entertainment"},
		{ID: "PRD007", Name: "LG 55-inch OLED TV", Category: "Electronics", Price: 2500000, Stock: 25, Description: "4K OLED television with stunning picture quality"},
	}
}

func (oa *OrderingAgent) handleOrderRequest(content string) string {
	product := oa.extractProduct(content)
	quantity := oa.extractQuantity(content)

	if product == nil {
		return "죄송합니다. 해당 상품을 찾을 수 없습니다. 상품 목록을 확인하려면 '상품 목록'이라고 말씀해주세요."
	}

	if product.Stock < quantity {
		return fmt.Sprintf("죄송합니다. '%s' 상품의 재고가 부족합니다. (현재 재고: %d개)", product.Name, product.Stock)
	}

	// Create mock order
	order := Order{
		OrderID:           fmt.Sprintf("ORD%d", time.Now().Unix()),
		ProductID:         product.ID,
		ProductName:       product.Name,
		Quantity:          quantity,
		TotalPrice:        product.Price * float64(quantity),
		ShippingAddress:   "서울시 강남구 테헤란로 123 (주문자 주소)",
		Status:            "주문 완료",
		EstimatedDelivery: time.Now().AddDate(0, 0, 3).Format("2006-01-02"),
		CreatedAt:         time.Now(),
	}

	return fmt.Sprintf(`주문이 완료되었습니다!

📦 주문 정보:
- 주문번호: %s
- 상품명: %s
- 수량: %d개
- 총 금액: ₩%s
- 배송지: %s
- 예상 배송일: %s

배송 상태는 주문번호로 조회하실 수 있습니다.`,
		order.OrderID,
		order.ProductName,
		order.Quantity,
		formatPrice(order.TotalPrice),
		order.ShippingAddress,
		order.EstimatedDelivery,
	)
}

func (oa *OrderingAgent) handleProductListRequest() string {
	var lines []string
	lines = append(lines, "🛍️ 판매 중인 상품 목록:\n")

	for i, p := range oa.products {
		if p.Stock > 0 {
			lines = append(lines, fmt.Sprintf("%d. %s - ₩%s (재고: %d개)\n   %s",
				i+1, p.Name, formatPrice(p.Price), p.Stock, p.Description))
		}
	}

	lines = append(lines, "\n주문하시려면 '상품명을 주문해줘'라고 말씀해주세요.")
	return strings.Join(lines, "\n")
}

func (oa *OrderingAgent) handleGeneralOrderingRequest(content string) string {
	return fmt.Sprintf("Ordering Agent received your request: '%s'. I can help with product orders, delivery tracking, and inventory management. Try saying '상품 목록' to see available products.", content)
}

func (oa *OrderingAgent) extractProduct(content string) *Product {
	c := strings.ToLower(content)
	for _, p := range oa.products {
		if strings.Contains(c, strings.ToLower(p.Name)) {
			return &p
		}
		// Check for common abbreviations
		if strings.Contains(p.Name, "iPhone") && strings.Contains(c, "아이폰") {
			return &p
		}
		if strings.Contains(p.Name, "MacBook") && strings.Contains(c, "맥북") {
			return &p
		}
		if strings.Contains(p.Name, "AirPods") && (strings.Contains(c, "에어팟") || strings.Contains(c, "airpods")) {
			return &p
		}
		if strings.Contains(p.Name, "Galaxy") && (strings.Contains(c, "갤럭시") || strings.Contains(c, "galaxy")) {
			return &p
		}
	}
	return nil
}

func (oa *OrderingAgent) extractQuantity(content string) int {
	// Simple extraction - look for numbers
	// Default to 1 if not found
	c := strings.ToLower(content)
	if strings.Contains(c, "2개") || strings.Contains(c, "two") {
		return 2
	}
	if strings.Contains(c, "3개") || strings.Contains(c, "three") {
		return 3
	}
	return 1
}

func formatPrice(price float64) string {
	return fmt.Sprintf("%.0f", price)
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
