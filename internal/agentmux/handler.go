package agentmux

import (
	"net/http"
	"strings"

	"github.com/sage-x-project/sage-a2a-go/pkg/server"
)

// BuildAgentHandler는 에이전트명("payment"|"medical")에 맞춰
// /status, /{agent}/status, /process, /{agent}/process 를 모두 매핑합니다.
// DID 미들웨어(mw)가 있으면 /process와 /{agent}/process에만 적용합니다.
func BuildAgentHandler(agentName string, openMux, protectedMux *http.ServeMux, mw *server.DIDAuthMiddleware) http.Handler {
	agent := strings.Trim(strings.ToLower(agentName), "/")

	// 보호된 핸들러 (DID 미들웨어 적용)
	var guarded http.Handler = protectedMux
	if mw != nil {
		guarded = mw.Wrap(protectedMux)
	}

	root := http.NewServeMux()

	// Open endpoints
	root.Handle("/status", openMux)
	root.Handle("/"+agent+"/status", openMux)

	// Protected endpoints (둘 다 같은 핸들러)
	root.Handle("/process", guarded)
	root.Handle("/"+agent+"/process", guarded)

	return root
}
