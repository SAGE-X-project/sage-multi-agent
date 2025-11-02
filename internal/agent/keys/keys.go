// Package keys provides high-level abstractions for loading and managing cryptographic keys.
// This package wraps sage's low-level crypto/formats functionality to provide a simpler API
// that can be easily migrated to sage-a2a-go.
package keys

import (
	"fmt"
	"os"

	sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
	"github.com/sage-x-project/sage/pkg/agent/crypto/formats"
)

// KeyPair represents a cryptographic key pair (public + private key).
// This is currently a type alias of sage's KeyPair, but abstracts the dependency.
type KeyPair = sagecrypto.KeyPair

// LoadFromJWKFile loads a key pair from a JWK file.
// This is the primary method for loading agent signing and KEM keys.
//
// Parameters:
//   - path: File path to the JWK file
//
// Returns:
//   - KeyPair: The loaded key pair
//   - error: Error if file cannot be read or parsed
//
// Example:
//
//	signKey, err := keys.LoadFromJWKFile("/path/to/signing_key.jwk")
//	if err != nil {
//	    return fmt.Errorf("load signing key: %w", err)
//	}
func LoadFromJWKFile(path string) (KeyPair, error) {
	if path == "" {
		return nil, fmt.Errorf("key file path is empty")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JWK file %s: %w", path, err)
	}

	return LoadFromJWKBytes(raw)
}

// LoadFromJWKBytes loads a key pair from JWK bytes.
// This is useful when the JWK is already in memory (e.g., from environment variables).
//
// Parameters:
//   - data: JWK data as bytes
//
// Returns:
//   - KeyPair: The loaded key pair
//   - error: Error if data cannot be parsed
func LoadFromJWKBytes(data []byte) (KeyPair, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("JWK data is empty")
	}

	importer := formats.NewJWKImporter()
	kp, err := importer.Import(data, sagecrypto.KeyFormatJWK)
	if err != nil {
		return nil, fmt.Errorf("import JWK: %w", err)
	}

	// Type assertion to KeyPair
	keyPair, ok := kp.(sagecrypto.KeyPair)
	if !ok {
		return nil, fmt.Errorf("imported key is not a KeyPair")
	}

	return keyPair, nil
}

// LoadFromEnv loads a key pair from a JWK file path specified in an environment variable.
// This is a convenience method that combines os.Getenv and LoadFromJWKFile.
//
// Parameters:
//   - envVar: Name of the environment variable containing the file path
//
// Returns:
//   - KeyPair: The loaded key pair
//   - error: Error if environment variable is not set or file cannot be loaded
//
// Example:
//
//	signKey, err := keys.LoadFromEnv("PAYMENT_JWK_FILE")
//	if err != nil {
//	    return fmt.Errorf("load signing key: %w", err)
//	}
func LoadFromEnv(envVar string) (KeyPair, error) {
	path := os.Getenv(envVar)
	if path == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envVar)
	}

	return LoadFromJWKFile(path)
}

// KeyConfig represents configuration for loading multiple keys.
// This is used by the agent framework to load all required keys at once.
type KeyConfig struct {
	// SigningKeyFile is the path to the signing key JWK file
	SigningKeyFile string

	// KEMKeyFile is the path to the KEM key JWK file (for HPKE)
	KEMKeyFile string
}

// KeySet represents a complete set of keys for an agent.
type KeySet struct {
	// SigningKey is used for signing HTTP messages (RFC 9421)
	SigningKey KeyPair

	// KEMKey is used for HPKE key encapsulation
	KEMKey KeyPair
}

// LoadKeySet loads a complete set of keys from the provided configuration.
// This is the recommended method for initializing agent keys.
//
// Parameters:
//   - config: Key configuration specifying file paths
//
// Returns:
//   - *KeySet: The loaded key set
//   - error: Error if any key cannot be loaded
//
// Example:
//
//	keySet, err := keys.LoadKeySet(keys.KeyConfig{
//	    SigningKeyFile: "/path/to/signing_key.jwk",
//	    KEMKeyFile:     "/path/to/kem_key.jwk",
//	})
func LoadKeySet(config KeyConfig) (*KeySet, error) {
	if config.SigningKeyFile == "" {
		return nil, fmt.Errorf("signing key file path is required")
	}
	if config.KEMKeyFile == "" {
		return nil, fmt.Errorf("KEM key file path is required")
	}

	signKey, err := LoadFromJWKFile(config.SigningKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load signing key: %w", err)
	}

	kemKey, err := LoadFromJWKFile(config.KEMKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load KEM key: %w", err)
	}

	return &KeySet{
		SigningKey: signKey,
		KEMKey:     kemKey,
	}, nil
}

// LoadKeySetFromEnv loads a complete set of keys from environment variables.
// This is a convenience method for loading keys in production environments.
//
// Parameters:
//   - signingEnvVar: Name of the environment variable for signing key file path
//   - kemEnvVar: Name of the environment variable for KEM key file path
//
// Returns:
//   - *KeySet: The loaded key set
//   - error: Error if any key cannot be loaded
//
// Example:
//
//	keySet, err := keys.LoadKeySetFromEnv("PAYMENT_JWK_FILE", "PAYMENT_KEM_JWK_FILE")
func LoadKeySetFromEnv(signingEnvVar, kemEnvVar string) (*KeySet, error) {
	signPath := os.Getenv(signingEnvVar)
	if signPath == "" {
		return nil, fmt.Errorf("environment variable %s is not set", signingEnvVar)
	}

	kemPath := os.Getenv(kemEnvVar)
	if kemPath == "" {
		return nil, fmt.Errorf("environment variable %s is not set", kemEnvVar)
	}

	return LoadKeySet(KeyConfig{
		SigningKeyFile: signPath,
		KEMKeyFile:     kemPath,
	})
}
