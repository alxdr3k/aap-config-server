package parser

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var validEnvNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateMetadata enforces the minimum set of identifying fields that every
// service YAML must carry. service/org/project is the composite key the store
// indexes on; a missing field would register the file at an unreachable
// location or a different service than the directory layout implies.
func validateMetadata(file string, m ServiceMetadata) error {
	var missing []string
	if m.Service == "" {
		missing = append(missing, "metadata.service")
	}
	if m.Org == "" {
		missing = append(missing, "metadata.org")
	}
	if m.Project == "" {
		missing = append(missing, "metadata.project")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: missing required field(s): %v", file, missing)
	}
	return nil
}

// validateSecretEntry enforces that every secrets.yaml entry carries a
// complete k8s_secret pointer. Phase-2 secret sync needs name/namespace/key
// to be present to actually resolve a value; accepting a half-filled entry
// now would push the failure downstream where the cause is harder to trace.
func validateSecretEntry(i int, entry SecretEntry) error {
	var missing []string
	if entry.ID == "" {
		missing = append(missing, "id")
	}
	if entry.K8sSecret.Name == "" {
		missing = append(missing, "k8s_secret.name")
	}
	if entry.K8sSecret.Namespace == "" {
		missing = append(missing, "k8s_secret.namespace")
	}
	if entry.K8sSecret.Key == "" {
		missing = append(missing, "k8s_secret.key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("secrets.yaml: entry %d missing required field(s): %v", i, missing)
	}
	return nil
}

func validateConfigSchema(data []byte) error {
	root, err := rootMapping("config.yaml", data)
	if err != nil {
		return err
	}
	if err := validateMappingKeys("config.yaml", root, "root", []string{"version", "metadata", "config"}); err != nil {
		return err
	}
	if err := validateMetadataSchema("config.yaml", child(root, "metadata")); err != nil {
		return err
	}
	if node := child(root, "version"); node != nil {
		if err := requireScalar("config.yaml", "version", node); err != nil {
			return err
		}
	}
	if node := child(root, "config"); node != nil {
		if err := requireMapping("config.yaml", "config", node); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvVarsSchema(data []byte) error {
	root, err := rootMapping("env_vars.yaml", data)
	if err != nil {
		return err
	}
	if err := validateMappingKeys("env_vars.yaml", root, "root", []string{"version", "metadata", "env_vars"}); err != nil {
		return err
	}
	if err := validateMetadataSchema("env_vars.yaml", child(root, "metadata")); err != nil {
		return err
	}
	if node := child(root, "version"); node != nil {
		if err := requireScalar("env_vars.yaml", "version", node); err != nil {
			return err
		}
	}
	return validateEnvVarsBlock("env_vars.yaml", "env_vars", child(root, "env_vars"))
}

func validateSecretsSchema(data []byte) error {
	root, err := rootMapping("secrets.yaml", data)
	if err != nil {
		return err
	}
	if err := validateMappingKeys("secrets.yaml", root, "root", []string{"version", "secrets"}); err != nil {
		return err
	}
	if node := child(root, "version"); node != nil {
		if err := requireScalar("secrets.yaml", "version", node); err != nil {
			return err
		}
	}
	secrets := child(root, "secrets")
	if secrets == nil {
		return nil
	}
	if secrets.Kind != yaml.SequenceNode {
		return fmt.Errorf("secrets.yaml: secrets must be a sequence")
	}
	for i, item := range secrets.Content {
		path := fmt.Sprintf("secrets[%d]", i)
		if err := requireMapping("secrets.yaml", path, item); err != nil {
			return err
		}
		if err := validateMappingKeys("secrets.yaml", item, path, []string{"id", "description", "k8s_secret"}); err != nil {
			return err
		}
		for _, key := range []string{"id", "description"} {
			if node := child(item, key); node != nil {
				if err := requireScalar("secrets.yaml", path+"."+key, node); err != nil {
					return err
				}
			}
		}
		k8sSecret := child(item, "k8s_secret")
		if k8sSecret == nil {
			continue
		}
		k8sPath := path + ".k8s_secret"
		if err := requireMapping("secrets.yaml", k8sPath, k8sSecret); err != nil {
			return err
		}
		if err := validateMappingKeys("secrets.yaml", k8sSecret, k8sPath, []string{"name", "namespace", "key"}); err != nil {
			return err
		}
		for _, key := range []string{"name", "namespace", "key"} {
			if node := child(k8sSecret, key); node != nil {
				if err := requireScalar("secrets.yaml", k8sPath+"."+key, node); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateDefaultsSchema(data []byte) error {
	root, err := rootMapping("_defaults/common.yaml", data)
	if err != nil {
		return err
	}
	if err := validateMappingKeys("_defaults/common.yaml", root, "root", []string{"config", "env_vars"}); err != nil {
		return err
	}
	if node := child(root, "config"); node != nil {
		if err := requireMapping("_defaults/common.yaml", "config", node); err != nil {
			return err
		}
	}
	return validateEnvVarsBlock("_defaults/common.yaml", "env_vars", child(root, "env_vars"))
}

func validateMetadataSchema(file string, node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if err := requireMapping(file, "metadata", node); err != nil {
		return err
	}
	if err := validateMappingKeys(file, node, "metadata", []string{"service", "org", "project", "updated_at"}); err != nil {
		return err
	}
	for _, key := range []string{"service", "org", "project", "updated_at"} {
		if value := child(node, key); value != nil {
			if err := requireScalar(file, "metadata."+key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateEnvVarsBlock(file, path string, node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if err := requireMapping(file, path, node); err != nil {
		return err
	}
	if err := validateMappingKeys(file, node, path, []string{"plain", "secret_refs"}); err != nil {
		return err
	}
	for _, key := range []string{"plain", "secret_refs"} {
		if err := validateStringMap(file, path+"."+key, child(node, key)); err != nil {
			return err
		}
	}
	return nil
}

func validateStringMap(file, path string, node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if err := requireMapping(file, path, node); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s: %s key must be a scalar", file, path)
		}
		name := key.Value
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%s: duplicate key %q in %s", file, name, path)
		}
		seen[name] = struct{}{}
		if !validEnvNameRe.MatchString(name) {
			return fmt.Errorf("%s: %s key %q must be a valid environment variable name", file, path, name)
		}
		if value.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s: %s.%s value must be a scalar", file, path, name)
		}
	}
	return nil
}

func rootMapping(file string, data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: document must be a mapping", file)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: document must be a mapping", file)
	}
	return root, nil
}

func validateMappingKeys(file string, node *yaml.Node, path string, allowed []string) error {
	allowedSet := map[string]struct{}{}
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	seen := map[string]struct{}{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s: %s key must be a scalar", file, path)
		}
		name := key.Value
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%s: duplicate key %q in %s", file, name, path)
		}
		seen[name] = struct{}{}
		if _, ok := allowedSet[name]; !ok {
			return fmt.Errorf("%s: unknown field %s in %s; allowed fields: %s",
				file, name, path, strings.Join(sortedKeys(allowedSet), ", "))
		}
	}
	return nil
}

func requireMapping(file, path string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: %s must be a mapping", file, path)
	}
	return nil
}

func requireScalar(file, path string, node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s: %s must be a scalar", file, path)
	}
	return nil
}

func child(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
