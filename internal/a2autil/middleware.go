package a2autil

import (
	"fmt"
	"net/http"

	// a2a-go: DID verifier, key selector, RFC9421 verifier 인터페이스/구현
	"github.com/sage-x-project/sage-a2a-go/pkg/server"
	"github.com/sage-x-project/sage-a2a-go/pkg/verifier"
)

// DIDAuth wraps the a2a-go server middleware so callers don't depend on a2a-go directly.
type DIDAuth struct {
	Mw *server.DIDAuthMiddleware
}

// BuildDIDMiddleware wires on-chain DID verification using your provided EthereumClient.
// - ec:        a2a-go EthereumClient (온체인 레지스트리/키 해석 구현체; 너희 쪽에서 만든 것 전달)
// - optional:  true면 서명헤더 없어도 통과(데모/점진적 롤아웃용), false면 반드시 서명 필요(권장)
func BuildDIDMiddleware(ec verifier.EthereumClient, optional bool) *DIDAuth {
	// Key selection policy (e.g., protocol-aware). 여기서는 기본 선택기 사용.
	selector := verifier.NewDefaultKeySelector(ec)

	// RFC9421 HTTP signature verifier (본문 포함 검증은 요청 자체가 그 헤더 세트를 충족해야 함)
	sigVerifier := verifier.NewRFC9421Verifier()

	// DID verifier = (온체인 클라이언트 + 키선택 + HTTP서명검증)
	didVerifier := verifier.NewDefaultDIDVerifier(ec, selector, sigVerifier)

	// a2a-go의 표준 미들웨어로 감싼다.
	mw := server.NewDIDAuthMiddlewareWithVerifier(didVerifier)
	mw.SetOptional(optional)

	// JSON 형식 401로 응답(디폴트는 text/plain)
	mw.SetErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// 최소한의 정보만 노출 (디버그가 필요하면 detail 출력 유지)
		_, _ = w.Write([]byte(`{"error":"signature_verification_failed","detail":"` + sanitizeErr(err.Error()) + `"}`))
	})

	return &DIDAuth{Mw: mw}
}

// BuildDIDMiddlewareFromChain creates a DID-auth middleware backed by an EthereumClient.
//
// For now in the demo, we support a file-backed client (generated_agent_keys.json) as the resolver.
// Later you can plug a real on-chain resolver that implements verifier.EthereumClient.
func BuildDIDMiddlewareFromChain(keysJSONPath string, optional bool) (*server.DIDAuthMiddleware, error) {
	if keysJSONPath == "" {
		keysJSONPath = "generated_agent_keys.json" // sane default for demo
	}

	// 1) resolver (file-backed)
	staticClient, err := NewStaticEthereumClientFromFile(keysJSONPath)
	if err != nil {
		return nil, fmt.Errorf("resolver init: %w", err)
	}

	// 2) wrap with DIDAuthMiddleware (it internally builds DefaultDIDVerifier+RFC9421)
	var ethClient verifier.EthereumClient = staticClient
	mw := server.NewDIDAuthMiddleware(ethClient)
	mw.SetOptional(optional)
	return mw, nil
}

// sanitizeErr trims/control chars if you want; kept no-op for brevity.
// You can harden it (e.g., cap length, strip quotes) to avoid header injection via error text.
func sanitizeErr(s string) string { return s }
