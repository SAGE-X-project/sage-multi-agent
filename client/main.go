package main

import (
    "flag"
    "fmt"
    "net/http"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    "github.com/a2aproject/a2a-go/a2apb"
    apihandlers "github.com/sage-x-project/sage-multi-agent/api"
)

func main() {
    port := flag.Int("port", 8086, "")
    grpcTarget := flag.String("grpc", "localhost:8084", "")
    flag.Parse()

    conn, err := grpc.NewClient(*grpcTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil { panic(err) }
    defer conn.Close()

    gateway := apihandlers.NewA2AGateway(a2apb.NewA2AServiceClient(conn))
    http.HandleFunc("/send/prompt", gateway.HandlePrompt)

    _ = http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
}
