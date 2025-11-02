package planning

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

// PlanningAgent handles travel & accommodation requests IN-PROC,
// and (optionally) forwards to an EXTERNAL planning service.
// Only the outbound call (agent -> external) is A2A-signed when SAGE is ON.
type PlanningAgent struct {
	Name        string
	SAGEEnabled bool

	ExternalURL string // e.g. http://external-planning:19081 (empty => local only)

	// Outbound signing
	myDID sagedid.AgentDID
	myKey keys.KeyPair
	a2a   *a2aclient.A2AClient

	httpClient *http.Client

	hotels []Hotel
}

type Hotel struct {
	Name     string  `json:"name"`
	Location string  `json:"location"`
	Price    float64 `json:"price"`
	Rating   float64 `json:"rating"`
	URL      string  `json:"url"`
}

func NewPlanningAgent(name string) *PlanningAgent {
	return &PlanningAgent{
		Name:        name,
		SAGEEnabled: envBool("PLANNING_SAGE_ENABLED", true),
		ExternalURL: strings.TrimRight(envOr("PLANNING_EXTERNAL_URL", ""), "/"),
		httpClient:  http.DefaultClient,
		hotels:      initHotels(),
	}
}

// IN-PROC entrypoint (Root -> Planning)
func (pa *PlanningAgent) Process(ctx context.Context, msg types.AgentMessage) (types.AgentMessage, error) {
	// If external URL is set, prefer forwarding to external service.
	if pa.ExternalURL != "" {
		if out, err := pa.forwardExternal(ctx, &msg); err == nil {
			return *out, nil
		}
		// On external error, gracefully fall back to local logic.
	}

	// Local logic (same UX as before)
	content := strings.ToLower(msg.Content)
	var responseContent string
	if strings.Contains(content, "hotel") || strings.Contains(content, "accommodation") {
		responseContent = pa.handleHotelRequest(msg.Content)
	} else {
		responseContent = pa.handleGeneralPlanningRequest(msg.Content)
	}

	return types.AgentMessage{
		ID:        "resp-" + msg.ID,
		From:      pa.Name,
		To:        msg.From,
		Type:      "response",
		Content:   responseContent,
		Timestamp: time.Now(),
		Metadata:  map[string]any{"agent_type": "planning"},
	}, nil
}

// ---- Outbound (agent -> external) ----

func (pa *PlanningAgent) forwardExternal(ctx context.Context, msg *types.AgentMessage) (*types.AgentMessage, error) {
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pa.ExternalURL+"/process", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Optional body integrity (tamper detection)
	req.Header.Set("Content-Digest", a2autil.ComputeContentDigest(body))

	resp, err := pa.do(ctx, req)
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
			From:      "external-planning",
			To:        msg.From,
			Type:      "error",
			Content:   fmt.Sprintf("external error: %d %s", resp.StatusCode, strings.TrimSpace(buf.String())),
			Timestamp: time.Now(),
		}, nil
	}

	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		out = types.AgentMessage{
			ID:        msg.ID + "-ext",
			From:      "external-planning",
			To:        msg.From,
			Type:      "response",
			Content:   strings.TrimSpace(buf.String()),
			Timestamp: time.Now(),
		}
	}
	return &out, nil
}

func (pa *PlanningAgent) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Initialize signing client lazily (only when SAGE ON + actually needed)
	if pa.SAGEEnabled {
		if pa.a2a == nil {
			if err := pa.initSigning(); err != nil {
				return nil, err
			}
		}
		return pa.a2a.Do(ctx, req)
	}
	return pa.httpClient.Do(req)
}

func (pa *PlanningAgent) initSigning() error {
	jwk := strings.TrimSpace(os.Getenv("PLANNING_JWK_FILE"))
	if jwk == "" {
		return fmt.Errorf("PLANNING_JWK_FILE required for SAGE signing")
	}
	kp, err := keys.LoadFromJWKFile(jwk)
	if err != nil {
		return fmt.Errorf("load planning key: %w", err)
	}

	didStr := strings.TrimSpace(os.Getenv("PLANNING_DID"))
	if didStr == "" {
		if ecdsaPriv, ok := kp.PrivateKey().(*ecdsa.PrivateKey); ok {
			addr := ethcrypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex()
			didStr = "did:sage:ethereum:" + addr
		} else if id := strings.TrimSpace(kp.ID()); id != "" {
			didStr = "did:sage:generated:" + id
		} else {
			return fmt.Errorf("PLANNING_DID not set and cannot derive from key")
		}
	}

	pa.myKey = kp
	pa.myDID = sagedid.AgentDID(didStr)
	pa.a2a = a2aclient.NewA2AClient(pa.myDID, pa.myKey, http.DefaultClient)
	return nil
}

// ---- Local helpers (unchanged UX) ----

func initHotels() []Hotel {
	return []Hotel{
		{Name: "Grand Hotel Seoul", Location: "Myeongdong", Price: 250.0, Rating: 4.5, URL: "https://grandhotel.com/seoul"},
		{Name: "Plaza Hotel", Location: "Myeongdong", Price: 300.0, Rating: 4.7, URL: "https://plaza.com/seoul"},
		{Name: "Budget Inn", Location: "Myeongdong", Price: 80.0, Rating: 3.5, URL: "https://budgetinn.com/seoul"},
		{Name: "Luxury Suite", Location: "Gangnam", Price: 450.0, Rating: 4.9, URL: "https://luxury.com/seoul"},
		{Name: "City Center Hotel", Location: "Jongno", Price: 180.0, Rating: 4.2, URL: "https://citycenter.com/seoul"},
	}
}

func (pa *PlanningAgent) handleHotelRequest(content string) string {
	location := pa.extractLocation(content)
	relevant := pa.findHotelsByLocation(location)
	if len(relevant) == 0 {
		return fmt.Sprintf("No hotels found in %s. Please try another location.", location)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Hotel recommendations for %s:", location))
	for i, h := range relevant {
		if i >= 3 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s - $%.2f/night, Rating: %.1f/5 - Book at: %s",
			i+1, h.Name, h.Price, h.Rating, h.URL))
	}
	return strings.Join(lines, "\n")
}

func (pa *PlanningAgent) handleGeneralPlanningRequest(content string) string {
	return fmt.Sprintf("Planning Agent received your request: '%s'. I can help with hotel bookings, travel planning, and accommodation arrangements.", content)
}

func (pa *PlanningAgent) extractLocation(content string) string {
	locations := []string{"myeongdong", "gangnam", "jongno", "hongdae", "itaewon"}
	c := strings.ToLower(content)
	for _, loc := range locations {
		if strings.Contains(c, loc) {
			return strings.Title(loc)
		}
	}
	if strings.Contains(c, "seoul") || strings.Contains(c, "hotel") {
		return "Myeongdong"
	}
	return "Seoul"
}

func (pa *PlanningAgent) findHotelsByLocation(location string) []Hotel {
	var results []Hotel
	for _, h := range pa.hotels {
		if strings.EqualFold(h.Location, location) || location == "Seoul" {
			results = append(results, h)
		}
	}
	// naive sort by rating desc
	for i := 0; i < len(results)-1; i++ {
		for j := 0; j < len(results)-i-1; j++ {
			if results[j].Rating < results[j+1].Rating {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
	return results
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
