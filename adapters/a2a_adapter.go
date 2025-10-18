package adapters

import (
	"context"
	"fmt"
	"time"

	"iter"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2agrpc"
	"github.com/a2aproject/a2a-go/a2asrv"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// Router: root가 구현해야 하는 단 하나의 인터페이스
type Router interface {
	RouteRequest(ctx context.Context, req *types.AgentMessage) (*types.AgentMessage, error)
}

// 내부 gRPC 핸들러: a2a-go → Router 로 브릿지
type handler struct {
	router Router
}

// NewA2AGRPCHandler exposes a2a-go gRPC handler wired to the provided router.
// Agents can use this to start/own the gRPC server lifecycle and register the handler.
func NewA2AGRPCHandler(router Router, cardProducer a2asrv.AgentCardProducer) *a2agrpc.GRPCHandler {
	return a2agrpc.NewHandler(cardProducer, &handler{router: router})
}

// --- a2asrv.RequestHandler 구현 ---

func (h *handler) OnSendMessage(ctx context.Context, p *a2a.MessageSendParams) (a2a.SendMessageResult, error) {
	if p == nil || p.Message == nil {
		return nil, fmt.Errorf("empty a2a message")
	}
	// A2A → 공통 DTO로 변환
	req := &types.AgentMessage{
		From:      "a2a-client",
		To:        "root",
		Content:   extractTextFromA2A(p.Message),
		Type:      "request",
		Timestamp: time.Now(),
	}
	// Router로 위임
	resp, err := h.router.RouteRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	// 공통 DTO → A2A 메시지로 변환
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: resp.Content})
	return msg, nil
}

func (h *handler) OnSendMessageStream(ctx context.Context, p *a2a.MessageSendParams) iter.Seq2[a2a.Event, error] {
	// not implemented: return nil iterator per a2a-go expectations
	return nil
}
func (h *handler) OnGetTask(ctx context.Context, q *a2a.TaskQueryParams) (*a2a.Task, error) {
	return nil, fmt.Errorf("not implemented")
}
func (h *handler) OnCancelTask(ctx context.Context, id *a2a.TaskIDParams) (*a2a.Task, error) {
	return nil, fmt.Errorf("not implemented")
}
func (h *handler) OnResubscribeToTask(ctx context.Context, id *a2a.TaskIDParams) iter.Seq2[a2a.Event, error] {
	// not implemented: return nil iterator per a2a-go expectations
	return nil
}
func (h *handler) OnSetTaskPushConfig(ctx context.Context, p *a2a.TaskPushConfig) (*a2a.TaskPushConfig, error) {
	return nil, fmt.Errorf("not implemented")
}
func (h *handler) OnGetTaskPushConfig(ctx context.Context, p *a2a.GetTaskPushConfigParams) (*a2a.TaskPushConfig, error) {
	return nil, fmt.Errorf("not implemented")
}
func (h *handler) OnListTaskPushConfig(ctx context.Context, p *a2a.ListTaskPushConfigParams) ([]*a2a.TaskPushConfig, error) {
	return nil, fmt.Errorf("not implemented")
}
func (h *handler) OnDeleteTaskPushConfig(ctx context.Context, p *a2a.DeleteTaskPushConfigParams) error {
	return fmt.Errorf("not implemented")
}

func extractTextFromA2A(m *a2a.Message) string {
	var out string
	for _, part := range m.Parts {
		if tp, ok := part.(a2a.TextPart); ok {
			out += tp.Text
		}
	}
	return out
}
