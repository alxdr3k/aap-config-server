package parser

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseSecrets parses the contents of a secrets.yaml file.
// It contains only metadata — plaintext values are never present.
func ParseSecrets(data []byte) (*SecretsConfig, error) {
	var cfg SecretsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse secrets.yaml: %w", err)
	}
	return &cfg, nil
}

// ParseDefaults parses the contents of a _defaults/common.yaml file.
func ParseDefaults(data []byte) (*DefaultsConfig, error) {
	var cfg DefaultsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse _defaults/common.yaml: %w", err)
	}
	return &cfg, nil
}
