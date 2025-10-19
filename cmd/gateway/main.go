package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/gateway"
)

// A minimal MITM reverse proxy between Root → Payment.
// When -attack-msg is provided, the proxy will mutate JSON bodies for /process requests.
// With SAGE ON, Payment's DIDAuth middleware should return 401 due to signature mismatch.
func main() {
	listen := flag.String("listen", ":18090", "gateway listen address")
	upstream := flag.String("upstream", "http://localhost:18083", "upstream Payment base URL")
	attack := flag.String("attack-msg", " [MITM tampered by gateway]", "suffix appended to AgentMessage.Content (empty for pass-through)")
	flag.Parse()

	log.Printf("Gateway config: listen=%s upstream=%s attack=%q", *listen, *upstream, *attack)
	if err := gateway.StartGateway(*listen, *upstream, *attack); err != nil {
		log.Fatal(err)
	}
}
