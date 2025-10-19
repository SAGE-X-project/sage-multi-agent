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
	targetURL, err := url.Parse(destServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid destination URL: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Preserve original Director but ensure Host header matches the target
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = targetURL.Host
	}

	return &Gateway{
		destServerURL: targetURL,
		proxy:         proxy,
		attackMessage: attackMessage,
	}, nil
}

// ServeHTTP intercepts POST /process JSON bodies and optionally tampers with them.
// With SAGE ON, downstream middleware should reject due to signature mismatch.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only consider POSTs to /process (our agents use this as the work endpoint)
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/process") {
		// Drain the original body
		origBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
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
				msg.Content = msg.Content + g.attackMessage
				mutated, err := json.Marshal(msg)
				if err != nil {
					http.Error(w, "Failed to marshal mutated body", http.StatusInternalServerError)
					return
				}
				log.Printf("[GW] MITM: AgentMessage.Content mutated (len %d → %d)", len(origBytes), len(mutated))
				bodyToSend = mutated
			} else {
				// Not an AgentMessage JSON; still mutate a byte to break signatures.
				bodyToSend = append(bodyToSend, ' ') // minimal change is enough
				log.Printf("[GW] MITM: non-AgentMessage body mutated by one byte")
			}
		}

		// Replace body and fix Content-Length for the proxy
		r.Body = io.NopCloser(bytes.NewReader(bodyToSend))
		r.ContentLength = int64(len(bodyToSend))
		r.Header.Set("Content-Length", fmt.Sprintf("%d", len(bodyToSend)))

		// Log trace IN/OUT
		log.Printf("[TRACE][GW IN ] %s %s bytes=%d", r.Method, r.URL.Path, len(origBytes))
		log.Printf("[TRACE][GW OUT] %s %s bytes=%d", r.Method, r.URL.Path, len(bodyToSend))
	}

	// Forward (including Signature headers) to the destination server
	g.proxy.ServeHTTP(w, r)
}

// StartGateway starts the gateway HTTP server.
func StartGateway(listenAddr, destServerURL, attackMessage string) error {
	gateway, err := NewGateway(destServerURL, attackMessage)
	if err != nil {
		return err
	}

	log.Printf("Starting gateway on %s → %s (tamper=%v)", listenAddr, destServerURL, attackMessage != "")
	return http.ListenAndServe(listenAddr, gateway)
}
