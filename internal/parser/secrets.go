package parser

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseSecrets parses the contents of a secrets.yaml file.
// It contains only metadata — plaintext values are never present — but the
// metadata identifies real K8s Secret objects and missing or partial pointers
// would silently corrupt whatever code reads the list later (secret-mount,
// SealedSecret sync, etc.). We validate every entry fully up front.
func ParseSecrets(data []byte) (*SecretsConfig, error) {
	if err := validateSecretsSchema(data); err != nil {
		return nil, err
	}

	var cfg SecretsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse secrets.yaml: %w", err)
	}
	for i, entry := range cfg.Secrets {
		if err := validateSecretEntry(i, entry); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

// ParseDefaults parses the contents of a _defaults/common.yaml file.
func ParseDefaults(data []byte) (*DefaultsConfig, error) {
	if err := validateDefaultsSchema(data); err != nil {
		return nil, err
	}

	var cfg DefaultsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse _defaults/common.yaml: %w", err)
	}
	return &cfg, nil
}
