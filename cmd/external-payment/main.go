package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sage-x-project/sage-multi-agent/internal/a2autil"
	"github.com/sage-x-project/sage-multi-agent/types"
)

func main() {
	port := flag.Int("port", 19083, "port")
	requireSig := flag.Bool("require", true, "require signature (false = optional)")
	flag.Parse()

	// Build DID middleware (local file verifier)
	mw, err := a2autil.BuildDIDMiddleware(true)
	if err != nil {
		log.Printf("[external-payment] DID middleware init failed: %v (running without verify)", err)
	}
	mw.SetErrorHandler(newCompactDIDErrorHandler(log.Default()))
	open := http.NewServeMux()
	open.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":         "external-payment",
			"type":         "payment",
			"sage_enabled": true,
			"time":         time.Now().Format(time.RFC3339),
		})
	})

	protected := http.NewServeMux()
	protected.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		// read body
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		r.Body.Close()

		var in types.AgentMessage
		if err := json.Unmarshal(buf.Bytes(), &in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// Do "payment" work (demo: echo)
		out := types.AgentMessage{
			ID:        in.ID + "-ok",
			From:      "external-payment",
			To:        in.From,
			Type:      "response",
			Content:   fmt.Sprintf("External payment processed at %s (echo): %s", time.Now().Format(time.RFC3339), strings.TrimSpace(in.Content)),
			Timestamp: time.Now(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	var handler http.Handler = open
	if mw != nil {
		wrapped := mw.Wrap(protected)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/status":
				open.ServeHTTP(w, r)
			default:
				wrapped.ServeHTTP(w, r)
			}
		})
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[boot] external-payment on %s (requireSig=%v)", addr, *requireSig)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// newCompactDIDErrorHandler logs only the final (root) error message
// and returns a minimal JSON response to the client.
func newCompactDIDErrorHandler(l *log.Logger) func(w http.ResponseWriter, r *http.Request, err error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		re := rootError(err)

		// log: 딱 에러 메시지만
		if l != nil {
			l.Printf("[did-auth] %s", re.Error())
		}

		// client response: 최소 정보만(JSON)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "unauthorized",
			"reason": re.Error(),
		})
	}
}

// rootError returns the deepest wrapped error.
func rootError(err error) error {
	e := err
	for {
		u := errors.Unwrap(e)
		if u == nil {
			return e
		}
		e = u
	}
}
