# 10 Project Retrospective

프로젝트 중/후의 회고. Milestone마다 갱신하고, 종료 시 외부 knowledge base
승격을 위한 extraction packet을 준비한다.

회고는 "무엇을 배웠는가"를 기록하고, extraction packet은 "그 중 무엇을 외부
knowledge base로 승격할 후보인가"를 기록한다. 두 단계를 분리한다.

## Cadence

- Milestone 회고: milestone 종료 시 아래 형식으로 추가한다.
- Final 회고: 프로젝트 종료 시 `templates/EXTRACTION_TEMPLATE.md`를 채운다.

승격 자체는 외부 knowledge base의 자체 review / ingestion 프로세스를 통해
이뤄진다. 회고에서 candidate로 표시했다고 해서 자동 승격되는 것은 아니다.

## Milestone Retrospective — P0-M3 Documentation Migration

- Date: in progress
- Attendees: maintainers / Codex

### What went well

- Existing PRD/HLD/ADR content was already rich enough to seed the boilerplate structure.
- README feature matrix clearly separates implemented behavior from target design.

### What didn't

- PRD phase checkboxes had drifted from actual implementation status.
- HLD package map contains target packages that are not implemented yet.

### What confused us

- Existing `FR-*` IDs differ from boilerplate `REQ-*` examples.
- ADR-003 describes finer-grained concurrency than current code implements.

### Lesson candidates

| Candidate | 간단 설명 | Cross-project 가치? | Promote later? |
|---|---|---|---|
| Keep target design separate from current implementation docs | PRD/HLD can describe target architecture, but `docs/current/*` must describe code reality. | yes | later |
| Preserve old document paths during documentation migrations | Compatibility stubs avoid breaking active links while canonical paths change. | yes | later |

### Actions

| Action | Owner | Due |
|---|---|---|
| Resolve `Q-001` requirement ID strategy. | maintainers |  |
| Resolve `Q-002` ADR-003 vs current mutex behavior. | maintainers |  |

---

## Final Retrospective

Project is not complete. Fill this section when the project closes.

### Extraction packet

Use `templates/EXTRACTION_TEMPLATE.md` when preparing external knowledge-base
promotion candidates. Do not promote raw transcript or stale drafts directly.
