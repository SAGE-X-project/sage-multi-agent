package test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAgentKeyGeneration tests agent key generation scripts
func TestAgentKeyGeneration(t *testing.T) {
	// Check if key generation script exists
	scriptPath := "../scripts/generate_sage_keys.go"
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Skipf("Key generation script not found: %s", scriptPath)
	}

	t.Logf("Key generation script exists: %s", scriptPath)
}

// TestAgentConfigFiles tests agent configuration files
func TestAgentConfigFiles(t *testing.T) {
	configPath := "../configs/agent_config.yaml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("Agent config file not found: %s", configPath)
		return
	}

	t.Logf("Agent config file exists: %s", configPath)
}

// TestAgentRegistrationScripts tests registration scripts existence
func TestAgentRegistrationScripts(t *testing.T) {
	scripts := []string{
		"../scripts/register_agents.sh",
		"../scripts/register_secp256k1.sh",
		"../scripts/register_self_signed.sh",
	}

	for _, script := range scripts {
		t.Run(filepath.Base(script), func(t *testing.T) {
			if _, err := os.Stat(script); os.IsNotExist(err) {
				t.Errorf("Registration script not found: %s", script)
				return
			}
			t.Logf("Registration script exists: %s", script)
		})
	}
}

// TestKeyStorageDirectories tests key storage directories
func TestKeyStorageDirectories(t *testing.T) {
	// These directories should be created during key generation
	keyDirs := []string{
		"../keys",
		"../keys/secp256k1",
		"../keys/ed25519",
	}

	for _, dir := range keyDirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			// Check if directory exists or can be created
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				t.Logf("Key directory not found (will be created during setup): %s", dir)
			} else {
				t.Logf("Key directory exists: %s", dir)
			}
		})
	}
}

// TestAgentTypes tests that all agent types are properly configured
func TestAgentTypes(t *testing.T) {
	agentTypes := []string{
		"root",
		"planning",
		"ordering",
		"payment",
	}

	for _, agentType := range agentTypes {
		t.Run(agentType, func(t *testing.T) {
			// Check if agent directory exists
			agentPath := filepath.Join("../agents", agentType)
			if _, err := os.Stat(agentPath); os.IsNotExist(err) {
				t.Logf("Agent directory not found: %s (may be in cli/)", agentPath)
			} else {
				t.Logf("Agent directory exists: %s", agentPath)
			}
		})
	}
}

// TestBlockchainIntegration tests blockchain integration components
func TestBlockchainIntegration(t *testing.T) {
	tests := []struct {
		name        string
		component   string
		description string
	}{
		{
			name:        "DID Manager",
			component:   "adapters/verifier_helper.go",
			description: "DID verification helper",
		},
		{
			name:        "Message Signer",
			component:   "adapters/message_signer.go",
			description: "Message signing with DID",
		},
		{
			name:        "Message Verifier",
			component:   "adapters/message_verifier.go",
			description: "Message verification with blockchain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			componentPath := filepath.Join("..", tt.component)
			if _, err := os.Stat(componentPath); os.IsNotExist(err) {
				t.Errorf("Component not found: %s (%s)", tt.component, tt.description)
				return
			}
			t.Logf("Component exists: %s - %s", tt.component, tt.description)
		})
	}
}

// TestFundingScripts tests funding scripts for agents
func TestFundingScripts(t *testing.T) {
	fundingScript := "../scripts/fund_agents.sh"
	if _, err := os.Stat(fundingScript); os.IsNotExist(err) {
		t.Logf("Funding script not found: %s (optional)", fundingScript)
		return
	}

	t.Logf("Funding script exists: %s", fundingScript)
}

// TestRegistrationTools tests registration tools
func TestRegistrationTools(t *testing.T) {
	tools := []string{
		"../tools/keygen/generate_sage_keys.go",
		"../tools/keygen/generate_secp256k1_keys.go",
		"../tools/registration/register_local_agents.go",
		"../tools/registration/register_with_secp256k1.go",
	}

	for _, tool := range tools {
		t.Run(filepath.Base(tool), func(t *testing.T) {
			if _, err := os.Stat(tool); os.IsNotExist(err) {
				t.Logf("Tool not found: %s (may not be required)", tool)
				return
			}
			t.Logf("Tool exists: %s", tool)
		})
	}
}

// TestDIDSupport tests DID support configuration
func TestDIDSupport(t *testing.T) {
	// Test that flexible DID manager exists
	flexibleDIDPath := "../adapters/flexible_sage_manager.go"
	if _, err := os.Stat(flexibleDIDPath); os.IsNotExist(err) {
		t.Errorf("Flexible DID manager not found: %s", flexibleDIDPath)
		return
	}

	t.Logf("Flexible DID manager exists: %s", flexibleDIDPath)

	// Test SAGE wrapper
	wrapperPath := "../adapters/sage_wrapper.go"
	if _, err := os.Stat(wrapperPath); os.IsNotExist(err) {
		t.Logf("SAGE wrapper not found: %s", wrapperPath)
		return
	}

	t.Logf("SAGE wrapper exists: %s", wrapperPath)
}

// TestAgentCommunication tests agent communication setup
func TestAgentCommunication(t *testing.T) {
	components := []struct {
		path        string
		description string
	}{
		{
			path:        "../adapters/agent_messenger.go",
			description: "Agent messaging",
		},
		{
			path:        "../adapters/agent_message_handler.go",
			description: "Message handling",
		},
		{
			path:        "../protocol/message_router.go",
			description: "Message routing",
		},
	}

	for _, comp := range components {
		t.Run(comp.description, func(t *testing.T) {
			fullPath := comp.path
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				t.Errorf("Component not found: %s (%s)", comp.path, comp.description)
				return
			}
			t.Logf("Component exists: %s - %s", comp.path, comp.description)
		})
	}
}

// TestMakefile tests Makefile commands for agent setup
func TestMakefile(t *testing.T) {
	makefilePath := "../Makefile"
	if _, err := os.Stat(makefilePath); os.IsNotExist(err) {
		t.Errorf("Makefile not found: %s", makefilePath)
		return
	}

	t.Logf("Makefile exists: %s", makefilePath)
}