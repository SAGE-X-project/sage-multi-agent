// cmd/gateway/main.go
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/sage-x-project/sage-multi-agent/gateway"
)

// A minimal MITM reverse proxy between agent → external agent.
// When -attack-msg is provided, the proxy will mutate JSON bodies for /process requests.
// With SAGE ON, upstream DIDAuth middleware should return 4xx due to signature mismatch.
func main() {
	listen := flag.String("listen", ":18090", "gateway listen address")
	upstream := flag.String("upstream", "http://localhost:18083", "upstream target base URL")
	attack := flag.String("attack-msg", "", "suffix appended to AgentMessage.Content (empty = pass-through)")
	flag.Parse()

	log.Printf("Gateway config: listen=%s upstream=%s attack=%q", *listen, *upstream, *attack)

	gw, err := gateway.NewGateway(*upstream, *attack)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Gateway config: listen=%s upstream=%s attack=%q", *listen, *upstream, *attack)
	log.Fatal(http.ListenAndServe(*listen, gw))
}
