package agentmux

import (
	"net/http"
	"strings"

	"github.com/sage-x-project/sage-a2a-go/pkg/server"
)

// BuildAgentHandler maps all endpoints for the given agent name ("payment"|"medical"):
// /status, /{agent}/status, /process, /{agent}/process.
// If a DID middleware (mw) is provided, apply it only to /process and /{agent}/process.
func BuildAgentHandler(agentName string, openMux, protectedMux *http.ServeMux, mw *server.DIDAuthMiddleware) http.Handler {
	agent := strings.Trim(strings.ToLower(agentName), "/")

    // Protected handler (apply DID middleware)
	var guarded http.Handler = protectedMux
	if mw != nil {
		guarded = mw.Wrap(protectedMux)
	}

	root := http.NewServeMux()

	// Open endpoints
	root.Handle("/status", openMux)
	root.Handle("/"+agent+"/status", openMux)

    // Protected endpoints (both mapped to the same handler)
	root.Handle("/process", guarded)
	root.Handle("/"+agent+"/process", guarded)

	return root
}
