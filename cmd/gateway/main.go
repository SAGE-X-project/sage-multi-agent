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
	// 조작 대상: POST .../process
	if req != nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/process") && t.attackMsg != "" {
		ct := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))

		// HPKE(application/sage+hpke)은 기본적으로 건드리지 않음 (변조시 서명/복호 실패 데모 원하면 여기서 깨면 됨)
		if strings.HasPrefix(ct, "application/json") {
			var body []byte
			if req.Body != nil {
				body, _ = io.ReadAll(req.Body)
				_ = req.Body.Close()
			}
			newBody := body

			// JSON 파싱 후 Content 필드에 주입, 실패하면 _gw_tamper 필드 추가, 그래도 안되면 단순 append
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

	// 아웃바운드 덤프 (조작 전/후 최종 송신 패킷)
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
		_ = origDirector // 경로/쿼리는 원본 유지
		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
		req.Host = u.Host // @authority 일관성
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

// 인바운드 풀덤프 + 바디 재주입
func dumpInboundMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// /.../process 로 들어오는 POST만 바디까지 풀덤프
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
	// Env 기본값 + 플래그
	listenDef := envOr("GW_LISTEN", ":5500")
	payDef := envOr("PAYMENT_UPSTREAM", "http://localhost:19083")
	medDef := envOr("MEDICAL_UPSTREAM", "http://localhost:19082")
	attackDef := os.Getenv("ATTACK_MESSAGE") // tamper 모드에서 스크립트가 세팅

	listen := flag.String("listen", listenDef, "listen address")
	payUp := flag.String("pay-upstream", payDef, "payment upstream")
	medUp := flag.String("med-upstream", medDef, "medical upstream")
	attackMsg := flag.String("attack-msg", attackDef, "tamper message (empty = pass-through)")
	flag.Parse()

	mux := http.NewServeMux()

	// Upstreams (경로 유지)
	mux.Handle("/payment/", proxyKeepPath(*payUp, *attackMsg, true))
	mux.Handle("/medical/", proxyKeepPath(*medUp, *attackMsg, true))

	// 헬스체크
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
