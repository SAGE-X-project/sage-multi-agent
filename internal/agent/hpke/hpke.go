// Package hpke provides high-level abstractions for HPKE (Hybrid Public Key Encryption).
// This package wraps sage's HPKE client and sage-a2a-go's HPKE server to provide
// a unified API that can be easily migrated to sage-a2a-go.
package hpke

import (
	"context"
	"fmt"

	sagehpke "github.com/sage-x-project/sage/pkg/agent/hpke"
	sagedid "github.com/sage-x-project/sage/pkg/agent/did"
	transport "github.com/sage-x-project/sage/pkg/agent/transport"
	a2ahpke "github.com/sage-x-project/sage-a2a-go/pkg/hpke"

	agentdid "github.com/sage-x-project/sage-multi-agent/internal/agent/did"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/keys"
	"github.com/sage-x-project/sage-multi-agent/internal/agent/session"
)

// Server provides HPKE server functionality for receiving encrypted messages.
// This wraps sage-a2a-go's HPKE server.
type Server struct {
	underlying *a2ahpke.Server
}

// ServerConfig contains configuration for creating an HPKE server.
type ServerConfig struct {
	// SigningKey is the server's signing key pair
	SigningKey keys.KeyPair

	// KEMKey is the server's KEM key pair for HPKE
	KEMKey keys.KeyPair

	// DID is the server's DID
	DID sagedid.AgentDID

	// Resolver is the DID resolver for verifying client DIDs
	Resolver *agentdid.Resolver

	// SessionManager manages HPKE encryption sessions
	SessionManager *session.Manager
}

// NewServer creates a new HPKE server.
// The server handles incoming encrypted messages using HPKE.
//
// Parameters:
//   - config: Server configuration
//
// Returns:
//   - *Server: The initialized HPKE server
//   - error: Error if server cannot be created
//
// Example:
//
//	server, err := hpke.NewServer(hpke.ServerConfig{
//	    SigningKey:     keySet.SigningKey,
//	    KEMKey:         keySet.KEMKey,
//	    DID:            serverDID,
//	    Resolver:       resolver,
//	    SessionManager: sessionMgr,
//	})
func NewServer(config ServerConfig) (*Server, error) {
	if config.SigningKey == nil {
		return nil, fmt.Errorf("signing key is required")
	}
	if config.KEMKey == nil {
		return nil, fmt.Errorf("KEM key is required")
	}
	if config.DID == "" {
		return nil, fmt.Errorf("DID is required")
	}
	if config.Resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}
	if config.SessionManager == nil {
		return nil, fmt.Errorf("session manager is required")
	}

	// Create sage-a2a-go HPKE server
	// Note: NewServer expects string DID and Resolver interface
	srv, err := a2ahpke.NewServer(
		config.SigningKey,
		config.SessionManager.GetUnderlying(),
		string(config.DID), // Convert AgentDID to string
		config.Resolver.GetKeyClient(), // Use EthereumClient which implements Resolver
		&a2ahpke.ServerOptions{
			KEM: config.KEMKey,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create HPKE server: %w", err)
	}

	return &Server{
		underlying: srv,
	}, nil
}

// HandleMessage processes an encrypted HPKE message.
// This is the main entry point for HPKE server operations.
//
// Parameters:
//   - ctx: Context for the operation
//   - msg: The encrypted HPKE message to process
//
// Returns:
//   - *transport.Response: The response to send back
//   - error: Error if message cannot be processed
func (s *Server) HandleMessage(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
	return s.underlying.HandleMessage(ctx, msg)
}

// GetUnderlying returns the underlying sage-a2a-go HPKE server.
// This is used for HTTP server integration.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, HTTP servers will work with this Server type directly.
//
// Returns:
//   - *a2ahpke.Server: The underlying HPKE server
func (s *Server) GetUnderlying() *a2ahpke.Server {
	return s.underlying
}

// Client provides HPKE client functionality for sending encrypted messages.
// This wraps sage's HPKE client.
type Client struct {
	underlying *sagehpke.Client
}

// ClientConfig contains configuration for creating an HPKE client.
type ClientConfig struct {
	// Transport is the underlying transport for sending messages
	Transport Transport

	// Resolver is the DID resolver for resolving target DIDs
	Resolver *agentdid.Resolver

	// SigningKey is the client's signing key pair
	SigningKey keys.KeyPair

	// ClientDID is the client's DID
	ClientDID sagedid.AgentDID

	// SessionManager manages HPKE encryption sessions
	SessionManager *session.Manager
}

// NewClient creates a new HPKE client.
// The client sends encrypted messages to remote HPKE servers.
//
// Parameters:
//   - config: Client configuration
//
// Returns:
//   - *Client: The initialized HPKE client
//   - error: Error if client cannot be created
//
// Example:
//
//	client, err := hpke.NewClient(hpke.ClientConfig{
//	    Transport:      transport,
//	    Resolver:       resolver,
//	    SigningKey:     keySet.SigningKey,
//	    ClientDID:      clientDID,
//	    SessionManager: sessionMgr,
//	})
func NewClient(config ClientConfig) (*Client, error) {
	if config.Transport == nil {
		return nil, fmt.Errorf("transport is required")
	}
	if config.Resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}
	if config.SigningKey == nil {
		return nil, fmt.Errorf("signing key is required")
	}
	if config.ClientDID == "" {
		return nil, fmt.Errorf("client DID is required")
	}
	if config.SessionManager == nil {
		return nil, fmt.Errorf("session manager is required")
	}

	// Create sage HPKE client
	// Note: sage's hpke.NewClient returns a Client directly, not an error
	cli := sagehpke.NewClient(
		config.Transport,
		config.Resolver.GetKeyClient(), // Use EthereumClient which implements Resolver
		config.SigningKey,
		string(config.ClientDID), // Convert AgentDID to string
		sagehpke.DefaultInfoBuilder{},
		config.SessionManager.GetUnderlying(),
	)

	return &Client{
		underlying: cli,
	}, nil
}

// SendHandshake initiates an HPKE handshake with the target server.
// This establishes an encrypted session for subsequent data messages.
//
// Parameters:
//   - ctx: Context for the operation
//   - targetDID: The target server's DID
//   - payload: Optional payload to send with the handshake
//
// Returns:
//   - *transport.Response: The handshake response from the server
//   - string: The Key ID (KID) for the established session
//   - error: Error if handshake fails
//
// Example:
//
//	resp, kid, err := client.SendHandshake(ctx, targetDID, nil)
//	if err != nil {
//	    return fmt.Errorf("HPKE handshake failed: %w", err)
//	}
//
// TODO: Implement based on sage HPKE client API
// func (c *Client) SendHandshake(ctx context.Context, targetDID sagedid.AgentDID, payload []byte) (*transport.Response, string, error) {
// 	return c.underlying.SendHandshake(ctx, string(targetDID), payload)
// }

// SendData sends an encrypted data message using an established HPKE session.
// This requires a prior successful handshake.
//
// Parameters:
//   - ctx: Context for the operation
//   - kid: The Key ID from the handshake
//   - targetDID: The target server's DID
//   - payload: The data to send (will be encrypted)
//
// Returns:
//   - *transport.Response: The response from the server
//   - error: Error if message cannot be sent
//
// Example:
//
//	resp, err := client.SendData(ctx, kid, targetDID, []byte("secret data"))
//	if err != nil {
//	    return fmt.Errorf("send data failed: %w", err)
//	}
//
// TODO: Implement based on sage HPKE client API
// func (c *Client) SendData(ctx context.Context, kid string, targetDID sagedid.AgentDID, payload []byte) (*transport.Response, error) {
// 	return c.underlying.SendData(ctx, kid, string(targetDID), payload)
// }

// GetUnderlying returns the underlying sage HPKE client.
// This is used for advanced operations not yet wrapped.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, all operations will be available through this Client type.
//
// Returns:
//   - *sagehpke.Client: The underlying HPKE client
func (c *Client) GetUnderlying() *sagehpke.Client {
	return c.underlying
}

// Future enhancements (to be implemented when migrating to sage-a2a-go):
//
// SendHandshakeWithRetry(ctx, targetDID, payload, maxRetries) - Automatic retry logic
// SendDataWithRetry(ctx, kid, targetDID, payload, maxRetries) - Automatic retry logic
// CloseSession(kid) - Explicitly close an HPKE session
// GetSessionInfo(kid) - Get information about an active session
