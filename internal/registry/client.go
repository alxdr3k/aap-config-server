package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxConsoleRegistryResponseBytes int64 = 4 << 20

// Loader loads the full Console App Registry.
type Loader interface {
	LoadApps(ctx context.Context) ([]App, error)
}

// ClientOptions configures a Console API client.
type ClientOptions struct {
	BaseURL    string
	HTTPClient *http.Client
}

// ConsoleClient reads App Registry snapshots from AAP Console.
type ConsoleClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// NewConsoleClient builds a Console App Registry client.
func NewConsoleClient(opts ClientOptions) (*ConsoleClient, error) {
	baseURL, err := parseBaseURL(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ConsoleClient{baseURL: baseURL, httpClient: httpClient}, nil
}

// LoadApps fetches all apps from GET /api/v1/apps?all=true.
func (c *ConsoleClient) LoadApps(ctx context.Context) ([]App, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/apps"
	endpoint.RawQuery = "all=true"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build console registry request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("load console app registry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("load console app registry: unexpected status %d", resp.StatusCode)
	}
	apps, err := decodeApps(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode console app registry: %w", err)
	}
	return normalizeApps(apps)
}

func parseBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("CONSOLE_API_URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse CONSOLE_API_URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("CONSOLE_API_URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("CONSOLE_API_URL must include a host")
	}
	return parsed, nil
}

func decodeApps(r io.Reader) ([]App, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxConsoleRegistryResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxConsoleRegistryResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxConsoleRegistryResponseBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	var apps []App
	if err := json.Unmarshal(raw, &apps); err == nil {
		return apps, nil
	}

	var envelope struct {
		Apps []App `json:"apps"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.Apps == nil {
		return nil, fmt.Errorf("response must be an app array or an object with apps")
	}
	return envelope.Apps, nil
}
