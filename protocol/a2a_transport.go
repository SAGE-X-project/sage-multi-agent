package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sage-x-project/sage/pkg/agent/transport"
)

// A2ADoer: requires only the Do(ctx, req) signature that payment.Agent already exposes
type A2ADoer interface {
	Do(ctx context.Context, req *http.Request) (*http.Response, error)
}

// A2ATransport:
//   - Always POST to {baseURL}/process
//   - Never add Signature/Content-Digest directly (A2ADoer handles signing)
//   - Modes:
//     - Handshake: SecureMessage(JSON) + X-SAGE-HPKE: v1 (no KID)
//     - HPKE data: when msg.Metadata["hpke_kid"] exists, send payload as-is with (Content-Type: application/sage+hpke, X-SAGE-HPKE, X-KID)
//     - Plain data: send payload as-is with (Content-Type: application/json)
type A2ATransport struct {
	doer          A2ADoer
	baseURL       string
	hpkeHandshake bool
}

func NewA2ATransport(doer A2ADoer, baseURL string, hpkeHandshake bool) *A2ATransport {
	return &A2ATransport{doer: doer, baseURL: strings.TrimRight(baseURL, "/"), hpkeHandshake: hpkeHandshake}
}

func (t *A2ATransport) Send(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
	if t.doer == nil || t.baseURL == "" {
		return nil, fmt.Errorf("transport not initialized")
	}
	if msg == nil {
		return nil, fmt.Errorf("nil message")
	}

	var (
		body        []byte
		err         error
		contentType = "application/json"
		useHPKE     = false
		kid         string
	)

	if t.hpkeHandshake {
    // Handshake: send the entire SecureMessage as JSON
		body, err = json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("marshal secure message: %w", err)
		}
        useHPKE = true // X-SAGE-HPKE: v1 (no KID)
	} else {
		if len(msg.Payload) == 0 {
			return nil, fmt.Errorf("empty payload")
		}
		body = msg.Payload

		if msg.Metadata != nil {
			if k := msg.Metadata["hpke_kid"]; k != "" {
				useHPKE = true
				kid = k
				contentType = "application/sage+hpke"
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/process", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	if useHPKE {
		req.Header.Set("X-SAGE-HPKE", "v1")
		if kid != "" {
			req.Header.Set("X-KID", kid)
		}
	}

	req.Header.Set("X-SAGE-DID", msg.DID)
	req.Header.Set("X-SAGE-Message-ID", msg.ID)
	if msg.ContextID != "" {
		req.Header.Set("X-SAGE-Context-ID", msg.ContextID)
	}
	if msg.TaskID != "" {
		req.Header.Set("X-SAGE-Task-ID", msg.TaskID)
	}
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }

    // Pass through A2A signing + DID middleware
	resp, err := t.doer.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("http send (a2a): %w", err)
	}

	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

    // Handshake expects a transport.Response JSON
	if t.hpkeHandshake {
		var wire struct {
			Success   bool   `json:"success"`
			MessageID string `json:"message_id"`
			TaskID    string `json:"task_id"`
			Data      []byte `json:"data"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(respBody, &wire); err != nil {
			return &transport.Response{
				Success:   resp.StatusCode/100 == 2,
				MessageID: msg.ID,
				TaskID:    msg.TaskID,
				Data:      respBody,
				Error:     nil,
			}, nil
		}
		out := &transport.Response{
			Success:   wire.Success,
			MessageID: wire.MessageID,
			TaskID:    wire.TaskID,
			Data:      wire.Data,
		}
		if out.MessageID == "" {
			out.MessageID = msg.ID
		}
		if out.TaskID == "" {
			out.TaskID = msg.TaskID
		}
		if wire.Error != "" {
			out.Success = false
			out.Error = fmt.Errorf("%s", wire.Error)
		}
		return out, nil
	}

    // Data mode: forward raw body as Response.Data
	return &transport.Response{
		Success:   resp.StatusCode/100 == 2,
		MessageID: msg.ID,
		TaskID:    msg.TaskID,
		Data:      respBody,
		Error:     nil,
	}, nil
}
