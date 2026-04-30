package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientFetchesConfigAndEnvVars(t *testing.T) {
	var sawConfig bool
	var sawEnv bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("Accept header: %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("Authorization") != "Bearer agent-key" {
			t.Fatalf("Authorization header: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v1/orgs/org/projects/project/services/service/config":
			sawConfig = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"metadata": {"org":"org","project":"project","service":"service","version":"abc123","updated_at":"2026-04-30T01:02:03Z"},
				"config": {"model_list": []}
			}`))
		case "/api/v1/orgs/org/projects/project/services/service/env_vars":
			sawEnv = true
			if got := r.URL.Query().Get("resolve_secrets"); got != "true" {
				t.Fatalf("resolve_secrets query: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"metadata": {"org":"org","project":"project","service":"service","version":"abc123","updated_at":"2026-04-30T01:02:03Z"},
				"env_vars": {"plain":{"LOG_LEVEL":"INFO"},"secrets":{"API_KEY":"secret"}}
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
	}))
	defer srv.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:    srv.URL,
		APIKey:     "agent-key",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ref := ServiceRef{Org: "org", Project: "project", Service: "service"}
	configSnapshot, err := client.FetchConfig(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchConfig: %v", err)
	}
	if configSnapshot.Metadata.Version != "abc123" || len(configSnapshot.Config) != 1 {
		t.Fatalf("config snapshot: %+v", configSnapshot)
	}
	envSnapshot, err := client.FetchEnvVars(context.Background(), ref, true)
	if err != nil {
		t.Fatalf("FetchEnvVars: %v", err)
	}
	if envSnapshot.EnvVars.Plain["LOG_LEVEL"] != "INFO" || envSnapshot.EnvVars.Secrets["API_KEY"] != "secret" {
		t.Fatalf("env snapshot: %+v", envSnapshot)
	}
	if !sawConfig || !sawEnv {
		t.Fatalf("expected both endpoints to be called")
	}
}

func TestClientEscapesServicePathSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/prefix/api/v1/orgs/org%20a/projects/proj%2Fb/services/svc%20c/config" {
			t.Fatalf("escaped path: %s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"metadata":{},"config":{}}`))
	}))
	defer srv.Close()

	client, err := NewClient(ClientOptions{BaseURL: srv.URL + "/prefix/", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.FetchConfig(context.Background(), ServiceRef{Org: "org a", Project: "proj/b", Service: "svc c"})
	if err != nil {
		t.Fatalf("FetchConfig: %v", err)
	}
}

func TestClientReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	client, err := NewClient(ClientOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.FetchConfig(context.Background(), ServiceRef{Org: "org", Project: "project", Service: "service"})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected StatusError, got %T %v", err, err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status code: %d", statusErr.StatusCode)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{},"config":"` + strings.Repeat("x", 64) + `"}`))
	}))
	defer srv.Close()

	client, err := NewClient(ClientOptions{BaseURL: srv.URL, HTTPClient: srv.Client(), MaxResponseBytes: 16})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.FetchConfig(context.Background(), ServiceRef{Org: "org", Project: "project", Service: "service"})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}
