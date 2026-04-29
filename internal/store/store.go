package store

import (
	"context"
	"errors"
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
	"github.com/aap/config-server/internal/secret"
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

	secretDeps secret.Dependencies
}

// Option customizes Store dependencies.
type Option func(*Store)

// WithSecretDependencies wires secret sealing and apply adapters into admin
// secret writes. Config/env-only writes keep working without these adapters.
func WithSecretDependencies(deps secret.Dependencies) Option {
	return func(s *Store) {
		s.secretDeps = deps.WithDefaults()
	}
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
func New(repo gitops.GitRepo, opts ...Option) *Store {
	s := &Store{repo: repo}
	for _, opt := range opts {
		opt(s)
	}
	s.secretDeps = s.secretDeps.WithDefaults()
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
// snapshot. A transient network pull failure is logged and we fall back to
// the on-disk checkout so a brief outage doesn't block startup; later
// background polls catch up when the remote becomes reachable.
// Context cancellation/deadline errors are propagated so callers can
// actually abort startup when they ask to.
func (s *Store) LoadFromRepo(ctx context.Context) error {
	if err := s.repo.CloneOrOpen(ctx); err != nil {
		return fmt.Errorf("clone/open repo: %w", err)
	}
	if _, _, err := s.repo.Pull(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("initial pull: %w", err)
		}
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
// This is the background-poll path. Operators calling POST /api/v1/admin/reload must
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
	hasSecrets, err := validateSecretWrites(req.Secrets)
	if err != nil {
		return nil, err
	}
	if req.Config == nil && req.EnvVars == nil && !hasSecrets {
		return nil, apperror.New(apperror.CodeValidation, "at least one of config, env_vars, or secrets must be provided")
	}
	if hasSecrets {
		if s.secretDeps.Sealer == nil {
			return nil, apperror.New(apperror.CodeValidation, "secret sealer is not configured")
		}
		if s.secretDeps.Applier == nil {
			return nil, apperror.New(apperror.CodeValidation, "secret applier is not configured")
		}
		defer destroySecretWrites(req.Secrets)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	svcPath := ServicePath(req.Org, req.Project, req.Service)
	now := time.Now().UTC()

	files := map[string][]byte{}
	var writtenFiles []string
	var sealedManifests []secret.SealedManifest

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

	if hasSecrets {
		sec, manifests, err := s.buildSecretArtifacts(ctx, req)
		if err != nil {
			return nil, err
		}
		data, err := yaml.Marshal(sec)
		if err != nil {
			return nil, apperror.Wrap(apperror.CodeInternal, "marshal secrets.yaml", err)
		}
		path := filepath.Join(svcPath, "secrets.yaml")
		files[path] = data
		writtenFiles = append(writtenFiles, "secrets.yaml")

		for _, manifest := range manifests {
			manifestPath := filepath.ToSlash(manifest.Path)
			rel, ok := serviceRelativePath(svcPath, manifestPath)
			if !ok {
				return nil, apperror.New(apperror.CodeInternal,
					fmt.Sprintf("sealed manifest path %q is outside service path", manifest.Path))
			}
			files[filepath.FromSlash(manifestPath)] = manifest.YAML
			writtenFiles = append(writtenFiles, rel)
		}
		sealedManifests = manifests
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

	if len(sealedManifests) > 0 {
		if err := applySealedManifests(ctx, s.secretDeps.Applier, sealedManifests); err != nil {
			slog.Error("apply sealed secrets after commit failed", "err", err)
			result.ApplyFailed = true
			result.ApplyError = err.Error()
		}
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

func (s *Store) buildSecretArtifacts(ctx context.Context, req *ChangeRequest) (*parser.SecretsConfig, []secret.SealedManifest, error) {
	existing := s.current().data[ServiceKey{Org: req.Org, Project: req.Project, Service: req.Service}.String()]
	var existingSecrets *parser.SecretsConfig
	if existing != nil {
		existingSecrets = existing.Secrets
	}

	entries := mergeSecretEntries(existingSecrets, req.Secrets)
	manifests := make([]secret.SealedManifest, 0, len(req.Secrets))
	for _, name := range sortedSecretNames(req.Secrets) {
		write := req.Secrets[name]
		data := make(map[string]secret.Value, len(write.Data))
		for _, key := range sortedSecretDataKeys(write.Data) {
			data[key] = write.Data[key]
		}
		manifest, err := s.secretDeps.Sealer.Seal(ctx, secret.SealRequest{
			Org:       req.Org,
			Project:   req.Project,
			Service:   req.Service,
			Namespace: write.Namespace,
			Name:      name,
			Data:      data,
		})
		if err != nil {
			return nil, nil, apperror.Wrap(apperror.CodeInternal,
				fmt.Sprintf("seal secret %s/%s", write.Namespace, name), err)
		}
		manifests = append(manifests, manifest)
	}

	return &parser.SecretsConfig{
		Version: "1",
		Secrets: entries,
	}, manifests, nil
}

type secretPointer struct {
	namespace string
	name      string
	key       string
}

func mergeSecretEntries(existing *parser.SecretsConfig, writes map[string]SecretWrite) []parser.SecretEntry {
	byPointer := map[secretPointer]parser.SecretEntry{}
	idInUse := map[string]secretPointer{}
	if existing != nil {
		for _, entry := range existing.Secrets {
			ptr := secretPointer{
				namespace: entry.K8sSecret.Namespace,
				name:      entry.K8sSecret.Name,
				key:       entry.K8sSecret.Key,
			}
			byPointer[ptr] = entry
			if entry.ID != "" {
				idInUse[entry.ID] = ptr
			}
		}
	}

	for _, name := range sortedSecretNames(writes) {
		write := writes[name]
		for _, key := range sortedSecretDataKeys(write.Data) {
			ptr := secretPointer{namespace: write.Namespace, name: name, key: key}
			if _, ok := byPointer[ptr]; ok {
				continue
			}
			id := uniqueSecretID(write.Namespace, name, key, idInUse)
			entry := parser.SecretEntry{
				ID: id,
				K8sSecret: parser.K8sSecret{
					Name:      name,
					Namespace: write.Namespace,
					Key:       key,
				},
			}
			byPointer[ptr] = entry
			idInUse[id] = ptr
		}
	}

	entries := make([]parser.SecretEntry, 0, len(byPointer))
	for _, entry := range byPointer {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ID != entries[j].ID {
			return entries[i].ID < entries[j].ID
		}
		if entries[i].K8sSecret.Namespace != entries[j].K8sSecret.Namespace {
			return entries[i].K8sSecret.Namespace < entries[j].K8sSecret.Namespace
		}
		if entries[i].K8sSecret.Name != entries[j].K8sSecret.Name {
			return entries[i].K8sSecret.Name < entries[j].K8sSecret.Name
		}
		return entries[i].K8sSecret.Key < entries[j].K8sSecret.Key
	})
	return entries
}

func uniqueSecretID(namespace, name, key string, idInUse map[string]secretPointer) string {
	candidates := []string{
		key,
		name + "-" + key,
		namespace + "-" + name + "-" + key,
	}
	for _, id := range candidates {
		if _, ok := idInUse[id]; !ok {
			return id
		}
	}
	base := candidates[len(candidates)-1]
	for i := 2; ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if _, ok := idInUse[id]; !ok {
			return id
		}
	}
}

func validateSecretWrites(writes map[string]SecretWrite) (bool, error) {
	if len(writes) == 0 {
		return false, nil
	}
	for name, write := range writes {
		if err := validateName("secret name", name); err != nil {
			return false, err
		}
		if err := validateName("secret namespace", write.Namespace); err != nil {
			return false, err
		}
		if len(write.Data) == 0 {
			return false, apperror.New(apperror.CodeValidation,
				fmt.Sprintf("secret %s/%s data is required", write.Namespace, name))
		}
		for key := range write.Data {
			if err := validateName("secret key", key); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func destroySecretWrites(writes map[string]SecretWrite) {
	for _, write := range writes {
		for key, value := range write.Data {
			value.Destroy()
			write.Data[key] = value
		}
	}
}

func applySealedManifests(ctx context.Context, applier secret.Applier, manifests []secret.SealedManifest) error {
	var errs []string
	for _, manifest := range manifests {
		if err := applier.ApplySealedSecret(ctx, manifest); err != nil {
			errs = append(errs, fmt.Sprintf("apply sealed secret %s/%s: %v", manifest.Namespace, manifest.Name, err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func serviceRelativePath(svcPath, manifestPath string) (string, bool) {
	prefix := filepath.ToSlash(svcPath) + "/"
	if !strings.HasPrefix(manifestPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(manifestPath, prefix), true
}

func sortedSecretNames(writes map[string]SecretWrite) []string {
	names := make([]string, 0, len(writes))
	for name := range writes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedSecretDataKeys(data map[string]secret.Value) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
