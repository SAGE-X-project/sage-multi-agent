package adapters

import (
    "context"
    "net"
    "testing"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    "github.com/a2aproject/a2a-go/a2apb"
    "github.com/sage-x-project/sage-multi-agent/types"
)

// testRouter implements Router for testing.
type testRouter struct{}

func (testRouter) RouteRequest(ctx context.Context, req *types.AgentMessage) (*types.AgentMessage, error) {
    return &types.AgentMessage{From: "root", To: req.From, Content: "echo: " + req.Content, Timestamp: time.Now(), Type: "response"}, nil
}

func TestGRPCAdapter_SendMessage(t *testing.T) {
    addr := ":18084"
    // Agent-owned gRPC server; register adapter handler
    lis, err := net.Listen("tcp", addr)
    if err != nil { t.Fatalf("listen: %v", err) }
    s := grpc.NewServer()
    NewA2AGRPCHandler(testRouter{}, nil).RegisterWith(s)
    go s.Serve(lis)
    t.Cleanup(func(){ s.Stop(); lis.Close() })
    time.Sleep(100 * time.Millisecond)

    // Dial gRPC and use protobuf client directly
    conn, err := grpc.NewClient("localhost:18084", grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil { t.Fatalf("dial: %v", err) }
    defer conn.Close()

    cli := a2apb.NewA2AServiceClient(conn)
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()

    req := &a2apb.SendMessageRequest{ Request: &a2apb.Message{ Role: a2apb.Role_ROLE_USER, Content: []*a2apb.Part{{ Part: &a2apb.Part_Text{ Text: "hello" } }}, }, }
    resp, err := cli.SendMessage(ctx, req)
    if err != nil { t.Fatalf("SendMessage: %v", err) }

    if resp.GetMsg() == nil { t.Fatalf("expected message response, got %T", resp.GetPayload()) }
    got := ""
    for _, p := range resp.GetMsg().GetContent() { got += p.GetText() }
    if got != "echo: hello" { t.Fatalf("unexpected content: %q", got) }
}
