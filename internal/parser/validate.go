package parser

import "fmt"

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
