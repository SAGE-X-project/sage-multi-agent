package hpke

import (
	sagetransport "github.com/sage-x-project/sage/pkg/agent/transport"
)

// Transport is a type alias for the sage transport.MessageTransport interface.
// This allows package users to reference the Transport type without
// importing sage directly.
type Transport = sagetransport.MessageTransport
