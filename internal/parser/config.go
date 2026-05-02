package parser

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseConfig parses the contents of a config.yaml file and validates that
// the required metadata identifying the service (service/org/project) is
// present. Downstream code indexes the snapshot by (org, project, service)
// so missing metadata would produce an unreachable entry.
func ParseConfig(data []byte) (*ServiceConfig, error) {
	if err := validateConfigSchema(data); err != nil {
		return nil, err
	}

	var cfg ServiceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config.yaml: %w", err)
	}
	if err := validateMetadata("config.yaml", cfg.Metadata); err != nil {
		return nil, err
	}
	return &cfg, nil
}
