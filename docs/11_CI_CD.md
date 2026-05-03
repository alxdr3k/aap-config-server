# 11 CI/CD Guide

Stack-neutral guidance for documenting and operating continuous integration
and continuous delivery / deployment.

This file explains what a project should decide and document. The active
project-specific commands and procedures live elsewhere:

| Topic | Canonical location |
|---|---|
| Test, lint, typecheck, build, eval commands | `docs/current/TESTING.md` |
| Runtime environment, deployment ownership, secrets, rollback notes | `docs/current/OPERATIONS.md` |
| Step-by-step operational procedures | `docs/05_RUNBOOK.md` |
| CI/CD acceptance gates and test evidence | `docs/06_ACCEPTANCE_TESTS.md` |
| Roadmap slices, gate status, and evidence links | `docs/04_IMPLEMENTATION_PLAN.md` |
| Major CI/CD architecture or release policy decisions | `docs/adr/` |
| Lightweight CI/CD decisions | `docs/08_DECISION_REGISTER.md` |
| Planning worksheet or migration packet | `docs/templates/CI_CD_TEMPLATE.md` |
| Example workflows to copy and adapt | `.github/workflows/*.yml.example` |

Rules:

- Document real commands only after the project has them.
- Do not invent manual deployment steps to fill a blank section.
- Keep secrets out of docs and workflow logs.
- Prefer one documented path from commit to release over hidden local steps.
- If deployment is owned by another system or team, document that boundary.

## Baseline Model

Use this default flow unless the project has a reason to do something else:

```text
work branch pull request to dev
  -> CI: install, lint, typecheck, test, build, docs/schema checks
  -> review
  -> merge to dev
  -> promote or pull request dev to main
  -> CI on main
  -> package or identify release artifact
  -> deploy to staging or preview
  -> smoke / acceptance checks
  -> promote to production with required approval when appropriate
  -> post-deploy checks and release notes
```

For libraries, CLIs, mobile apps, data pipelines, and infrastructure projects,
"deploy" may mean publish, package, sign, upload, apply infrastructure, run a
scheduled job, or promote a model. Use the same principles: reproducible build,
traceable artifact, explicit gate, reversible or mitigatable release.

## CI Design

CI should answer one question: is this change safe enough to review, merge, or
release?

Recommended checks:

| Check | Purpose | Notes |
|---|---|---|
| Install / dependency resolution | Proves the lockfile or dependency spec works | Use frozen or locked install modes when available. |
| Format or lint | Catches style and static defects | Make required only when stable enough to avoid noisy failures. |
| Typecheck / compile | Catches contract errors | Applies to typed languages and generated client/server code. |
| Unit tests | Fast behavior coverage | Should run on every PR. |
| Integration tests | Cross-module or external boundary coverage | Use service containers, test doubles, or ephemeral dependencies as appropriate. |
| Build / package | Proves the releasable artifact can be produced | Required for apps, libraries, containers, mobile, and CLIs. |
| Migration / schema checks | Proves data changes are valid | Include generated schema docs when the project has them. |
| Security / dependency scan | Surfaces known vulnerabilities and policy issues | Start as non-blocking if the project needs time to triage backlog. |
| Docs freshness | Catches stale implementation docs | The included doc freshness workflow is intentionally a soft warning. |

Recommended CI properties:

- Run on pull requests and on protected branch pushes.
- Use pinned major versions for third-party actions and review upgrades.
- Use least-privilege `permissions`.
- Keep jobs deterministic. Avoid depending on undeclared local state.
- Cache dependencies, but never cache secrets or mutable build outputs as truth.
- Upload artifacts only when they help debugging or release traceability.
- Split slow jobs from fast required jobs when cycle time becomes painful.
- Give required checks stable names so branch protection does not drift.
- Use concurrency cancellation for superseded PR runs.

## CD Design

CD should answer one question: what exact artifact changed in which environment,
who or what approved it, and how do we recover?

Recommended deployment model:

| Stage | Typical trigger | Required evidence |
|---|---|---|
| Preview | Pull request, optional | Build artifact, preview URL, smoke result |
| Staging | Merge to main, scheduled, or manual | Main CI passing, artifact ID, migration status, smoke result |
| Production | Tag, release, manual approval, or promotion | Staging result or explicit waiver, approver when required, rollback plan |

Recommended CD properties:

- Deploy the same artifact that passed CI when the platform supports it.
- Record release identity: commit SHA, tag, build number, image digest, package
  version, migration version, or model version.
- Use environment protection for production and other sensitive targets.
- Prefer OIDC or platform-native identity over long-lived cloud credentials.
- Run smoke checks after deploy and define what happens when they fail.
- Keep migrations explicit. Document whether they run before, during, or after
  deploy.
- Separate deploy, release, and exposure when possible. Feature flags,
  progressive delivery, and config toggles can reduce rollback pressure.
- Keep rollback and roll-forward procedures documented in the runbook.

## Environment Inventory

Record only environments the project really has.

| Environment | Purpose | Trigger | Owner | Gate | Rollback / recovery |
|---|---|---|---|---|---|
| local | Developer feedback | manual |  |  |  |
| CI | PR and branch validation | pull request / push |  | required checks | rerun / fix forward |
| preview | Optional PR validation | pull request |  | smoke / manual review | destroy preview |
| staging | Release candidate validation | main / manual |  | AC / smoke / eval | redeploy prior artifact |
| production | User-facing release | tag / promotion / manual |  | approval / smoke | rollback or roll forward |

## Service-Type Notes

Use these as prompts, not mandatory sections.

| Project type | CI focus | CD focus |
|---|---|---|
| Web app / API | unit, integration, build, contract tests | deploy artifact, migrations, smoke, rollback |
| Worker / queue service | unit, integration, idempotency tests | deploy workers safely, drain or pause queues, retry policy |
| CLI / library | test matrix, package build, examples | versioning, signing, publish, changelog |
| Mobile / desktop app | build matrix, signing checks, device tests | store upload, staged rollout, crash monitoring |
| Data pipeline | schema tests, fixture runs, backfill simulation | scheduler deploy, data quality checks, rollback or replay |
| ML / eval-heavy system | dataset integrity, eval gates, regression checks | model or prompt versioning, canary, monitoring |
| Infrastructure / IaC | validate, plan, policy checks | apply approval, state backup, drift detection |

## Branch And Release Policy

Document the active policy in the project once chosen.

Default branch policy: `feat/*` or `fix/*` -> `dev` -> `main`. `dev` is the
integration branch; `main` is the release source. Document any project-specific
exception, such as direct PRs to `main` or release branches.

Questions to answer:

- Which branches are protected?
- Which CI checks are required before merge?
- Are merge queues used?
- Who can approve production deployment?
- Is production released from `main`, tags, release branches, or a package
  registry?
- Are hotfixes separate from normal releases?
- Are release notes required?
- What is the policy for failed required checks?

## Secrets And Configuration

Document names and ownership, not secret values.

Checklist:

- Secret storage location: GitHub environment secrets, cloud secret manager,
  vault, platform config, or another owner.
- Secret rotation owner and cadence.
- Which workflows or environments can read each secret.
- Whether pull requests from forks can run privileged steps.
- Which variables are safe to show in logs.
- Which config values are build-time vs deploy-time.

## Data And Migration Gates

When data can be changed by a release, make the gate explicit.

Recommended policy:

- Prefer backward-compatible migrations.
- Use expand / migrate / contract for risky schema changes.
- Document backup, restore, and point-in-time recovery assumptions.
- Verify migrations in CI or staging before production.
- Record whether migrations are automatic, manual, or owned by another system.
- Treat destructive data changes as an ADR or explicit DEC.

## Failure Handling

Document failure modes before the first serious incident.

| Failure | Expected response |
|---|---|
| CI required check fails | Fix forward or explicitly waive only through the documented process. |
| CI infrastructure flakes | Rerun once, then record if repeated; do not hide systemic flakes. |
| Deploy command fails before traffic changes | Stop, inspect logs, rerun only if idempotent. |
| Migration fails | Follow data recovery procedure; do not retry destructive migrations blindly. |
| Smoke test fails after deploy | Roll back, disable exposure, or roll forward according to runbook. |
| Production incident after release | Use incident procedure; link release artifact and commit SHA. |

## New Project Checklist

1. Decide which commands must exist before the first PR: install, lint,
   typecheck, unit test, build, migration check, eval, or smoke.
2. Put the real commands in `docs/current/TESTING.md`.
3. Copy `.github/workflows/ci.yml.example` to `.github/workflows/ci.yml` and
   replace the placeholder setup and command steps.
4. Use `docs/templates/CI_CD_TEMPLATE.md` as a worksheet if CI/CD ownership,
   environments, gates, or migration steps need a single planning packet.
5. Decide whether CD exists now. If not, write "No deployment pipeline
   currently defined" in the deployment sections.
6. When CD exists, copy `.github/workflows/cd.yml.example` or document the
   external CD owner.
7. Record environments, secret ownership, approval gates, and rollback in
   `docs/current/OPERATIONS.md` and `docs/05_RUNBOOK.md`.
8. Map release and smoke gates to `docs/06_ACCEPTANCE_TESTS.md`.
9. Track CI/CD enablement slices and evidence in `docs/04_IMPLEMENTATION_PLAN.md`.
10. Add trace rows for important CI/CD decisions, requirements, gates, and
   slices in `docs/09_TRACEABILITY_MATRIX.md`.

## Migrating Existing Projects

Use this when applying the boilerplate to a project that already has build,
test, release, or deployment behavior.

1. Inventory all existing automation: GitHub Actions, other CI systems,
   deploy platforms, package registries, cron jobs, release scripts, Makefiles,
   local-only commands, and manual runbooks.
2. Identify the real source of truth for each command. Preserve exact command
   names and paths in `docs/current/TESTING.md`; do not normalize them into
   commands you wish existed.
3. Classify existing checks as required, optional, flaky, obsolete, or unknown.
   Put unknowns under `Needs audit`.
4. Map existing environments to the environment inventory: preview, staging,
   production, package registry, data platform, infrastructure account, or
   external owner.
5. Record deployment ownership in `docs/current/OPERATIONS.md`: GitHub Actions,
   another CI/CD tool, platform auto-deploy, release manager, or external team.
6. Move manual deploy and rollback steps into `docs/05_RUNBOOK.md`. If the
   steps are tribal knowledge, mark the source anchor or write `anchor missing`.
7. Map existing CI jobs, smoke tests, staging checks, production approvals, and
   release validations to `AC-###` / `TEST-###` entries when they verify product
   or operational acceptance.
8. Add or update `docs/04_IMPLEMENTATION_PLAN.md` slices for CI/CD migration
   work: workflow adoption, branch protection, secret migration, deploy
   automation, smoke tests, rollback validation, and decommissioning old paths.
9. Preserve evidence: workflow file path, CI run, PR, release, commit SHA,
   artifact digest, package version, dashboard, DEC, ADR, Q, or incident link.
10. Start migrated checks as non-blocking if failure history is unknown. Promote
    them to required checks only after the team understands failures and fixes.
11. Compare old and new release paths for at least one release before removing
    the old path.
12. Record dropped or intentionally deferred CI/CD behavior in the roadmap
    ledger or decision register, not as silent deletion.
