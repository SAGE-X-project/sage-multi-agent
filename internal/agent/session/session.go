// Package session provides high-level abstractions for HPKE session management.
// This package wraps sage's session.Manager to provide a simpler API
// that can be easily migrated to sage-a2a-go.
package session

import (
	sagesession "github.com/sage-x-project/sage/pkg/agent/session"
)

// Manager manages HPKE encryption sessions for agent-to-agent communication.
// It maintains a mapping of Key IDs (KID) to encryption contexts, allowing
// stateful encrypted communication after the initial handshake.
//
// This is currently a wrapper around sage's session.Manager, but abstracts
// the dependency for future migration to sage-a2a-go.
type Manager struct {
	underlying *sagesession.Manager
}

// NewManager creates a new session manager.
// The session manager is required for HPKE server/client operations.
//
// Returns:
//   - *Manager: A new session manager instance
//
// Example:
//
//	sessionMgr := session.NewManager()
func NewManager() *Manager {
	return &Manager{
		underlying: sagesession.NewManager(),
	}
}

// GetUnderlying returns the underlying sage session.Manager.
// This is used for integration with HPKE server/client which currently
// require sage's session.Manager interface.
//
// NOTE: This method exists only for the prototype phase. Once migrated to
// sage-a2a-go, HPKE server/client will accept this Manager type directly.
//
// Returns:
//   - *sagesession.Manager: The underlying sage session manager
func (m *Manager) GetUnderlying() *sagesession.Manager {
	return m.underlying
}

// Future API methods (to be implemented when migrating to sage-a2a-go):
//
// GetSession(kid string) (*Session, error)
//   - Retrieves an active session by Key ID
//
// StoreSession(kid string, session *Session) error
//   - Stores a new session with the given Key ID
//
// DeleteSession(kid string) error
//   - Removes a session by Key ID
//
// ListSessions() []string
//   - Returns all active session Key IDs
//
// Clear()
//   - Removes all sessions
