package websocket

import "errors"

// Common websocket errors
var (
	// ErrBufferFull is returned when the send buffer is full
	ErrBufferFull = errors.New("websocket: send buffer is full")

	// ErrNotConnected is returned when trying to send on a disconnected client
	ErrNotConnected = errors.New("websocket: not connected")

	// ErrAlreadyConnected is returned when trying to connect an already connected client
	ErrAlreadyConnected = errors.New("websocket: already connected")

	// ErrMaxReconnectAttemptsReached is returned when max reconnection attempts are exceeded
	ErrMaxReconnectAttemptsReached = errors.New("websocket: max reconnection attempts reached")
)