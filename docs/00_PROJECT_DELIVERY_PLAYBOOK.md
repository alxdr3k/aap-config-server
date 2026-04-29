# 00 Project Delivery Playbook

이 프로젝트의 문서화/의사결정/전달 방식 요약.

## Philosophy

```text
Question
 → Proposed Answer
  → Decision / ADR
   → PRD / HLD / Runbook / Acceptance Tests
    → Traceability Matrix
     → Retrospective
      → Extraction packet
       → external knowledge-base review / promotion
```

질문을 먼저 남기고, 답이 정해지면 결정으로 승격하고, 결정은 요구사항/설계/운영 문서에 반영하고, 연결은 Traceability로 추적한다. 회고에서 reusable 지식이 도출되면 extraction packet 으로 정리하여 외부 knowledge base 의 review / 승격 프로세스에 넘긴다. 승격 자체는 외부 knowledge base 가 결정한다.

## Source-of-truth

| Artefact | File |
|---|---|
| 열린 질문 | `07_QUESTIONS_REGISTER.md` |
| 가벼운 결정 | `08_DECISION_REGISTER.md` |
| 중대한 결정 | `adr/ADR-####.md` |
| 요구사항 | `01_PRD.md` |
| 설계 | `02_HLD.md` |
| 가정 검증 | `03_RISK_SPIKES.md` |
| Roadmap / status ledger | `04_IMPLEMENTATION_PLAN.md` |
| 운영 절차 | `05_RUNBOOK.md` |
| 검증 기준 | `06_ACCEPTANCE_TESTS.md` |
| 연결 매트릭스 | `09_TRACEABILITY_MATRIX.md` |
| 회고 | `10_PROJECT_RETROSPECTIVE.md` |

## ID 규약

```text
Q-001        Question
DEC-001      Decision Register entry
ADR-0001     Architecture Decision Record
REQ-001      Requirement
NFR-001      Non-functional requirement
AC-001       Acceptance criterion
TEST-001     Test
SPIKE-001    Risk spike
P0-M1        Milestone
TRK          Track code (example)
TRK-1B       Phase inside a track
TRK-1B.5     Commit-sized slice / task
TRACE-001    Traceability row
```

Roadmap taxonomy:

```text
Milestone = 제품 / 사용자 관점의 delivery gate
Track     = 기술 영역 / 큰 흐름
Phase     = track 안의 구현 단계
Slice     = 커밋 가능한 구현 단위
Gate      = 검증 / acceptance 기준
Evidence  = code / tests / PR / docs 같은 완료 근거
```

## Cadence

- **Daily**: 질문을 `07_`에, 새 결정 후보를 `08_` 또는 `adr/`에.
- **Weekly**: roadmap/status ledger, Traceability matrix, acceptance gate 상태 점검.
- **Milestone**: Retrospective 갱신, lesson 승격 후보 식별.
- **Project end**: 최종 retrospective → extraction packet 준비 → 외부 knowledge base review / 승격.

## Implementation-stage docs

위의 numbered 문서들은 project-stage delivery artifacts (의도/계획/검증)이다.
구현이 시작된 이후에는 implementation-stage 문서들이 추가로 필요하다.

- `docs/context/current-state.md` 를 첫 read로 사용한다 — 새 세션의 압축된 진입점.
- `docs/04_IMPLEMENTATION_PLAN.md` 를 roadmap / status ledger로 사용한다 — milestone, track, phase, slice, gate, evidence의 canonical 위치.
- `docs/current/` 를 implementation-state 네비게이션 문서로 사용한다 (CODE_MAP, DATA_MODEL, RUNTIME, TESTING, OPERATIONS).
- numbered 문서들 (`01_PRD` ~ `10_RETROSPECTIVE`) 은 project delivery artifacts 로 유지한다.
- `docs/discovery/` 는 ongoing exploration / 임시 분석에 사용한다.
- `docs/design/archive/` 는 과거 design 노트 보관용이다.
- `docs/generated/` 는 코드/스키마에서 파생된 generated reference 용이다.

규칙:

- current-state 는 짧게 유지한다.
- 모든 history 를 current-state 에 누적하지 않는다.
- roadmap / phase / slice inventory는 `04_IMPLEMENTATION_PLAN.md`에 두고 current-state나 current docs에 복제하지 않는다.
- 코드 변경이 behavior/schema/runtime 에 영향을 주면 같은 PR 에서 current 문서들을 업데이트한다.
- discovery / archive 는 implementation authority 가 아니다.
- generated 문서는 손으로 편집하지 않는다.

상세 정책은 `docs/DOCUMENTATION.md` 와 `AGENTS.md` 를 참고한다.

## Extraction and external knowledge-base promotion

회고에서 reusable 지식이 도출되면 외부 knowledge base 로의 승격 후보를
정리한다. Boilerplate 는 project 측의 extraction packet 만 정의하며, 외부
knowledge base 의 schema / sensitivity / ingestion 정책은 해당 knowledge base
가 따로 관리한다.

### Boundary

- Project repo 는 PRD/HLD/ADR/runbook/acceptance tests/implementation plan/current docs 의 canonical 위치다.
- 외부 knowledge base 는 project hub 요약, reusable lesson, cross-project principle, resource note, project 간 링크의 canonical 위치다.
- 같은 결정을 두 곳에 복제하지 않는다 — project-specific 결정은 project repo ADR/DEC 에 두고, 필요하면 외부 knowledge base 에서 링크로 참조한다.

### Categorization rules

후보가 발견되면 다음 매핑을 따라 분류한다 — `Kind` 어휘는 [`templates/EXTRACTION_TEMPLATE.md`](templates/EXTRACTION_TEMPLATE.md) 의 allowed values 와 일치한다.

- 결정이 project-specific → project repo 의 ADR / DEC 에 둔다. 필요하면 외부 knowledge base 에서 링크로 참조한다. Extraction packet 에 별도 row 를 만들지 않는다.
- 결정이 cross-project / 근본 원칙 수준 → `adr_candidate` 또는 `lesson_candidate` 로 등록한다.
- 교훈이 cross-project 로 재사용 가능 → `lesson_candidate` 로 등록한다.
- 외부 reference / 개념 / 도구가 재사용 가능 → `resource_candidate` 로 등록한다.
- Project hub 의 stable state 변화 → `project_hub_update` 로 등록한다.
- Stale / rejected / sensitive / cross-project 가치 없는 project-specific 디테일 → `do_not_promote` 로 등록한다.
- 답이 없는 채로 보존할 가치가 있는 질문 → `open_question` 으로 등록한다.
- 해본 결과 안 되는 것으로 판명된 접근 → `negative_knowledge` 로 등록한다.

### When to prepare an extraction packet

- Major milestone 종료 — reusable cross-project 지식 또는 ADR-level 결정 후보가 도출됐을 때
- Final retrospective — 프로젝트 종료 시 항상
- Major discovery note 종료 — 다회차 외부 input(AI 상담, 리서치)이 안정된 결론에 도달했을 때
- 닫는 register entry — Q / DEC / SPIKE 가 reusable lesson 을 만들었을 때

### How

1. [`templates/EXTRACTION_TEMPLATE.md`](templates/EXTRACTION_TEMPLATE.md) 을 복사한다.
2. Extraction candidate table 에 후보를 채운다 — `Kind` 와 `Action` 어휘를 따른다.
3. `Do not promote` 를 검토한다. 빈 채로 두지 않는다 — 검토 후 실제로 없을 때만 `None — reviewed` 라고 명시한다.
4. Source anchor (repo / path / commit / PR / ADR / DEC / Q) 를 가능한 한 채운다. 모르면 `anchor missing` 으로 명시한다.
5. Created / Modified / Promoted / Dropped 보고 포맷으로 결과를 요약한다.

### Candidate vs promoted

- Extraction packet 의 모든 row 는 candidate 다.
- 실제 promotion 은 외부 knowledge base 의 review / ingestion 프로세스가 결정한다.
- Project 측에서 `promote` action 으로 표시한 후보도 외부 knowledge base 가 거절할 수 있다.

### Drop semantics

`drop` 은 "외부 curated knowledge base 에 승격하지 않음" 을 의미한다.

- Project repo / git history / transcript / artifact store / 원본 register entry 의 삭제를 의미하지 않는다.
- 다음은 일반적으로 drop 한다 — raw Q&A transcript, PRD wording draft, 임시 비교표, rejected recommendation, stale plan, 기존 lesson/resource 의 중복, 외부 knowledge base 에 부적합한 sensitive 내용, cross-project 가치가 없는 project-specific 구현 디테일.

### Raw Q&A handling

- Raw Q&A 는 source material 로 활용한다.
- Promotion 전에 distill 한다 — lesson 한 줄, resource note, 또는 ADR candidate 로 정리한다.
- Raw transcript 자체는 외부 knowledge base 가 명시적으로 transcript 를 원하지 않는 한 promote 하지 않는다.

### Source anchors

- Repo / path / commit / PR / ADR / DEC / Q 를 가능한 한 채운다.
- 알 수 없으면 `anchor missing` 으로 명시한다. 추측해서 만들지 않는다.

### Knowledge-base neutrality

이 boilerplate 는 특정 knowledge base 에 종속되지 않는다. `alxdr3k/second-brain`
같은 vault 가 흔한 target 이지만 강제 사항은 아니다. 외부 knowledge base 측
정책 (folder layout, frontmatter, ingestion rule, sensitivity classification) 은
해당 knowledge base 의 자체 문서가 따로 관리한다 — boilerplate 에 그 본문을
복사하지 않는다.
