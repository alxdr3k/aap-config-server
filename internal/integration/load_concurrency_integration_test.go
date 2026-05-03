//go:build integration

// Tests in this file must NOT call t.Parallel: metrics.ResetForTest mutates a
// package-level registry and sequential execution is required for isolation.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aap/config-server/internal/gitops"
	"github.com/aap/config-server/internal/handler"
	"github.com/aap/config-server/internal/metrics"
	"github.com/aap/config-server/internal/store"
)

// doJSONGoroutineSafe is like doJSON but safe to call from spawned goroutines:
// it returns (statusCode, transportError) instead of calling t.Fatalf.
func doJSONGoroutineSafe(srv *httptest.Server, method, path string, body any) (int, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return 0, fmt.Errorf("encode body: %w", err)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", "test-api-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// newSrv is a convenience helper: builds a Store from repo, wires a Handler,
// and returns a running httptest.Server registered for cleanup.
func newSrv(t *testing.T, repo gitops.GitRepo, opts ...handler.Option) (*store.Store, *httptest.Server) {
	t.Helper()
	st := store.New(repo)
	if err := st.LoadFromRepo(context.Background()); err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	mux := http.NewServeMux()
	h := handler.New(st, fakeReadiness{}, "test-api-key", opts...)
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, srv
}

// ─── concurrent admin write profiles ─────────────────────────────────────────

// TestLoad_ConcurrentAdminConfigWrites fires N concurrent config-write
// requests and verifies all complete with HTTP 200. ADR-005's global store
// mutex serialises the writes; this exercises that boundary under load.
func TestLoad_ConcurrentAdminConfigWrites(t *testing.T) {
	metrics.ResetForTest()

	const (
		org, project, svc = "loadorg", "loadproj", "concursvc"
		workers           = 8
	)
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	_, srv := newSrv(t, repo)

	type result struct {
		id     int
		status int
		err    error
	}
	results := make(chan result, workers)

	for i := range workers {
		go func(i int) {
			body := map[string]any{
				"org":     org,
				"project": project,
				"service": svc,
				"config":  map[string]any{"worker_id": i, "ts": time.Now().UnixNano()},
				"message": fmt.Sprintf("concurrent write %d", i),
			}
			code, err := doJSONGoroutineSafe(srv, http.MethodPost, "/api/v1/admin/changes", body)
			results <- result{id: i, status: code, err: err}
		}(i)
	}

	for range workers {
		r := <-results
		if r.err != nil {
			t.Errorf("worker %d: transport error: %v", r.id, r.err)
			continue
		}
		if r.status != http.StatusOK {
			t.Errorf("worker %d: got status %d, want 200", r.id, r.status)
		}
	}

	// Final read must return valid config.
	resp := doJSON(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/config", org, project, svc),
		nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final GET config: got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode final config: %v", err)
	}
	if _, ok := body["config"]; !ok {
		t.Errorf("config key missing in final response: %v", body)
	}
}

// TestLoad_ConcurrentAdminEnvVarsWrites fires N concurrent env-var-write
// requests and verifies all complete with HTTP 200 and the final state is
// consistent and readable.
func TestLoad_ConcurrentAdminEnvVarsWrites(t *testing.T) {
	metrics.ResetForTest()

	const (
		org, project, svc = "loadorg", "loadproj", "envcursvc"
		workers           = 8
	)
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	_, srv := newSrv(t, repo)

	type result struct {
		id     int
		status int
		err    error
	}
	results := make(chan result, workers)

	for i := range workers {
		go func(i int) {
			body := map[string]any{
				"org":     org,
				"project": project,
				"service": svc,
				"env_vars": map[string]any{
					"plain": map[string]string{
						"WORKER_ID": fmt.Sprintf("%d", i),
						"TS":        fmt.Sprintf("%d", time.Now().UnixNano()),
					},
				},
				"message": fmt.Sprintf("concurrent env_vars write %d", i),
			}
			code, err := doJSONGoroutineSafe(srv, http.MethodPost, "/api/v1/admin/changes", body)
			results <- result{id: i, status: code, err: err}
		}(i)
	}

	for range workers {
		r := <-results
		if r.err != nil {
			t.Errorf("worker %d: transport error: %v", r.id, r.err)
			continue
		}
		if r.status != http.StatusOK {
			t.Errorf("worker %d: got status %d, want 200", r.id, r.status)
		}
	}

	resp := doJSON(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/env_vars", org, project, svc),
		nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final GET env_vars: got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode final env_vars: %v", err)
	}
	if _, ok := body["env_vars"]; !ok {
		t.Errorf("env_vars key missing in final response: %v", body)
	}
}

// ─── concurrent watch profile ─────────────────────────────────────────────────

// TestLoad_ConcurrentWatchWaitsUnblockOnWrite starts N long-poll watch
// goroutines against the same service/config version, then triggers a single
// admin write. All watchers must return HTTP 200 (changed) rather than 304
// (timeout).
func TestLoad_ConcurrentWatchWaitsUnblockOnWrite(t *testing.T) {
	metrics.ResetForTest()
	ctx := context.Background()

	const (
		org, project, svc = "watchorg", "watchproj", "watchsvc"
		watchers          = 6
	)
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	st, srv := newSrv(t, repo)

	// Capture the current config resource version so watchers block on it.
	configVersion, _, err := st.ResourceVersion(ctx, org, project, svc, "config")
	if err != nil {
		t.Fatalf("ResourceVersion: %v", err)
	}

	watchPath := fmt.Sprintf(
		"/api/v1/orgs/%s/projects/%s/services/%s/config/watch?version=%s&timeout=10s",
		org, project, svc, configVersion,
	)

	type watchResult struct {
		id     int
		status int
		err    error
	}
	results := make(chan watchResult, watchers)

	var started sync.WaitGroup
	started.Add(watchers)

	for i := range watchers {
		go func(i int) {
			started.Done() // signal: goroutine is running (request imminent)
			code, err := doJSONGoroutineSafe(srv, http.MethodGet, watchPath, nil)
			results <- watchResult{id: i, status: code, err: err}
		}(i)
	}

	// Wait for all goroutines to be scheduled, then give them time to
	// establish TCP connections and reach the server's wait loop before
	// triggering the write.
	started.Wait()
	time.Sleep(150 * time.Millisecond)

	// Trigger the write that will advance the store version.
	writeBody := map[string]any{
		"org":     org,
		"project": project,
		"service": svc,
		"config":  map[string]any{"app_port": 5555, "trigger": "watch-unblock"},
		"message": "watch-unblock test write",
	}
	resp := doJSON(t, srv, http.MethodPost, "/api/v1/admin/changes", writeBody)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trigger write: got status %d, want 200", resp.StatusCode)
	}

	// Collect watcher outcomes within a generous deadline.
	deadline := time.After(15 * time.Second)
	for range watchers {
		select {
		case r := <-results:
			if r.err != nil {
				t.Errorf("watcher %d: transport error: %v", r.id, r.err)
			} else if r.status != http.StatusOK {
				t.Errorf("watcher %d: got status %d, want 200 (version changed)", r.id, r.status)
			}
		case <-deadline:
			t.Fatal("timed out collecting watcher results")
		}
	}
}

// ─── concurrent Config Agent polling profile ──────────────────────────────────

// TestLoad_ConcurrentConfigAgentPolling simulates N Config Agent goroutines
// concurrently polling the config read endpoint. Reads are served from the
// in-memory snapshot with no serialisation, so this exercises the read-path
// concurrency floor.
func TestLoad_ConcurrentConfigAgentPolling(t *testing.T) {
	metrics.ResetForTest()

	const (
		org, project, svc = "agentorg", "agentproj", "agentsvc"
		agents            = 16
		pollsPerAgent     = 5
	)
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	_, srv := newSrv(t, repo)

	configPath := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/config", org, project, svc)

	type pollError struct {
		agent, poll, code int
	}
	errCh := make(chan pollError, agents*pollsPerAgent)

	var wg sync.WaitGroup
	for a := range agents {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			for p := range pollsPerAgent {
				code, err := doJSONGoroutineSafe(srv, http.MethodGet, configPath, nil)
				if err != nil {
					errCh <- pollError{agent: a, poll: p, code: -1}
					continue
				}
				if code != http.StatusOK {
					errCh <- pollError{agent: a, poll: p, code: code}
				}
			}
		}(a)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		if e.code == -1 {
			t.Errorf("agent %d poll %d: transport error", e.agent, e.poll)
		} else {
			t.Errorf("agent %d poll %d: got status %d, want 200", e.agent, e.poll, e.code)
		}
	}
}

// ─── concurrent env-var polling profile ──────────────────────────────────────

// TestLoad_ConcurrentEnvVarsPolling simulates N agents concurrently polling
// the env-vars read endpoint.
func TestLoad_ConcurrentEnvVarsPolling(t *testing.T) {
	metrics.ResetForTest()

	const (
		org, project, svc = "agentorg", "agentproj", "envagsvc"
		agents            = 16
		pollsPerAgent     = 5
	)
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	_, srv := newSrv(t, repo)

	evPath := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/env_vars", org, project, svc)

	type pollError struct {
		agent, poll, code int
	}
	errCh := make(chan pollError, agents*pollsPerAgent)

	var wg sync.WaitGroup
	for a := range agents {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			for p := range pollsPerAgent {
				code, err := doJSONGoroutineSafe(srv, http.MethodGet, evPath, nil)
				if err != nil {
					errCh <- pollError{agent: a, poll: p, code: -1}
					continue
				}
				if code != http.StatusOK {
					errCh <- pollError{agent: a, poll: p, code: code}
				}
			}
		}(a)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		if e.code == -1 {
			t.Errorf("agent %d poll %d: transport error", e.agent, e.poll)
		} else {
			t.Errorf("agent %d poll %d: got status %d, want 200", e.agent, e.poll, e.code)
		}
	}
}

// ─── mixed read/write profile ─────────────────────────────────────────────────

// TestLoad_ConcurrentMixedWritesAndReads fires concurrent admin writes and
// concurrent reads simultaneously. Writes serialise through the store mutex
// (ADR-005); reads are served from the snapshot concurrently. The test verifies
// that all operations complete without errors under their interleaved execution.
func TestLoad_ConcurrentMixedWritesAndReads(t *testing.T) {
	metrics.ResetForTest()

	const (
		org, project, svc = "mixorg", "mixproj", "mixsvc"
		writers           = 4
		readers           = 12
	)
	_, repo := newLocalGitRepo(t, seedFiles(org, project, svc))
	_, srv := newSrv(t, repo)

	configPath := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/services/%s/config", org, project, svc)

	type result struct {
		kind   string
		id     int
		status int
		err    error
	}
	results := make(chan result, writers+readers)

	for i := range writers {
		go func(i int) {
			body := map[string]any{
				"org":     org,
				"project": project,
				"service": svc,
				"config":  map[string]any{"writer": i, "ts": time.Now().UnixNano()},
				"message": fmt.Sprintf("mixed write %d", i),
			}
			code, err := doJSONGoroutineSafe(srv, http.MethodPost, "/api/v1/admin/changes", body)
			results <- result{kind: "write", id: i, status: code, err: err}
		}(i)
	}

	for i := range readers {
		go func(i int) {
			code, err := doJSONGoroutineSafe(srv, http.MethodGet, configPath, nil)
			results <- result{kind: "read", id: i, status: code, err: err}
		}(i)
	}

	for range writers + readers {
		r := <-results
		if r.err != nil {
			t.Errorf("%s %d: transport error: %v", r.kind, r.id, r.err)
			continue
		}
		if r.status != http.StatusOK {
			t.Errorf("%s %d: got status %d, want 200", r.kind, r.id, r.status)
		}
	}
}
