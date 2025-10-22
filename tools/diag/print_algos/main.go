package main

import (
    "encoding/json"
    "fmt"
    "log"

    sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
    _ "github.com/sage-x-project/sage/pkg/agent/crypto/keys" // ensure RegisterAlgorithm runs via init()
)

func main() {
    // Helper to pretty print
    toJSON := func(v any) string {
        b, _ := json.MarshalIndent(v, "", "  ")
        return string(b)
    }

    // Check registry entries for common key types
    for _, kt := range []sagecrypto.KeyType{sagecrypto.KeyTypeSecp256k1, sagecrypto.KeyTypeEd25519, sagecrypto.KeyTypeRSA} {
        info, err := sagecrypto.GetAlgorithmInfo(kt)
        if err != nil {
            log.Printf("GetAlgorithmInfo(%s) ERROR: %v", kt, err)
        } else {
            log.Printf("GetAlgorithmInfo(%s): %s", kt, toJSON(info))
        }
        alg, err := sagecrypto.GetRFC9421AlgorithmName(kt)
        if err != nil {
            log.Printf("GetRFC9421AlgorithmName(%s) ERROR: %v", kt, err)
        } else {
            log.Printf("GetRFC9421AlgorithmName(%s): %s", kt, alg)
        }
    }

    // List RFC9421 supported algorithms
    list := sagecrypto.ListRFC9421SupportedAlgorithms()
    fmt.Printf("RFC9421-supported algorithms: %v\n", list)
}

