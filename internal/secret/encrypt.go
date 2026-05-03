package secret

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	sealedcrypto "github.com/bitnami-labs/sealed-secrets/pkg/crypto"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const sealedSecretCertPath = "/v1/cert.pem"

// PublicKeyRequest identifies the SealedSecret controller certificate to fetch.
type PublicKeyRequest struct {
	ControllerNamespace string
	ControllerName      string
}

// PublicKeyProvider fetches the RSA public key used to seal secrets for the
// target SealedSecret controller.
type PublicKeyProvider interface {
	PublicKey(ctx context.Context, req PublicKeyRequest) (*rsa.PublicKey, error)
}

// ControllerPublicKeyProvider fetches the SealedSecret controller certificate
// through the Kubernetes service proxy, matching kubeseal's /v1/cert.pem path.
type ControllerPublicKeyProvider struct {
	client kubernetes.Interface
	now    func() time.Time
}

// NewControllerPublicKeyProvider creates a public-key provider backed by the
// Kubernetes CoreV1 service proxy.
func NewControllerPublicKeyProvider(client kubernetes.Interface) (*ControllerPublicKeyProvider, error) {
	if client == nil {
		return nil, errors.New("kubernetes client is required")
	}
	return &ControllerPublicKeyProvider{
		client: client,
		now:    time.Now,
	}, nil
}

// PublicKey implements PublicKeyProvider.
func (p *ControllerPublicKeyProvider) PublicKey(ctx context.Context, req PublicKeyRequest) (*rsa.PublicKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePublicKeyRequest(req); err != nil {
		return nil, err
	}

	services := p.client.CoreV1().Services(req.ControllerNamespace)
	service, err := services.Get(ctx, req.ControllerName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get sealed secret controller service %s/%s: %w",
			req.ControllerNamespace, req.ControllerName, err)
	}
	if len(service.Spec.Ports) == 0 {
		return nil, fmt.Errorf("sealed secret controller service %s/%s has no ports",
			req.ControllerNamespace, req.ControllerName)
	}
	port := service.Spec.Ports[0]
	portRef := port.Name
	if portRef == "" {
		if port.Port == 0 {
			return nil, fmt.Errorf("sealed secret controller service %s/%s first port has no name or number",
				req.ControllerNamespace, req.ControllerName)
		}
		portRef = strconv.Itoa(int(port.Port))
	}

	response := services.ProxyGet("http", req.ControllerName, portRef, sealedSecretCertPath, nil)
	if response == nil {
		return nil, fmt.Errorf("fetch sealed secret controller certificate %s/%s: empty proxy response",
			req.ControllerNamespace, req.ControllerName)
	}
	data, err := response.DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch sealed secret controller certificate %s/%s: %w",
			req.ControllerNamespace, req.ControllerName, err)
	}
	pubKey, err := publicKeyFromPEMCertificate(data, p.now())
	if err != nil {
		return nil, fmt.Errorf("parse sealed secret controller certificate %s/%s: %w",
			req.ControllerNamespace, req.ControllerName, err)
	}
	return pubKey, nil
}

// PublicKeyEncryptor encrypts SealedSecret data items with the controller's
// public key and Bitnami's sealed-secrets hybrid encryption format.
type PublicKeyEncryptor struct {
	controllerNamespace string
	controllerName      string
	provider            PublicKeyProvider
	random              io.Reader
}

// NewPublicKeyEncryptor creates an Encryptor backed by a SealedSecret
// controller public-key provider.
func NewPublicKeyEncryptor(controllerNamespace, controllerName string, provider PublicKeyProvider) (*PublicKeyEncryptor, error) {
	if err := validatePathSegment("controller namespace", controllerNamespace); err != nil {
		return nil, err
	}
	if err := validatePathSegment("controller name", controllerName); err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, errors.New("sealed secret public key provider is required")
	}
	return &PublicKeyEncryptor{
		controllerNamespace: controllerNamespace,
		controllerName:      controllerName,
		provider:            provider,
		random:              rand.Reader,
	}, nil
}

// Encrypt implements Encryptor.
func (e *PublicKeyEncryptor) Encrypt(ctx context.Context, req EncryptRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validatePathSegment("namespace", req.Namespace); err != nil {
		return "", err
	}
	if err := validatePathSegment("name", req.Name); err != nil {
		return "", err
	}
	if err := validatePathSegment("key", req.Key); err != nil {
		return "", err
	}
	scope, err := normalizeSealScope(req.Scope)
	if err != nil {
		return "", err
	}

	pubKey, err := e.provider.PublicKey(ctx, PublicKeyRequest{
		ControllerNamespace: e.controllerNamespace,
		ControllerName:      e.controllerName,
	})
	if err != nil {
		return "", fmt.Errorf("lookup sealed secret public key %s/%s: %w",
			e.controllerNamespace, e.controllerName, err)
	}
	if pubKey == nil {
		return "", fmt.Errorf("lookup sealed secret public key %s/%s: empty public key",
			e.controllerNamespace, e.controllerName)
	}

	plaintext := req.Value.Bytes()
	defer zeroBytes(plaintext)
	ciphertext, err := sealedcrypto.HybridEncrypt(e.random, pubKey, plaintext, sealedSecretEncryptionLabel(req.Namespace, req.Name, scope))
	if err != nil {
		return "", fmt.Errorf("encrypt sealed secret value %s/%s/%s: %w",
			req.Namespace, req.Name, req.Key, err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// NewControllerPublicKeySealer wires a controller public-key provider into the
// deterministic SealedSecret YAML generator.
func NewControllerPublicKeySealer(scope, controllerNamespace, controllerName string, client kubernetes.Interface) (*DeterministicSealer, error) {
	provider, err := NewControllerPublicKeyProvider(client)
	if err != nil {
		return nil, err
	}
	encryptor, err := NewPublicKeyEncryptor(controllerNamespace, controllerName, provider)
	if err != nil {
		return nil, err
	}
	return NewDeterministicSealer(scope, encryptor)
}

func validatePublicKeyRequest(req PublicKeyRequest) error {
	if err := validatePathSegment("controller namespace", req.ControllerNamespace); err != nil {
		return err
	}
	if err := validatePathSegment("controller name", req.ControllerName); err != nil {
		return err
	}
	return nil
}

func sealedSecretEncryptionLabel(namespace, name, scope string) []byte {
	switch scope {
	case SealedSecretScopeClusterWide:
		return nil
	case SealedSecretScopeNamespaceWide:
		return []byte(namespace)
	default:
		return []byte(fmt.Sprintf("%s/%s", namespace, name))
	}
}

func publicKeyFromPEMCertificate(data []byte, now time.Time) (*rsa.PublicKey, error) {
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil, errors.New("no PEM certificate found")
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}

		certs, err := x509.ParseCertificates(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PEM certificate: %w", err)
		}
		if len(certs) == 0 {
			return nil, errors.New("PEM block contained no certificates")
		}

		cert := certs[0]
		if now.Before(cert.NotBefore) {
			return nil, fmt.Errorf("certificate is not valid before %s", cert.NotBefore.Format(time.RFC3339))
		}
		if now.After(cert.NotAfter) {
			return nil, fmt.Errorf("certificate expired at %s", cert.NotAfter.Format(time.RFC3339))
		}
		pubKey, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("certificate public key must be RSA, got %T", cert.PublicKey)
		}
		return pubKey, nil
	}
}
