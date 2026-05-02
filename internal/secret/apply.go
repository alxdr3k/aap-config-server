package secret

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
)

var (
	sealedSecretGVR = schema.GroupVersionResource{
		Group:    "bitnami.com",
		Version:  "v1alpha1",
		Resource: "sealedsecrets",
	}
	sealedSecretGVK = schema.GroupVersionKind{
		Group:   "bitnami.com",
		Version: "v1alpha1",
		Kind:    "SealedSecret",
	}
)

// DynamicApplier applies Bitnami SealedSecret manifests through a Kubernetes
// dynamic client. It creates missing objects and updates existing objects.
type DynamicApplier struct {
	client  dynamic.Interface
	timeout time.Duration
}

// NewDynamicApplier creates a Kubernetes applier for SealedSecret manifests.
func NewDynamicApplier(client dynamic.Interface, timeout time.Duration) (*DynamicApplier, error) {
	if client == nil {
		return nil, errors.New("kubernetes dynamic client is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("kubernetes apply timeout must be > 0, got %s", timeout)
	}
	return &DynamicApplier{client: client, timeout: timeout}, nil
}

// ApplySealedSecret implements Applier.
func (a *DynamicApplier) ApplySealedSecret(ctx context.Context, manifest SealedManifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	obj, err := sealedSecretObjectFromManifest(manifest)
	if err != nil {
		return err
	}

	applyCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	resource := a.client.Resource(sealedSecretGVR).Namespace(manifest.Namespace)
	existing, err := resource.Get(applyCtx, manifest.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = resource.Create(applyCtx, obj, metav1.CreateOptions{})
	case err != nil:
		return fmt.Errorf("get sealed secret %s/%s: %w", manifest.Namespace, manifest.Name, err)
	default:
		mergeExistingMetadata(obj, existing)
		_, err = resource.Update(applyCtx, obj, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply sealed secret %s/%s: %w", manifest.Namespace, manifest.Name, err)
	}
	return nil
}

// mergeExistingMetadata copies metadata fields that other actors (operators,
// controllers, deployment tools) may have attached to the live object —
// labels, annotations, and finalizers — into the new object before issuing
// an Update. Without this, every reapply of a generated SealedSecret would
// clobber operator metadata even when the encrypted payload is the only
// field we intend to update.
//
// Phase-1 trade-off (the alternative is server-side apply with a field
// manager, which is the K8s-native solution but requires CRD schema support
// that the Bitnami SealedSecret type does not reliably expose):
//
//   - Labels/annotations: keys we set in the generated manifest always win;
//     operator-set keys we do not declare are preserved.
//   - Finalizers: copied through unfiltered. This means a stale finalizer
//     attached by a removed controller can persist and block deletion;
//     operators clearing such finalizers manually is an accepted Phase-1
//     procedure. The alternative — stripping finalizers on every Update —
//     was rejected because it would actively break controller-managed
//     deletion semantics during normal operation, a higher-frequency
//     failure than the rare stale-finalizer case. See ADR-004.
func mergeExistingMetadata(obj, existing *unstructured.Unstructured) {
	obj.SetResourceVersion(existing.GetResourceVersion())

	if finalizers := existing.GetFinalizers(); len(finalizers) > 0 {
		obj.SetFinalizers(finalizers)
	}

	merged := mergeStringMaps(existing.GetLabels(), obj.GetLabels())
	if merged != nil {
		obj.SetLabels(merged)
	}
	merged = mergeStringMaps(existing.GetAnnotations(), obj.GetAnnotations())
	if merged != nil {
		obj.SetAnnotations(merged)
	}
}

func mergeStringMaps(existing, ours map[string]string) map[string]string {
	if len(existing) == 0 && len(ours) == 0 {
		return nil
	}
	merged := make(map[string]string, len(existing)+len(ours))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range ours {
		merged[k] = v
	}
	return merged
}

func sealedSecretObjectFromManifest(manifest SealedManifest) (*unstructured.Unstructured, error) {
	if manifest.Namespace == "" {
		return nil, errors.New("sealed manifest namespace is required")
	}
	if manifest.Name == "" {
		return nil, errors.New("sealed manifest name is required")
	}
	if len(manifest.YAML) == 0 {
		return nil, errors.New("sealed manifest yaml is required")
	}

	raw, err := k8syaml.ToJSON(manifest.YAML)
	if err != nil {
		return nil, fmt.Errorf("decode sealed manifest yaml: %w", err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("unmarshal sealed manifest yaml: %w", err)
	}
	if got := obj.GroupVersionKind(); got != sealedSecretGVK {
		return nil, fmt.Errorf("sealed manifest kind must be %s, got %s", sealedSecretGVK.String(), got.String())
	}
	if got := obj.GetNamespace(); got != manifest.Namespace {
		return nil, fmt.Errorf("sealed manifest namespace mismatch: manifest=%q yaml=%q", manifest.Namespace, got)
	}
	if got := obj.GetName(); got != manifest.Name {
		return nil, fmt.Errorf("sealed manifest name mismatch: manifest=%q yaml=%q", manifest.Name, got)
	}
	obj.SetGroupVersionKind(sealedSecretGVK)
	return obj, nil
}
