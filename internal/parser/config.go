package parser

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseConfig parses the contents of a config.yaml file.
func ParseConfig(data []byte) (*ServiceConfig, error) {
	var cfg ServiceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config.yaml: %w", err)
	}
	return &cfg, nil
}
