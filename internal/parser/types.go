package parser

// ServiceConfig represents a parsed config.yaml for a service.
// The Config block is free-form to support any service's settings.
type ServiceConfig struct {
	Version  string            `yaml:"version"`
	Metadata ServiceMetadata   `yaml:"metadata"`
	Config   map[string]any    `yaml:"config"`
}

// ServiceMetadata holds the identity of a service config file.
type ServiceMetadata struct {
	Service   string `yaml:"service"`
	Org       string `yaml:"org"`
	Project   string `yaml:"project"`
	UpdatedAt string `yaml:"updated_at"`
}

// EnvVarsConfig represents a parsed env_vars.yaml for a service.
type EnvVarsConfig struct {
	Version  string          `yaml:"version"`
	Metadata ServiceMetadata `yaml:"metadata"`
	EnvVars  EnvVars         `yaml:"env_vars"`
}

// EnvVars holds plain environment variables and references to K8s secrets.
type EnvVars struct {
	Plain      map[string]string `yaml:"plain"`
	SecretRefs map[string]string `yaml:"secret_refs"`
}

// SecretsConfig represents a parsed secrets.yaml for a service.
// It contains only metadata — never plaintext secret values.
type SecretsConfig struct {
	Version string        `yaml:"version"`
	Secrets []SecretEntry `yaml:"secrets"`
}

// SecretEntry describes one K8s Secret reference.
type SecretEntry struct {
	ID          string    `yaml:"id"`
	Description string    `yaml:"description"`
	K8sSecret   K8sSecret `yaml:"k8s_secret"`
}

// K8sSecret identifies a specific key within a K8s Secret object.
type K8sSecret struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	Key       string `yaml:"key"`
}

// DefaultsConfig is the structure of a _defaults/common.yaml file.
// It carries config and env_vars blocks but no service-specific metadata.
type DefaultsConfig struct {
	Config  map[string]any `yaml:"config"`
	EnvVars EnvVars        `yaml:"env_vars"`
}
