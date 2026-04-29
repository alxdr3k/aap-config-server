# 03 Risk Spikes

기술 가정을 실험으로 검증하는 짧은 탐색 작업.

## How to use

- PRD의 ASM-### 중 결과에 큰 영향을 주는 가정 → SPIKE-###로 승격.
- Spike는 시간 박싱 (예: 1~3일). 결과는 여기에 기록.
- 결과가 결정으로 굳어지면 `08_DECISION_REGISTER.md` 또는 ADR로 옮긴다.

## Spikes

### SPIKE-001: Validate service-level mutex need before implementing ADR-003 fully

- Hypothesis: Current global git serialization is sufficient for Phase-1 write volume; service-level mutex can stay deferred until measurable contention exists.
- Owner: maintainers
- Time-box: 1 day
- Start / End: not scheduled
- Status: deferred

**Experiment**

Review expected admin write concurrency, measure current admin write latency
under concurrent requests, and decide whether the service-level mutex in
ADR-003 is required now or should be superseded/deferred.

**Result**

`ADR-005` accepts current global Git/store serialization for Phase-1. The spike
is deferred until measured write volume or contention suggests that
service-level concurrency is needed.

**Decision / Next Step**

- Decision: `ADR-005`.
- Follow-up: before high write volume, measure concurrent admin write behavior
  and either implement `ADR-003` fully or replace it with a new scale decision.

---

### SPIKE-002: Define deployment ownership for Config Server manifests

- Hypothesis: Deployment manifests may belong in the Helm/config repo rather than this binary repo.
- Owner: maintainers
- Time-box: 0.5 day
- Start / End: 2026-04-29
- Status: resolved

**Experiment**

Trace current AAP deployment ownership and decide whether this repo should add
Helm/K8s manifests or only publish image/runtime docs.

**Result**

`DEC-003` keeps this repo focused on the binary, Docker image build, runtime
configuration, and runbook guidance. Helm/Kubernetes manifests remain in the
owning deployment repo/system unless a future decision moves ownership here.

**Decision / Next Step**

- Decision: `DEC-003`.
- Follow-up: update this decision if deployment ownership changes.
