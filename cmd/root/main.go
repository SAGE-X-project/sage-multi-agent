// cmd/root-agent/main.go
package main

import (
	"flag"
	"log"

	"github.com/sage-x-project/sage-multi-agent/agents/root"
)

func main() {
	port := flag.Int("port", 18080, "root agent port")
	payment := flag.String("payment", "http://localhost:18083", "payment agent base URL")
	planning := flag.String("planning", "http://localhost:18081", "planning agent base URL")
	ordering := flag.String("ordering", "http://localhost:18082", "ordering agent base URL")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	ra := root.NewRootAgent("root", *port)
	ra.RegisterAgent("payment", "payment", *payment)
	ra.RegisterAgent("planning", "planning", *planning)
	ra.RegisterAgent("ordering", "ordering", *ordering)

	if err := ra.Start(); err != nil {
		log.Fatalf("[root] server error: %v", err)
	}
}
