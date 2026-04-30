package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	defaultLeaderLeaseDuration = 15 * time.Second
	defaultLeaderRenewDeadline = 10 * time.Second
	defaultLeaderRetryPeriod   = 2 * time.Second
)

// LeaderConfig describes the Kubernetes Lease used to elect one active Config
// Agent replica for a target service.
type LeaderConfig struct {
	Namespace       string
	LeaseName       string
	Identity        string
	LeaseDuration   time.Duration
	RenewDeadline   time.Duration
	RetryPeriod     time.Duration
	ReleaseOnCancel bool
}

// LeaderCallbacks are invoked by the underlying client-go leader elector.
type LeaderCallbacks struct {
	OnStartedLeading func(context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(string)
}

// DefaultLeaderConfig returns the production timing defaults used by
// client-go's core clients.
func DefaultLeaderConfig(namespace, leaseName, identity string) LeaderConfig {
	return LeaderConfig{
		Namespace:       namespace,
		LeaseName:       leaseName,
		Identity:        identity,
		LeaseDuration:   defaultLeaderLeaseDuration,
		RenewDeadline:   defaultLeaderRenewDeadline,
		RetryPeriod:     defaultLeaderRetryPeriod,
		ReleaseOnCancel: true,
	}
}

// RunLeaderElection blocks until ctx is cancelled, invoking callbacks whenever
// this replica starts or stops leading. Standby takeover is delegated to
// client-go's Lease-based leader election implementation.
func RunLeaderElection(ctx context.Context, client kubernetes.Interface, cfg LeaderConfig, callbacks LeaderCallbacks) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if client == nil {
		return errors.New("kubernetes client is required")
	}
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return err
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Namespace: cfg.Namespace,
			Name:      cfg.LeaseName,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	}
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		ReleaseOnCancel: cfg.ReleaseOnCancel,
		Name:            cfg.LeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: callbacks.OnStartedLeading,
			OnStoppedLeading: callbacks.OnStoppedLeading,
			OnNewLeader:      callbacks.OnNewLeader,
		},
	})
	if err != nil {
		return fmt.Errorf("create leader elector: %w", err)
	}

	elector.Run(ctx)
	return nil
}

// Validate checks the local contract before client-go talks to the Kubernetes
// API. Namespaces are DNS labels; Lease names follow Kubernetes object-name
// DNS subdomain constraints.
func (c LeaderConfig) Validate() error {
	c = c.Normalized()
	if c.Namespace == "" {
		return errors.New("leader election namespace is required")
	}
	if errs := validation.IsDNS1123Label(c.Namespace); len(errs) > 0 {
		return fmt.Errorf("leader election namespace is invalid: %s", strings.Join(errs, "; "))
	}
	if c.LeaseName == "" {
		return errors.New("leader election lease name is required")
	}
	if errs := validation.IsDNS1123Subdomain(c.LeaseName); len(errs) > 0 {
		return fmt.Errorf("leader election lease name is invalid: %s", strings.Join(errs, "; "))
	}
	if c.Identity == "" {
		return errors.New("leader election identity is required")
	}
	if c.LeaseDuration <= 0 {
		return fmt.Errorf("leader election lease duration must be > 0, got %s", c.LeaseDuration)
	}
	if c.RenewDeadline <= 0 {
		return fmt.Errorf("leader election renew deadline must be > 0, got %s", c.RenewDeadline)
	}
	if c.RetryPeriod <= 0 {
		return fmt.Errorf("leader election retry period must be > 0, got %s", c.RetryPeriod)
	}
	if c.LeaseDuration <= c.RenewDeadline {
		return errors.New("leader election lease duration must be greater than renew deadline")
	}
	if c.RenewDeadline <= c.RetryPeriod {
		return errors.New("leader election renew deadline must be greater than retry period")
	}
	return nil
}

// Normalized returns a copy with whitespace-only input noise removed.
func (c LeaderConfig) Normalized() LeaderConfig {
	c.Namespace = strings.TrimSpace(c.Namespace)
	c.LeaseName = strings.TrimSpace(c.LeaseName)
	c.Identity = strings.TrimSpace(c.Identity)
	return c
}
