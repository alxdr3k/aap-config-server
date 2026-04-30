package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const (
	rolloutConfigHashAnnotation = "config-agent/config-hash"
	rolloutRestartAtAnnotation  = "config-agent/restart-at"
)

// RolloutTarget identifies the Deployment restarted by one Config Agent.
type RolloutTarget struct {
	Namespace      string
	DeploymentName string
}

// RolloutResult reports the annotations written to the target Deployment.
type RolloutResult struct {
	ConfigHash string
	RestartAt  string
}

// KubernetesRolloutPatcher patches target Deployment pod-template annotations.
type KubernetesRolloutPatcher struct {
	client  kubernetes.Interface
	timeout time.Duration
	now     func() time.Time
}

// NewKubernetesRolloutPatcher creates a Config Agent rollout patcher.
func NewKubernetesRolloutPatcher(client kubernetes.Interface, timeout time.Duration) (*KubernetesRolloutPatcher, error) {
	if client == nil {
		return nil, errors.New("kubernetes client is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("kubernetes rollout timeout must be > 0, got %s", timeout)
	}
	return &KubernetesRolloutPatcher{client: client, timeout: timeout, now: time.Now}, nil
}

// TriggerRollout patches pod-template annotations so Kubernetes creates a new
// ReplicaSet according to the Deployment's configured rolling-update strategy.
func (p *KubernetesRolloutPatcher) TriggerRollout(ctx context.Context, target RolloutTarget, payloads ApplyPayloads) (RolloutResult, error) {
	if p == nil {
		return RolloutResult{}, errors.New("kubernetes rollout patcher is required")
	}
	if p.client == nil {
		return RolloutResult{}, errors.New("kubernetes client is required")
	}
	if ctx == nil {
		return RolloutResult{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return RolloutResult{}, err
	}
	target = target.normalized()
	if err := target.Validate(); err != nil {
		return RolloutResult{}, err
	}

	now := p.now
	if now == nil {
		now = time.Now
	}
	result := RolloutResult{
		ConfigHash: payloadConfigHash(payloads),
		RestartAt:  now().UTC().Format(time.RFC3339Nano),
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						rolloutConfigHashAnnotation: result.ConfigHash,
						rolloutRestartAtAnnotation:  result.RestartAt,
					},
				},
			},
		},
	})
	if err != nil {
		return RolloutResult{}, fmt.Errorf("build deployment patch %s/%s: %w", target.Namespace, target.DeploymentName, err)
	}

	applyCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	_, err = p.client.AppsV1().Deployments(target.Namespace).
		Patch(applyCtx, target.DeploymentName, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return RolloutResult{}, fmt.Errorf("patch deployment %s/%s: %w", target.Namespace, target.DeploymentName, err)
	}
	return result, nil
}

// Validate checks that the rollout patch is constrained to a concrete
// Deployment name.
func (t RolloutTarget) Validate() error {
	if t.Namespace == "" {
		return errors.New("target namespace is required")
	}
	if errs := validation.IsDNS1123Label(t.Namespace); len(errs) > 0 {
		return fmt.Errorf("target namespace %q is invalid: %s", t.Namespace, strings.Join(errs, "; "))
	}
	if t.DeploymentName == "" {
		return errors.New("target deployment name is required")
	}
	if errs := validation.IsDNS1123Subdomain(t.DeploymentName); len(errs) > 0 {
		return fmt.Errorf("target deployment name %q is invalid: %s", t.DeploymentName, strings.Join(errs, "; "))
	}
	return nil
}

func (t RolloutTarget) normalized() RolloutTarget {
	t.Namespace = strings.TrimSpace(t.Namespace)
	t.DeploymentName = strings.TrimSpace(t.DeploymentName)
	return t
}

func payloadConfigHash(payloads ApplyPayloads) string {
	sum := sha256.New()
	sum.Write([]byte(configMapDataKey))
	sum.Write([]byte{0})
	sum.Write(payloads.ConfigYAML)
	sum.Write([]byte{0})
	sum.Write([]byte(envSHDataKey))
	sum.Write([]byte{0})
	sum.Write(payloads.EnvSH)
	return hex.EncodeToString(sum.Sum(nil))
}
