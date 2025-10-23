// gateway/gateway.go
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/sage-x-project/sage-multi-agent/types"
)

// Gateway is a simple reverse proxy that can tamper with signed HTTP bodies.
// It demonstrates that when SAGE is ON, any body tampering will break the RFC9421 signature.
type Gateway struct {
	destServerURL *url.URL
	proxy         *httputil.ReverseProxy
	attackMessage string // if non-empty, append to AgentMessage.Content; else pass-through
}

// NewGateway creates a reverse proxy to destServerURL. If attackMessage is non-empty,
// the gateway will mutate the JSON body for /process requests (AgentMessage).
func NewGateway(destServerURL string, attackMessage string) (*Gateway, error) {
	u, err := url.Parse(destServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid destination URL %q: %w", destServerURL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(u)

	// Preserve original Director but ensure URL/Host target the upstream,
	// and keep Host consistent so @authority / @target-uri verification is stable.
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		if orig != nil {
			orig(req)
		} else {
			req.URL.Scheme = u.Scheme
			req.URL.Host = u.Host
		}
		// Force Host header to match upstream
		req.Host = u.Host
	}

	// Optional: nicer error logging from the proxy
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		log.Printf("[GW] proxy error: %v", err)
		http.Error(rw, "Bad Gateway", http.StatusBadGateway)
	}

	return &Gateway{
		destServerURL: u,
		proxy:         proxy,
		attackMessage: attackMessage,
	}, nil
}

// ServeHTTP intercepts POST /process JSON bodies and optionally tampers with them.
// With SAGE ON, downstream middleware should reject due to signature mismatch.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only consider POSTs to /process (our agents use this as the work endpoint)
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/process") {
		// Read and close the original body
		origBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		// Default: forward original body (no tamper)
		bodyToSend := origBytes

		// If attack message is set, try to parse as AgentMessage and mutate Content
		if g.attackMessage != "" {
			var msg types.AgentMessage
			if err := json.Unmarshal(origBytes, &msg); err == nil {
				// Mutate the content to simulate MITM
				oldLen := len(msg.Content)
				msg.Content = msg.Content + g.attackMessage
				if mutated, mErr := json.Marshal(msg); mErr == nil {
					log.Printf("[GW] MITM: AgentMessage.Content mutated (len %d → %d)", oldLen, len(msg.Content))
					bodyToSend = mutated
				} else {
					log.Printf("[GW] failed to marshal mutated body: %v", mErr)
				}
			} else {
				// Not an AgentMessage JSON; still mutate a byte to break signatures.
				bodyToSend = append(bodyToSend, ' ')
				log.Printf("[GW] MITM: non-AgentMessage body mutated by 1 byte")
			}
		}

		// Replace body and fix Content-Length for the proxy
		r.Body = io.NopCloser(bytes.NewReader(bodyToSend))
		r.ContentLength = int64(len(bodyToSend))
		r.Header.Set("Content-Length", strconv.Itoa(len(bodyToSend)))
		// Allow the transport to re-read the body if it retries
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyToSend)), nil
		}

		// Log trace IN/OUT sizes
		log.Printf("[TRACE][GW IN ] %s %s bytes=%d", r.Method, r.URL.Path, len(origBytes))
		log.Printf("[TRACE][GW OUT] %s %s bytes=%d", r.Method, r.URL.Path, len(bodyToSend))
	}

	// Forward (including Signature headers) to the destination server
	g.proxy.ServeHTTP(w, r)
}
