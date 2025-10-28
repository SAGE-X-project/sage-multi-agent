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

// Gateway is a simple reverse proxy between Payment (agent) and External.
// It logs exact HTTP packets (inbound/outbound) for debugging frontend flows and can optionally
// tamper with plaintext bodies to demonstrate signature/HPKE protections.
type Gateway struct {
	destServerURL *url.URL
	proxy         *httputil.ReverseProxy
	attackMessage string // if non-empty, append to AgentMessage.Content; else pass-through
}

// dumpRT logs the exact outbound HTTP packet (request-line + headers + body) that the proxy sends.
type dumpRT struct {
	base http.RoundTripper
}

func (d *dumpRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/process") {
		if dump, err := httputil.DumpRequestOut(req, true); err == nil {
			log.Printf("\n===== GW OUTBOUND >>> %s %s =====\n%s\n===== END GW OUTBOUND =====\n",
				req.Method, req.URL.String(), dump)
		}
	}
	return d.base.RoundTrip(req)
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

	// Log the exact outbound packet the proxy sends
	proxy.Transport = &dumpRT{base: http.DefaultTransport}

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

// ServeHTTP intercepts POST /process bodies and optionally tampers with them.
// With SAGE ON, downstream DID middleware should reject mutated plaintext; with HPKE ON, ciphertext
// is unreadable and tamper causes decrypt failure at External.
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

		// Dump the incoming packet EXACTLY as received by the gateway
		// (request-line + headers + body)
		{
			clone := r.Clone(r.Context())
			clone.Body = io.NopCloser(bytes.NewReader(origBytes))
			clone.ContentLength = int64(len(origBytes))
			clone.Header.Set("Content-Length", strconv.Itoa(len(origBytes)))
			if dump, err := httputil.DumpRequest(clone, true); err == nil {
				// print ONLY the raw HTTP packet
				log.Printf("\n===== GW INBOUND  <<< %s %s =====\n%s\n===== END GW INBOUND  =====\n",
					clone.Method, clone.URL.String(), dump)
			}
		}
		// Default: forward original body (no tamper)
		bodyToSend := origBytes

		if g.attackMessage != "" {
			switch {
            case isHPKEHandshake(r):
                // Never tamper with HPKE handshake
                log.Printf("[GW] SKIP tamper: HPKE handshake")

            case isHPKEData(r):
                // HPKE data mode: slightly corrupt one byte of ciphertext
                mut := tamperCiphertextFlip(origBytes)
				if !bytes.Equal(mut, origBytes) {
					bodyToSend = mut
					log.Printf("[GW] MITM: HPKE ciphertext mutated (flip 1 byte) len=%d", len(bodyToSend))
				} else {
					log.Printf("[GW] WARN: ciphertext tamper produced identical bytes (len=%d)", len(origBytes))
				}

            default:
                // Attempt to mutate Content only for plaintext AgentMessage
                var msg types.AgentMessage
				if err := json.Unmarshal(origBytes, &msg); err == nil {
					oldLen := len(msg.Content)
					msg.Content = msg.Content + g.attackMessage
					if mutated, mErr := json.Marshal(msg); mErr == nil {
						bodyToSend = mutated
						log.Printf("[GW] MITM: AgentMessage.Content mutated (len %d → %d)", oldLen, len(msg.Content))
					} else {
						log.Printf("[GW] failed to marshal mutated body: %v", mErr)
					}
                } else {
                    // Not JSON → append a single byte to break signature (optional)
                    bodyToSend = append(append([]byte{}, origBytes...), ' ')
					log.Printf("[GW] MITM: non-JSON body mutated by 1 byte (len=%d→%d)", len(origBytes), len(bodyToSend))
				}
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

func isHPKEHandshake(r *http.Request) bool {
    // HPKE handshake detection is tolerant to header casing variations.
    // Handshake if: X-SAGE-HPKE: v1 AND no X-KID AND TaskID equals "hpke/complete@v1" (when present)
    hpke := strings.EqualFold(r.Header.Get("X-SAGE-HPKE"), "v1")
    kid := strings.TrimSpace(r.Header.Get("X-KID"))
    // Accept both X-SAGE-Task-ID and X-SAGE-Task-Id
    task := strings.TrimSpace(r.Header.Get("X-SAGE-Task-ID"))
    if task == "" {
        task = strings.TrimSpace(r.Header.Get("X-SAGE-Task-Id"))
    }
    if !hpke || kid != "" {
        return false
    }
    // When task header exists, ensure it matches; otherwise treat as handshake by headers alone
    if task != "" && !strings.EqualFold(task, "hpke/complete@v1") {
        return false
    }
    return true
}

func isHPKEData(r *http.Request) bool {
    // HPKE data mode: X-SAGE-HPKE: v1 && X-KID present
    return strings.EqualFold(r.Header.Get("X-SAGE-HPKE"), "v1") &&
        strings.TrimSpace(r.Header.Get("X-KID")) != ""
}

func tamperCiphertextFlip(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	out := make([]byte, len(b))
	copy(out, b)
	out[0] ^= 0x01
	return out
}
