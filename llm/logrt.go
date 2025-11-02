package llm

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"regexp"
)

type loggingRT struct{ base http.RoundTripper }

var authRe = regexp.MustCompile(`(?i)Authorization:\s*Bearer\s+[A-Za-z0-9\-\._~+/=]+`)

func (l *loggingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	// req dump
	var reqDump []byte
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(b))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
		d, _ := httputil.DumpRequestOut(req, true)
		reqDump = d
	}
	safe := authRe.ReplaceAll(reqDump, []byte("Authorization: Bearer ***REDACTED***"))
	if len(safe) > 0 {
		log.Printf("\n===== LLM OUTBOUND >>> %s %s =====\n%s\n===== END LLM OUTBOUND =====\n", req.Method, req.URL.String(), safe)
	}

	// send
	resp, err := l.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// resp dump
	if resp != nil && resp.Body != nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(bytes.NewReader(b))
		d, _ := httputil.DumpResponse(resp, true)
		if len(d) > 4096 { // truncate if too long
			d = append(d[:4096], []byte("\n... (truncated) ...")...)
		}
		log.Printf("\n===== LLM INBOUND  <<< %s %s =====\n%s\n===== END LLM INBOUND  =====\n", req.Method, req.URL.String(), d)
	}
	return resp, nil
}
