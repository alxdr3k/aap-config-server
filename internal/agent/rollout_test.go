package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestKubernetesRolloutPatcherPatchesDeploymentAnnotations(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ai-platform", Name: "litellm"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"keep": "true"},
				},
			},
		},
	})
	patcher := newTestRolloutPatcher(t, client, fixedRolloutTime())
	payloads := ApplyPayloads{
		ConfigYAML: []byte("model_list: []\n"),
		EnvSH:      []byte("export API_KEY='secret'\n"),
	}

	result, err := patcher.TriggerRollout(context.Background(), testRolloutTarget(), payloads)
	if err != nil {
		t.Fatalf("TriggerRollout: %v", err)
	}

	deploy, err := client.AppsV1().Deployments("ai-platform").Get(context.Background(), "litellm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Deployment: %v", err)
	}
	annotations := deploy.Spec.Template.Annotations
	if annotations["keep"] != "true" {
		t.Fatalf("existing annotation should be preserved: %+v", annotations)
	}
	if annotations[rolloutConfigHashAnnotation] != result.ConfigHash || result.ConfigHash != payloadConfigHash(payloads) {
		t.Fatalf("config hash annotation/result mismatch: annotations=%+v result=%+v", annotations, result)
	}
	if annotations[rolloutRestartAtAnnotation] != "2026-04-30T04:05:06.000000007Z" ||
		result.RestartAt != "2026-04-30T04:05:06.000000007Z" {
		t.Fatalf("restart-at annotation/result mismatch: annotations=%+v result=%+v", annotations, result)
	}
}

func TestPayloadConfigHashUsesPayloadBoundaries(t *testing.T) {
	first := payloadConfigHash(ApplyPayloads{ConfigYAML: []byte("ab"), EnvSH: []byte("c")})
	second := payloadConfigHash(ApplyPayloads{ConfigYAML: []byte("a"), EnvSH: []byte("bc")})
	if first == second {
		t.Fatalf("payload boundaries should affect hash: %q", first)
	}
	if first != payloadConfigHash(ApplyPayloads{ConfigYAML: []byte("ab"), EnvSH: []byte("c")}) {
		t.Fatal("payload hash should be deterministic")
	}
}

func TestKubernetesRolloutPatcherUsesConfiguredDeploymentName(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "target-deploy"},
	})
	patcher := newTestRolloutPatcher(t, client, fixedRolloutTime())

	_, err := patcher.TriggerRollout(context.Background(), RolloutTarget{
		Namespace:      "ns",
		DeploymentName: "target-deploy",
	}, ApplyPayloads{})
	if err != nil {
		t.Fatalf("TriggerRollout: %v", err)
	}

	for _, action := range client.Actions() {
		if action.GetResource().Resource != "deployments" || action.GetNamespace() != "ns" {
			t.Fatalf("unexpected action target: %#v", action)
		}
		if named, ok := action.(k8stesting.PatchAction); ok && named.GetName() != "target-deploy" {
			t.Fatalf("deployment patch target: %#v", action)
		}
	}
}

func TestKubernetesRolloutPatcherValidation(t *testing.T) {
	client := fake.NewSimpleClientset()
	if _, err := NewKubernetesRolloutPatcher(nil, time.Second); err == nil {
		t.Fatal("nil client should be rejected")
	}
	if _, err := NewKubernetesRolloutPatcher(client, 0); err == nil {
		t.Fatal("non-positive timeout should be rejected")
	}

	patcher := newTestRolloutPatcher(t, client, fixedRolloutTime())
	tests := []RolloutTarget{
		{DeploymentName: "deploy"},
		{Namespace: "UPPER", DeploymentName: "deploy"},
		{Namespace: "ns"},
		{Namespace: "ns", DeploymentName: "Bad_Name"},
	}
	for _, target := range tests {
		if _, err := patcher.TriggerRollout(context.Background(), target, ApplyPayloads{}); err == nil {
			t.Fatalf("TriggerRollout(%+v): expected validation error", target)
		}
	}
}

func TestKubernetesRolloutPatcherContextCancellation(t *testing.T) {
	client := fake.NewSimpleClientset()
	patcher := newTestRolloutPatcher(t, client, fixedRolloutTime())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := patcher.TriggerRollout(ctx, testRolloutTarget(), ApplyPayloads{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestKubernetesRolloutPatcherErrorContext(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("patch", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})
	patcher := newTestRolloutPatcher(t, client, fixedRolloutTime())

	_, err := patcher.TriggerRollout(context.Background(), testRolloutTarget(), ApplyPayloads{})
	if err == nil || !strings.Contains(err.Error(), "patch deployment ai-platform/litellm") ||
		!strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected contextual boom error, got %v", err)
	}
}

func newTestRolloutPatcher(t *testing.T, client *fake.Clientset, now time.Time) *KubernetesRolloutPatcher {
	t.Helper()
	patcher, err := NewKubernetesRolloutPatcher(client, time.Second)
	if err != nil {
		t.Fatalf("NewKubernetesRolloutPatcher: %v", err)
	}
	patcher.now = func() time.Time { return now }
	return patcher
}

func testRolloutTarget() RolloutTarget {
	return RolloutTarget{
		Namespace:      "ai-platform",
		DeploymentName: "litellm",
	}
}

func fixedRolloutTime() time.Time {
	return time.Date(2026, 4, 30, 4, 5, 6, 7, time.UTC)
}
