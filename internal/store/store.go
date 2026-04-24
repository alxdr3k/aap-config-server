package store

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aap/config-server/internal/apperror"
	"github.com/aap/config-server/internal/gitops"
	"github.com/aap/config-server/internal/parser"
)

var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validateName(field, value string) error {
	if !validNameRe.MatchString(value) {
		return apperror.New(apperror.CodeValidation,
			fmt.Sprintf("%s %q contains invalid characters", field, value))
	}
	return nil
}

// reloadState records the outcome of the most recent reload attempt.
type reloadState struct {
	at  time.Time
	err error // non-nil when the last reload failed
}

// Store is the in-memory config store.
// Reads are served from an atomically swapped snapshot (COW pattern), providing
// lock-free reads during background refreshes.
type Store struct {
	// snapshot is the current read-only view; updated via atomic pointer swap.
	snapshot atomic.Pointer[snapshot]

	// lastReload records the outcome of the most recent reload attempt.
	lastReload atomic.Pointer[reloadState]

	// mu serialises writes (ApplyChanges / DeleteChanges / background refresh).
	mu sync.Mutex

	repo gitops.GitRepo
}

// snapshot is an immutable view of all service data at a given git version.
type snapshot struct {
	data    map[string]*ServiceData // key: ServiceKey.String()
	version string                  // git HEAD commit hash
}

func newSnapshot(data map[string]*ServiceData, version string) *snapshot {
	return &snapshot{data: data, version: version}
}

// New creates a Store backed by the given GitRepo.
func New(repo gitops.GitRepo) *Store {
	s := &Store{repo: repo}
	s.snapshot.Store(newSnapshot(make(map[string]*ServiceData), ""))
	return s
}

func (s *Store) current() *snapshot {
	return s.snapshot.Load()
}

// LoadFromRepo performs the initial clone/open and full config load.
//
// After CloneOrOpen — which only touches the network for a fresh clone — we
// run one Pull so that an already-present local clone (dev box, persistent
// volume) is brought up to the remote HEAD before we build the first
// snapshot. A pull failure here is not fatal: we log and fall back to the
// on-disk checkout so a transient network blip doesn't block startup; the
// background poll and /readyz degraded state will surface the drift.
func (s *Store) LoadFromRepo(ctx context.Context) error {
	if err := s.repo.CloneOrOpen(ctx); err != nil {
		return fmt.Errorf("clone/open repo: %w", err)
	}
	if _, _, err := s.repo.Pull(ctx); err != nil {
		slog.Warn("initial pull failed; serving on-disk checkout until background poll recovers",
			"err", err)
	}
	return s.reload(ctx)
}

// RefreshFromRepo pulls remote changes and reloads if the HEAD moved.
// Returns updated=true only when the in-memory snapshot was actually swapped;
// if HEAD moved on the remote but reload failed (e.g. malformed YAML), the
// last-known-good snapshot stays in place and updated=false is returned with
// the reload error.
//
// This is the background-poll path. Operators calling POST /admin/reload must
// use ReloadFromRepo instead: a degraded store whose HEAD has not moved needs
// to re-parse the current checkout to recover, which RefreshFromRepo would
// silently skip.
func (s *Store) RefreshFromRepo(ctx context.Context) (bool, error) {
	hash, updated, err := s.repo.Pull(ctx)
	if err != nil {
		return false, err
	}
	if !updated {
		slog.Debug("git pull: already up to date", "hash", hash)
		return false, nil
	}
	slog.Info("git pull: detected changes", "hash", hash)
	if err := s.reload(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// ReloadFromRepo pulls remote changes and unconditionally re-parses the
// current checkout into a fresh snapshot. Unlike RefreshFromRepo this is a
// force reload: it runs even when git HEAD did not move, so a degraded store
// recovers once the offending YAML on the current HEAD has been fixed (either
// by a no-op reload of the same commit, or by amending the file in place for
// a local dev clone).
//
// Returns updated=true when the serving snapshot was swapped (i.e. the reload
// produced a new HEAD or the last reload had failed and now succeeds).
func (s *Store) ReloadFromRepo(ctx context.Context) (bool, error) {
	hash, pullUpdated, err := s.repo.Pull(ctx)
	if err != nil {
		return false, err
	}
	if pullUpdated {
		slog.Info("git pull: detected changes", "hash", hash)
	} else {
		slog.Debug("git pull: already up to date; force reloading anyway", "hash", hash)
	}

	prevVersion := s.HeadVersion()
	wasDegraded := s.IsDegraded()

	if err := s.reload(ctx); err != nil {
		return false, err
	}

	// Report updated=true when the serving snapshot meaningfully changed —
	// either HEAD moved, or we just recovered from a degraded state.
	swapped := pullUpdated || s.HeadVersion() != prevVersion || wasDegraded
	return swapped, nil
}

// HeadVersion returns the git commit hash of the currently loaded snapshot.
func (s *Store) HeadVersion() string {
	return s.current().version
}

// IsDegraded reports whether the most recent reload attempt failed. When true
// the server is serving the last-known-good snapshot.
func (s *Store) IsDegraded() bool {
	rs := s.lastReload.Load()
	return rs != nil && rs.err != nil
}

// StatusInfo returns a point-in-time operational snapshot of the store.
func (s *Store) StatusInfo() StoreStatus {
	snap := s.current()
	si := StoreStatus{
		Version:        snap.version,
		ServicesLoaded: len(snap.data),
	}
	if rs := s.lastReload.Load(); rs != nil {
		si.LastReloadAt = rs.at
		if rs.err != nil {
			si.IsDegraded = true
			si.LastReloadError = rs.err.Error()
		}
	}
	return si
}

// GetConfig returns the parsed config for a service.
func (s *Store) GetConfig(ctx context.Context, org, project, service string) (*ServiceData, error) {
	snap := s.current()
	key := ServiceKey{Org: org, Project: project, Service: service}.String()
	d, ok := snap.data[key]
	if !ok {
		return nil, apperror.New(apperror.CodeNotFound,
			fmt.Sprintf("service not found: %s/%s/%s", org, project, service))
	}
	return d, nil
}

// ListOrgs returns all known org names.
func (s *Store) ListOrgs() []string {
	snap := s.current()
	seen := map[string]struct{}{}
	for key := range snap.data {
		parts := strings.SplitN(key, "/", 3)
		if len(parts) >= 1 {
			seen[parts[0]] = struct{}{}
		}
	}
	return keys(seen)
}

// ListProjects returns all project names within an org.
func (s *Store) ListProjects(org string) []string {
	snap := s.current()
	seen := map[string]struct{}{}
	prefix := org + "/"
	for key := range snap.data {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := key[len(prefix):]
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) >= 1 {
			seen[parts[0]] = struct{}{}
		}
	}
	return keys(seen)
}

// ListServices returns all services within a project.
func (s *Store) ListServices(org, project string) []ServiceInfo {
	snap := s.current()
	prefix := org + "/" + project + "/"
	var result []ServiceInfo
	for key, d := range snap.data {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		svcName := key[len(prefix):]
		result = append(result, ServiceInfo{
			Name:       svcName,
			HasConfig:  d.Config != nil,
			HasEnvVars: d.EnvVars != nil,
			HasSecrets: d.Secrets != nil,
			UpdatedAt:  d.UpdatedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// ApplyChanges writes config/env_vars updates, commits, pushes, and refreshes memory.
func (s *Store) ApplyChanges(ctx context.Context, req *ChangeRequest) (*ChangeResult, error) {
	if req.Org == "" || req.Project == "" || req.Service == "" {
		return nil, apperror.New(apperror.CodeValidation, "org, project and service are required")
	}
	if err := validateName("org", req.Org); err != nil {
		return nil, err
	}
	if err := validateName("project", req.Project); err != nil {
		return nil, err
	}
	if err := validateName("service", req.Service); err != nil {
		return nil, err
	}
	if req.Config == nil && req.EnvVars == nil {
		return nil, apperror.New(apperror.CodeValidation, "at least one of config or env_vars must be provided")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	svcPath := ServicePath(req.Org, req.Project, req.Service)
	now := time.Now().UTC()

	files := map[string][]byte{}
	var writtenFiles []string

	if req.Config != nil {
		cfg := &parser.ServiceConfig{
			Version: "1",
			Metadata: parser.ServiceMetadata{
				Service:   req.Service,
				Org:       req.Org,
				Project:   req.Project,
				UpdatedAt: now.Format(time.RFC3339),
			},
			Config: req.Config,
		}
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, apperror.Wrap(apperror.CodeInternal, "marshal config.yaml", err)
		}
		path := filepath.Join(svcPath, "config.yaml")
		files[path] = data
		writtenFiles = append(writtenFiles, "config.yaml")
	}

	if req.EnvVars != nil {
		ev := &parser.EnvVarsConfig{
			Version: "1",
			Metadata: parser.ServiceMetadata{
				Service:   req.Service,
				Org:       req.Org,
				Project:   req.Project,
				UpdatedAt: now.Format(time.RFC3339),
			},
			EnvVars: *req.EnvVars,
		}
		data, err := yaml.Marshal(ev)
		if err != nil {
			return nil, apperror.Wrap(apperror.CodeInternal, "marshal env_vars.yaml", err)
		}
		path := filepath.Join(svcPath, "env_vars.yaml")
		files[path] = data
		writtenFiles = append(writtenFiles, "env_vars.yaml")
	}

	msg := req.Message
	if msg == "" {
		msg = fmt.Sprintf("update config for %s/%s/%s", req.Org, req.Project, req.Service)
	}

	hash, err := s.repo.CommitAndPush(ctx, msg, files)
	if err != nil {
		return nil, err
	}

	result := &ChangeResult{
		Version:   hash,
		UpdatedAt: now,
		Files:     writtenFiles,
	}

	// Reload in-memory snapshot using COW. If this fails the git write has
	// already happened, so we can't pretend it didn't; instead we keep the
	// previous (last-known-good) snapshot and tell the caller about it.
	if err := s.reloadUnlocked(ctx); err != nil {
		slog.Error("reload after commit failed; serving stale snapshot until next successful reload", "err", err)
		result.ReloadFailed = true
		result.ReloadError = err.Error()
	}

	return result, nil
}

// DeleteChanges removes a service's config files, commits, pushes, and refreshes memory.
func (s *Store) DeleteChanges(ctx context.Context, req *DeleteRequest) (*DeleteResult, error) {
	if req.Org == "" || req.Project == "" || req.Service == "" {
		return nil, apperror.New(apperror.CodeValidation, "org, project and service are required")
	}
	if err := validateName("org", req.Org); err != nil {
		return nil, err
	}
	if err := validateName("project", req.Project); err != nil {
		return nil, err
	}
	if err := validateName("service", req.Service); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	svcPath := ServicePath(req.Org, req.Project, req.Service)
	paths := []string{
		filepath.Join(svcPath, "config.yaml"),
		filepath.Join(svcPath, "env_vars.yaml"),
		filepath.Join(svcPath, "secrets.yaml"),
	}

	msg := fmt.Sprintf("delete config for %s/%s/%s", req.Org, req.Project, req.Service)
	hash, err := s.repo.DeleteAndPush(ctx, msg, paths)
	if err != nil {
		return nil, err
	}

	result := &DeleteResult{
		Version:      hash,
		UpdatedAt:    time.Now().UTC(),
		DeletedFiles: []string{"config.yaml", "env_vars.yaml", "secrets.yaml"},
	}

	// Reload in-memory snapshot from the new HEAD so any concurrent remote
	// changes that were pulled in during DeleteAndPush's retry loop are also
	// reflected. If reload fails, keep the last-known-good snapshot and report
	// the failure so operators can react.
	if err := s.reloadUnlocked(ctx); err != nil {
		slog.Error("reload after delete failed; serving stale snapshot until next successful reload", "err", err)
		result.ReloadFailed = true
		result.ReloadError = err.Error()
	}

	return result, nil
}

// reload reads all configs from the repository into a new snapshot (called with mu held).
func (s *Store) reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadUnlocked(ctx)
}

// reloadUnlocked is the same as reload but assumes mu is already held.
// Reload is fail-closed: if any config file cannot be parsed the snapshot is
// NOT swapped and the previous last-known-good view keeps serving.
func (s *Store) reloadUnlocked(_ context.Context) error {
	data := make(map[string]*ServiceData)
	var parseErrors []string

	hash, err := s.repo.Snapshot(func(path string, raw []byte) error {
		if perr := parseAndStore(path, raw, data); perr != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", path, perr))
		}
		return nil
	})
	if err != nil {
		reloadErr := fmt.Errorf("repo snapshot: %w", err)
		s.lastReload.Store(&reloadState{at: time.Now(), err: reloadErr})
		return reloadErr
	}
	if len(parseErrors) > 0 {
		reloadErr := fmt.Errorf("refusing to swap snapshot: %d file(s) failed to parse: %s",
			len(parseErrors), strings.Join(parseErrors, "; "))
		s.lastReload.Store(&reloadState{at: time.Now(), err: reloadErr})
		return reloadErr
	}

	// Fallback: services whose YAML carried no parseable metadata.updated_at
	// get the reload time so listings still show *some* timestamp.
	now := time.Now()
	for _, sd := range data {
		if sd.UpdatedAt.IsZero() {
			sd.UpdatedAt = now
		}
	}

	s.snapshot.Store(newSnapshot(data, hash))
	s.lastReload.Store(&reloadState{at: time.Now(), err: nil})
	slog.Info("config store reloaded", "services", len(data), "version", hash[:min(8, len(hash))])
	return nil
}

// parseAndStore determines the file type and updates the data map accordingly.
//
// UpdatedAt precedence: the latest parseable metadata.updated_at across the
// service's YAML files wins. If neither config.yaml nor env_vars.yaml carries
// a usable timestamp, reloadUnlocked falls back to reload wall-clock time.
func parseAndStore(path string, raw []byte, data map[string]*ServiceData) error {
	// Only handle files inside orgs/…/services/…
	key, fileType, ok := classifyPath(path)
	if !ok {
		return nil
	}

	sd := data[key]
	if sd == nil {
		sd = &ServiceData{}
		data[key] = sd
	}

	switch fileType {
	case "config":
		cfg, err := parser.ParseConfig(raw)
		if err != nil {
			return fmt.Errorf("parse config.yaml: %w", err)
		}
		sd.Config = cfg
		applyMetadataUpdatedAt(sd, cfg.Metadata.UpdatedAt)
	case "env_vars":
		ev, err := parser.ParseEnvVars(raw)
		if err != nil {
			return fmt.Errorf("parse env_vars.yaml: %w", err)
		}
		sd.EnvVars = ev
		applyMetadataUpdatedAt(sd, ev.Metadata.UpdatedAt)
	case "secrets":
		sec, err := parser.ParseSecrets(raw)
		if err != nil {
			return fmt.Errorf("parse secrets.yaml: %w", err)
		}
		sd.Secrets = sec
	}

	return nil
}

// applyMetadataUpdatedAt adopts iso as sd.UpdatedAt if it parses and is more
// recent than what's already there (zero-time is treated as "unset").
func applyMetadataUpdatedAt(sd *ServiceData, iso string) {
	if iso == "" {
		return
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return
	}
	if sd.UpdatedAt.IsZero() || t.After(sd.UpdatedAt) {
		sd.UpdatedAt = t
	}
}

// classifyPath maps a repo-relative file path to a service key and file type.
// paths must match: configs/orgs/{org}/projects/{proj}/services/{svc}/{type}.yaml
func classifyPath(path string) (key string, fileType string, ok bool) {
	// Normalise separators
	path = filepath.ToSlash(path)

	// Expected prefix: configs/orgs/{org}/projects/{proj}/services/{svc}/
	const prefix = "configs/orgs/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := path[len(prefix):]

	// orgs/{org}/projects/{proj}/services/{svc}/{file}
	parts := strings.Split(rest, "/")
	// parts: [org, "projects", proj, "services", svc, file]
	if len(parts) < 6 || parts[1] != "projects" || parts[3] != "services" {
		return "", "", false
	}
	org, proj, svc, file := parts[0], parts[2], parts[4], parts[5]

	// Skip sealed-secrets subdirectory and _defaults
	if file == "sealed-secrets" || strings.HasPrefix(file, "_") {
		return "", "", false
	}

	switch file {
	case "config.yaml":
		fileType = "config"
	case "env_vars.yaml":
		fileType = "env_vars"
	case "secrets.yaml":
		fileType = "secrets"
	default:
		return "", "", false
	}

	return ServiceKey{Org: org, Project: proj, Service: svc}.String(), fileType, true
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

