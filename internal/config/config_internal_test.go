package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidate_RejectsExplicitZeroK8sApplyTimeout(t *testing.T) {
	c := &ServerConfig{
		GitURL:                  "git@host:repo.git",
		GitPollInterval:         30 * time.Second,
		APIKey:                  "k",
		k8sApplyTimeoutExplicit: true,
	}

	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "K8S_APPLY_TIMEOUT") {
		t.Fatalf("expected K8S_APPLY_TIMEOUT error, got %v", err)
	}
}
