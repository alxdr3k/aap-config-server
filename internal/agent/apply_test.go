package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestKubernetesApplierCreatesConfigMapAndSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	applier := newTestKubernetesApplier(t, client)

	err := applier.ApplyRenderedPayloads(context.Background(), testApplyTarget(), ApplyPayloads{
		ConfigYAML: []byte("model_list: []\n"),
		EnvSH:      []byte("export API_KEY='secret'\n"),
	})
	if err != nil {
		t.Fatalf("ApplyRenderedPayloads: %v", err)
	}

	cm, err := client.CoreV1().ConfigMaps("ai-platform").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get ConfigMap: %v", err)
	}
	if cm.Data[configMapDataKey] != "model_list: []\n" {
		t.Fatalf("config.yaml data: %q", cm.Data[configMapDataKey])
	}
	secret, err := client.CoreV1().Secrets("ai-platform").Get(context.Background(), "litellm-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Secret: %v", err)
	}
	if secret.Type != corev1.SecretTypeOpaque {
		t.Fatalf("secret type: %s", secret.Type)
	}
	if string(secret.Data[envSHDataKey]) != "export API_KEY='secret'\n" {
		t.Fatalf("env.sh data: %q", secret.Data[envSHDataKey])
	}
}

func TestKubernetesApplierPatchesExistingResources(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ai-platform", Name: "litellm-config"},
			Data: map[string]string{
				configMapDataKey: "old\n",
				"other.yaml":     "keep\n",
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ai-platform", Name: "litellm-env"},
			Data: map[string][]byte{
				envSHDataKey: []byte("old\n"),
				"other":      []byte("keep"),
			},
		},
	)
	applier := newTestKubernetesApplier(t, client)

	err := applier.ApplyRenderedPayloads(context.Background(), testApplyTarget(), ApplyPayloads{
		ConfigYAML: []byte("new: true\n"),
		EnvSH:      []byte("export LOG_LEVEL='debug'\n"),
	})
	if err != nil {
		t.Fatalf("ApplyRenderedPayloads: %v", err)
	}

	cm, err := client.CoreV1().ConfigMaps("ai-platform").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get ConfigMap: %v", err)
	}
	if cm.Data[configMapDataKey] != "new: true\n" || cm.Data["other.yaml"] != "keep\n" {
		t.Fatalf("configmap data not patched as expected: %+v", cm.Data)
	}
	secret, err := client.CoreV1().Secrets("ai-platform").Get(context.Background(), "litellm-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Secret: %v", err)
	}
	if string(secret.Data[envSHDataKey]) != "export LOG_LEVEL='debug'\n" || string(secret.Data["other"]) != "keep" {
		t.Fatalf("secret data not patched as expected: %+v", secret.Data)
	}
}

func TestKubernetesApplierUsesConfiguredResourceNames(t *testing.T) {
	client := fake.NewSimpleClientset()
	applier := newTestKubernetesApplier(t, client)

	err := applier.ApplyRenderedPayloads(context.Background(), ApplyTarget{
		Namespace:     "ns",
		ConfigMapName: "cm-name",
		SecretName:    "secret-name",
	}, ApplyPayloads{ConfigYAML: []byte("{}\n"), EnvSH: []byte{}})
	if err != nil {
		t.Fatalf("ApplyRenderedPayloads: %v", err)
	}

	for _, action := range client.Actions() {
		if action.GetNamespace() != "ns" {
			t.Fatalf("action namespace should be constrained to ns: %#v", action)
		}
		switch action.GetResource().Resource {
		case "configmaps":
			if named, ok := action.(k8stesting.CreateAction); ok && named.GetObject().(metav1.Object).GetName() != "cm-name" {
				t.Fatalf("configmap create target: %#v", action)
			}
			if named, ok := action.(k8stesting.PatchAction); ok && named.GetName() != "cm-name" {
				t.Fatalf("configmap patch target: %#v", action)
			}
		case "secrets":
			if named, ok := action.(k8stesting.CreateAction); ok && named.GetObject().(metav1.Object).GetName() != "secret-name" {
				t.Fatalf("secret create target: %#v", action)
			}
			if named, ok := action.(k8stesting.PatchAction); ok && named.GetName() != "secret-name" {
				t.Fatalf("secret patch target: %#v", action)
			}
		default:
			t.Fatalf("unexpected resource action: %#v", action)
		}
	}
}

func TestKubernetesApplierValidation(t *testing.T) {
	client := fake.NewSimpleClientset()
	if _, err := NewKubernetesApplier(nil, time.Second); err == nil {
		t.Fatal("nil client should be rejected")
	}
	if _, err := NewKubernetesApplier(client, 0); err == nil {
		t.Fatal("non-positive timeout should be rejected")
	}

	applier := newTestKubernetesApplier(t, client)
	tests := []ApplyTarget{
		{ConfigMapName: "cm", SecretName: "secret"},
		{Namespace: "UPPER", ConfigMapName: "cm", SecretName: "secret"},
		{Namespace: "ns", SecretName: "secret"},
		{Namespace: "ns", ConfigMapName: "Bad_Name", SecretName: "secret"},
		{Namespace: "ns", ConfigMapName: "cm"},
		{Namespace: "ns", ConfigMapName: "cm", SecretName: "Bad_Name"},
	}
	for _, target := range tests {
		if err := applier.ApplyRenderedPayloads(context.Background(), target, ApplyPayloads{}); err == nil {
			t.Fatalf("ApplyRenderedPayloads(%+v): expected validation error", target)
		}
	}
}

func TestKubernetesApplierContextCancellation(t *testing.T) {
	client := fake.NewSimpleClientset()
	applier := newTestKubernetesApplier(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := applier.ApplyRenderedPayloads(ctx, testApplyTarget(), ApplyPayloads{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestKubernetesApplierErrorContext(t *testing.T) {
	tests := []struct {
		name     string
		verb     string
		resource string
		want     string
	}{
		{name: "patch configmap", verb: "patch", resource: "configmaps", want: "patch configmap ai-platform/litellm-config"},
		{name: "create configmap", verb: "create", resource: "configmaps", want: "create configmap ai-platform/litellm-config"},
		{name: "patch secret", verb: "patch", resource: "secrets", want: "patch secret ai-platform/litellm-env"},
		{name: "create secret", verb: "create", resource: "secrets", want: "create secret ai-platform/litellm-env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			client.PrependReactor(tt.verb, tt.resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("boom")
			})
			applier := newTestKubernetesApplier(t, client)
			err := applier.ApplyRenderedPayloads(context.Background(), testApplyTarget(), ApplyPayloads{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("expected contextual boom error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func newTestKubernetesApplier(t *testing.T, client *fake.Clientset) *KubernetesApplier {
	t.Helper()
	applier, err := NewKubernetesApplier(client, time.Second)
	if err != nil {
		t.Fatalf("NewKubernetesApplier: %v", err)
	}
	return applier
}

func testApplyTarget() ApplyTarget {
	return ApplyTarget{
		Namespace:     "ai-platform",
		ConfigMapName: "litellm-config",
		SecretName:    "litellm-env",
	}
}
