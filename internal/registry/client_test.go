package registry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aap/config-server/internal/registry"
)

func TestConsoleClient_LoadApps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/console/api/v1/apps" {
			t.Fatalf("path: got %q", r.URL.Path)
		}
		if r.URL.Query().Get("all") != "true" {
			t.Fatalf("all query: got %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept: got %q", got)
		}
		_, _ = w.Write([]byte(`{"apps":[{"org":"myorg","project":"ai","name":"litellm"}]}`))
	}))
	defer srv.Close()

	client, err := registry.NewConsoleClient(registry.ClientOptions{
		BaseURL:    srv.URL + "/console",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	apps, err := client.LoadApps(context.Background())
	if err != nil {
		t.Fatalf("LoadApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps: got %d", len(apps))
	}
	if apps[0].Org != "myorg" || apps[0].Project != "ai" || apps[0].Service != "litellm" {
		t.Fatalf("normalized app: %+v", apps[0])
	}
}

func TestConsoleClient_LoadAppsArrayResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"org":"myorg","project":"ai","service":"litellm"}]`))
	}))
	defer srv.Close()

	client, err := registry.NewConsoleClient(registry.ClientOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	apps, err := client.LoadApps(context.Background())
	if err != nil {
		t.Fatalf("LoadApps: %v", err)
	}
	if apps[0].Name != "litellm" {
		t.Fatalf("expected name fallback from service, got %+v", apps[0])
	}
}

func TestConsoleClient_LoadAppsRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client, err := registry.NewConsoleClient(registry.ClientOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	if _, err := client.LoadApps(context.Background()); err == nil {
		t.Fatal("expected bad status error")
	}
}

func TestConsoleClient_LoadAppsRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", (4<<20)+1)))
	}))
	defer srv.Close()

	client, err := registry.NewConsoleClient(registry.ClientOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	_, err = client.LoadApps(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestConsoleClient_LoadAppsRejectsInvalidUpdatedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"org":"myorg","project":"ai","service":"litellm","updated_at":"not-a-time"}]`))
	}))
	defer srv.Close()

	client, err := registry.NewConsoleClient(registry.ClientOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	_, err = client.LoadApps(context.Background())
	if err == nil || !strings.Contains(err.Error(), "updated_at") {
		t.Fatalf("expected updated_at error, got %v", err)
	}
}

func TestNewConsoleClientRejectsInvalidBaseURL(t *testing.T) {
	for _, raw := range []string{"", "://bad", "ftp://console.example", "http:///missing-host"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := registry.NewConsoleClient(registry.ClientOptions{BaseURL: raw}); err == nil {
				t.Fatal("expected invalid base URL error")
			}
		})
	}
}
