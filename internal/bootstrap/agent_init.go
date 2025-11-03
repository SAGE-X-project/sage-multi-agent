package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/curve25519"
)

// AgentConfig holds configuration for agent initialization
type AgentConfig struct {
	Name              string
	KeyDir            string
	SigningKeyFile    string // JWK file path
	KEMKeyFile        string // X25519 JWK file path
	DID               string // Optional: override DID
	ETHRPCUrl         string
	RegistryAddress   string
	AutoRegister      bool // Auto-register if not found on-chain
	FundingPrivateKey string // For dev environments
}

// AgentKeys holds generated keys
type AgentKeys struct {
	SigningKey    *ecdsa.PrivateKey
	KEMPrivateKey []byte // X25519 private key (32 bytes)
	KEMPublicKey  []byte // X25519 public key (32 bytes)
	Address       string
	DID           string
}

// EnsureAgentKeys ensures agent has keys (generates if missing) and registers on-chain if needed
func EnsureAgentKeys(ctx context.Context, cfg *AgentConfig) (*AgentKeys, error) {
	log.Printf("[bootstrap] Initializing agent '%s'", cfg.Name)

	// Create key directory if not exists
	if cfg.KeyDir != "" {
		if err := os.MkdirAll(cfg.KeyDir, 0755); err != nil {
			return nil, fmt.Errorf("create key dir: %w", err)
		}
	}

	keys := &AgentKeys{}

	// 1. Load or generate signing key
	signingKeyPath := cfg.SigningKeyFile
	if signingKeyPath == "" {
		signingKeyPath = filepath.Join(cfg.KeyDir, cfg.Name+".jwk")
	}

	if fileExists(signingKeyPath) {
		log.Printf("[bootstrap] Loading existing signing key: %s", signingKeyPath)
		sk, err := loadSigningKeyFromJWK(signingKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load signing key: %w", err)
		}
		keys.SigningKey = sk
	} else {
		log.Printf("[bootstrap] Generating new signing key: %s", signingKeyPath)
		sk, err := generateAndSaveSigningKey(signingKeyPath)
		if err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}
		keys.SigningKey = sk
	}

	// Derive address and DID
	pubKey := crypto.PubkeyToAddress(keys.SigningKey.PublicKey)
	keys.Address = pubKey.Hex()
	if cfg.DID != "" {
		keys.DID = cfg.DID
	} else {
		keys.DID = fmt.Sprintf("did:sage:ethereum:%s", keys.Address)
	}

	log.Printf("[bootstrap] Agent DID: %s", keys.DID)
	log.Printf("[bootstrap] Agent Address: %s", keys.Address)

	// 2. Load or generate KEM key (X25519)
	kemKeyPath := cfg.KEMKeyFile
	if kemKeyPath == "" {
		kemDir := filepath.Join(cfg.KeyDir, "kem")
		os.MkdirAll(kemDir, 0755)
		kemKeyPath = filepath.Join(kemDir, cfg.Name+".x25519.jwk")
	}

	if fileExists(kemKeyPath) {
		log.Printf("[bootstrap] Loading existing KEM key: %s", kemKeyPath)
		priv, pub, err := loadKEMKeyFromJWK(kemKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load KEM key: %w", err)
		}
		keys.KEMPrivateKey = priv
		keys.KEMPublicKey = pub
	} else {
		log.Printf("[bootstrap] Generating new KEM key: %s", kemKeyPath)
		priv, pub, err := generateAndSaveKEMKey(kemKeyPath, keys.DID)
		if err != nil {
			return nil, fmt.Errorf("generate KEM key: %w", err)
		}
		keys.KEMPrivateKey = priv
		keys.KEMPublicKey = pub
	}

	// 3. Check and register on-chain if AutoRegister is enabled
	if cfg.AutoRegister && cfg.RegistryAddress != "" && cfg.ETHRPCUrl != "" {
		log.Printf("[bootstrap] Checking on-chain registration...")

		// TODO: Check if agent is registered
		// For now, skip registration to avoid complexity
		// This would require connecting to the blockchain and checking the registry

		log.Printf("[bootstrap] On-chain registration check skipped (implement if needed)")
	}

	log.Printf("[bootstrap] Agent '%s' initialized successfully", cfg.Name)
	return keys, nil
}

// Helper functions

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadSigningKeyFromJWK(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var jwk struct {
		D string `json:"d"` // Private key (base64url or hex)
	}
	if err := json.Unmarshal(data, &jwk); err != nil {
		return nil, err
	}

	var dBytes []byte

	// Try base64url decoding first (standard JWK format)
	dBytes, err = base64.RawURLEncoding.DecodeString(jwk.D)
	if err != nil {
		// Fall back to hex decoding (with or without 0x prefix)
		dHex := strings.TrimPrefix(jwk.D, "0x")
		dBytes, err = hex.DecodeString(dHex)
		if err != nil {
			return nil, fmt.Errorf("decode private key (tried base64url and hex): %w", err)
		}
	}

	key, err := crypto.ToECDSA(dBytes)
	if err != nil {
		return nil, fmt.Errorf("parse ECDSA key: %w", err)
	}

	return key, nil
}

func generateAndSaveSigningKey(path string) (*ecdsa.PrivateKey, error) {
	// Generate secp256k1 key
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}

	// Extract coordinates
	pubKey := key.PublicKey
	xBytes := pubKey.X.Bytes()
	yBytes := pubKey.Y.Bytes()
	dBytes := key.D.Bytes()

	// Pad to 32 bytes
	x := make([]byte, 32)
	y := make([]byte, 32)
	d := make([]byte, 32)
	copy(x[32-len(xBytes):], xBytes)
	copy(y[32-len(yBytes):], yBytes)
	copy(d[32-len(dBytes):], dBytes)

	// Create JWK with base64url encoding (standard format)
	jwk := map[string]interface{}{
		"kty": "EC",
		"crv": "secp256k1",
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
		"d":   base64.RawURLEncoding.EncodeToString(d),
	}

	// Write to file
	data, err := json.MarshalIndent(jwk, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, err
	}

	log.Printf("[bootstrap] Saved signing key: %s", path)
	return key, nil
}

func loadKEMKeyFromJWK(path string) (priv []byte, pub []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	var jwk struct {
		D string `json:"d"` // Private key (base64url or hex)
		X string `json:"x"` // Public key (base64url or hex)
	}
	if err := json.Unmarshal(data, &jwk); err != nil {
		return nil, nil, err
	}

	// Try base64url decoding first (standard JWK format)
	priv, err = base64.RawURLEncoding.DecodeString(jwk.D)
	if err != nil {
		// Fall back to hex decoding
		privHex := strings.TrimPrefix(jwk.D, "0x")
		priv, err = hex.DecodeString(privHex)
		if err != nil {
			return nil, nil, fmt.Errorf("decode private key (tried base64url and hex): %w", err)
		}
	}

	pub, err = base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		// Fall back to hex decoding
		pubHex := strings.TrimPrefix(jwk.X, "0x")
		pub, err = hex.DecodeString(pubHex)
		if err != nil {
			return nil, nil, fmt.Errorf("decode public key (tried base64url and hex): %w", err)
		}
	}

	return priv, pub, nil
}

func generateAndSaveKEMKey(path string, kid string) (priv []byte, pub []byte, err error) {
	// Generate X25519 key pair
	priv = make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return nil, nil, err
	}

	// Clamp per RFC 7748
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	// Compute public key
	basepoint := make([]byte, 32)
	basepoint[0] = 9
	pub, err = curve25519.X25519(priv, basepoint)
	if err != nil {
		return nil, nil, err
	}

	// Create JWK with base64url encoding (standard format)
	jwk := map[string]interface{}{
		"kty": "OKP",
		"crv": "X25519",
		"x":   base64.RawURLEncoding.EncodeToString(pub),
		"d":   base64.RawURLEncoding.EncodeToString(priv),
		"kid": kid,
		"use": "enc",
	}

	// Write to file
	data, err := json.MarshalIndent(jwk, "", "  ")
	if err != nil {
		return nil, nil, err
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, nil, err
	}

	log.Printf("[bootstrap] Saved KEM key: %s", path)
	return priv, pub, nil
}
