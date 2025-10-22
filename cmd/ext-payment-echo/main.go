// Minimal external echo without deps
package main

import (
  "encoding/json"
  "flag"
  "fmt"
  "log"
  "net/http"
)

func main() {
  port := flag.Int("port", 19083, "port")
  flag.Parse()

  mux := http.NewServeMux()
  mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type","application/json")
    w.Write([]byte(`{"id":"ext","from":"external","to":"payment","content":"OK (external echo)","type":"response"}`))
  })
  mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type","application/json")
    json.NewEncoder(w).Encode(map[string]any{
      "name":"external-echo",
      "type":"payment",
      "sage_enabled": false,
    })
  })
  addr := fmt.Sprintf(":%d", *port)
  log.Printf("external echo on %s\n", addr)
  log.Fatal(http.ListenAndServe(addr, mux))
}
