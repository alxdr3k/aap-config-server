package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/aap/config-server/internal/config"
)

// Validate is exercised directly so tests don't have to mutate global env/flags.

func TestValidate_RequiresGitURL(t *testing.T) {
	c := &config.ServerConfig{
		GitPollInterval: 30 * time.Second,
		APIKey:          "k",
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "GIT_URL") {
		t.Fatalf("expected GIT_URL error, got %v", err)
	}
}

func TestValidate_RequiresAPIKey_Default(t *testing.T) {
	c := &config.ServerConfig{
		GitURL:          "git@host:repo.git",
		GitPollInterval: 30 * time.Second,
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "API_KEY is required") {
		t.Fatalf("expected API_KEY required error, got %v", err)
	}
}

func TestValidate_APIKeyNotRequiredWhenDevOptIn(t *testing.T) {
	c := &config.ServerConfig{
		GitURL:                  "git@host:repo.git",
		GitPollInterval:         30 * time.Second,
		AllowUnauthenticatedDev: true,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected err with dev opt-in: %v", err)
	}
}

func TestValidate_RejectsNonPositivePollInterval(t *testing.T) {
	for _, d := range []time.Duration{0, -1 * time.Second} {
		c := &config.ServerConfig{
			GitURL:          "git@host:repo.git",
			GitPollInterval: d,
			APIKey:          "k",
		}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "GIT_POLL_INTERVAL") {
			t.Errorf("interval=%s: expected poll interval error, got %v", d, err)
		}
	}
}

func TestValidate_HappyPath(t *testing.T) {
	c := &config.ServerConfig{
		GitURL:          "git@host:repo.git",
		GitPollInterval: 30 * time.Second,
		APIKey:          "secret",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidate_RejectsSSHAndBasicAuthTogether(t *testing.T) {
	c := &config.ServerConfig{
		GitURL:          "https://host/repo.git",
		GitPollInterval: 30 * time.Second,
		APIKey:          "k",
		GitSSHKeyPath:   "/tmp/key",
		GitUsername:     "u",
		GitPassword:     "p",
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "GIT_SSH_KEY") {
		t.Fatalf("expected mutex error, got %v", err)
	}
}

func TestValidate_RejectsPartialBasicAuth(t *testing.T) {
	for _, tc := range []struct{ user, pass string }{
		{"u", ""},
		{"", "p"},
	} {
		c := &config.ServerConfig{
			GitURL:          "https://host/repo.git",
			GitPollInterval: 30 * time.Second,
			APIKey:          "k",
			GitUsername:     tc.user,
			GitPassword:     tc.pass,
		}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "GIT_USERNAME and GIT_PASSWORD") {
			t.Errorf("user=%q pass=%q: expected both-required error, got %v", tc.user, tc.pass, err)
		}
	}
}

func TestValidate_HappyPathBasicAuth(t *testing.T) {
	c := &config.ServerConfig{
		GitURL:          "https://host/repo.git",
		GitPollInterval: 30 * time.Second,
		APIKey:          "k",
		GitUsername:     "u",
		GitPassword:     "p",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidate_AppliesSecretRuntimeDefaults(t *testing.T) {
	c := &config.ServerConfig{
		GitURL:          "git@host:repo.git",
		GitPollInterval: 30 * time.Second,
		APIKey:          "k",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.SecretMountPath != "/secrets" {
		t.Errorf("SecretMountPath default: want /secrets, got %q", c.SecretMountPath)
	}
	if c.SealedSecretControllerNamespace != "kube-system" {
		t.Errorf("controller namespace default: got %q", c.SealedSecretControllerNamespace)
	}
	if c.SealedSecretControllerName != "sealed-secrets-controller" {
		t.Errorf("controller name default: got %q", c.SealedSecretControllerName)
	}
	if c.SealedSecretScope != "strict" {
		t.Errorf("scope default: got %q", c.SealedSecretScope)
	}
	if c.K8sApplyTimeout != 10*time.Second {
		t.Errorf("K8sApplyTimeout default: got %s", c.K8sApplyTimeout)
	}
	if !c.SecretAuditEnabled() {
		t.Error("SecretAuditLogEnabled default should be true")
	}
	if c.ConsoleAPITimeout != 5*time.Second {
		t.Errorf("ConsoleAPITimeout default: got %s", c.ConsoleAPITimeout)
	}
	if c.ConsoleRegistryBootstrapAttempts != 5 {
		t.Errorf("ConsoleRegistryBootstrapAttempts default: got %d", c.ConsoleRegistryBootstrapAttempts)
	}
	if c.ConsoleRegistryBootstrapInitialBackoff != time.Second {
		t.Errorf("ConsoleRegistryBootstrapInitialBackoff default: got %s", c.ConsoleRegistryBootstrapInitialBackoff)
	}
	if c.ConsoleRegistryBootstrapMaxBackoff != 30*time.Second {
		t.Errorf("ConsoleRegistryBootstrapMaxBackoff default: got %s", c.ConsoleRegistryBootstrapMaxBackoff)
	}
}

func TestValidate_PreservesExplicitSecretAuditDisabled(t *testing.T) {
	disabled := false
	c := &config.ServerConfig{
		GitURL:                     "git@host:repo.git",
		GitPollInterval:            30 * time.Second,
		APIKey:                     "k",
		SecretAuditLogEnabled:      &disabled,
		SecretMountPath:            "/custom-secrets",
		SealedSecretScope:          "namespace-wide",
		SealedSecretControllerName: "sealed-secrets-controller",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.SecretAuditEnabled() {
		t.Fatal("explicit audit disabled setting should be preserved")
	}
	if c.K8sApplyTimeout != 10*time.Second {
		t.Errorf("partial runtime config should default K8sApplyTimeout, got %s", c.K8sApplyTimeout)
	}
	if c.SealedSecretControllerNamespace != "kube-system" {
		t.Errorf("partial runtime config should default controller namespace, got %q", c.SealedSecretControllerNamespace)
	}
}

func TestValidate_SecretRuntimeValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.ServerConfig)
		want   string
	}{
		{
			name: "relative mount path",
			mutate: func(c *config.ServerConfig) {
				c.SecretMountPath = "secrets"
			},
			want: "SECRET_MOUNT_PATH",
		},
		{
			name: "bad sealed secret scope",
			mutate: func(c *config.ServerConfig) {
				c.SealedSecretScope = "wide"
			},
			want: "SEALED_SECRET_SCOPE",
		},
		{
			name: "negative k8s apply timeout",
			mutate: func(c *config.ServerConfig) {
				c.K8sApplyTimeout = -time.Second
			},
			want: "K8S_APPLY_TIMEOUT",
		},
		{
			name: "bad console api url",
			mutate: func(c *config.ServerConfig) {
				c.ConsoleAPIURL = "ftp://console.example"
			},
			want: "CONSOLE_API_URL",
		},
		{
			name: "negative console api timeout",
			mutate: func(c *config.ServerConfig) {
				c.ConsoleAPITimeout = -time.Second
			},
			want: "CONSOLE_API_TIMEOUT",
		},
		{
			name: "negative registry bootstrap attempts",
			mutate: func(c *config.ServerConfig) {
				c.ConsoleRegistryBootstrapAttempts = -1
			},
			want: "CONSOLE_REGISTRY_BOOTSTRAP_ATTEMPTS",
		},
		{
			name: "negative registry bootstrap initial backoff",
			mutate: func(c *config.ServerConfig) {
				c.ConsoleRegistryBootstrapInitialBackoff = -time.Second
			},
			want: "CONSOLE_REGISTRY_BOOTSTRAP_INITIAL_BACKOFF",
		},
		{
			name: "registry bootstrap max below initial",
			mutate: func(c *config.ServerConfig) {
				c.ConsoleRegistryBootstrapInitialBackoff = 2 * time.Second
				c.ConsoleRegistryBootstrapMaxBackoff = time.Second
			},
			want: "CONSOLE_REGISTRY_BOOTSTRAP_MAX_BACKOFF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &config.ServerConfig{
				GitURL:          "git@host:repo.git",
				GitPollInterval: 30 * time.Second,
				APIKey:          "k",
			}
			tc.mutate(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s error, got %v", tc.want, err)
			}
		})
	}
}
