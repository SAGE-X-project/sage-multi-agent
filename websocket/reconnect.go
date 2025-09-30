package websocket

import (
	"context"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Initial delay before first reconnection attempt
	initialReconnectDelay = 1 * time.Second
	// Maximum delay between reconnection attempts
	maxReconnectDelay = 30 * time.Second
	// Factor to multiply delay after each failed attempt
	reconnectDelayMultiplier = 2
	// Maximum number of reconnection attempts (0 for infinite)
	maxReconnectAttempts = 0
)

// ReconnectingClient wraps a websocket connection with automatic reconnection
type ReconnectingClient struct {
	// URL to connect to
	url *url.URL

	// Current websocket connection
	conn *websocket.Conn
	connMu sync.RWMutex

	// Reconnection state
	reconnectDelay time.Duration
	reconnectCount int
	isConnected    bool
	connectMu      sync.Mutex

	// Channels for communication
	send      chan []byte
	receive   chan []byte
	done      chan struct{}
	reconnect chan struct{}

	// Callbacks
	onConnect    func()
	onDisconnect func()
	onMessage    func([]byte)

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewReconnectingClient creates a new reconnecting websocket client
func NewReconnectingClient(urlStr string) (*ReconnectingClient, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &ReconnectingClient{
		url:            u,
		reconnectDelay: initialReconnectDelay,
		send:           make(chan []byte, 256),
		receive:        make(chan []byte, 256),
		done:           make(chan struct{}),
		reconnect:      make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
	}

	return client, nil
}

// Connect establishes the websocket connection
func (rc *ReconnectingClient) Connect() error {
	rc.connectMu.Lock()
	defer rc.connectMu.Unlock()

	if rc.isConnected {
		return nil
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(rc.url.String(), nil)
	if err != nil {
		return err
	}

	rc.connMu.Lock()
	rc.conn = conn
	rc.connMu.Unlock()

	rc.isConnected = true
	rc.reconnectCount = 0
	rc.reconnectDelay = initialReconnectDelay

	// Call onConnect callback if set
	if rc.onConnect != nil {
		go rc.onConnect()
	}

	// Start read and write pumps
	go rc.readPump()
	go rc.writePump()

	log.Printf("WebSocket connected to %s", rc.url.String())
	return nil
}

// Disconnect closes the websocket connection
func (rc *ReconnectingClient) Disconnect() {
	rc.connectMu.Lock()
	defer rc.connectMu.Unlock()

	if !rc.isConnected {
		return
	}

	rc.isConnected = false

	rc.connMu.Lock()
	if rc.conn != nil {
		rc.conn.Close()
		rc.conn = nil
	}
	rc.connMu.Unlock()

	// Call onDisconnect callback if set
	if rc.onDisconnect != nil {
		go rc.onDisconnect()
	}

	log.Printf("WebSocket disconnected from %s", rc.url.String())
}

// reconnectLoop handles automatic reconnection
func (rc *ReconnectingClient) reconnectLoop() {
	for {
		select {
		case <-rc.ctx.Done():
			return
		case <-rc.reconnect:
			if rc.isConnected {
				continue
			}

			// Apply exponential backoff
			time.Sleep(rc.reconnectDelay)

			// Attempt reconnection
			err := rc.Connect()
			if err != nil {
				rc.reconnectCount++
				log.Printf("Reconnection attempt %d failed: %v", rc.reconnectCount, err)

				// Check max attempts
				if maxReconnectAttempts > 0 && rc.reconnectCount >= maxReconnectAttempts {
					log.Printf("Max reconnection attempts reached, giving up")
					rc.cancel()
					return
				}

				// Increase delay with exponential backoff
				rc.reconnectDelay *= reconnectDelayMultiplier
				if rc.reconnectDelay > maxReconnectDelay {
					rc.reconnectDelay = maxReconnectDelay
				}

				// Schedule next reconnection attempt
				select {
				case rc.reconnect <- struct{}{}:
				default:
				}
			}
		}
	}
}

// readPump reads messages from the websocket connection
func (rc *ReconnectingClient) readPump() {
	defer func() {
		rc.Disconnect()
		// Trigger reconnection
		select {
		case rc.reconnect <- struct{}{}:
		default:
		}
	}()

	rc.connMu.RLock()
	conn := rc.conn
	rc.connMu.RUnlock()

	if conn == nil {
		return
	}

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}

		// Send to receive channel
		select {
		case rc.receive <- message:
		case <-rc.ctx.Done():
			return
		}

		// Call onMessage callback if set
		if rc.onMessage != nil {
			go rc.onMessage(message)
		}
	}
}

// writePump writes messages to the websocket connection
func (rc *ReconnectingClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		rc.Disconnect()
	}()

	rc.connMu.RLock()
	conn := rc.conn
	rc.connMu.RUnlock()

	if conn == nil {
		return
	}

	for {
		select {
		case message, ok := <-rc.send:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("WebSocket ping error: %v", err)
				return
			}

		case <-rc.ctx.Done():
			return
		}
	}
}

// Send sends a message to the websocket
func (rc *ReconnectingClient) Send(message []byte) error {
	select {
	case rc.send <- message:
		return nil
	case <-rc.ctx.Done():
		return context.Canceled
	default:
		return ErrBufferFull
	}
}

// Receive returns the channel for receiving messages
func (rc *ReconnectingClient) Receive() <-chan []byte {
	return rc.receive
}

// Start starts the reconnecting client
func (rc *ReconnectingClient) Start() error {
	// Start reconnection loop
	go rc.reconnectLoop()

	// Initial connection
	if err := rc.Connect(); err != nil {
		// Schedule reconnection
		select {
		case rc.reconnect <- struct{}{}:
		default:
		}
		return err
	}

	return nil
}

// Stop stops the reconnecting client
func (rc *ReconnectingClient) Stop() {
	rc.cancel()
	rc.Disconnect()
	close(rc.send)
	close(rc.receive)
}

// SetOnConnect sets the callback for connection events
func (rc *ReconnectingClient) SetOnConnect(fn func()) {
	rc.onConnect = fn
}

// SetOnDisconnect sets the callback for disconnection events
func (rc *ReconnectingClient) SetOnDisconnect(fn func()) {
	rc.onDisconnect = fn
}

// SetOnMessage sets the callback for message events
func (rc *ReconnectingClient) SetOnMessage(fn func([]byte)) {
	rc.onMessage = fn
}

// IsConnected returns the connection status
func (rc *ReconnectingClient) IsConnected() bool {
	rc.connectMu.Lock()
	defer rc.connectMu.Unlock()
	return rc.isConnected
}