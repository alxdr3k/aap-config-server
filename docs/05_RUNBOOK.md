# 05 Runbook

운영 절차 모음. "장애/배포/데이터 작업을 어떻게 처리하는가"를 담는다.

## How to Deploy

This repo currently defines the binary and Docker image build, not the full
Helm/Kubernetes deployment.

```bash
make build
make docker-build
```

- Prerequisites: Go 1.26+, access to the config Git repository, and required runtime env vars.
- Rollback method: roll back the deployed image or Git config repo commit
  through the owning deployment system.
- Deployment manifests: Helm/Kubernetes manifests remain outside this repo by
  `DEC-003`.

## How to Run Locally

```bash
export GIT_URL=git@github.com:myorg/aap-helm-charts.git
export GIT_SSH_KEY=$HOME/.ssh/id_ed25519
export API_KEY=$(openssl rand -hex 32)
export GIT_POLL_INTERVAL=30s

make build
./bin/config-server -addr :8080
```

For local smoke tests only:

```bash
export ALLOW_UNAUTHENTICATED_DEV=true
```

## How to Monitor

- Liveness: `GET /healthz`
- Readiness: `GET /readyz`
- Operational status: `GET /api/v1/status`, including `app_registry` cache/load state.
- Logs: JSON `slog` output on stdout.
- Metrics: no Prometheus endpoint currently implemented.

## Common Incidents

### Incident: Degraded readiness after reload failure

- Symptom: `/readyz` returns 503 `degraded`.
- Detection: `/api/v1/status` has `is_degraded: true` and `last_reload_error`.
- Mitigation: fix malformed YAML or dirty `configs/` checkout, then call `POST /api/v1/admin/reload`.
- Root-cause investigation: inspect parser errors, config repo HEAD, and local checkout status.
- Related: `AC-007`, `TEST-007`.

### Incident: Admin write committed but did not reload

- Symptom: `POST /api/v1/admin/changes` returns `503 committed_but_reload_failed`.
- Detection: response includes `version` and `reload_error`.
- Mitigation: treat Git commit as already written; fix the repo state and force reload.
- Root-cause investigation: inspect newly committed YAML and any pre-existing malformed files on the branch.
- Related: `AC-005`, `AC-007`.

### Incident: Admin secret write committed but did not apply

- Symptom: `POST /api/v1/admin/changes` returns `503 committed_but_apply_failed`.
- Detection: response includes `version`, written SealedSecret file paths, and `apply_error`.
- Mitigation: treat Git commit as already written; fix K8s access/controller issues, then re-apply the committed SealedSecret manifest or retry the admin write. Client disconnects after commit do not cancel the server-managed apply attempt.
- Root-cause investigation: inspect Config Server service account RBAC, SealedSecret controller availability, and the committed encrypted manifest.
- Related: `AC-020`, `TEST-020`.

### Incident: App Registry load degraded

- Symptom: `/readyz` returns 200 but `/api/v1/status` reports `degraded_components: ["app_registry"]`.
- Detection: `app_registry.status` is `degraded` and `app_registry.last_load_error` explains the Console load failure.
- Mitigation: restore AAP Console API reachability; Console webhook retries can update changed records, and a Config Server restart reloads the full registry.
- Root-cause investigation: inspect `CONSOLE_API_URL`, network policy, API auth, and Config Server logs for `app registry bootstrap failed`.
- Related: `AC-021`, `TEST-021`.

### Incident: Dirty config checkout blocks snapshot

- Symptom: reload fails with `configs/ worktree is dirty`.
- Detection: `/api/v1/status` exposes the reload error.
- Mitigation: remove or commit out-of-band changes under `configs/`; do not serve from mutated checkout state.
- Root-cause investigation: identify non-server processes writing into `GIT_LOCAL_PATH`.
- Related: `AC-008`, `TEST-008`.

### Incident: Protected endpoint returns unauthorized

- Symptom: admin endpoint or secret metadata endpoint returns 401.
- Detection: JSON error envelope with `code: unauthorized`.
- Mitigation: send `Authorization: Bearer <API_KEY>` or `X-API-Key` matching server env.
- Root-cause investigation: check client/server key rotation and deployment env.
- Related: `AC-006`, `TEST-006`.

## Data Operations

- Backup: Git remote is the canonical persisted state.
- Restore: revert or repair config repo commits through Git, then force reload.
- Migration checklist:
  - Validate YAML schema before merging config repo changes.
  - Confirm `/api/v1/status.version` after reload.
  - Confirm `/readyz` is healthy.

## Rotations / On-call

- Owner: maintainers / platform operators.
- Escalation path: not defined in this repo.

## Change Log

| Date | Change | By |
|---|---|---|
| 2026-04-29 | Added initial runbook during boilerplate migration. | Codex |
