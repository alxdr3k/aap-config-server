package secret_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aap/config-server/internal/secret"
)

type fakeEncryptor struct {
	keys   []string
	scopes []string
	err    error
}

func (f *fakeEncryptor) Encrypt(_ context.Context, req secret.EncryptRequest) (string, error) {
	f.keys = append(f.keys, req.Key)
	f.scopes = append(f.scopes, req.Scope)
	if f.err != nil {
		return "", f.err
	}
	return "sealed-" + req.Key, nil
}

func TestDeterministicSealer_SealProducesStableYAML(t *testing.T) {
	enc := &fakeEncryptor{}
	sealer, err := secret.NewDeterministicSealer(secret.SealedSecretScopeNamespaceWide, enc)
	if err != nil {
		t.Fatalf("NewDeterministicSealer: %v", err)
	}

	req := validSealRequest()
	req.Data = map[string]secret.Value{
		"master-key":   secret.NewValue([]byte("plain-master")),
		"database-url": secret.NewValue([]byte("plain-db")),
	}
	manifest, err := sealer.Seal(context.Background(), req)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if manifest.Path != "configs/orgs/myorg/projects/ai/services/litellm/sealed-secrets/ai-platform/litellm-secrets.yaml" {
		t.Fatalf("manifest path: got %q", manifest.Path)
	}
	if !reflect.DeepEqual(enc.keys, []string{"database-url", "master-key"}) {
		t.Fatalf("encrypt order should be sorted, got %v", enc.keys)
	}
	if !reflect.DeepEqual(enc.scopes, []string{
		secret.SealedSecretScopeNamespaceWide,
		secret.SealedSecretScopeNamespaceWide,
	}) {
		t.Fatalf("encrypt scopes should match sealer scope, got %v", enc.scopes)
	}

	got := string(manifest.YAML)
	assertContainsInOrder(t, got,
		"apiVersion: bitnami.com/v1alpha1",
		"kind: SealedSecret",
		"metadata:",
		"  name: litellm-secrets",
		"  namespace: ai-platform",
		"  annotations:",
		"    sealedsecrets.bitnami.com/namespace-wide: \"true\"",
		"spec:",
		"  encryptedData:",
		"    database-url: sealed-database-url",
		"    master-key: sealed-master-key",
		"  template:",
		"    metadata:",
		"      name: litellm-secrets",
		"      namespace: ai-platform",
		"    type: Opaque",
	)
	if strings.Contains(got, "plain-master") || strings.Contains(got, "plain-db") {
		t.Fatalf("sealed manifest leaked plaintext: %s", got)
	}
}

func TestDeterministicSealer_ClusterWideAnnotation(t *testing.T) {
	sealer, err := secret.NewDeterministicSealer(secret.SealedSecretScopeClusterWide, &fakeEncryptor{})
	if err != nil {
		t.Fatalf("NewDeterministicSealer: %v", err)
	}

	req := validSealRequest()
	req.Name = "provider-keys"
	req.Data = map[string]secret.Value{"api-key": secret.NewValue([]byte("plain"))}
	manifest, err := sealer.Seal(context.Background(), req)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.Contains(string(manifest.YAML), "sealedsecrets.bitnami.com/cluster-wide: \"true\"") {
		t.Fatalf("cluster-wide annotation missing:\n%s", string(manifest.YAML))
	}
}

func TestDeterministicSealer_PathIncludesNamespace(t *testing.T) {
	sealer, err := secret.NewDeterministicSealer(secret.SealedSecretScopeStrict, &fakeEncryptor{})
	if err != nil {
		t.Fatalf("NewDeterministicSealer: %v", err)
	}

	req := validSealRequest()
	req.Namespace = "tenant-a"
	first, err := sealer.Seal(context.Background(), req)
	if err != nil {
		t.Fatalf("Seal first: %v", err)
	}

	req.Namespace = "tenant-b"
	second, err := sealer.Seal(context.Background(), req)
	if err != nil {
		t.Fatalf("Seal second: %v", err)
	}

	if first.Path == second.Path {
		t.Fatalf("paths should differ by namespace, got %q", first.Path)
	}
}

func TestDeterministicSealer_Validation(t *testing.T) {
	if _, err := secret.NewDeterministicSealer("bad", &fakeEncryptor{}); err == nil {
		t.Fatal("bad scope should be rejected")
	}
	if _, err := secret.NewDeterministicSealer("", nil); err == nil {
		t.Fatal("nil encryptor should be rejected")
	}

	sealer, err := secret.NewDeterministicSealer("", &fakeEncryptor{})
	if err != nil {
		t.Fatalf("NewDeterministicSealer: %v", err)
	}

	tests := []func() secret.SealRequest{
		func() secret.SealRequest {
			req := validSealRequest()
			req.Org = ""
			return req
		},
		func() secret.SealRequest {
			req := validSealRequest()
			req.Project = "../p"
			return req
		},
		func() secret.SealRequest {
			req := validSealRequest()
			req.Service = ""
			return req
		},
		func() secret.SealRequest {
			req := validSealRequest()
			req.Namespace = ""
			return req
		},
		func() secret.SealRequest {
			req := validSealRequest()
			req.Name = "../n"
			return req
		},
		func() secret.SealRequest {
			req := validSealRequest()
			req.Data = nil
			return req
		},
		func() secret.SealRequest {
			req := validSealRequest()
			req.Data = map[string]secret.Value{"../k": secret.NewValue([]byte("v"))}
			return req
		},
	}
	for _, buildReq := range tests {
		req := buildReq()
		if _, err := sealer.Seal(context.Background(), req); err == nil {
			t.Fatalf("Seal(%+v): expected validation error", req)
		}
	}
}

func TestDeterministicSealer_EncryptError(t *testing.T) {
	sealer, err := secret.NewDeterministicSealer("", &fakeEncryptor{err: errors.New("boom")})
	if err != nil {
		t.Fatalf("NewDeterministicSealer: %v", err)
	}

	req := validSealRequest()
	req.Namespace = "ns"
	req.Name = "name"
	req.Data = map[string]secret.Value{"key": secret.NewValue([]byte("value"))}
	_, err = sealer.Seal(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "encrypt sealed secret key") {
		t.Fatalf("expected encrypt context, got %v", err)
	}
}

func validSealRequest() secret.SealRequest {
	return secret.SealRequest{
		Org:       "myorg",
		Project:   "ai",
		Service:   "litellm",
		Namespace: "ai-platform",
		Name:      "litellm-secrets",
		Data:      map[string]secret.Value{"key": secret.NewValue([]byte("value"))},
	}
}

func assertContainsInOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	pos := 0
	for _, part := range parts {
		next := strings.Index(text[pos:], part)
		if next < 0 {
			t.Fatalf("missing %q after offset %d in:\n%s", part, pos, text)
		}
		pos += next + len(part)
	}
}
