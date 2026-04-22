package parser

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseEnvVars parses the contents of an env_vars.yaml file.
func ParseEnvVars(data []byte) (*EnvVarsConfig, error) {
	var cfg EnvVarsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse env_vars.yaml: %w", err)
	}
	return &cfg, nil
}
