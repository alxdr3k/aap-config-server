# ADR-005: Phase-1 global Git serialization

> **상태**: Accepted
> **일자**: 2026-04-29

## 컨텍스트

ADR-003 chose service-level mutexes plus pull-rebase for the target concurrent
write design. The current Phase-1 implementation is narrower:

- `internal/gitops.Repo` uses one mutex to serialize pull, snapshot, commit,
  push, and delete operations against the local worktree.
- `internal/store.Store` serializes admin writes, deletes, and background
  refreshes before they reach Git.
- Current accepted gates focus on correctness and dirty-checkout safety, not
  high write throughput.

This creates a documented gap: ADR-003 describes the target design, while code
intentionally uses a simpler global serialization boundary.

## 결정 동인

- Phase-1 config changes are expected to be low-frequency operational writes.
- The global mutex keeps the local Git worktree simple and prevents concurrent
  push/reload races.
- Implementing service-level mutexes now would require broader changes across
  store, Git, reload, and retry behavior without evidence of current contention.
- The target design should remain visible before the project scales write
  volume.

## 선택지

### 1. Implement ADR-003 now — 미채택

Implement service-level locks and pull-rebase immediately.

- **장점**: target design and implementation match.
- **단점**: expands implementation risk before measurable throughput need.

### 2. Keep global serialization for Phase-1 — 채택

Keep current global Git/store serialization and defer service-level concurrency
until write contention or scale requirements justify it.

- **장점**: simple, already tested, and aligned with current write volume.
- **단점**: unrelated service writes cannot proceed in parallel.

## 결정

Keep the current global Git/store serialization for Phase-1. Do not implement
service-level mutexes until write volume or contention data shows the simpler
boundary is insufficient.

ADR-003 remains the target concurrency design for a future scale point. Before
that point, run `SPIKE-001` to measure expected concurrent admin write volume
and decide whether to implement ADR-003 fully or replace it with a different
scale strategy.

## 영향

### 긍정적

- Current implementation is explicitly accepted instead of treated as an
  undocumented ADR drift.
- Git worktree mutation, reload, and dirty-checkout protection remain easy to
  reason about.
- Phase-1 correctness tests continue to cover the supported behavior.

### 부정적

- Writes for different services are serialized even when they touch different
  files.
- ADR-003 target design and Phase-1 implementation now require readers to check
  both ADRs.

## 관련 문서

- [ADR-003: 동시 변경 처리 — 서비스별 Mutex](./003-concurrent-change-per-service-mutex.md)
- [SPIKE-001: Validate service-level mutex need](../03_RISK_SPIKES.md)
- [Q-002](../07_QUESTIONS_REGISTER.md)
