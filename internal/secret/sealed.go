package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SealedSecretScopeStrict        = "strict"
	SealedSecretScopeNamespaceWide = "namespace-wide"
	SealedSecretScopeClusterWide   = "cluster-wide"

	sealedSecretAPIVersion = "bitnami.com/v1alpha1"
	sealedSecretKind       = "SealedSecret"
	sealedSecretTypeOpaque = "Opaque"

	namespaceWideAnnotation = "sealedsecrets.bitnami.com/namespace-wide"
	clusterWideAnnotation   = "sealedsecrets.bitnami.com/cluster-wide"
)

// EncryptRequest carries one plaintext value into the SealedSecret encryptor.
type EncryptRequest struct {
	Namespace string
	Name      string
	Key       string
	Scope     string
	Value     Value
}

// Encryptor encrypts one K8s Secret key for a SealedSecret manifest.
type Encryptor interface {
	Encrypt(ctx context.Context, req EncryptRequest) (string, error)
}

// DeterministicSealer generates Bitnami SealedSecret YAML with stable field
// and encryptedData key ordering. It delegates cryptography to Encryptor.
type DeterministicSealer struct {
	scope     string
	encryptor Encryptor
}

// NewDeterministicSealer creates a Sealer for the configured SealedSecret scope.
func NewDeterministicSealer(scope string, encryptor Encryptor) (*DeterministicSealer, error) {
	normalized, err := normalizeSealScope(scope)
	if err != nil {
		return nil, err
	}
	if encryptor == nil {
		return nil, errors.New("secret encryptor is required")
	}
	return &DeterministicSealer{scope: normalized, encryptor: encryptor}, nil
}

// Seal implements Sealer.
func (s *DeterministicSealer) Seal(ctx context.Context, req SealRequest) (SealedManifest, error) {
	if err := ctx.Err(); err != nil {
		return SealedManifest{}, err
	}
	if err := validatePathSegment("org", req.Org); err != nil {
		return SealedManifest{}, err
	}
	if err := validatePathSegment("project", req.Project); err != nil {
		return SealedManifest{}, err
	}
	if err := validatePathSegment("service", req.Service); err != nil {
		return SealedManifest{}, err
	}
	if err := validatePathSegment("namespace", req.Namespace); err != nil {
		return SealedManifest{}, err
	}
	if err := validatePathSegment("name", req.Name); err != nil {
		return SealedManifest{}, err
	}
	if len(req.Data) == 0 {
		return SealedManifest{}, errors.New("at least one secret data key is required")
	}

	keys := make([]string, 0, len(req.Data))
	for key := range req.Data {
		if err := validatePathSegment("key", key); err != nil {
			return SealedManifest{}, err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	encrypted := make([]keyValue, 0, len(keys))
	for _, key := range keys {
		value := req.Data[key]
		ciphertext, err := s.encryptor.Encrypt(ctx, EncryptRequest{
			Namespace: req.Namespace,
			Name:      req.Name,
			Key:       key,
			Scope:     s.scope,
			Value:     value,
		})
		if err != nil {
			return SealedManifest{}, fmt.Errorf("encrypt sealed secret key %s/%s/%s: %w",
				req.Namespace, req.Name, key, err)
		}
		if ciphertext == "" {
			return SealedManifest{}, fmt.Errorf("encrypt sealed secret key %s/%s/%s: empty ciphertext",
				req.Namespace, req.Name, key)
		}
		encrypted = append(encrypted, keyValue{key: key, value: ciphertext})
	}

	raw, err := encodeSealedSecretYAML(req.Namespace, req.Name, s.scope, encrypted)
	if err != nil {
		return SealedManifest{}, err
	}

	return SealedManifest{
		Namespace: req.Namespace,
		Name:      req.Name,
		Path:      sealedSecretPath(req),
		YAML:      raw,
	}, nil
}

type keyValue struct {
	key   string
	value string
}

func encodeSealedSecretYAML(namespace, name, scope string, encrypted []keyValue) ([]byte, error) {
	metadata := []*yaml.Node{
		scalar("name"), quotedScalar(name),
		scalar("namespace"), quotedScalar(namespace),
	}
	if annotations := scopeAnnotations(scope); len(annotations) > 0 {
		metadata = append(metadata, scalar("annotations"), mapping(annotations...))
	}

	encryptedNodes := make([]*yaml.Node, 0, len(encrypted)*2)
	for _, kv := range encrypted {
		encryptedNodes = append(encryptedNodes, scalar(kv.key), quotedScalar(kv.value))
	}

	root := mapping(
		scalar("apiVersion"), scalar(sealedSecretAPIVersion),
		scalar("kind"), scalar(sealedSecretKind),
		scalar("metadata"), mapping(metadata...),
		scalar("spec"), mapping(
			scalar("encryptedData"), mapping(encryptedNodes...),
			scalar("template"), mapping(
				scalar("metadata"), mapping(
					scalar("name"), quotedScalar(name),
					scalar("namespace"), quotedScalar(namespace),
				),
				scalar("type"), scalar(sealedSecretTypeOpaque),
			),
		),
	)

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("encode sealed secret yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close sealed secret yaml encoder: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("...\n")), nil
}

func scopeAnnotations(scope string) []*yaml.Node {
	switch scope {
	case SealedSecretScopeNamespaceWide:
		return []*yaml.Node{scalar(namespaceWideAnnotation), quotedScalar("true")}
	case SealedSecretScopeClusterWide:
		return []*yaml.Node{scalar(clusterWideAnnotation), quotedScalar("true")}
	default:
		return nil
	}
}

func sealedSecretPath(req SealRequest) string {
	return filepath.ToSlash(filepath.Join(
		"configs", "orgs", req.Org, "projects", req.Project, "services", req.Service,
		"sealed-secrets", req.Namespace, req.Name+".yaml",
	))
}

func normalizeSealScope(scope string) (string, error) {
	switch strings.TrimSpace(scope) {
	case "", SealedSecretScopeStrict:
		return SealedSecretScopeStrict, nil
	case SealedSecretScopeNamespaceWide:
		return SealedSecretScopeNamespaceWide, nil
	case SealedSecretScopeClusterWide:
		return SealedSecretScopeClusterWide, nil
	default:
		return "", fmt.Errorf("sealed secret scope must be one of %q, %q, or %q, got %q",
			SealedSecretScopeStrict, SealedSecretScopeNamespaceWide, SealedSecretScopeClusterWide, scope)
	}
}

func mapping(nodes ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Content: nodes}
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

func quotedScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value, Style: yaml.DoubleQuotedStyle}
}
