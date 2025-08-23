package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

// CapabilitiesValidator validates agent capabilities against the schema
type CapabilitiesValidator struct {
	schema *gojsonschema.Schema
}

// NewCapabilitiesValidator creates a new capabilities validator
func NewCapabilitiesValidator() (*CapabilitiesValidator, error) {
	schemaPath := "docs/capabilities-schema.json"
	schemaData, err := ioutil.ReadFile(schemaPath)
	if err != nil {
		// If schema file doesn't exist, use embedded schema
		schemaData = []byte(embeddedSchema)
	}

	schemaLoader := gojsonschema.NewBytesLoader(schemaData)
	schema, err := gojsonschema.NewSchema(schemaLoader)
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	return &CapabilitiesValidator{
		schema: schema,
	}, nil
}

// Validate validates capabilities against the schema
func (cv *CapabilitiesValidator) Validate(capabilities interface{}) error {
	// Convert capabilities to JSON
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	// Validate against schema
	documentLoader := gojsonschema.NewBytesLoader(capabilitiesJSON)
	result, err := cv.schema.Validate(documentLoader)
	if err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	if !result.Valid() {
		var errors []string
		for _, err := range result.Errors() {
			errors = append(errors, fmt.Sprintf("- %s", err))
		}
		return fmt.Errorf("capabilities validation failed:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

// ValidateAll validates all agent capabilities in a config
func (cv *CapabilitiesValidator) ValidateAll(config *Config) error {
	for agentType, agent := range config.Agents {
		if err := cv.Validate(agent.Capabilities); err != nil {
			return fmt.Errorf("validation failed for agent %s: %w", agentType, err)
		}
	}
	return nil
}

// GetRequiredSkills returns the minimum required skills for an agent type
func GetRequiredSkills(agentType string) []string {
	switch agentType {
	case "root", "orchestrator":
		return []string{"task_routing", "agent_coordination"}
	case "ordering":
		return []string{"order_processing"}
	case "planning":
		return []string{"trip_planning"}
	default:
		return []string{}
	}
}

// ValidateSkills checks if an agent has the required skills
func ValidateSkills(agentType string, skills []string) error {
	required := GetRequiredSkills(agentType)
	skillMap := make(map[string]bool)
	for _, skill := range skills {
		skillMap[skill] = true
	}

	var missing []string
	for _, req := range required {
		if !skillMap[req] {
			missing = append(missing, req)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required skills for %s agent: %v", agentType, missing)
	}

	return nil
}

// Embedded schema as fallback
const embeddedSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Agent Capabilities Schema",
  "type": "object",
  "required": ["type", "version", "skills"],
  "properties": {
    "type": {
      "type": "string",
      "enum": ["root", "orchestrator", "ordering", "planning", "specialist", "utility"]
    },
    "version": {
      "type": "string",
      "pattern": "^\\d+\\.\\d+\\.\\d+$"
    },
    "skills": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "minItems": 1
    }
  }
}`