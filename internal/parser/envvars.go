package parser

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseEnvVars parses the contents of an env_vars.yaml file. The metadata
// block (service/org/project) is required for the same reason as config.yaml
// — it's the key the store indexes on.
func ParseEnvVars(data []byte) (*EnvVarsConfig, error) {
	if err := validateEnvVarsSchema(data); err != nil {
		return nil, err
	}

	var cfg EnvVarsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse env_vars.yaml: %w", err)
	}
	if err := validateMetadata("env_vars.yaml", cfg.Metadata); err != nil {
		return nil, err
	}
	return &cfg, nil
}
