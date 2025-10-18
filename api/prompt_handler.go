package api

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/a2aproject/a2a-go/a2apb"
    "github.com/sage-x-project/sage-multi-agent/types"
)

// A2AGateway exposes HTTP handlers that forward requests to the A2A gRPC service.
type A2AGateway struct {
    client a2apb.A2AServiceClient
}

func NewA2AGateway(client a2apb.A2AServiceClient) *A2AGateway {
    return &A2AGateway{client: client}
}

// HandlePrompt accepts a prompt from the frontend and forwards it to the A2A server.
func (g *A2AGateway) HandlePrompt(w http.ResponseWriter, r *http.Request) {
    defer r.Body.Close()
    var req types.PromptRequest
    _ = json.NewDecoder(r.Body).Decode(&req)

    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()

    pbMsg := &a2apb.Message{
        Role:    a2apb.Role_ROLE_USER,
        Content: []*a2apb.Part{{Part: &a2apb.Part_Text{Text: req.Prompt}}},
    }

    res, err := g.client.SendMessage(ctx, &a2apb.SendMessageRequest{Request: pbMsg})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    var out string
    if m := res.GetMsg(); m != nil {
        for _, p := range m.GetContent() { out += p.GetText() }
    } else if t := res.GetTask(); t != nil {
        if t.GetStatus().GetUpdate() != nil {
            for _, p := range t.GetStatus().GetUpdate().GetContent() { out += p.GetText() }
        }
    } else {
        out = fmt.Sprintf("unexpected result: %T", res.GetPayload())
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]string{"response": out})
}

