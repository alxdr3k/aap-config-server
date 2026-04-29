---
name: verify
description: 요구사항을 빠짐없이 구현했는지 확인하고 수정. 누락 없을 때까지 반복
---
<!-- my-skill:generated
skill: verify
base-sha256: d4f9268415f98c5922be0283a86fc5f9c80e0342f9099b83b1b451521b3d4ea4
overlay-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
output-sha256: d4f9268415f98c5922be0283a86fc5f9c80e0342f9099b83b1b451521b3d4ea4
do-not-edit: edit .codex/skill-overrides/verify.md instead
-->

요구사항을 빠짐없이 구현했는지 확인하고 수정한다. 누락이 없을 때까지 반복한다.

## 절차

1. 사용자 요청, 최근 diff, 관련 issue/PR/review 내용을 요구사항 목록으로 분해한다.
2. `update_plan`으로 검증 항목을 만든다. 각 항목은 파일/영역 힌트를 포함한다.
3. 관련 코드와 테스트를 읽어 요구사항별 구현 여부를 확인한다.
4. 누락, 버그, 테스트 공백이 있으면 바로 수정한다. 수동 편집은 `apply_patch`를 사용한다.
5. 수정마다 가장 작은 관련 테스트를 먼저 실행한다.
6. 마지막에는 repo가 정의한 전체 검증 명령을 실행한다.

## 원칙

- 검증 명령을 새로 만들지 않는다. 필요한 명령은 `docs/current/TESTING.md`,
  `docs/TESTING.md`, package/Makefile/language-native test config, repo guidance에 있는 것을 우선 사용한다.
- 실패한 검증은 같은 루프 안에서 원인을 고치고 다시 실행한다.
- 요구사항이 모호하면 합리적인 해석을 먼저 적고 진행한다. 사용자 결정 없이는
  안전하게 판단할 수 없는 경우에만 멈춰서 질문한다.
- 최종 보고에는 확인한 요구사항, 수정한 누락, 실행한 검증, 남은 리스크를 포함한다.
