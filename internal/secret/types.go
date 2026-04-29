package secret

import (
	"context"
	"time"
)

// RuntimeConfig groups secret-related runtime knobs. It is intentionally
// adapter-neutral so the composition root can wire volume, sealing, K8s, and
// audit implementations without coupling those packages to HTTP handlers.
type RuntimeConfig struct {
	MountPath                       string
	SealedSecretControllerNamespace string
	SealedSecretControllerName      string
	SealedSecretScope               string
	K8sApplyTimeout                 time.Duration
	AuditLogEnabled                 bool
}

// Reference identifies one plaintext value stored in a mounted K8s Secret.
// ID is the logical id from secrets.yaml; Namespace/Name/Key identify the
// concrete K8s Secret file under the mounted secret root.
type Reference struct {
	ID        string
	Namespace string
	Name      string
	Key       string
}

// Value holds plaintext secret bytes at an adapter boundary. Bytes returns a
// copy so callers cannot mutate retained storage by accident; Destroy performs
// best-effort zeroing for implementations that keep the value in memory.
type Value struct {
	data []byte
}

// NewValue clones plaintext bytes into a boundary value.
func NewValue(data []byte) Value {
	cp := append([]byte(nil), data...)
	return Value{data: cp}
}

// Bytes returns a copy of the plaintext bytes.
func (v Value) Bytes() []byte {
	return append([]byte(nil), v.data...)
}

// Destroy zeroes the retained plaintext bytes on a best-effort basis.
func (v *Value) Destroy() {
	for i := range v.data {
		v.data[i] = 0
	}
	v.data = nil
}

// VolumeReader reads plaintext values from the K8s Secret volume mount.
type VolumeReader interface {
	Read(ctx context.Context, ref Reference) (Value, error)
}

// VolumeOp identifies the kind of mounted secret file change.
type VolumeOp string

const (
	VolumeOpWrite  VolumeOp = "write"
	VolumeOpRemove VolumeOp = "remove"
)

// VolumeEvent reports that a mounted secret file was refreshed or removed.
type VolumeEvent struct {
	Reference Reference
	Path      string
	Op        VolumeOp
	Err       error
}

// VolumeWatcher watches mounted K8s Secret files and refreshes reader state
// when kubelet updates the projected files.
type VolumeWatcher interface {
	Watch(ctx context.Context, refs []Reference) (<-chan VolumeEvent, error)
}

// SealRequest is the plaintext input to a SealedSecret adapter. Data values
// are plaintext and must not be logged.
type SealRequest struct {
	Namespace string
	Name      string
	Data      map[string]Value
}

// SealedManifest is the encrypted YAML artifact that can be written to Git
// and applied to Kubernetes.
type SealedManifest struct {
	Namespace string
	Name      string
	Path      string
	YAML      []byte
}

// Sealer converts plaintext K8s Secret data into an encrypted SealedSecret
// manifest.
type Sealer interface {
	Seal(ctx context.Context, req SealRequest) (SealedManifest, error)
}

// Applier applies encrypted SealedSecret manifests to Kubernetes.
type Applier interface {
	ApplySealedSecret(ctx context.Context, manifest SealedManifest) error
}

// AuditEvent records non-sensitive secret activity. It must never include
// plaintext values.
type AuditEvent struct {
	At        time.Time
	Action    string
	Result    string
	Org       string
	Project   string
	Service   string
	SecretIDs []string
}

// Auditor writes non-sensitive secret audit events.
type Auditor interface {
	Record(ctx context.Context, event AuditEvent) error
}

// NoopAuditor is the default boundary implementation for tests and for
// deployments that intentionally disable secret audit logs.
type NoopAuditor struct{}

// Record implements Auditor.
func (NoopAuditor) Record(context.Context, AuditEvent) error { return nil }

// Dependencies groups secret adapters so higher-level services can be wired
// explicitly and tested with fakes.
type Dependencies struct {
	VolumeReader  VolumeReader
	VolumeWatcher VolumeWatcher
	Sealer        Sealer
	Applier       Applier
	Auditor       Auditor
}

// WithDefaults fills optional adapters with no-op implementations.
func (d Dependencies) WithDefaults() Dependencies {
	if d.Auditor == nil {
		d.Auditor = NoopAuditor{}
	}
	return d
}
