package gateway

import "encoding/json"

// jsonrpc type used in a2a library
// we need to copy it here because it's internal module in the a2a library

// Version is the JSON-RPC version.
const Version = "2.0"

// internal jsonrpc type used in a2a library
type Message struct {
	// JSONRPC specifies the version of the JSON-RPC protocol. MUST be "2.0".
	JSONRPC string `json:"jsonrpc"`
	// ID is an identifier established by the Client that MUST contain a String,
	// Number, or NULL value if included. If it is not included it is assumed
	// to be a notification. The value SHOULD normally not be Null and Numbers
	// SHOULD NOT contain fractional parts.
	ID interface{} `json:"id,omitempty"`
}

type Request struct {
	Message
	// Method is a String containing the name of the method to be invoked.
	Method string `json:"method"`
	// Params is a Structured value that holds the parameter values to be used
	// during the invocation of the method. This member MAY be omitted.
	// It's stored as raw JSON to be decoded by the method handler.
	Params json.RawMessage `json:"params,omitempty"`
}