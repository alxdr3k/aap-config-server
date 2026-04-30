//go:build e2e

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestConfigAgentE2ESmokeFetchRenderApplyAndRollout(t *testing.T) {
	ctx := context.Background()
	ref := ServiceRef{Org: "myorg", Project: "ai", Service: "litellm"}
	updatedAt := time.Date(2026, 4, 30, 5, 0, 0, 0, time.UTC)
	snapshotClient := &sequenceSnapshotClient{
		configs: []*ConfigSnapshot{{
			Metadata: Metadata{Org: ref.Org, Project: ref.Project, Service: ref.Service, Version: "rev-1", UpdatedAt: updatedAt},
			Config: map[string]any{
				"model_list": []any{
					map[string]any{
						"model_name": "gpt-4o",
						"litellm_params": map[string]any{
							"api_key": "os.environ/AZURE_API_KEY",
							"model":   "azure/gpt-4o",
						},
					},
				},
				"router_settings": map[string]any{"num_retries": json.Number("3")},
			},
		}},
		envs: []*EnvVarsSnapshot{{
			Metadata: Metadata{Org: ref.Org, Project: ref.Project, Service: ref.Service, Version: "rev-1", UpdatedAt: updatedAt},
			EnvVars: EnvVars{
				Plain:   map[string]string{"LOG_LEVEL": "INFO"},
				Secrets: map[string]string{"AZURE_API_KEY": "secret-value"},
			},
		}},
	}
	loop, err := NewFetchLoop(snapshotClient, FetchLoopConfig{
		Ref:                 ref,
		ResolveSecrets:      true,
		PollInterval:        time.Second,
		RetryInitialBackoff: time.Millisecond,
		RetryMaxBackoff:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewFetchLoop: %v", err)
	}

	result, err := loop.FetchOnce(ctx)
	if err != nil {
		t.Fatalf("FetchOnce: %v", err)
	}
	if !result.Initial || !result.Changed || len(snapshotClient.resolveSecrets) != 1 || !snapshotClient.resolveSecrets[0] {
		t.Fatalf("fetch result did not detect initial resolved change: result=%+v resolveCalls=%+v", result, snapshotClient.resolveSecrets)
	}
	debouncer, err := NewDebouncer(DebounceConfig{
		Cooldown:    10 * time.Second,
		QuietPeriod: 10 * time.Second,
		MaxWait:     time.Minute,
	})
	if err != nil {
		t.Fatalf("NewDebouncer: %v", err)
	}
	if decision := debouncer.RecordChange(result.State.ConfigUpdatedAt); !decision.ApplyNow {
		t.Fatalf("initial change should pass leading-edge debounce: %+v", decision)
	}

	configYAML, err := RenderConfigYAML(result.Config)
	if err != nil {
		t.Fatalf("RenderConfigYAML: %v", err)
	}
	envSH, err := RenderEnvSH(result.EnvVars)
	if err != nil {
		t.Fatalf("RenderEnvSH: %v", err)
	}
	payloads := ApplyPayloads{ConfigYAML: configYAML, EnvSH: envSH}

	kubeClient := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ai-platform", Name: "litellm"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"existing": "keep"}},
			},
		},
	})
	applier, err := NewKubernetesApplier(kubeClient, time.Second)
	if err != nil {
		t.Fatalf("NewKubernetesApplier: %v", err)
	}
	patcher, err := NewKubernetesRolloutPatcher(kubeClient, time.Second)
	if err != nil {
		t.Fatalf("NewKubernetesRolloutPatcher: %v", err)
	}
	patcher.now = func() time.Time { return time.Date(2026, 4, 30, 5, 1, 2, 3, time.UTC) }

	if err := applier.ApplyRenderedPayloads(ctx, ApplyTarget{
		Namespace:     "ai-platform",
		ConfigMapName: "litellm-config",
		SecretName:    "litellm-env",
	}, payloads); err != nil {
		t.Fatalf("ApplyRenderedPayloads: %v", err)
	}
	rollout, err := patcher.TriggerRollout(ctx, RolloutTarget{
		Namespace:      "ai-platform",
		DeploymentName: "litellm",
	}, payloads)
	if err != nil {
		t.Fatalf("TriggerRollout: %v", err)
	}
	loop.MarkHandled(result)

	configMap, err := kubeClient.CoreV1().ConfigMaps("ai-platform").Get(ctx, "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get ConfigMap: %v", err)
	}
	configData := configMap.Data[configMapDataKey]
	if !strings.Contains(configData, "model_name: gpt-4o") ||
		!strings.Contains(configData, "api_key: os.environ/AZURE_API_KEY") ||
		!strings.Contains(configData, "num_retries: 3") {
		t.Fatalf("rendered config.yaml did not contain expected native payload:\n%s", configData)
	}

	secret, err := kubeClient.CoreV1().Secrets("ai-platform").Get(ctx, "litellm-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Secret: %v", err)
	}
	wantEnvSH := "export AZURE_API_KEY='secret-value'\nexport LOG_LEVEL='INFO'\n"
	if got := string(secret.Data[envSHDataKey]); got != wantEnvSH {
		t.Fatalf("env.sh data:\ngot  %q\nwant %q", got, wantEnvSH)
	}

	deployment, err := kubeClient.AppsV1().Deployments("ai-platform").Get(ctx, "litellm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Deployment: %v", err)
	}
	annotations := deployment.Spec.Template.Annotations
	if annotations["existing"] != "keep" {
		t.Fatalf("existing deployment annotation should be preserved: %+v", annotations)
	}
	if annotations[rolloutConfigHashAnnotation] != payloadConfigHash(payloads) ||
		rollout.ConfigHash != annotations[rolloutConfigHashAnnotation] {
		t.Fatalf("rollout config hash mismatch: result=%+v annotations=%+v", rollout, annotations)
	}
	if annotations[rolloutRestartAtAnnotation] != "2026-04-30T05:01:02.000000003Z" {
		t.Fatalf("restart annotation: %q", annotations[rolloutRestartAtAnnotation])
	}
	if loop.State().Revision != "rev-1" {
		t.Fatalf("handled fetch state revision: %+v", loop.State())
	}
}
