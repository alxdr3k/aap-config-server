package secret_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	sealedcrypto "github.com/bitnami-labs/sealed-secrets/pkg/crypto"

	"github.com/aap/config-server/internal/secret"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	restclient "k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestControllerPublicKeyProvider_FetchesServiceCertificate(t *testing.T) {
	priv, certPEM := testSealedSecretCert(t)
	client := fakeControllerClient(t, certPEM)

	provider, err := secret.NewControllerPublicKeyProvider(client)
	if err != nil {
		t.Fatalf("NewControllerPublicKeyProvider: %v", err)
	}
	pub, err := provider.PublicKey(context.Background(), secret.PublicKeyRequest{
		ControllerNamespace: "kube-system",
		ControllerName:      "sealed-secrets-controller",
	})
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}

	if pub.E != priv.E || pub.N.Cmp(priv.N) != 0 {
		t.Fatal("fetched public key did not match controller certificate")
	}
}

func TestControllerPublicKeyProvider_UsesNumericServicePortWhenUnnamed(t *testing.T) {
	_, certPEM := testSealedSecretCert(t)
	client := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "sealed-secrets-controller",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8080}},
		},
	})
	client.PrependProxyReactor("services", func(action k8stesting.Action) (bool, restclient.ResponseWrapper, error) {
		proxyAction, ok := action.(k8stesting.ProxyGetAction)
		if !ok {
			t.Fatalf("proxy action type: %T", action)
		}
		if proxyAction.GetPort() != "8080" {
			t.Fatalf("proxy port: got %q", proxyAction.GetPort())
		}
		return true, rawResponse{data: certPEM}, nil
	})

	provider, err := secret.NewControllerPublicKeyProvider(client)
	if err != nil {
		t.Fatalf("NewControllerPublicKeyProvider: %v", err)
	}
	if _, err := provider.PublicKey(context.Background(), secret.PublicKeyRequest{
		ControllerNamespace: "kube-system",
		ControllerName:      "sealed-secrets-controller",
	}); err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
}

func TestNewControllerPublicKeySealer_SealsWithFetchedCertificate(t *testing.T) {
	priv, certPEM := testSealedSecretCert(t)
	client := fakeControllerClient(t, certPEM)
	sealer, err := secret.NewControllerPublicKeySealer(
		secret.SealedSecretScopeStrict,
		"kube-system",
		"sealed-secrets-controller",
		client,
	)
	if err != nil {
		t.Fatalf("NewControllerPublicKeySealer: %v", err)
	}

	req := validSealRequest()
	req.Data = map[string]secret.Value{"api-key": secret.NewValue([]byte("supersecret"))}
	manifest, err := sealer.Seal(context.Background(), req)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	var doc struct {
		Spec struct {
			EncryptedData map[string]string `yaml:"encryptedData"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(manifest.YAML, &doc); err != nil {
		t.Fatalf("unmarshal sealed manifest: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.Spec.EncryptedData["api-key"])
	if err != nil {
		t.Fatalf("encryptedData should be base64: %v", err)
	}
	plaintext, err := sealedcrypto.HybridDecrypt(
		rand.Reader,
		map[string]*rsa.PrivateKey{"test": priv},
		raw,
		[]byte("ai-platform/litellm-secrets"),
	)
	if err != nil {
		t.Fatalf("HybridDecrypt sealed manifest value: %v", err)
	}
	if string(plaintext) != "supersecret" {
		t.Fatalf("plaintext: got %q", plaintext)
	}
}

func TestPublicKeyEncryptor_EncryptsWithScopeLabels(t *testing.T) {
	priv, _ := testSealedSecretCert(t)
	provider := &fakePublicKeyProvider{key: &priv.PublicKey}
	encryptor, err := secret.NewPublicKeyEncryptor("kube-system", "sealed-secrets-controller", provider)
	if err != nil {
		t.Fatalf("NewPublicKeyEncryptor: %v", err)
	}

	tests := []struct {
		scope string
		label string
	}{
		{scope: secret.SealedSecretScopeStrict, label: "app-ns/litellm-secrets"},
		{scope: secret.SealedSecretScopeNamespaceWide, label: "app-ns"},
		{scope: secret.SealedSecretScopeClusterWide, label: ""},
	}
	for _, tc := range tests {
		t.Run(tc.scope, func(t *testing.T) {
			ciphertext, err := encryptor.Encrypt(context.Background(), secret.EncryptRequest{
				Namespace: "app-ns",
				Name:      "litellm-secrets",
				Key:       "api-key",
				Scope:     tc.scope,
				Value:     secret.NewValue([]byte("supersecret")),
			})
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if strings.Contains(ciphertext, "supersecret") {
				t.Fatal("ciphertext leaked plaintext")
			}

			raw, err := base64.StdEncoding.DecodeString(ciphertext)
			if err != nil {
				t.Fatalf("ciphertext should be base64: %v", err)
			}
			plaintext, err := sealedcrypto.HybridDecrypt(rand.Reader, map[string]*rsa.PrivateKey{"test": priv}, raw, []byte(tc.label))
			if err != nil {
				t.Fatalf("HybridDecrypt with expected label: %v", err)
			}
			if string(plaintext) != "supersecret" {
				t.Fatalf("plaintext: got %q", plaintext)
			}
		})
	}

	if len(provider.requests) != len(tests) {
		t.Fatalf("public key provider calls: got %d want %d", len(provider.requests), len(tests))
	}
	for _, req := range provider.requests {
		if req.ControllerNamespace != "kube-system" || req.ControllerName != "sealed-secrets-controller" {
			t.Fatalf("provider request: %+v", req)
		}
	}
}

func TestPublicKeyEncryptor_ValidationAndErrorsDoNotLeakPlaintext(t *testing.T) {
	if _, err := secret.NewPublicKeyEncryptor("", "controller", &fakePublicKeyProvider{}); err == nil {
		t.Fatal("empty controller namespace should be rejected")
	}
	if _, err := secret.NewPublicKeyEncryptor("kube-system", "", &fakePublicKeyProvider{}); err == nil {
		t.Fatal("empty controller name should be rejected")
	}
	if _, err := secret.NewPublicKeyEncryptor("kube-system", "controller", nil); err == nil {
		t.Fatal("nil public key provider should be rejected")
	}

	provider := &fakePublicKeyProvider{err: errors.New("lookup boom")}
	encryptor, err := secret.NewPublicKeyEncryptor("kube-system", "sealed-secrets-controller", provider)
	if err != nil {
		t.Fatalf("NewPublicKeyEncryptor: %v", err)
	}

	_, err = encryptor.Encrypt(context.Background(), secret.EncryptRequest{
		Namespace: "app-ns",
		Name:      "litellm-secrets",
		Key:       "api-key",
		Scope:     secret.SealedSecretScopeStrict,
		Value:     secret.NewValue([]byte("supersecret")),
	})
	if err == nil {
		t.Fatal("provider error should be returned")
	}
	if !strings.Contains(err.Error(), "lookup sealed secret public key kube-system/sealed-secrets-controller") {
		t.Fatalf("missing public key lookup context: %v", err)
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("error leaked plaintext: %v", err)
	}
}

func TestControllerPublicKeyProvider_Validation(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider, err := secret.NewControllerPublicKeyProvider(client)
	if err != nil {
		t.Fatalf("NewControllerPublicKeyProvider: %v", err)
	}
	if _, err := secret.NewControllerPublicKeyProvider(nil); err == nil {
		t.Fatal("nil Kubernetes client should be rejected")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.PublicKey(ctx, secret.PublicKeyRequest{
		ControllerNamespace: "kube-system",
		ControllerName:      "sealed-secrets-controller",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: got %v", err)
	}

	if _, err := provider.PublicKey(context.Background(), secret.PublicKeyRequest{}); err == nil {
		t.Fatal("empty controller reference should be rejected")
	}
}

type fakePublicKeyProvider struct {
	key      *rsa.PublicKey
	err      error
	requests []secret.PublicKeyRequest
}

func (f *fakePublicKeyProvider) PublicKey(_ context.Context, req secret.PublicKeyRequest) (*rsa.PublicKey, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.key, nil
}

type rawResponse struct {
	data []byte
	err  error
}

func (r rawResponse) DoRaw(context.Context) ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.data, nil
}

func (r rawResponse) Stream(context.Context) (io.ReadCloser, error) {
	if r.err != nil {
		return nil, r.err
	}
	return io.NopCloser(bytes.NewReader(r.data)), nil
}

func fakeControllerClient(t *testing.T, certPEM []byte) *fake.Clientset {
	t.Helper()

	client := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "sealed-secrets-controller",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: "http"}},
		},
	})
	client.PrependProxyReactor("services", func(action k8stesting.Action) (bool, restclient.ResponseWrapper, error) {
		proxyAction, ok := action.(k8stesting.ProxyGetAction)
		if !ok {
			t.Fatalf("proxy action type: %T", action)
		}
		if proxyAction.GetNamespace() != "kube-system" {
			t.Fatalf("proxy namespace: got %q", proxyAction.GetNamespace())
		}
		if proxyAction.GetScheme() != "http" {
			t.Fatalf("proxy scheme: got %q", proxyAction.GetScheme())
		}
		if proxyAction.GetName() != "sealed-secrets-controller" {
			t.Fatalf("proxy service name: got %q", proxyAction.GetName())
		}
		if proxyAction.GetPort() != "http" {
			t.Fatalf("proxy port: got %q", proxyAction.GetPort())
		}
		if proxyAction.GetPath() != "/v1/cert.pem" {
			t.Fatalf("proxy path: got %q", proxyAction.GetPath())
		}
		return true, rawResponse{data: certPEM}, nil
	})
	return client
}

func testSealedSecretCert(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()

	priv, cert, err := sealedcrypto.GeneratePrivateKeyAndCert(2048, time.Hour, "sealed-secrets-controller")
	if err != nil {
		t.Fatalf("GeneratePrivateKeyAndCert: %v", err)
	}
	if _, ok := cert.PublicKey.(*rsa.PublicKey); !ok {
		t.Fatalf("generated non-RSA public key: %T", cert.PublicKey)
	}
	return priv, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}
