// cmd/gateway/main.go
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func computeContentDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
}

// looksLikeHPKEHandshake returns true if the request appears to be an HPKE handshake.
// Heuristics:
//  1. X-SAGE-HPKE: v1 AND X-KID is empty  -> likely a handshake (no session key ID yet)
//  2. X-SAGE-TASK-ID starts with "hpke/"  -> handshake/HPKE control task
func looksLikeHPKEHandshake(req *http.Request) bool {
	hpke := strings.TrimSpace(req.Header.Get("X-SAGE-HPKE"))
	kid := strings.TrimSpace(req.Header.Get("X-KID"))
	task := strings.ToLower(strings.TrimSpace(req.Header.Get("X-SAGE-TASK-ID")))

	if strings.EqualFold(hpke, "v1") && kid == "" {
		return true
	}
	if strings.HasPrefix(task, "hpke/") {
		return true
	}
	return false
}

// tamperTransport optionally injects an "attack message" into outbound JSON bodies
// for POST .../process calls, while skipping HPKE traffic.
// - Never tampers with application/sage+hpke (ciphertext)
// - Never tampers with JSON that looks like an HPKE handshake
// - Only tampers with plain JSON data-mode requests
type tamperTransport struct {
	base            http.RoundTripper
	attackMsg       string
	recomputeDigest bool
}

func (t *tamperTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	isProcessPost := (req != nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/process"))

	// --- Tamper only on data-mode JSON (not HPKE handshake, not HPKE ciphertext) ---
	if isProcessPost && t.attackMsg != "" {
		ct := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))

		// HPKE ciphertext: do not touch
		if strings.HasPrefix(ct, "application/sage+hpke") {
			// pass
		} else if strings.HasPrefix(ct, "application/json") {
			// HPKE handshake (JSON control path): do not touch
			if looksLikeHPKEHandshake(req) {
				// pass
			} else {
				// Plain JSON data-mode: inject the attack message
				var body []byte
				if req.Body != nil {
					body, _ = io.ReadAll(req.Body)
					_ = req.Body.Close()
				}
				newBody := body

				// Best-effort: if it’s a JSON object, append to "Content" if present,
				// otherwise add a _gw_tamper field; if not JSON, append raw text.
				var m map[string]any
				if len(body) > 0 && body[0] == '{' && json.Unmarshal(body, &m) == nil {
					if old, ok := m["Content"].(string); ok {
						m["Content"] = old + "\n" + t.attackMsg
					} else {
						m["_gw_tamper"] = t.attackMsg
					}
					if b2, err := json.Marshal(m); err == nil {
						newBody = b2
					} else {
						newBody = append(body, []byte("\n"+t.attackMsg)...)
					}
				} else {
					newBody = append(body, []byte("\n"+t.attackMsg)...)
				}

				req.Body = io.NopCloser(bytes.NewReader(newBody))
				req.ContentLength = int64(len(newBody))
				req.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
				req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(newBody)), nil }

				// If upstream validates Content-Digest, recompute it after tamper
				if t.recomputeDigest {
					req.Header.Set("Content-Digest", computeContentDigest(newBody))
				}
			}
		}
	}

	// --- Always dump the final outbound packet for POST .../process (after tamper/no-tamper) ---
	if isProcessPost {
		if dump, err := httputil.DumpRequestOut(req, true); err == nil {
			log.Printf("\n===== GW OUTBOUND >>> %s %s =====\n%s\n===== END GW OUTBOUND =====\n",
				req.Method, req.URL.String(), dump)
		} else {
			log.Printf("[GW][WARN] outbound dump error: %v", err)
		}
	}

	return t.base.RoundTrip(req)
}

// proxyKeepPath builds a reverse proxy that preserves the original request path/query,
// replaces only the scheme/host, and uses tamperTransport for outbound traffic.
func proxyKeepPath(target string, attackMsg string, recomputeDigest bool) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("bad upstream url %q: %v", target, err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)

	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		_ = origDirector // keep original path/query
		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
		req.Host = u.Host // keep @authority consistent
	}

	rp.Transport = &tamperTransport{
		base:            http.DefaultTransport,
		attackMsg:       attackMsg,
		recomputeDigest: recomputeDigest,
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("[GW][ERR] %s %s: %v", r.Method, r.URL.String(), e)
		http.Error(w, "gateway error: "+e.Error(), http.StatusBadGateway)
	}
	return rp
}

// dumpInboundMW logs inbound requests. For POST .../process it dumps the full request
// (including body), then reinjects the body so handlers/proxy can read it again.
func dumpInboundMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/process") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read body", http.StatusBadRequest)
				return
			}
			_ = r.Body.Close()

			clone := r.Clone(r.Context())
			clone.Body = io.NopCloser(bytes.NewReader(body))
			clone.ContentLength = int64(len(body))
			clone.Header.Set("Content-Length", strconv.Itoa(len(body)))

			if dump, err := httputil.DumpRequest(clone, true); err == nil {
				log.Printf("\n===== GW INBOUND  <<< %s %s =====\n%s\n===== END GW INBOUND  =====\n",
					clone.Method, clone.URL.Path, dump)
			} else {
				log.Printf("[GW][WARN] inbound dump error: %v", err)
			}

			// Re-inject body for downstream handlers/proxy
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		} else {
			log.Printf("\n===== GW INBOUND  <<< %s %s =====", r.Method, r.URL.Path)
		}

		rw := &recorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)

		log.Printf("[TRACE][GW] %s %s status=%d dur=%s",
			r.Method, r.URL.String(), rw.status, time.Since(start))
	})
}

// recorder tracks the status code written by the handler for logging.
type recorder struct {
	http.ResponseWriter
	status int
}

func (rw *recorder) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func main() {
	// Env defaults + flags
	listenDef := envOr("GW_LISTEN", ":5500")
	payDef := envOr("PAYMENT_UPSTREAM", "http://localhost:19083")
	medDef := envOr("MEDICAL_UPSTREAM", "http://localhost:19082")
	attackDef := os.Getenv("ATTACK_MESSAGE") // non-empty in tamper mode

	listen := flag.String("listen", listenDef, "listen address")
	payUp := flag.String("pay-upstream", payDef, "payment upstream")
	medUp := flag.String("med-upstream", medDef, "medical upstream")
	attackMsg := flag.String("attack-msg", attackDef, "tamper message (empty = pass-through)")
	flag.Parse()

	mux := http.NewServeMux()

	// Upstreams (preserve original path/query; tamper only on plain JSON data-mode)
	mux.Handle("/payment/", proxyKeepPath(*payUp, *attackMsg, true))
	mux.Handle("/medical/", proxyKeepPath(*medUp, *attackMsg, true))

	// Health endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"gw":"ready","tamper":` + strconv.FormatBool(strings.TrimSpace(*attackMsg) != "") + `}`))
	})

	h := dumpInboundMW(mux)

	log.Printf("[GW] listening on %s\nPAYMENT_UPSTREAM=%s\nMEDICAL_UPSTREAM=%s\nATTACK_MESSAGE=%q",
		*listen, *payUp, *medUp, *attackMsg)

	if err := http.ListenAndServe(*listen, h); err != nil {
		log.Fatal(err)
	}
}
