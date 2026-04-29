# CI/CD Plan - <Project>

Use this template as a CI/CD planning worksheet or migration packet.

Canonical project details still live in the focused docs:

- Commands: `docs/current/TESTING.md`
- Deployment ownership and environment state: `docs/current/OPERATIONS.md`
- Step-by-step procedures: `docs/05_RUNBOOK.md`
- Acceptance gates: `docs/06_ACCEPTANCE_TESTS.md`
- Migration slices and evidence: `docs/04_IMPLEMENTATION_PLAN.md`

If a project keeps a completed copy of this worksheet, link to those canonical
sections instead of duplicating long commands or procedures.

## Summary

- CI owner:
- CD owner:
- CI system:
- CD system:
- Protected branches:
- Branch flow: feat/* -> dev -> main / direct PR -> main / release branch / other
- Release source: main / tag / release branch / package registry / other

## Required Checks

| Check | Workflow / job | Command or tool | Required? | Notes |
|---|---|---|---|---|
| Install |  |  | yes / no |  |
| Lint |  |  | yes / no |  |
| Typecheck / compile |  |  | yes / no |  |
| Unit tests |  |  | yes / no |  |
| Integration tests |  |  | yes / no |  |
| Build / package |  |  | yes / no |  |
| Migration / schema check |  |  | yes / no |  |
| Security / dependency scan |  |  | yes / no |  |
| Docs freshness |  |  | yes / no |  |

## Environments

| Environment | Purpose | Trigger | Approval | Secrets source | Gate | Rollback |
|---|---|---|---|---|---|---|
| preview |  |  |  |  |  |  |
| staging |  |  |  |  |  |  |
| production |  |  |  |  |  |  |

## Artifact / Release Identity

- Artifact type: container image / package / binary / static bundle / model /
  infra plan / other
- Artifact registry:
- Version format:
- Release notes:
- Provenance / digest:

## Deployment Flow

```text
commit / PR
  -> CI checks
  -> merge or release trigger
  -> artifact selection
  -> deploy
  -> smoke / acceptance gate
  -> monitoring
```

## Data / Migration Policy

- Migration trigger:
- Backup or restore assumption:
- Backward compatibility policy:
- Destructive change policy:
- Verification command or gate:

## Secrets / Configuration

Document secret names, owners, and storage locations. Do not document values.

| Name | Owner | Storage location | Environments | Rotation / notes |
|---|---|---|---|---|
|  |  |  |  |  |

## Failure Handling

| Failure | Detection | Response | Owner | Link |
|---|---|---|---|---|
| CI failure |  |  |  |  |
| Deploy failure before traffic |  |  |  |  |
| Smoke failure after deploy |  |  |  |  |
| Migration failure |  |  |  |  |
| Production incident after release |  |  |  |  |

## Migration Notes

Use when adopting this plan in an existing project.

- Existing CI/CD systems:
- Existing release scripts:
- Manual deploy steps:
- Commands verified:
- Commands needing audit:
- Old path decommission plan:
- Evidence anchors:
