package secret_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aap/config-server/internal/secret"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDynamicApplier_ApplyCreatesSealedSecret(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	applier, err := secret.NewDynamicApplier(client, time.Second)
	if err != nil {
		t.Fatalf("NewDynamicApplier: %v", err)
	}

	manifest := sealedManifest("ai-platform", "litellm-secrets", "sealed-master")
	if err := applier.ApplySealedSecret(context.Background(), manifest); err != nil {
		t.Fatalf("ApplySealedSecret: %v", err)
	}

	got, err := client.Resource(sealedSecretGVR()).Namespace("ai-platform").
		Get(context.Background(), "litellm-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get applied SealedSecret: %v", err)
	}
	encrypted, ok, err := unstructured.NestedStringMap(got.Object, "spec", "encryptedData")
	if err != nil || !ok {
		t.Fatalf("encryptedData: ok=%v err=%v object=%v", ok, err, got.Object)
	}
	if encrypted["master-key"] != "sealed-master" {
		t.Fatalf("encryptedData master-key: got %q", encrypted["master-key"])
	}
}

func TestDynamicApplier_ApplyUpdatesExistingSealedSecret(t *testing.T) {
	existing := sealedSecretObject("ai-platform", "litellm-secrets", "old")
	existing.SetResourceVersion("7")
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), existing)
	applier, err := secret.NewDynamicApplier(client, time.Second)
	if err != nil {
		t.Fatalf("NewDynamicApplier: %v", err)
	}

	manifest := sealedManifest("ai-platform", "litellm-secrets", "new")
	if err := applier.ApplySealedSecret(context.Background(), manifest); err != nil {
		t.Fatalf("ApplySealedSecret: %v", err)
	}

	got, err := client.Resource(sealedSecretGVR()).Namespace("ai-platform").
		Get(context.Background(), "litellm-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get applied SealedSecret: %v", err)
	}
	encrypted, ok, err := unstructured.NestedStringMap(got.Object, "spec", "encryptedData")
	if err != nil || !ok {
		t.Fatalf("encryptedData: ok=%v err=%v object=%v", ok, err, got.Object)
	}
	if encrypted["master-key"] != "new" {
		t.Fatalf("encryptedData master-key: got %q", encrypted["master-key"])
	}
}

func TestDynamicApplier_Validation(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	if _, err := secret.NewDynamicApplier(nil, time.Second); err == nil {
		t.Fatal("nil client should be rejected")
	}
	if _, err := secret.NewDynamicApplier(client, 0); err == nil {
		t.Fatal("non-positive timeout should be rejected")
	}

	applier, err := secret.NewDynamicApplier(client, time.Second)
	if err != nil {
		t.Fatalf("NewDynamicApplier: %v", err)
	}

	tests := []secret.SealedManifest{
		{Name: "n", YAML: sealedManifest("ns", "n", "v").YAML},
		{Namespace: "ns", YAML: sealedManifest("ns", "n", "v").YAML},
		{Namespace: "ns", Name: "n"},
		{Namespace: "ns", Name: "n", YAML: []byte("not: [valid")},
		{Namespace: "ns", Name: "n", YAML: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: \"n\"\n  namespace: \"ns\"\n")},
		{Namespace: "ns", Name: "n", YAML: sealedManifest("other", "n", "v").YAML},
		{Namespace: "ns", Name: "n", YAML: sealedManifest("ns", "other", "v").YAML},
	}
	for _, manifest := range tests {
		if err := applier.ApplySealedSecret(context.Background(), manifest); err == nil {
			t.Fatalf("ApplySealedSecret(%+v): expected validation error", manifest)
		}
	}
}

func TestDynamicApplier_ContextCancellation(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	applier, err := secret.NewDynamicApplier(client, time.Second)
	if err != nil {
		t.Fatalf("NewDynamicApplier: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = applier.ApplySealedSecret(ctx, sealedManifest("ns", "n", "v"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDynamicApplier_ErrorContext(t *testing.T) {
	tests := []struct {
		name string
		verb string
		want string
	}{
		{name: "get", verb: "get", want: "get sealed secret namespace/secret-name"},
		{name: "create", verb: "create", want: "apply sealed secret namespace/secret-name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			client.PrependReactor(tt.verb, "sealedsecrets", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("boom")
			})
			applier, err := secret.NewDynamicApplier(client, time.Second)
			if err != nil {
				t.Fatalf("NewDynamicApplier: %v", err)
			}

			err = applier.ApplySealedSecret(context.Background(), sealedManifest("namespace", "secret-name", "v"))
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("expected contextual boom error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func sealedManifest(namespace, name, value string) secret.SealedManifest {
	return secret.SealedManifest{
		Namespace: namespace,
		Name:      name,
		YAML:      []byte(sealedSecretYAML(namespace, name, value)),
	}
}

func sealedSecretObject(namespace, name, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "bitnami.com/v1alpha1",
		"kind":       "SealedSecret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"encryptedData": map[string]any{"master-key": value},
		},
	}}
}

func sealedSecretYAML(namespace, name, value string) string {
	return strings.Join([]string{
		"apiVersion: bitnami.com/v1alpha1",
		"kind: SealedSecret",
		"metadata:",
		"  name: \"" + name + "\"",
		"  namespace: \"" + namespace + "\"",
		"spec:",
		"  encryptedData:",
		"    master-key: \"" + value + "\"",
		"  template:",
		"    metadata:",
		"      name: \"" + name + "\"",
		"      namespace: \"" + namespace + "\"",
		"    type: Opaque",
		"",
	}, "\n")
}

func sealedSecretGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "bitnami.com",
		Version:  "v1alpha1",
		Resource: "sealedsecrets",
	}
}
