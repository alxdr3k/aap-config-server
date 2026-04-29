package secret_test

import (
	"context"
	"testing"

	"github.com/aap/config-server/internal/secret"
)

func TestValue_ClonesInputAndOutput(t *testing.T) {
	raw := []byte("top-secret")
	v := secret.NewValue(raw)
	raw[0] = 'x'

	got := v.Bytes()
	if string(got) != "top-secret" {
		t.Fatalf("value should clone input, got %q", string(got))
	}

	got[0] = 'y'
	if string(v.Bytes()) != "top-secret" {
		t.Fatal("Bytes should return a copy")
	}
}

func TestValue_DestroyZerosRetainedBytes(t *testing.T) {
	v := secret.NewValue([]byte("top-secret"))
	v.Destroy()
	if len(v.Bytes()) != 0 {
		t.Fatalf("destroyed value should return empty bytes, got %q", string(v.Bytes()))
	}
}

func TestDependencies_WithDefaults(t *testing.T) {
	deps := secret.Dependencies{}.WithDefaults()
	if deps.Auditor == nil {
		t.Fatal("expected default auditor")
	}
	if err := deps.Auditor.Record(context.Background(), secret.AuditEvent{}); err != nil {
		t.Fatalf("noop audit record: %v", err)
	}
}
