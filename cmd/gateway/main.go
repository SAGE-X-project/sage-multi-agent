package main

import (
	"bytes"
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

func proxyKeepPath(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("bad upstream url %q: %v", target, err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)

	// 기본 Director가 path를 건드릴 수 있으므로, host/scheme만 덮어쓰고 path/rawpath/rawquery는 원본 유지
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		_ = origDirector // 의도적으로 사용 안 함
		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
		req.Host = u.Host // @authority 일관성
	}

	// 아웃바운드(업스트림으로 나가는) 패킷 원문 덤프
	rp.Transport = &dumpTransport{base: http.DefaultTransport}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("[GW][ERR] %s %s: %v", r.Method, r.URL.String(), e)
		http.Error(w, "gateway error: "+e.Error(), http.StatusBadGateway)
	}
	return rp
}

// === 인바운드 풀덤프 + 바디 재주입 미들웨어 ===
func dumpInboundMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()

		// /.../process 로 들어오는 POST만 바디까지 풀덤프
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/process") {
			// 원본 바디 읽기
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read body", http.StatusBadRequest)
				return
			}
			_ = r.Body.Close()

			// 인바운드 원문 패킷 찍기 (요청라인+헤더+바디)
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

			// 바디 재주입 (프록시/핸들러가 다시 읽을 수 있도록)
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
			r.GetBody = func() (io.ReadCloser, error) { // DumpRequestOut이 재사용 가능
				return io.NopCloser(bytes.NewReader(body)), nil
			}
		} else {
			// 기타 요청은 라인만 찍기
			log.Printf("\n===== GW INBOUND  <<< %s %s =====", r.Method, r.URL.Path)
		}

		rw := &recorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)

		log.Printf("[TRACE][GW] %s %s status=%d dur=%s",
			r.Method, r.URL.String(), rw.status, time.Since(start))
	})
}

// 아웃바운드 패킷 덤프용 RoundTripper
type dumpTransport struct{ base http.RoundTripper }

func (t *dumpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// /.../process 로 나가는 POST만 덤프
	if req != nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/process") {
		// DumpRequestOut(true)는 req.GetBody가 있으면 그걸 사용함.
		if dump, err := httputil.DumpRequestOut(req, true); err == nil {
			log.Printf("\n===== GW OUTBOUND >>> %s %s =====\n%s\n===== END GW OUTBOUND =====\n",
				req.Method, req.URL.String(), dump)
		} else {
			log.Printf("[GW][WARN] outbound dump error: %v", err)
		}
	}
	return t.base.RoundTrip(req)
}

// 응답 상태코드 기록용
type recorder struct {
	http.ResponseWriter
	status int
}

func (rw *recorder) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func main() {
	listen := envOr("GW_LISTEN", ":5500")
	payUp := envOr("PAYMENT_UPSTREAM", "http://localhost:19083")
	medUp := envOr("MEDICAL_UPSTREAM", "http://localhost:19082")
	plnUp := envOr("PLANNING_UPSTREAM", "")

	mux := http.NewServeMux()

	// /payment/*  → payment upstream (경로 그대로 전달)
	mux.Handle("/payment/", proxyKeepPath(payUp))
	// /medical/*  → medical upstream
	mux.Handle("/medical/", proxyKeepPath(medUp))
	// (옵션) /planning/* → planning upstream
	if plnUp != "" {
		mux.Handle("/planning/", proxyKeepPath(plnUp))
	}

	// 헬스체크
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"gw":"ready"}`))
	})

	h := dumpInboundMW(mux)

	log.Printf("[GW] listening on %s\nPAYMENT_UPSTREAM=%s\nMEDICAL_UPSTREAM=%s\nPLANNING_UPSTREAM=%s",
		listen, payUp, medUp, plnUp)

	if err := http.ListenAndServe(listen, h); err != nil {
		log.Fatal(err)
	}
}
