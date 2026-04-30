package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
var validK8sDNSLabelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var errStopHistory = errors.New("stop history iteration")

func validateName(field, value string) error {
	if !validNameRe.MatchString(value) {
		return apperror.New(apperror.CodeValidation,
			fmt.Sprintf("%s %q contains invalid characters", field, value))
	}
	return nil
}

func validateK8sDNSLabel(field, value string) error {
	if value == "" || len(value) > 63 || !validK8sDNSLabelRe.MatchString(value) {
		return apperror.New(apperror.CodeValidation,
			fmt.Sprintf("%s %q must be a Kubernetes DNS label", field, value))
	}
	return nil
}

func validateK8sDNSSubdomain(field, value string) error {
	if value == "" || len(value) > 253 {
		return apperror.New(apperror.CodeValidation,
			fmt.Sprintf("%s %q must be a Kubernetes DNS subdomain", field, value))
	}
	for _, part := range strings.Split(value, ".") {
		if err := validateK8sDNSLabel(field, part); err != nil {
			return err
		}
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

	// versionChanged is closed after a successful snapshot version change.
	versionWatchMu sync.Mutex
	versionChanged chan struct{}

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
	s := &Store{repo: repo, versionChanged: make(chan struct{})}
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

// ResourceVersion returns a resource-scoped version token and the loaded git
// HEAD from the same snapshot. The resource token only changes when that
// service resource's payload changes, which lets watch handlers avoid waking
// config clients for env-only commits and vice versa.
func (s *Store) ResourceVersion(ctx context.Context, org, project, service, resource string) (string, string, error) {
	snap := s.current()
	key := ServiceKey{Org: org, Project: project, Service: service}.String()
	d, ok := snap.data[key]
	if !ok {
		return "", snap.version, apperror.New(apperror.CodeNotFound,
			fmt.Sprintf("service not found: %s/%s/%s", org, project, service))
	}
	version := resourceVersion(d, resource, snap.version)
	if version == "" {
		version = snap.version
	}
	return version, snap.version, nil
}

// WaitForVersionChange blocks until the store's loaded git version differs
// from version or ctx is cancelled. It returns immediately when the caller's
// version is already stale.
func (s *Store) WaitForVersionChange(ctx context.Context, version string) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("context is required")
	}
	for {
		current, changed := s.versionChangeState(version)
		if current != version {
			return current, true, nil
		}
		select {
		case <-ctx.Done():
			current, _ := s.versionChangeState(version)
			if current != version {
				return current, true, nil
			}
			return current, false, ctx.Err()
		case <-changed:
		}
	}
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

// GetConfigAtVersion returns config.yaml for a service as it existed at a Git
// commit. The service must exist in the current snapshot so path parameters
// cannot be used to read arbitrary repository files.
func (s *Store) GetConfigAtVersion(ctx context.Context, org, project, service, version string) (*ServiceData, error) {
	if version == "" {
		return nil, apperror.New(apperror.CodeValidation, "version is required")
	}
	if _, err := s.GetConfig(ctx, org, project, service); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path := ServicePath(org, project, service) + "/config.yaml"
	raw, err := s.repo.ReadFileAtCommit(version, path)
	if err != nil {
		if errors.Is(err, gitops.ErrFileNotFoundAtCommit) {
			return &ServiceData{ConfigResourceVersion: version}, nil
		}
		if errors.Is(err, gitops.ErrCommitNotFound) {
			return nil, apperror.Wrap(apperror.CodeNotFound, "historical version not found", err)
		}
		return nil, apperror.Wrap(apperror.CodeInternal, "read historical config.yaml", err)
	}
	cfg, err := parser.ParseConfig(raw)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeInternal, "parse historical config.yaml", err)
	}
	d := &ServiceData{
		Config:                cfg,
		ConfigResourceVersion: version,
		configDigest:          digestBytes(raw),
	}
	applyMetadataUpdatedAt(d, cfg.Metadata.UpdatedAt)
	return d, nil
}

// GetEnvVarsAtVersion returns env_vars.yaml for a service as it existed at a
// Git commit. Secret value resolution is intentionally handled only for the
// current snapshot by the HTTP layer.
func (s *Store) GetEnvVarsAtVersion(ctx context.Context, org, project, service, version string) (*ServiceData, error) {
	if version == "" {
		return nil, apperror.New(apperror.CodeValidation, "version is required")
	}
	if _, err := s.GetConfig(ctx, org, project, service); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path := ServicePath(org, project, service) + "/env_vars.yaml"
	raw, err := s.repo.ReadFileAtCommit(version, path)
	if err != nil {
		if errors.Is(err, gitops.ErrFileNotFoundAtCommit) {
			return &ServiceData{EnvVarsResourceVersion: version}, nil
		}
		if errors.Is(err, gitops.ErrCommitNotFound) {
			return nil, apperror.Wrap(apperror.CodeNotFound, "historical version not found", err)
		}
		return nil, apperror.Wrap(apperror.CodeInternal, "read historical env_vars.yaml", err)
	}
	envVars, err := parser.ParseEnvVars(raw)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeInternal, "parse historical env_vars.yaml", err)
	}
	d := &ServiceData{
		EnvVars:                envVars,
		EnvVarsResourceVersion: version,
		envVarsDigest:          digestBytes(raw),
	}
	applyMetadataUpdatedAt(d, envVars.Metadata.UpdatedAt)
	return d, nil
}

// History returns service-scoped Git history entries with optional file,
// limit, and before-cursor filtering.
func (s *Store) History(ctx context.Context, opts HistoryOptions) ([]HistoryEntry, error) {
	if opts.Org == "" || opts.Project == "" || opts.Service == "" {
		return nil, apperror.New(apperror.CodeValidation, "org, project and service are required")
	}
	if opts.Limit <= 0 {
		return nil, apperror.New(apperror.CodeValidation, "limit must be greater than 0")
	}
	if opts.File != "" && !validHistoryFile(opts.File) {
		return nil, apperror.New(apperror.CodeValidation, "file must be one of config, env_vars, or secrets")
	}
	if _, err := s.GetConfig(ctx, opts.Org, opts.Project, opts.Service); err != nil {
		return nil, err
	}

	var entries []HistoryEntry
	beforeSeen := opts.Before == ""
	err := s.repo.IterateServiceHistory(ctx, opts.Org, opts.Project, opts.Service, func(entry gitops.ServiceHistoryEntry) error {
		if !beforeSeen {
			if entry.Version == opts.Before {
				beforeSeen = true
			}
			return nil
		}
		if !historyEntryMatchesFile(entry, opts.File) {
			return nil
		}

		entries = append(entries, HistoryEntry{
			Version:      entry.Version,
			Message:      entry.Message,
			Author:       entry.Author,
			Timestamp:    entry.Timestamp,
			FilesChanged: historyChangedPaths(entry.FilesChanged),
		})
		if len(entries) >= opts.Limit {
			return errStopHistory
		}
		return nil
	})
	if errors.Is(err, errStopHistory) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func historyEntryMatchesFile(entry gitops.ServiceHistoryEntry, file string) bool {
	if file == "" {
		return true
	}
	for _, change := range entry.FilesChanged {
		if historyChangeMatchesFile(change, file) {
			return true
		}
	}
	return false
}

func validHistoryFile(file string) bool {
	switch file {
	case "config", "env_vars", "secrets":
		return true
	default:
		return false
	}
}

func historyChangeMatchesFile(change gitops.ServiceFileChange, file string) bool {
	switch file {
	case "config":
		return change.Kind == gitops.ServiceFileConfig
	case "env_vars":
		return change.Kind == gitops.ServiceFileEnvVars
	case "secrets":
		return change.Kind == gitops.ServiceFileSecrets || change.Kind == gitops.ServiceFileSealedSecret
	default:
		return false
	}
}

func historyChangedPaths(changes []gitops.ServiceFileChange) []string {
	paths := make([]string, len(changes))
	for i, change := range changes {
		paths[i] = change.Path
	}
	return paths
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

// PrepareRevert validates a target commit and builds the service-file restore
// plan without mutating Git or the in-memory snapshot.
func (s *Store) PrepareRevert(ctx context.Context, req *RevertRequest) (*RevertPlan, error) {
	if req == nil {
		return nil, apperror.New(apperror.CodeValidation, "revert request is required")
	}
	if req.Org == "" || req.Project == "" || req.Service == "" {
		return nil, apperror.New(apperror.CodeValidation, "org, project and service are required")
	}
	if req.TargetVersion == "" {
		return nil, apperror.New(apperror.CodeValidation, "target_version is required")
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
	if _, err := s.GetConfig(ctx, req.Org, req.Project, req.Service); err != nil {
		return nil, err
	}

	targetFiles, err := s.repo.ReadServiceFilesAtCommit(ctx, req.TargetVersion, req.Org, req.Project, req.Service)
	if err != nil {
		if errors.Is(err, gitops.ErrCommitNotFound) {
			return nil, apperror.Wrap(apperror.CodeNotFound, "target version not found", err)
		}
		return nil, apperror.Wrap(apperror.CodeInternal, "read target service files", err)
	}
	changedService, err := s.targetVersionChangedService(ctx, req)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeInternal, "validate target service history", err)
	}
	if !changedService {
		return nil, apperror.New(apperror.CodeNotFound, "target version did not change service files")
	}
	if len(targetFiles) == 0 {
		return nil, apperror.New(apperror.CodeNotFound, "target version has no service files")
	}

	currentFiles, err := s.repo.ReadServiceFilesAtCommit(ctx, s.HeadVersion(), req.Org, req.Project, req.Service)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeInternal, "read current service files", err)
	}

	targetByPath := serviceFileContentsByPath(targetFiles)
	currentByPath := serviceFileContentsByPath(currentFiles)
	restoredFiles := sortedMapKeys(targetByPath)
	deletedFiles := serviceFilesMissingFromTarget(currentByPath, targetByPath)
	svcPath := ServicePath(req.Org, req.Project, req.Service)
	files := make(map[string][]byte, len(targetByPath))
	for _, rel := range restoredFiles {
		files[filepath.ToSlash(filepath.Join(svcPath, rel))] = append([]byte(nil), targetByPath[rel]...)
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		msg = "Rollback to " + req.TargetVersion
	}

	return &RevertPlan{
		Org:           req.Org,
		Project:       req.Project,
		Service:       req.Service,
		TargetVersion: req.TargetVersion,
		Message:       msg,
		Files:         files,
		RestoredFiles: restoredFiles,
		DeletedFiles:  deletedFiles,
		Noop:          serviceFilesEqual(currentByPath, targetByPath),
	}, nil
}

func (s *Store) targetVersionChangedService(ctx context.Context, req *RevertRequest) (bool, error) {
	found := false
	err := s.repo.IterateServiceHistory(ctx, req.Org, req.Project, req.Service, func(entry gitops.ServiceHistoryEntry) error {
		if entry.Version == req.TargetVersion {
			found = true
			return errStopHistory
		}
		return nil
	})
	if errors.Is(err, errStopHistory) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return found, nil
}

func serviceFileContentsByPath(files []gitops.ServiceFileContent) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for _, file := range files {
		out[file.Path] = append([]byte(nil), file.Data...)
	}
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func serviceFilesMissingFromTarget(current, target map[string][]byte) []string {
	var deleted []string
	for path := range current {
		if _, ok := target[path]; !ok {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(deleted)
	return deleted
}

func serviceFilesEqual(current, target map[string][]byte) bool {
	if len(current) != len(target) {
		return false
	}
	for path, targetData := range target {
		currentData, ok := current[path]
		if !ok || !bytes.Equal(currentData, targetData) {
			return false
		}
	}
	return true
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
	if len(req.Secrets) > 0 {
		defer destroySecretWrites(req.Secrets)
	}
	hasSecrets, err := validateSecretWrites(req.Secrets)
	if err != nil {
		return nil, err
	}
	if req.Config == nil && req.EnvVars == nil && !hasSecrets {
		return nil, apperror.New(apperror.CodeValidation, "at least one of config, env_vars, or secrets must be provided")
	}
	auditResult := ""
	auditSecretIDs := []string(nil)
	if hasSecrets {
		auditResult = "failure"
		auditSecretIDs = secretWriteAuditIDs(req.Secrets)
		defer func() {
			s.recordSecretAudit(context.WithoutCancel(ctx), secret.AuditEvent{
				Action:    "secret_admin_write",
				Result:    auditResult,
				Org:       req.Org,
				Project:   req.Project,
				Service:   req.Service,
				SecretIDs: auditSecretIDs,
			})
		}()
		if s.secretDeps.Sealer == nil {
			auditResult = "configuration_error"
			return nil, apperror.New(apperror.CodeValidation, "secret sealer is not configured")
		}
		if s.secretDeps.Applier == nil {
			auditResult = "configuration_error"
			return nil, apperror.New(apperror.CodeValidation, "secret applier is not configured")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	svcPath := ServicePath(req.Org, req.Project, req.Service)
	now := time.Now().UTC()

	baseFiles := map[string][]byte{}
	var baseWrittenFiles []string
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
		baseFiles[path] = data
		baseWrittenFiles = append(baseWrittenFiles, "config.yaml")
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
		baseFiles[path] = data
		baseWrittenFiles = append(baseWrittenFiles, "env_vars.yaml")
	}

	msg := req.Message
	if msg == "" {
		msg = fmt.Sprintf("update config for %s/%s/%s", req.Org, req.Project, req.Service)
	}

	var writtenFiles []string
	hash := ""
	var commitErr error
	if hasSecrets {
		sealedManifests, err = s.buildSealedManifests(ctx, req)
		if err != nil {
			auditResult = "seal_failed"
			return nil, err
		}
		hash, commitErr = s.repo.CommitAndPushFunc(ctx, msg, func(reader gitops.FileReader) (map[string][]byte, error) {
			files := make(map[string][]byte, len(baseFiles)+1+len(sealedManifests))
			for path, data := range baseFiles {
				files[path] = data
			}
			nextWrittenFiles := append([]string(nil), baseWrittenFiles...)

			existingSecrets, err := readExistingSecrets(reader, filepath.Join(svcPath, "secrets.yaml"))
			if err != nil {
				return nil, err
			}
			sec := &parser.SecretsConfig{
				Version: "1",
				Secrets: mergeSecretEntries(existingSecrets, req.Secrets),
			}
			data, err := yaml.Marshal(sec)
			if err != nil {
				return nil, apperror.Wrap(apperror.CodeInternal, "marshal secrets.yaml", err)
			}
			path := filepath.Join(svcPath, "secrets.yaml")
			files[path] = data
			nextWrittenFiles = append(nextWrittenFiles, "secrets.yaml")

			for _, manifest := range sealedManifests {
				manifestPath := filepath.ToSlash(manifest.Path)
				rel, ok := serviceRelativePath(svcPath, manifestPath)
				if !ok {
					return nil, apperror.New(apperror.CodeInternal,
						fmt.Sprintf("sealed manifest path %q is outside service path", manifest.Path))
				}
				files[filepath.FromSlash(manifestPath)] = manifest.YAML
				nextWrittenFiles = append(nextWrittenFiles, rel)
			}
			writtenFiles = nextWrittenFiles
			return files, nil
		})
	} else {
		writtenFiles = append([]string(nil), baseWrittenFiles...)
		hash, commitErr = s.repo.CommitAndPush(ctx, msg, baseFiles)
	}
	if commitErr != nil {
		if hasSecrets {
			auditResult = "commit_failed"
		}
		return nil, commitErr
	}

	result := &ChangeResult{
		Version:   hash,
		UpdatedAt: now,
		Files:     writtenFiles,
	}

	if len(sealedManifests) > 0 {
		if err := applySealedManifests(context.WithoutCancel(ctx), s.secretDeps.Applier, sealedManifests); err != nil {
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
	if hasSecrets {
		auditResult = secretWriteAuditResult(result)
	}

	return result, nil
}

func readExistingSecrets(reader gitops.FileReader, path string) (*parser.SecretsConfig, error) {
	raw, err := reader.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, apperror.Wrap(apperror.CodeInternal, "read existing secrets.yaml", err)
	}
	sec, err := parser.ParseSecrets(raw)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeValidation, "parse existing secrets.yaml", err)
	}
	return sec, nil
}

func (s *Store) buildSealedManifests(ctx context.Context, req *ChangeRequest) ([]secret.SealedManifest, error) {
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
			return nil, apperror.Wrap(apperror.CodeInternal,
				fmt.Sprintf("seal secret %s/%s", write.Namespace, name), err)
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
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
		if err := validateK8sDNSSubdomain("secret name", name); err != nil {
			return false, err
		}
		if err := validateK8sDNSLabel("secret namespace", write.Namespace); err != nil {
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

func secretWriteAuditIDs(writes map[string]SecretWrite) []string {
	ids := make([]string, 0, len(writes))
	for _, name := range sortedSecretNames(writes) {
		write := writes[name]
		ids = append(ids, write.Namespace+"/"+name)
	}
	return ids
}

func secretWriteAuditResult(result *ChangeResult) string {
	switch {
	case result.ApplyFailed && result.ReloadFailed:
		return "apply_and_reload_failed"
	case result.ApplyFailed:
		return "apply_failed"
	case result.ReloadFailed:
		return "reload_failed"
	default:
		return "success"
	}
}

func (s *Store) recordSecretAudit(ctx context.Context, event secret.AuditEvent) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if s.secretDeps.Auditor == nil {
		return
	}
	if err := s.secretDeps.Auditor.Record(ctx, event); err != nil {
		slog.Warn("record secret audit event failed",
			"err", err,
			"action", event.Action,
			"result", event.Result,
			"org", event.Org,
			"project", event.Project,
			"service", event.Service,
			"secret_ids", event.SecretIDs)
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
		filepath.Join(svcPath, "sealed-secrets"),
	}

	msg := fmt.Sprintf("delete config for %s/%s/%s", req.Org, req.Project, req.Service)
	hash, err := s.repo.DeleteAndPush(ctx, msg, paths)
	if err != nil {
		return nil, err
	}

	result := &DeleteResult{
		Version:      hash,
		UpdatedAt:    time.Now().UTC(),
		DeletedFiles: []string{"config.yaml", "env_vars.yaml", "secrets.yaml", "sealed-secrets/"},
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
	s.applyResourceVersions(data, hash)

	prevVersion := s.HeadVersion()
	s.snapshot.Store(newSnapshot(data, hash))
	s.lastReload.Store(&reloadState{at: time.Now(), err: nil})
	if hash != prevVersion {
		s.notifyVersionChange()
	}
	slog.Info("config store reloaded", "services", len(data), "version", hash[:min(8, len(hash))])
	return nil
}

func (s *Store) versionChangeState(version string) (string, <-chan struct{}) {
	s.versionWatchMu.Lock()
	defer s.versionWatchMu.Unlock()
	current := s.HeadVersion()
	if current != version {
		return current, nil
	}
	return current, s.versionChanged
}

func (s *Store) notifyVersionChange() {
	s.versionWatchMu.Lock()
	defer s.versionWatchMu.Unlock()
	close(s.versionChanged)
	s.versionChanged = make(chan struct{})
}

func (s *Store) applyResourceVersions(data map[string]*ServiceData, version string) {
	prev := s.current()
	for key, sd := range data {
		var prevData *ServiceData
		if prev != nil {
			prevData = prev.data[key]
		}
		sd.ConfigResourceVersion = nextResourceVersion(
			sd.Config != nil,
			sd.configDigest,
			version,
			prevData,
			func(d *ServiceData) bool { return d.Config != nil },
			func(d *ServiceData) string { return d.configDigest },
			func(d *ServiceData) string { return d.ConfigResourceVersion },
		)
		sd.EnvVarsResourceVersion = nextResourceVersion(
			sd.EnvVars != nil,
			sd.envVarsDigest,
			version,
			prevData,
			func(d *ServiceData) bool { return d.EnvVars != nil },
			func(d *ServiceData) string { return d.envVarsDigest },
			func(d *ServiceData) string { return d.EnvVarsResourceVersion },
		)
	}
}

func nextResourceVersion(
	present bool,
	digest string,
	version string,
	prev *ServiceData,
	prevPresent func(*ServiceData) bool,
	prevDigest func(*ServiceData) string,
	prevVersion func(*ServiceData) string,
) string {
	if prev == nil {
		return version
	}
	if present {
		if prevPresent(prev) && digest != "" && digest == prevDigest(prev) && prevVersion(prev) != "" {
			return prevVersion(prev)
		}
		return version
	}
	if !prevPresent(prev) && prevVersion(prev) != "" {
		return prevVersion(prev)
	}
	return version
}

func resourceVersion(d *ServiceData, resource, fallback string) string {
	switch resource {
	case "config":
		if d.ConfigResourceVersion != "" {
			return d.ConfigResourceVersion
		}
	case "env_vars":
		if d.EnvVarsResourceVersion != "" {
			return d.EnvVarsResourceVersion
		}
	}
	return fallback
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
		sd.configDigest = digestBytes(raw)
		applyMetadataUpdatedAt(sd, cfg.Metadata.UpdatedAt)
	case "env_vars":
		ev, err := parser.ParseEnvVars(raw)
		if err != nil {
			return fmt.Errorf("parse env_vars.yaml: %w", err)
		}
		sd.EnvVars = ev
		sd.envVarsDigest = digestBytes(raw)
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

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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
