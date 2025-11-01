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

// Tamper + Outbound dump RoundTripper
type tamperTransport struct {
	base            http.RoundTripper
	attackMsg       string
	recomputeDigest bool
}

func (t *tamperTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    // Target for tampering: POST .../process
	if req != nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/process") && t.attackMsg != "" {
		ct := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))

        // Do not alter HPKE (application/sage+hpke) by default; tamper here only for demos
		if strings.HasPrefix(ct, "application/json") {
			var body []byte
			if req.Body != nil {
				body, _ = io.ReadAll(req.Body)
				_ = req.Body.Close()
			}
			newBody := body

            // Parse JSON and inject into Content; on failure add _gw_tamper; otherwise append
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

			if t.recomputeDigest {
				req.Header.Set("Content-Digest", computeContentDigest(newBody))
			}
		}
	}

    // Outbound dump (final packet after tamper/no-tamper)
	if req != nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/process") {
		if dump, err := httputil.DumpRequestOut(req, true); err == nil {
			log.Printf("\n===== GW OUTBOUND >>> %s %s =====\n%s\n===== END GW OUTBOUND =====\n",
				req.Method, req.URL.String(), dump)
		} else {
			log.Printf("[GW][WARN] outbound dump error: %v", err)
		}
	}
	return t.base.RoundTrip(req)
}

func proxyKeepPath(target string, attackMsg string, recomputeDigest bool) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("bad upstream url %q: %v", target, err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)

	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
    _ = origDirector // Preserve original path/query
		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
    req.Host = u.Host // Keep @authority consistent
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

// Inbound full dump + body re-injection
func dumpInboundMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

        // For POST to .../process, dump full request including body
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
    attackDef := os.Getenv("ATTACK_MESSAGE") // set by scripts in tamper mode

	listen := flag.String("listen", listenDef, "listen address")
	payUp := flag.String("pay-upstream", payDef, "payment upstream")
	medUp := flag.String("med-upstream", medDef, "medical upstream")
	attackMsg := flag.String("attack-msg", attackDef, "tamper message (empty = pass-through)")
	flag.Parse()

	mux := http.NewServeMux()

    // Upstreams (preserve path)
	mux.Handle("/payment/", proxyKeepPath(*payUp, *attackMsg, true))
	mux.Handle("/medical/", proxyKeepPath(*medUp, *attackMsg, true))

    // Health check
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
