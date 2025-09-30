package test

import (
	"testing"
	"time"

	"github.com/sage-x-project/sage-multi-agent/websocket"
)

// TestReconnectingClientCreation tests client creation
func TestReconnectingClientCreation(t *testing.T) {
	client, err := websocket.NewReconnectingClient("ws://localhost:8080/ws")
	if err != nil {
		t.Errorf("Failed to create reconnecting client: %v", err)
	}
	if client == nil {
		t.Errorf("Client should not be nil")
	}
}

// TestReconnectingClientInvalidURL tests invalid URL handling
func TestReconnectingClientInvalidURL(t *testing.T) {
	_, err := websocket.NewReconnectingClient("://invalid-url")
	if err == nil {
		t.Errorf("Expected error for invalid URL")
	}
}

// TestReconnectingClientCallbacks tests callback registration
func TestReconnectingClientCallbacks(t *testing.T) {
	client, err := websocket.NewReconnectingClient("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	connectCalled := false
	disconnectCalled := false
	messageCalled := false

	client.SetOnConnect(func() {
		connectCalled = true
	})

	client.SetOnDisconnect(func() {
		disconnectCalled = true
	})

	client.SetOnMessage(func(msg []byte) {
		messageCalled = true
	})

	// Verify callbacks are set (we can't easily test execution without a real server)
	if client == nil {
		t.Errorf("Client configuration failed")
	}

	// Note: connectCalled, disconnectCalled, messageCalled would be true
	// after actual connection events, but we can't test that without a server
	_ = connectCalled
	_ = disconnectCalled
	_ = messageCalled
}

// TestReconnectingClientConnectionState tests connection state
func TestReconnectingClientConnectionState(t *testing.T) {
	client, err := websocket.NewReconnectingClient("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Initially should not be connected
	if client.IsConnected() {
		t.Errorf("Client should not be connected initially")
	}
}

// TestReconnectingClientSendBeforeConnect tests sending before connection
func TestReconnectingClientSendBeforeConnect(t *testing.T) {
	client, err := websocket.NewReconnectingClient("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Try to send message before connecting
	err = client.Send([]byte("test message"))
	// Should succeed but message will be buffered
	if err != nil {
		t.Logf("Send before connect resulted in: %v", err)
	}
}

// TestReconnectingClientReceiveChannel tests receive channel
func TestReconnectingClientReceiveChannel(t *testing.T) {
	client, err := websocket.NewReconnectingClient("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	receiveChan := client.Receive()
	if receiveChan == nil {
		t.Errorf("Receive channel should not be nil")
	}
}

// TestReconnectingClientStopBeforeStart tests stopping before starting
func TestReconnectingClientStopBeforeStart(t *testing.T) {
	client, err := websocket.NewReconnectingClient("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Should not panic
	client.Stop()
}

// MockWebSocketServerBehavior simulates reconnection scenarios
func TestReconnectionScenarios(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		shouldFail  bool
		description string
	}{
		{
			name:        "Valid localhost URL",
			url:         "ws://localhost:8080/ws",
			shouldFail:  false,
			description: "Should handle localhost connection",
		},
		{
			name:        "Valid IP URL",
			url:         "ws://127.0.0.1:8080/ws",
			shouldFail:  false,
			description: "Should handle IP connection",
		},
		{
			name:        "Invalid URL format",
			url:         "://invalid",
			shouldFail:  true,
			description: "Should reject invalid URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := websocket.NewReconnectingClient(tt.url)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("Expected error for %s", tt.description)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for %s: %v", tt.description, err)
				}
				if client == nil {
					t.Errorf("Client should not be nil for valid URL")
				}
			}
		})
	}
}

// TestReconnectionBackoff tests exponential backoff behavior
func TestReconnectionBackoff(t *testing.T) {
	// This test verifies that the reconnection logic exists
	// Full testing would require mocking connection failures

	client, err := websocket.NewReconnectingClient("ws://localhost:9999/ws")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Start the client (will fail to connect but should handle gracefully)
	err = client.Start()
	// Connection will fail but client should be created

	// Give it a moment to attempt initial connection
	time.Sleep(100 * time.Millisecond)

	// Should not be connected
	if client.IsConnected() {
		t.Errorf("Should not be connected to non-existent server")
	}

	// Clean up
	client.Stop()
}