# ADR (Architecture Decision Records)

이 폴더에 중요한 아키텍처 결정을 기록한다. 포맷은 Michael Nygard 스타일을
따르되, 이 repo의 기존 파일명은 `001-...` 형식을 유지한다.

## When to write an ADR

- 되돌리기 어려운 결정
- 여러 컴포넌트/팀에 영향
- 장기 운영 비용을 바꾸는 결정

더 작은 결정은 `../08_DECISION_REGISTER.md`에 기록한다.

## Status

- `proposed` — 제안 중
- `accepted` — 채택
- `deprecated` — 더 이상 선호되지 않음
- `superseded` — 다른 ADR로 대체됨
- `rejected` — 기각

## Index

| ADR | Title | Status | Date |
|---|---|---|---|
| [001](./001-config-agent-leading-edge-debounce.md) | Config Agent Leading-edge Debounce | accepted | 2026-03-12 |
| [002](./002-central-config-agent-vs-sidecar.md) | Central Config Agent vs Sidecar | accepted | 2026-03-12 |
| [003](./003-concurrent-change-per-service-mutex.md) | Concurrent change handling: service-level mutex | accepted | 2026-03-12 |
| [004](./004-secret-storage-sealedsecret-volume-mount.md) | Secret storage: SealedSecret + Volume Mount | accepted | 2026-03-12 |

## Naming note

The boilerplate template suggests `ADR-<NNNN>-<kebab-title>.md`. Existing files
use `NNN-<kebab-title>.md`; keep that convention until the team explicitly
chooses a rename migration.

## Template

새 ADR을 만들 때는 `../templates/ADR_TEMPLATE.md`를 복사한다.
