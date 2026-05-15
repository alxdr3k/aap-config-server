---
name: dev-cycle
description: "전체 개발 사이클: sync -> discover -> implement -> verify -> review -> land. 플래그: --loop [N], --phase <id>, --opus-audit-every <N>"
---
<!-- my-skill:generated
skill: dev-cycle
base-sha256: 1244abcddbafb29dbeba81ddb2358e01188e4df28e611bfe62af2a2834d124c7
overlay-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
output-sha256: 1244abcddbafb29dbeba81ddb2358e01188e4df28e611bfe62af2a2834d124c7
do-not-edit: edit .codex/skill-overrides/dev-cycle.md instead
-->

# Dev Cycle

## Flags

- `--loop`: cycle 완료 후 반복한다. Step 3에서 **ALL CLEAR**이고 자동 승격한 후보가 없으면 종료한다.
- `--loop N`: 최대 N회 반복한다. **ALL CLEAR**이고 자동 승격한 후보가 없으면 N회 전에도 종료한다.
- `--phase <id>`: 탐색과 구현 범위를 해당 roadmap / task / milestone / track / phase / slice id로 제한한다. 값을 파싱하거나 변환하지 않는다.
- `--opus-audit-every <N>`: cycle 종료 후 직전 cycle index가 N의 배수면 read-only Opus 환기 audit pass를 1회 수행한다. N은 양의 정수다. 누적된 변경 방향성을 Opus가 검토하고 결과만 brief log에 기록한다. 코드 수정/commit/push는 하지 않는다.

## Invariants

- Step이 끝나면 사용자 입력 없이 다음 Step으로 진행한다.
- 멈추는 경우: 자동 승격할 후보가 없는 **ALL CLEAR**, 사용자 승인 없이는 안전하지 않은 분기, 인증/권한/destructive git state, 해결 불가 blocker.
- 사용자에게 보이는 보고, brief, finding, 질문은 한국어로 작성한다. 코드, 명령, 파일명, 원문 인용은 원문 언어를 유지한다.
- repo type, review base, sync, brief log, risk issue 처리는 helper가 담당한다.
- JSON brief 처리는 `jq`가 필요하다. `jq`가 없어서 helper가 실패하면 dependency blocker로 보고한다.
- 자동 승격은 repo의 현재 source of truth가 blocker 해소를 명시적으로 증명하는 status-only 변경에만 허용한다. 사용자 결정, 외부 관찰, 권한, 제품 판단, 추측이 필요하면 승격하지 않는다.
- helper 경로는 아래 순서로 찾는다.

```bash
DEV_CYCLE_HELPER=".agents/scripts/dev-cycle-helper.sh"
[ -x "$DEV_CYCLE_HELPER" ] || DEV_CYCLE_HELPER="$HOME/.agents/scripts/dev-cycle-helper.sh"
[ -x "$DEV_CYCLE_HELPER" ] || { echo "Missing dev-cycle-helper.sh"; exit 1; }
```

## Brief Log

새 실행의 첫 cycle에서만 초기화한다.
`init-brief`는 `.dev-cycle/dev-cycle-run-id`, `.dev-cycle/dev-cycle-start-epoch`, `.dev-cycle/dev-cycle-run.json`, `.dev-cycle/dev-cycle-briefs.jsonl`, `.dev-cycle/dev-cycle-briefs.md`를 만들고 export를 출력한다. Bash 호출 사이에 export가 사라져도 `finish-cycle-json`과 `summary-json`은 저장된 state를 검증해 이어 쓴다. JSONL이 canonical 기록이고 Markdown은 helper가 렌더링한 human log다.

```bash
eval "$("$DEV_CYCLE_HELPER" init-brief)"
```

이어서 실행하는 cycle이라면 `DEV_CYCLE_RUN_ID`와 `DEV_CYCLE_BRIEF_LOG`를 재사용하기 전에 반드시 검증한다.

```bash
"$DEV_CYCLE_HELPER" validate-brief "$DEV_CYCLE_RUN_ID" "$DEV_CYCLE_BRIEF_LOG"
```

검증 실패 또는 확신이 없으면 새 실행으로 보고 `init-brief`를 다시 실행한다. 단, cycle 종료 시점의 누락된 환경변수를 복구하려고 `init-brief`를 다시 실행하지 않는다. 그것은 새 brief log를 시작한다.

Cycle 종료 시 JSON payload를 `finish-cycle-json` stdin으로 넘긴다. **ALL CLEAR, blocked, publish 금지로 종료하는 경우도 먼저 `finish-cycle-json`을 실행한다.** helper는 JSON을 검증하고 repo/run metadata를 보강한 뒤 `.dev-cycle/dev-cycle-briefs.jsonl`에 append한다. 그 다음 사용자 브리핑 Markdown을 렌더링해 `.dev-cycle/dev-cycle-briefs.md`에 append하고 stdout ack JSON의 `rendered_markdown`에 넣는다.

```bash
"$DEV_CYCLE_HELPER" finish-cycle-json <<'JSON'
{
  "schema_version": 1,
  "cycle": 1,
  "result": "landed",
  "actions": [
    {"kind": "implement", "summary_ko": "이번 cycle에서 실제로 한 일"}
  ],
  "conclusion": {"summary_ko": "사용자가 바로 이해할 결론", "reason_ko": "선택"},
  "changes": [
    {"path": "수정 파일 또는 영역", "summary_ko": "선택"}
  ],
  "change_scope": {"kind": "docs_only_contract", "changed_files_count": 2, "contract_surface": true, "review_required": true},
  "verification_plan": {"profile": "docs_contract", "full_ci_required": false},
  "verification": [
    {"kind": "test", "status": "pass", "summary_ko": "검증 결과"}
  ],
  "review_land": {"status": "pushed", "summary_ko": "리뷰/반영 결과"},
  "next_candidates": [
    {"id": "후보 id", "status": "planned", "summary_ko": "무슨 작업인지", "unblock_ko": "시작 조건"}
  ],
  "auto_promotion_candidates": [
    {"id": "검토한 후보 id", "status": "planned", "summary_ko": "검토한 후보", "eligible": true, "reason_ko": "자동 승격 가능/불가 이유"}
  ],
  "auto_promotions": [
    {"id": "승격한 후보 id", "status_before": "planned", "status_after": "ready", "summary_ko": "승격 내용", "path": "수정한 status 파일", "reason_ko": "승격 근거"}
  ],
  "risks": [
    {"summary_ko": "남은 실제 리스크", "next_action_ko": "후속 조치"}
  ]
}
JSON
```

필수 필드: `schema_version`, `cycle`, `result`, `actions`, `conclusion.summary_ko`, `verification`, `review_land`, `risks`. `review_ship`은 legacy alias로만 허용한다. `cycle`은 1부터 시작하는 정수이고, `actions`와 `verification`은 비어 있으면 안 되며 각 항목에 사용자-visible `summary_ko`를 쓴다. 권장 `result` 값은 `landed`, `blocked`, `all_clear`, `doc_fix_needed`다. 리스크가 없으면 `risks: []`를 쓴다. Ready slice가 없어 `ALL CLEAR`로 끝낼 때는 실제 수행한 탐색/판단을 `actions`와 `conclusion`에 쓰고, ready가 아닌 다음 검토 후보는 최대 3개까지 `next_candidates`에 둔다. 자동 승격을 검토했다면 `auto_promotion_candidates`에 검토한 후보와 가능/불가 이유를 쓰고, 실제 승격한 항목은 `auto_promotions`에 쓴다. `change_scope`, `verification_plan`, 후보/승격 필드는 schema_version 1의 optional extension이다. 후보는 후속 안내이지 risk issue 대상이 아니므로 실제 리스크가 없으면 `risks: []`다.

`finish-cycle-json` stdout은 tool output일 뿐 사용자에게 자동 전달되지 않는다. stdout ack JSON에서 `rendered_markdown`만 추출해 사용자에게 그대로 보여준다. 이 메시지가 사용자에게 보이기 전에는 다음 `update_plan`, Step 1, Step 2, discovery, 파일 탐색, 또는 tool call을 하지 않는다. 한 줄짜리 "사이클 N 완료" 요약으로 대체하면 안 된다. `--loop` 또는 `--loop N`이면 user-visible brief를 보낸 뒤 ack의 `auto_promotions_count`로 loop 지속 여부를 판단한다.

`.dev-cycle/dev-cycle-briefs.jsonl`과 `.dev-cycle/dev-cycle-briefs.md`는 helper가 관리하는 append-only state다. cycle 결과를 고치려고 직접 편집하지 않는다. 특히 issue 생성 실패를 숨기려고 남은 risk를 `없음`으로 바꾸면 안 된다. 기존 env var 기반 `finish-cycle`은 legacy shim으로만 사용한다.

## Step 1 - Sync

새 실행의 첫 cycle에서는 항상 실행한다. `--loop` 또는 `--loop N`의 두 번째 이후 cycle에서는 repo type에 따라 분기한다.

- Direct-push repo: 같은 loop 실행에서 직전 cycle이 `landed`였거나 legacy `shipped`였거나 `all_clear` + `auto_promotions_count > 0`로 끝났고 local `main`이 clean이면 Step 1을 반복하지 않고 Step 2로 간다. 같은 세션의 직전 push 결과를 기준으로 다음 task를 고른다.
- Standard repo: PR merge 후 base branch sync가 필요하므로 두 번째 이후 cycle에서도 Step 1을 실행한다.
- 새 실행, context reset 이후 확신이 없는 경우, 직전 cycle이 `landed`/legacy `shipped`가 아닌 경우, branch/working tree가 예상과 다르면 첫 cycle처럼 Step 1을 실행한다.

```bash
"$DEV_CYCLE_HELPER" sync
REPO_TYPE="$("$DEV_CYCLE_HELPER" repo-type)"
REVIEW_BASE="$("$DEV_CYCLE_HELPER" review-base)"
"$DEV_CYCLE_HELPER" record-audit-baseline
echo "Repo type: $REPO_TYPE"
echo "Review base: $REVIEW_BASE"
```

`record-audit-baseline`은 첫 cycle이 끝나기 전 한 번만 post-sync HEAD를 `dev-cycle-run.json.base_sha`에 기록하고, 그 이후 호출은 idempotent로 무시한다. 첫 audit window의 START_SHA fallback이 sync로 가져온 upstream commits를 포함하지 않도록 한다.

## Step 2 - Discover

로컬에서 직접 탐색한다. 읽기 순서는 repo guidance/README, `docs/context/current-state.md`가 있으면 해당 파일, `docs/04_IMPLEMENTATION_PLAN.md`가 있으면 해당 파일의 current/status ledger, thin current docs, 작업 관련 source/tests 순서다. 긴 design/archive/generated 문서는 필요할 때만 읽는다.

판단 기준:

- 구현 후보를 우선하되 commit 가능한 task/slice 크기로 자른다.
- `NEXT TASK`는 ready, unblocked, authorized 작업만 선택한다. `planned`, `deferred`, `blocked` scope는 inventory로 보고할 수 있지만 실행 큐로 보지 않는다.
- ready slice가 없으면 `ALL CLEAR`로 판단하되, 다음에 검토할 non-ready slice를 최대 3개까지 함께 기록한다. 각 후보에는 status, 검토/해제 조건, 필요한 사용자 결정이나 외부 입력을 포함한다. 기계적으로 자동 승격 가능해 보이는 후보도 표시하되 Step 2에서는 파일을 수정하지 않는다.
- docs-only는 구현할 코드 작업이 없고 문서만 틀린 경우에만 선택한다.
- 문서와 코드가 둘 다 필요하면 구현 작업으로 반환하고 docs update를 acceptance criteria에 포함한다.
- `--phase <id>`가 있으면 해당 id 범위만 본다.

반환은 아래 중 하나:

**## NEXT TASK**
roadmap 위치 또는 task/slice id, 파일/영역, gate/acceptance criteria, docs update, validation을 포함한 하나의 작업.

**## DOC FIX NEEDED**
docs-only 수정 목록.

**## ALL CLEAR**
현재 상태 요약. ready slice가 없어서 종료하는 경우 다음 검토 후보를 포함한다.

## Step 3 - Decide

- **ALL CLEAR**: 종료하기 전에 Auto-Promotion Gate를 실행한다.
- **NEXT TASK**: Step 4로 간다.
- **DOC FIX NEEDED**: Step 4로 가되 작업 type은 `docs`.

### Auto-Promotion Gate

Step 2가 **ALL CLEAR**를 반환하면 아래 순서로 ready 자동 승격 가능성을 확인한다.

1. Step 2의 다음 검토 후보와 같은 roadmap/status ledger의 인접 slice를 확인한다. `--phase <id>`가 있으면 그 범위 밖 후보는 제외한다.
2. 후보가 자동 승격 가능한 조건은 모두 만족해야 한다: 현재 repo 문서/ledger/source가 선행 dependency나 gate 완료를 명시적으로 증명한다, 남은 조건이 status-only 문서 변경이다, 사용자 결정/외부 관찰/권한/제품 판단이 필요하지 않다, 승격 후 실행할 acceptance가 충분히 구체적이다.
3. 검토한 후보는 모두 `auto_promotion_candidates`에 기록한다. 자동 승격하지 않은 후보도 `eligible:false`와 이유를 남긴다.
4. 자동 승격 가능한 후보가 있으면 파일 수정 전에 repo type별 작업 위치를 확정한다. Direct-push repo는 `main`에서 진행한다. Standard repo는 Step 4의 branch 규칙을 먼저 적용해 base branch에서 직접 수정하지 않고, 새 branch를 만들었다면 `DEV_CYCLE_WORK_BRANCH`에 기록한다.
5. authoritative roadmap/status 파일을 수정해 `ready`로 승격하고, 각 변경을 `auto_promotions`와 `changes`에 기록한다. 여러 후보가 같은 근거로 기계적으로 승격 가능하면 모두 승격한다.
6. 승격 변경이 있으면 이번 cycle은 promotion-only cycle로 보고 Step 5부터 진행한다. 검증/리뷰/반영/PR merge gate는 일반 변경과 동일하게 적용한다. cycle 결과는 `result:"all_clear"`로 기록하고, `review_land`에는 승격 변경의 push/PR/merge 결과를 쓴다.
7. 승격 변경이 없으면 `result:"all_clear"` payload로 `finish-cycle-json`을 실행한 뒤 종료한다. 실제 탐색 행동과 결론은 `actions`/`conclusion`에 쓰고, Step 2의 후보는 `next_candidates`에 포함한다. 실제 리스크가 없으면 `risks: []`다.

`--loop`가 아닌 실행에서는 자동 승격 후 새로 ready가 된 작업을 같은 invocation에서 구현하지 않는다. 승격 내역을 brief에 남기고 종료한다. `--loop` 실행에서는 승격 변경이 원격 반영된 뒤 user-visible brief를 보여주고 다음 cycle로 계속 진행한다.

## Step 4 - Implement

- Direct-push repo: `main`에서 직접 작업한다.
- Standard repo: 작업 전 현재 branch를 확인한다. 현재 branch가 `$REVIEW_BASE` 또는 default/base branch이면 구현 전에 `codex/<short-description>` 또는 `<type>/<short-description>` 작업 브랜치를 새로 만든다. 이미 non-base 작업 브랜치면 유지한다.
- Standard repo에서는 작업 브랜치 이름을 `DEV_CYCLE_WORK_BRANCH`로 기록해 Step 8 push, Step 9 merge/cleanup에서 같은 브랜치를 사용한다. base branch에서 직접 구현하지 않는다.
- `update_plan`으로 작은 작업 단위를 만들고, 수동 편집은 `apply_patch`를 사용한다.
- Step 2의 task/slice를 구현한다. docs update가 acceptance criteria면 같은 cycle에서 처리한다.
- `--phase <id>` 범위를 벗어난 작업은 하지 않는다.

## Step 5 - Verify

먼저 변경 범위와 검증 프로필을 계산한다.

```bash
CHANGE_SCOPE_JSON="$("$DEV_CYCLE_HELPER" change-scope)"
```

- `verification_profile.full_ci_required == true`: `verify` 스킬 절차를 같은 세션에서 수행하고 repo guidance에 맞는 targeted verify를 수행한다.
- `verification_profile.full_ci_required == false`: 문서/계약 변경에 맞는 검증만 수행한다. 기본은 `git diff --check`이고, command/skill/schema/status 같은 contract docs는 render/generated consistency, schema/example validation처럼 실제로 의미 있는 검증을 추가한다. unit/app CI는 repo guidance가 touched docs에 명시적으로 요구할 때만 실행한다.
- 어떤 profile이든 Review Pass는 생략하지 않는다. 문서는 코드만큼 리뷰 대상이다.
- cycle payload에는 `CHANGE_SCOPE_JSON.change_scope`를 `change_scope`로, `CHANGE_SCOPE_JSON.verification_profile`을 `verification_plan`으로 기록한다.

- pass 또는 누락 수정 완료: Step 6.
- 해결 불가 blocker: `result:"blocked"` payload로 `finish-cycle-json`을 실행하고 중단.

## Step 6 - Review

리뷰 직전 다시 계산한다.

```bash
REVIEW_BASE="$("$DEV_CYCLE_HELPER" review-base)"
CHANGE_SCOPE_JSON="$("$DEV_CYCLE_HELPER" change-scope)"
REVIEW_DOSSIER_JSON="$("$DEV_CYCLE_HELPER" review-dossier)"
```

- `CHANGE_SCOPE_JSON.review_inputs`에 있는 base range, staged diff, unstaged diff, untracked files를 모두 리뷰한다.
- `REVIEW_DOSSIER_JSON.review_dossier`는 diff 크기, 파일 확산, 계약/중요 경로처럼 script가 계산 가능한 신호만 담는다. dossier가 없거나 helper가 실패하면 `CHANGE_SCOPE_JSON`과 아래 위험 trigger를 수동으로 적용한다.
- `review_dossier.risk_triggers`는 200/400라인 초과, 변경 파일 5개 초과, 보안/영속성/설정/배포/공개 command 경로 같은 휴리스틱 위험 신호다. reviewer 입력 정보로 참고한다.
- Direct-push repo와 Standard repo 모두 같은 입력 규칙을 쓴다. Standard repo도 `$REVIEW_BASE...HEAD`만 보지 않는다. commit 전 local diff와 untracked files가 있으면 반드시 Review Pass 입력에 포함한다.
- Review Pass는 diff review와 impact triage/scan이 함께 통과한 상태다. impact scan을 review OK 이후 별도 단계로 두지 않는다.
- Impact triage: docs/typo/slice/test-only처럼 외부 surface가 없으면 `Impact: local only`로 끝낸다.
- 위험 trigger: shared helper/API, command/skill, deploy/build/test infra, config/env/schema, persistence, auth/security, public CLI/output, 파일 경로/계약 변경, 변경 파일 5개 초과. 해당하면 변경된 symbol/path/env/command를 `rg`로 repo 전체에서 추적해 call site/docs/tests/deploy refs를 확인한다.
- 리뷰 결과는 그대로 수용하지 말고 적대적/비판적으로 재평가한다. 각 finding마다 주장, 근거, 재현 가능성, 실제 영향, severity, 범위 적합성을 확인하고 duplicate/이미 처리됨/추측성 edge/단순 취향이면 근거와 함께 제외한다.
- 유효한 finding은 가장 합리적인 해결 방식을 고른다: root-cause code fix, test 보강, 문서/계약 정정, 요구사항 clarification, 또는 사용자 결정 요청. 리뷰를 만족시키려고 보안/검증/계약을 약화하거나 symptom-only patch를 만들지 않는다.
### Review Loop

**Step 6 pass 조건: 리뷰어가 직접 실행되어 actionable finding 0을 반환한 경우에만 통과한다.** finding을 수정했다고 스스로 "pass"를 선언하는 것은 금지다. 수정 후에는 반드시 리뷰어를 재실행해야 한다.

각 pass는 다음 순서를 엄격히 따른다:

1. **리뷰어 실행** (Codex 또는 Opus) → finding 목록 수신
2. finding을 적대적/비판적으로 재평가 → 유효한 actionable finding 분류
3. 유효한 actionable finding이 **0이면**: Step 7로 간다 (**Review Pass**)
4. 유효한 actionable finding이 **있으면**: batch 수정 → targeted verify → **1로 돌아가 리뷰어를 반드시 재실행한다**

버그, regression, missing test, security/auth/data-loss, schema/runtime/docs 불일치 findings를 batch로 정리한다. fix가 surface를 넓히지 않았으면 다음 pass는 추가 diff 중심으로 본다.

합리적인 finding이 더 이상 나오지 않을 때까지 반복하되 hard upper는 20회다. 사용자 결정이 필요한 finding, 또는 fix를 적용했는데 같은 위치에 같은 주장이 다시 올라와 합의가 어려운 disagreement는 GitHub issue로 남기고 Step 7로 간다. 20회를 채우고도 남은 actionable finding이 있으면 GitHub issue로 남기고 Step 7로 간다. pass 횟수는 매 pass 시작 시 `update_plan` 체크박스에 `[Review pass N/20]` 형태로 기록해 context reset 이후에도 복원할 수 있도록 한다.

## Step 7 - Local Checks

`CHANGE_SCOPE_JSON.verification_profile`을 다시 확인한다.

- `full_ci_required == true`: repo guidance와 docs/testing에 정의된 full/pre-PR 검증을 실행한다.
- `full_ci_required == false`: Step 5에서 정한 문서/계약 검증 프로필만 반복한다. docs-only 변경 때문에 의미 없는 unit/app CI나 전체 CI를 기본 실행하지 않는다.
- 실패하면 수정 후 Step 5 또는 Step 7의 관련 검증을 반복한다.

## Step 8 - Land

- Direct-push repo: 의도한 파일만 stage, commit, `git push origin main`. PR은 만들지 않는다.
- Standard repo: 의도한 파일만 stage, commit, `DEV_CYCLE_WORK_BRANCH` push, GitHub app 또는 `gh pr create --base "$REVIEW_BASE" --head "$DEV_CYCLE_WORK_BRANCH" --draft=false`로 **draft가 아닌 open PR**을 생성한 뒤 Step 9로 간다.
- 사용자가 publish 금지를 명시했으면 여기서 멈추고 local state만 보고한다.

## Step 8.5 - Cycle Brief Gate

- 반영(land), ALL CLEAR, blocked, publish 금지 등 cycle을 끝내는 모든 경로에서 `finish-cycle-json`을 실행한다.
- `finish-cycle-json` ack JSON의 `rendered_markdown`을 사용자에게 먼저 보여준다.
- 이 브리핑이 사용자에게 보이기 전에는 `update_plan`으로 다음 task를 열거나, 다음 loop discovery를 시작하거나, 파일을 읽거나, 다른 tool을 호출하지 않는다.
- "사이클 N 완료" 같은 임의 요약은 허용되지 않는다. helper가 생성한 `rendered_markdown`을 임의로 축약하지 않는다.

## Step 9 - PR Merge Gate

- Direct-push repo: Step 9를 건너뛰고 cycle 종료 처리로 간다.
- Standard repo: 방금 연 open PR에 대해 `codex-loop` 스킬을 같은 세션에서 실행한다.
- `codex-loop`는 review feedback 처리, checks 확인, merge까지 완료해야 한다. 해당 PR이 merge되기 전에는 cycle을 마치거나 다음 loop로 넘어가지 않는다.
- merge 완료 후 `$REVIEW_BASE`로 checkout하고 `git pull --ff-only origin "$REVIEW_BASE"`로 sync한다.
- sync 후 local `DEV_CYCLE_WORK_BRANCH`를 삭제한다. squash merge 때문에 일반 삭제가 실패하면, PR merge와 clean working tree를 확인한 뒤 local branch만 강제 삭제한다. 이 cleanup이 끝나기 전에는 cycle을 마치거나 다음 loop로 넘어가지 않는다.
- Step 1 기준 상태가 깨끗한지 확인한 뒤 cycle 종료 처리를 한다.
- timeout, merge block, unresolved actionable feedback이면 `result:"blocked"` payload로 `finish-cycle-json`을 실행하고 중단한다.

## Step 10 - Opus Audit Gate

`--opus-audit-every <N>` flag가 있고 직전 cycle index가 N의 배수일 때만 수행한다. 그 외에는 이 step을 건너뛴다.

- 현재 Codex 환경에서 명시적으로 허용된 read-only Opus reviewer를 호출한다. 입력은 다음으로 제한한다:
  - 직전 N cycle의 brief log entries (`.dev-cycle/dev-cycle-briefs.jsonl`의 마지막 N개). **이게 canonical 검토 단위다.** cycle ↔ commit은 1:1이 아닐 수 있으므로 (zero-commit `all_clear` cycle, PR 피드백 다중 commit, squash 안 하는 direct-push repo 등) git range는 commit 갯수가 아니라 cycle entry의 `repo.head` 기준으로 잡는다.
  - 직전 N cycle의 누적 git log와 diff. range는 brief log에서 뽑는다:
    ```bash
    # after_cycle = 방금 끝낸 cycle, N = audit_every
    START_SHA="$(jq -r --argjson c "$((after_cycle - N))" 'select(.cycle == $c) | .repo.head' .dev-cycle/dev-cycle-briefs.jsonl | head -1)"
    END_SHA="$(jq -r --argjson c "$after_cycle" 'select(.cycle == $c) | .repo.head' .dev-cycle/dev-cycle-briefs.jsonl | head -1)"
    # 첫 audit (after_cycle == N)이면 cycle 0이 없어 START_SHA가 비어있다. init-brief가 .dev-cycle/dev-cycle-run.json에 저장한 base_sha를 fallback으로 쓴다.
    if [[ -z "$START_SHA" ]]; then
      START_SHA="$(jq -r '.base_sha // empty' .dev-cycle/dev-cycle-run.json)"
    fi
    git log --oneline "${START_SHA}..${END_SHA:-HEAD}"
    git diff --stat "${START_SHA}..${END_SHA:-HEAD}"
    ```
  - 현재 `docs/context/current-state.md` 또는 동등한 status ledger (있으면)
  - 직전 N cycle 동안 누적된 dossier `risk_triggers` 요약 (선택)
- Opus는 누적된 변경 방향성, codex finding 적용으로 인한 over-fit, 일관성 침식, 누적 ad-hoc 패턴, 빠진 후속 작업을 본다. 코드 수정/commit/push/PR 변경은 하지 않는다.
- 결과를 `audit-pass-json`에 넘기되 `--opus-audit-every`의 N을 첫 인자로 전달해 정확한 window 길이를 helper가 강제하도록 한다.

```bash
"$DEV_CYCLE_HELPER" audit-pass-json "$N" <<'JSON'
{
  "schema_version": 1,
  "after_cycle": <방금 끝낸 cycle 번호>,
  "over_cycles": [<검토한 cycle 번호 배열>],
  "summary_ko": "audit 결론",
  "findings": [
    {"summary_ko": "발견한 패턴", "severity": "high|medium|low (선택)"}
  ],
  "recommended_next": [
    {"id": "후속 task id 또는 라벨", "summary_ko": "권장 작업", "unblock_ko": "시작 조건 (선택)"}
  ],
  "no_action_reason_ko": "환기 결과 actionable 없음 (선택)"
}
JSON
```

- `over_cycles`는 정확히 N개, 1씩 증가하는 contiguous 배열로 채우고 마지막 항목이 `after_cycle`과 일치해야 한다 (예: `after_cycle=6`, `N=3`이면 `[4, 5, 6]`). 길이가 N이 아니거나 누락/future/non-contiguous cycle이면 helper가 거부한다.
- ack JSON의 `rendered_markdown`을 사용자에게 그대로 보여준 뒤 다음 cycle 또는 종료로 진행한다.
- audit pass가 실패하면 (Opus 실행 오류, `audit-pass-json` 거부, helper 오류 등 valid한 audit record가 만들어지지 않은 모든 케이스) **loop를 그 자리에서 멈추고 사용자에게 사유를 보고한다.** fail-open으로 진행하면 환기가 누락된 채로 후속 cycle이 돌아 audit gate가 무의미해진다. 사용자가 명시적으로 "이번 audit 건너뛰고 진행"을 지시한 경우에만 다음 cycle로 넘어간다.
- `recommended_next`는 다음 cycle Step 2 discovery에 참고 정보로 첨부할 수 있다.

## Loop

`--loop` 또는 `--loop N`이면 cycle brief를 append하고 사용자에게 보여준 뒤, Step 10 (Opus Audit Gate) 진입 조건이 맞으면 audit pass를 1회 수행한 다음 다음 cycle로 간다. 단, 직전 cycle이 `all_clear`이고 ack의 `auto_promotions_count`가 `0`이면 loop를 종료한다. 직전 cycle이 `all_clear`라도 `auto_promotions_count > 0`이면 새 ready 작업이 생긴 것이므로 다음 cycle로 계속 진행한다. Direct-push repo의 같은 loop 실행에서 직전 cycle이 `landed`였거나 legacy `shipped`였거나 `all_clear` + `auto_promotions_count > 0`로 끝났고 local `main`이 clean이면 다음 cycle은 Step 2부터 시작한다. 그 외에는 Step 1로 돌아간다. 이어받은 cycle에서는 brief log의 run id와 git log를 확인해 현재 loop의 이전 cycle만 복원한다.

종료 시 `"$DEV_CYCLE_HELPER" summary-json`을 실행하고 summary JSON의 `rendered_markdown`을 `최종 브리핑`으로 사용자에게 보여준다. 임의로 축약하지 않는다.
