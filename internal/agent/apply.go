package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const (
	configMapDataKey = "config.yaml"
	envSHDataKey     = "env.sh"
)

// ApplyTarget identifies the ConfigMap and Secret owned by one Config Agent.
type ApplyTarget struct {
	Namespace     string
	ConfigMapName string
	SecretName    string
}

// ApplyPayloads contains already-rendered payloads for Kubernetes resources.
type ApplyPayloads struct {
	ConfigYAML []byte
	EnvSH      []byte
}

// KubernetesApplier creates or patches target ConfigMap and Secret resources.
type KubernetesApplier struct {
	client  kubernetes.Interface
	timeout time.Duration
}

// NewKubernetesApplier creates a Config Agent Kubernetes resource applier.
func NewKubernetesApplier(client kubernetes.Interface, timeout time.Duration) (*KubernetesApplier, error) {
	if client == nil {
		return nil, errors.New("kubernetes client is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("kubernetes apply timeout must be > 0, got %s", timeout)
	}
	return &KubernetesApplier{client: client, timeout: timeout}, nil
}

// ApplyRenderedPayloads writes config.yaml to the target ConfigMap and env.sh
// to the target Secret. Existing resources are patched so unrelated data keys
// are preserved.
func (a *KubernetesApplier) ApplyRenderedPayloads(ctx context.Context, target ApplyTarget, payloads ApplyPayloads) error {
	if a == nil {
		return errors.New("kubernetes applier is required")
	}
	if a.client == nil {
		return errors.New("kubernetes client is required")
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target = target.normalized()
	if err := target.Validate(); err != nil {
		return err
	}

	applyCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	if err := a.applyConfigMap(applyCtx, target, payloads.ConfigYAML); err != nil {
		return err
	}
	if err := a.applySecret(applyCtx, target, payloads.EnvSH); err != nil {
		return err
	}
	return nil
}

// Validate checks that the applier is constrained to concrete resource names.
func (t ApplyTarget) Validate() error {
	if t.Namespace == "" {
		return errors.New("target namespace is required")
	}
	if errs := validation.IsDNS1123Label(t.Namespace); len(errs) > 0 {
		return fmt.Errorf("target namespace %q is invalid: %s", t.Namespace, strings.Join(errs, "; "))
	}
	if t.ConfigMapName == "" {
		return errors.New("target configmap name is required")
	}
	if errs := validation.IsDNS1123Subdomain(t.ConfigMapName); len(errs) > 0 {
		return fmt.Errorf("target configmap name %q is invalid: %s", t.ConfigMapName, strings.Join(errs, "; "))
	}
	if t.SecretName == "" {
		return errors.New("target secret name is required")
	}
	if errs := validation.IsDNS1123Subdomain(t.SecretName); len(errs) > 0 {
		return fmt.Errorf("target secret name %q is invalid: %s", t.SecretName, strings.Join(errs, "; "))
	}
	return nil
}

func (t ApplyTarget) normalized() ApplyTarget {
	t.Namespace = strings.TrimSpace(t.Namespace)
	t.ConfigMapName = strings.TrimSpace(t.ConfigMapName)
	t.SecretName = strings.TrimSpace(t.SecretName)
	return t
}

func (a *KubernetesApplier) applyConfigMap(ctx context.Context, target ApplyTarget, configYAML []byte) error {
	client := a.client.CoreV1().ConfigMaps(target.Namespace)
	patch, err := json.Marshal(map[string]any{
		"data": map[string]string{
			configMapDataKey: string(configYAML),
		},
	})
	if err != nil {
		return fmt.Errorf("build configmap patch %s/%s: %w", target.Namespace, target.ConfigMapName, err)
	}

	_, err = client.Patch(ctx, target.ConfigMapName, types.MergePatchType, patch, metav1.PatchOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: target.Namespace,
				Name:      target.ConfigMapName,
			},
			Data: map[string]string{
				configMapDataKey: string(configYAML),
			},
		}, metav1.CreateOptions{})
	case err != nil:
		return fmt.Errorf("patch configmap %s/%s: %w", target.Namespace, target.ConfigMapName, err)
	}
	if err != nil {
		return fmt.Errorf("create configmap %s/%s: %w", target.Namespace, target.ConfigMapName, err)
	}
	return nil
}

func (a *KubernetesApplier) applySecret(ctx context.Context, target ApplyTarget, envSH []byte) error {
	client := a.client.CoreV1().Secrets(target.Namespace)
	patch, err := json.Marshal(map[string]any{
		"data": map[string]string{
			envSHDataKey: base64.StdEncoding.EncodeToString(envSH),
		},
	})
	if err != nil {
		return fmt.Errorf("build secret patch %s/%s: %w", target.Namespace, target.SecretName, err)
	}

	_, err = client.Patch(ctx, target.SecretName, types.MergePatchType, patch, metav1.PatchOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = client.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: target.Namespace,
				Name:      target.SecretName,
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				envSHDataKey: envSH,
			},
		}, metav1.CreateOptions{})
	case err != nil:
		return fmt.Errorf("patch secret %s/%s: %w", target.Namespace, target.SecretName, err)
	}
	if err != nil {
		return fmt.Errorf("create secret %s/%s: %w", target.Namespace, target.SecretName, err)
	}
	return nil
}
