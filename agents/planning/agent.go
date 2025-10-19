package planning

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

// PlanningAgent handles travel and accommodation planning requests
type PlanningAgent struct {
	Name        string
	Port        int
	SAGEEnabled bool
	hotels      []Hotel

	// Inbound DID verification
	didVerifier verifier.DIDVerifier
	didMW       *servermw.DIDAuthMiddleware
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

	// Business logic: routing within planning
	var responseContent string
	if strings.Contains(strings.ToLower(request.Content), "hotel") ||
		strings.Contains(strings.ToLower(request.Content), "accommodation") {
		responseContent = pa.handleHotelRequest(request.Content)
	} else {
		responseContent = pa.handleGeneralPlanningRequest(request.Content)
	}

	// Build response
	response := &types.AgentMessage{
		ID:        fmt.Sprintf("resp-%s", request.ID),
		From:      pa.Name,
		To:        request.From,
		Content:   responseContent,
		Timestamp: request.Timestamp,
		Type:      "response",
		Metadata:  map[string]interface{}{"agent_type": "planning"},
	}

	return response, nil
}

// handleHotelRequest handles hotel booking requests
func (pa *PlanningAgent) handleHotelRequest(content string) string {
	location := pa.extractLocation(content)
	relevantHotels := pa.findHotelsByLocation(location)

	if len(relevantHotels) == 0 {
		return fmt.Sprintf("No hotels found in %s. Please try another location.", location)
	}

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
	locations := []string{"myeongdong", "gangnam", "jongno", "hongdae", "itaewon"}

	contentLower := strings.ToLower(content)
	for _, loc := range locations {
		if strings.Contains(contentLower, loc) {
			return strings.Title(loc)
		}
	}

	if strings.Contains(contentLower, "seoul") || strings.Contains(contentLower, "hotel") {
		return "Myeongdong"
	}
	return "Seoul"
}

// findHotelsByLocation finds hotels in a specific location (sorted by rating)
func (pa *PlanningAgent) findHotelsByLocation(location string) []Hotel {
	var results []Hotel
	for _, hotel := range pa.hotels {
		if strings.EqualFold(hotel.Location, location) || location == "Seoul" {
			results = append(results, hotel)
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

// Start starts the planning agent server, with DID middleware whose "optional" toggles via /toggle-sage.
func (pa *PlanningAgent) Start() error {
	// Initialize DID verifier (file-backed resolver)
	if v, err := newLocalDIDVerifier(); err != nil {
		log.Printf("[planning] DID verifier init failed: %v", err)
	} else {
		pa.didVerifier = v
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/process", pa.handleProcessRequest)
	mux.HandleFunc("/status", pa.handleStatus)
	mux.HandleFunc("/toggle-sage", pa.handleToggleSAGE)

	var handler http.Handler = mux
	if pa.didVerifier != nil {
		mw := servermw.NewDIDAuthMiddlewareWithVerifier(pa.didVerifier)
		mw.SetOptional(!pa.SAGEEnabled) // require signature only when SAGE is ON
		pa.didMW = mw
		handler = mw.Wrap(handler)
	}

	log.Printf("Planning Agent starting on port %d", pa.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", pa.Port), handler)
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
	_ = json.NewEncoder(w).Encode(response)
}

// handleStatus returns the agent status
func (pa *PlanningAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"name":         pa.Name,
		"sage_enabled": pa.SAGEEnabled,
		"type":         "planning",
		"hotels_count": len(pa.hotels),
		"time":         time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleToggleSAGE toggles SAGE mode and updates DID middleware "optional"
func (pa *PlanningAgent) handleToggleSAGE(w http.ResponseWriter, r *http.Request) {
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
	pa.SAGEEnabled = req.Enabled
	if pa.didMW != nil {
		pa.didMW.SetOptional(!pa.SAGEEnabled)
	}
	log.Printf("[planning] SAGE %v (verify optional=%v)", pa.SAGEEnabled, !pa.SAGEEnabled)
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
			log.Printf("[planning] skip DID %s: %v", a.DID, err)
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
