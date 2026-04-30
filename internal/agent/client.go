package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultMaxResponseBytes int64 = 4 << 20

// ServiceRef identifies the service the agent is responsible for.
type ServiceRef struct {
	Org     string
	Project string
	Service string
}

// Metadata is the common Config Server read response metadata.
type Metadata struct {
	Org       string    `json:"org"`
	Project   string    `json:"project"`
	Service   string    `json:"service"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConfigSnapshot is the Config Server config read response.
type ConfigSnapshot struct {
	Metadata Metadata       `json:"metadata"`
	Config   map[string]any `json:"config"`
}

// EnvVarsSnapshot is the Config Server env_vars read response.
type EnvVarsSnapshot struct {
	Metadata Metadata `json:"metadata"`
	EnvVars  EnvVars  `json:"env_vars"`
}

// EnvVars contains unresolved secret refs or resolved secret values, depending
// on whether resolve_secrets=true was used.
type EnvVars struct {
	Plain      map[string]string `json:"plain"`
	SecretRefs map[string]string `json:"secret_refs,omitempty"`
	Secrets    map[string]string `json:"secrets,omitempty"`
}

// Client fetches per-service config snapshots from Config Server.
type Client struct {
	baseURL          *url.URL
	apiKey           string
	httpClient       *http.Client
	maxResponseBytes int64
}

// ClientOptions configures a Config Server API client.
type ClientOptions struct {
	BaseURL          string
	APIKey           string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

// StatusError reports a non-2xx response from Config Server.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("config server returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("config server returned HTTP %d: %s", e.StatusCode, e.Body)
}

// NewClient validates options and returns a Config Server API client.
func NewClient(opts ClientOptions) (*Client, error) {
	rawURL := strings.TrimSpace(opts.BaseURL)
	if rawURL == "" {
		return nil, errors.New("config server base URL is required")
	}
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("config server base URL is invalid: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, errors.New("config server base URL must use http or https")
	}
	if baseURL.Host == "" {
		return nil, errors.New("config server base URL must include a host")
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	maxResponseBytes := opts.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}

	return &Client{
		baseURL:          baseURL,
		apiKey:           strings.TrimSpace(opts.APIKey),
		httpClient:       httpClient,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func (c *Client) FetchConfig(ctx context.Context, ref ServiceRef) (*ConfigSnapshot, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	endpoint := c.serviceEndpoint(ref, "config")
	var snapshot ConfigSnapshot
	if err := c.getJSON(ctx, endpoint, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (c *Client) FetchEnvVars(ctx context.Context, ref ServiceRef, resolveSecrets bool) (*EnvVarsSnapshot, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	endpoint := c.serviceEndpoint(ref, "env_vars")
	if resolveSecrets {
		query := endpoint.Query()
		query.Set("resolve_secrets", "true")
		endpoint.RawQuery = query.Encode()
	}
	var snapshot EnvVarsSnapshot
	if err := c.getJSON(ctx, endpoint, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r ServiceRef) Validate() error {
	if strings.TrimSpace(r.Org) == "" {
		return errors.New("org is required")
	}
	if strings.TrimSpace(r.Project) == "" {
		return errors.New("project is required")
	}
	if strings.TrimSpace(r.Service) == "" {
		return errors.New("service is required")
	}
	return nil
}

func (c *Client) serviceEndpoint(ref ServiceRef, suffix string) *url.URL {
	endpoint := *c.baseURL
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	segments := []string{"api", "v1", "orgs", ref.Org, "projects", ref.Project, "services", ref.Service, suffix}
	basePath := strings.TrimRight(endpoint.Path, "/")
	rawBasePath := strings.TrimRight(endpoint.EscapedPath(), "/")

	decodedSegments := make([]string, len(segments))
	escapedSegments := make([]string, len(segments))
	for i, segment := range segments {
		decodedSegments[i] = segment
		escapedSegments[i] = url.PathEscape(segment)
	}

	endpoint.Path = joinURLPath(basePath, decodedSegments)
	endpoint.RawPath = joinURLPath(rawBasePath, escapedSegments)
	return &endpoint
}

func (c *Client) getJSON(ctx context.Context, endpoint *url.URL, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := readLimited(resp.Body, c.maxResponseBytes)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &StatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return fmt.Errorf("decode config server response: %w", err)
	}
	return nil
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(r, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("config server response exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func joinURLPath(basePath string, segments []string) string {
	joined := strings.Join(segments, "/")
	if basePath == "" {
		return "/" + joined
	}
	return basePath + "/" + joined
}
