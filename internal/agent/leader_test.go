package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLeaderConfigValidate(t *testing.T) {
	cfg := DefaultLeaderConfig("default", "config-agent-litellm", "pod-a")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	normalized := DefaultLeaderConfig(" default ", " config.agent.litellm ", " pod-a ")
	if err := normalized.Validate(); err != nil {
		t.Fatalf("Validate should accept normalized DNS subdomain lease names: %v", err)
	}

	cfg.RenewDeadline = cfg.LeaseDuration
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "greater than renew deadline") {
		t.Fatalf("expected duration validation error, got %v", err)
	}
}

func TestLeaderElectionAcquiresLeaseAndStops(t *testing.T) {
	client := fake.NewSimpleClientset()
	cfg := fastLeaderConfig("pod-a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	stopped := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunLeaderElection(ctx, client, cfg, LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				close(started)
				cancel()
			},
			OnStoppedLeading: func() {
				close(stopped)
			},
		})
	}()

	waitClosed(t, started, "leader start")
	waitClosed(t, stopped, "leader stop")
	if err := <-errCh; err != nil {
		t.Fatalf("RunLeaderElection: %v", err)
	}
	if _, err := client.CoordinationV1().Leases(cfg.Namespace).Get(context.Background(), cfg.LeaseName, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected lease to exist: %v", err)
	}
}

func TestLeaderElectionStandbyTakesOverReleasedLease(t *testing.T) {
	client := fake.NewSimpleClientset()

	runUntilStartedAndStopped(t, client, fastLeaderConfig("active"))
	standby := fastLeaderConfig("standby")
	standby.ReleaseOnCancel = false
	runUntilStartedAndStopped(t, client, standby)

	lease, err := client.CoordinationV1().Leases("default").Get(context.Background(), "config-agent-litellm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "standby" {
		t.Fatalf("expected standby to take over, got holder=%v", lease.Spec.HolderIdentity)
	}
}

func fastLeaderConfig(identity string) LeaderConfig {
	cfg := DefaultLeaderConfig("default", "config-agent-litellm", identity)
	cfg.LeaseDuration = 120 * time.Millisecond
	cfg.RenewDeadline = 80 * time.Millisecond
	cfg.RetryPeriod = 10 * time.Millisecond
	return cfg
}

func runUntilStartedAndStopped(t *testing.T, client *fake.Clientset, cfg LeaderConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	stopped := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunLeaderElection(ctx, client, cfg, LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				close(started)
				cancel()
			},
			OnStoppedLeading: func() {
				close(stopped)
			},
		})
	}()

	waitClosed(t, started, "leader start")
	waitClosed(t, stopped, "leader stop")
	if err := <-errCh; err != nil {
		t.Fatalf("RunLeaderElection: %v", err)
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
