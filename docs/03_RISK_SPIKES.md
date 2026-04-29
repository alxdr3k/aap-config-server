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
- Status: open

**Experiment**

Review expected admin write concurrency, measure current admin write latency
under concurrent requests, and decide whether the service-level mutex in
ADR-003 is required now or should be superseded/deferred.

**Result**

Pending.

**Decision / Next Step**

- Decision: pending `Q-002`.
- Follow-up: either implementation slice under `CORE` or a superseding ADR.

---

### SPIKE-002: Define deployment ownership for Config Server manifests

- Hypothesis: Deployment manifests may belong in the Helm/config repo rather than this binary repo.
- Owner: maintainers
- Time-box: 0.5 day
- Start / End: not scheduled
- Status: open

**Experiment**

Trace current AAP deployment ownership and decide whether this repo should add
Helm/K8s manifests or only publish image/runtime docs.

**Result**

Pending.

**Decision / Next Step**

- Decision: pending `Q-003`.
- Follow-up: update `docs/current/OPERATIONS.md` and implementation plan.
