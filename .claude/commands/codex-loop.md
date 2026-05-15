---
name: codex-loop
description: 현재 PR의 codex 리뷰를 기다리고 코멘트 수정 후 push, 통과 reaction을 받으면 정책에 맞춰 merge
---

현재 작업 중인 PR에 대해 codex 리뷰를 기다리고, 코멘트가 달리면 수정 후 push. 통과 reaction까지 반복한 뒤 PR을 정책에 맞춰 merge한다.

사용자에게 보이는 보고, feedback 정리, 질문은 한국어로 작성한다. 코드, 명령, 파일명, 원문 인용은 원문 언어를 유지한다.

## Flags

- `--opus-review`: feedback 타당성 검토를 dossier 라우팅과 무관하게 항상 Opus sub-agent로 실행한다.

## 경로 선택

실행 중인 에이전트의 가용 도구 목록에 `mcp__github__subscribe_pr_activity`가 포함되어 있으면 (전형적으로 Claude Code 웹 세션 + GitHub MCP가 연결된 상태) **Path A: 이벤트 구독**을 사용한다. tool이 없으면 (local Claude Code CLI에서 GitHub MCP 미연결, opencode, codex CLI 등) **Path B: 폴링 스크립트**를 사용한다. 환경 이름이 아니라 실제 도구 가용성으로 판단한다.

Path A 실행 중 `subscribe_pr_activity`/`unsubscribe_pr_activity` 호출이나 probe용 `gh api` 호출이 auth/scope/네트워크 등 진행을 막는 오류로 실패하면 즉시 Path B로 fallback한다. transient 오류는 같은 wake-up에서 1회 재시도해도 되지만, 영구 오류는 지체 없이 Path B 스크립트를 foreground로 실행해 polling-based 흐름으로 계속한다.

GitHub은 codex bot의 pass reaction(`+1`)을 webhook으로 전달하지 않는다. Path A에서도 reaction은 직접 GitHub API로 확인해야 한다.

## Model Routing

Claude Code에서 model-routed sub-agent를 사용할 수 있으면 아래 원칙을 따른다. 사용할 수 없거나 handoff 비용이 더 크면 같은 세션에서 수행한다.

- PR 감지, 구독/폴링, pass reaction 확인, review 요청 comment 작성은 Path A에서는 main session, Path B에서는 `wait-codex-review.sh`가 담당한다. 이 작업을 Haiku sub-agent로 대체하지 않는다.
- `--opus-review`가 있으면 새 feedback이 도착할 때마다 dossier 결과와 무관하게 항상 Opus sub-agent로 타당성 검토를 수행한다.
- `--opus-review`가 없을 때: feedback의 타당성 검토는 main session에서 수행한다. `dev-cycle-helper.sh review-dossier`의 `risk_triggers`는 reviewer 입력 정보로 활용한다.
- feedback 수정은 Sonnet/main execution을 기본으로 하고, 작은 수정에는 별도 worker를 만들지 않는다.
- Haiku 또는 Explore는 PR metadata/comment를 짧게 요약하거나 넓은 read-only 탐색을 압축할 때만 사용한다.
- 같은 PR에서 동일 파일군에 대해 3회 이상 review/fix가 반복될 때만 Opus reviewer resume을 고려한다. 기본은 이전 finding 요약 + incremental diff를 새로 전달한다.

## Path A: 이벤트 구독

### 진입

1. PR을 식별한다. 인자로 받은 PR 번호/URL이 있으면 그대로, 없으면 `gh pr view --json number,url,baseRepository,headRefName,headRepository -q .`로 현재 브랜치 PR을 찾는다. 감지 실패 시 사용자에게 PR 번호/URL을 요청한다.
2. baseline timestamp를 한 번 캡처한다. 우선순위: ① Events API PushEvent (`repos/<head_repo>/events`의 `refs/heads/<branch>` push 중 최신 `created_at`) → ② PR timeline (`repos/<owner>/<repo>/issues/<pr>/timeline`의 `committed`/`head_ref_force_pushed`) → ③ HEAD 커밋 `committer.date`. 미래 시각은 현재로 클램프한다. 이후 wake-up에서 baseline 이전 활동은 무시한다. **이 baseline을 `last_push_at`으로도 기억해 둔다 — silent-approval probe의 기준 시각이 된다.**
3. PR 본문 reaction과 자신의 issue comment의 `eyes` reaction을 확인한다. 둘 다 없으면 `@codex review` 본문의 issue comment를 1개 남긴다. 이 코멘트는 feedback으로 처리하지 않는다 (작성자가 자신이고 본문이 정확히 `@codex review`이면 제외). 동일 baseline에서 이 게시는 1회로 한정한다.
4. **구독 인계 (skill takes ownership)**: 진입 시점에 이미 같은 PR에 대한 구독이 존재할 수 있다 (예: harness가 사용자 "watch/babysit/monitor PR" 요청을 받아 자동 구독했거나, Claude가 이전 작업 맥락에서 직접 `subscribe_pr_activity`를 호출했음). 처리 규칙:
   - **이번 `/codex-loop` invocation 시작 이후 본 entry 단계에서 직접 `subscribe_pr_activity`를 이미 호출한 흔적이 conversation에 있으면** (예: 이전 wake-up 사이클의 entry) → 그대로 유지하고 추가 호출하지 않는다. 같은 invocation 경계 내에서만 "skill이 소유한" 구독으로 간주한다.
   - **그 외 모든 경우** (이전 invocation의 잔여 구독, harness 자동 구독, 다른 skill/명령에서 한 구독) → 먼저 `mcp__github__unsubscribe_pr_activity { owner, repo, pullNumber }`를 호출해 외부/오래된 구독을 끊고, 이어서 `mcp__github__subscribe_pr_activity { owner, repo, pullNumber }`를 호출해 skill 명의로 다시 구독한다. 이 PR의 구독 lifecycle은 현 invocation의 skill이 단독으로 관리한다.
   - 판단이 모호하면 unsubscribe → subscribe 시퀀스를 실행한다. 둘 다 idempotent이고 비용도 작다.
5. **턴을 종료한다**. background polling이나 sleep loop를 절대 만들지 않는다.

### Wake-up 처리

`<github-webhook-activity>` 메시지가 도착하면 다음 순서로 처리한다.

1. baseline 이후 새 `issue_comment` / `review` / `review_comment`가 있는가? (작성자 무관, 단 본인이 남긴 `@codex review` 코멘트는 제외). 있다면 **"Feedback 처리"로 진행한다**. codex 본인뿐 아니라 다른 reviewer/사용자 코멘트도 포함한다.
2. 새 코멘트는 없고 CI/check 완료 등 다른 이벤트만 있으면 상태만 기록한 뒤, `last_push_at` 이후 경과 시간을 보고 **silent-approval probe** 조건을 확인한다 (아래 참조).
3. 처리할 항목이 없으면 즉시 turn을 종료해 다음 이벤트를 기다린다. sleep/polling으로 깨어 있지 않는다.

silent-approval probe는 codex의 reaction-only 통과를 잡기 위한 것이고, 1번 항목(새 코멘트 존재)이 충족된 wake-up에서는 따로 실행하지 않는다. 코멘트 처리 후 push하면 baseline이 갱신되며 그 이후의 reaction은 다음 사이클의 probe가 평가한다.

### Silent-approval probe

GitHub은 reaction(👍)을 webhook event로 전달하지 않으므로 codex가 코멘트 없이 reaction만 다는 경우는 wake-up이 오지 않을 수 있다. 이를 보완하기 위해 **`last_push_at` 이후 5분(`CODEX_SILENT_PROBE_DELAY`, 기본 300초)이 지났고 baseline 이후 codex 코멘트가 도착하지 않은** 상황에서만 다음 probe를 실행한다.

`wait-codex-review.sh`(Path B)와 동일한 신호 분류를 따른다. Pass actor / Pass reaction / review-request body는 환경변수 `CODEX_PASS_ACTOR` / `CODEX_PASS_REACTION` / `CODEX_REVIEW_REQUEST_BODY` (모두 Path A·B 공통)로 override 가능하므로 probe 명령은 이 변수를 반드시 사용한다. hardcode하면 override 사용 repo에서 pass를 놓친다.

- **Pass 신호**: PR body에 `$CODEX_PASS_ACTOR`의 `$CODEX_PASS_REACTION` reaction이 baseline 이후 추가됨
- **Eyes 신호**: PR body에 누구든 `eyes` reaction이 있거나, 본인이 이번 baseline에 남긴 `$CODEX_REVIEW_REQUEST_BODY` issue comment의 `reactions.eyes` count가 1 이상

조회 예시 (모두 `--paginate` + `jq -s 'add // []'` 패턴. `gh api --jq`는 jq의 `--arg`를 받지 못하므로 pipe로 jq에 직접 넘긴다. `wait-codex-review.sh`의 `fetch_list_or_empty`와 같은 방식):

```bash
: "${CODEX_PASS_ACTOR:=chatgpt-codex-connector[bot]}"
: "${CODEX_PASS_REACTION:=+1}"
: "${CODEX_REVIEW_REQUEST_BODY:=@codex review}"

# 1) PR body의 pass reaction (Pass)
gh api --paginate "repos/<owner>/<repo>/issues/<pr>/reactions" \
  | jq -s --arg base "<baseline>" \
         --arg actor "$CODEX_PASS_ACTOR" \
         --arg react "$CODEX_PASS_REACTION" '
      add // []
      | [.[] | select(.user.login==$actor)
             | select(.content==$react)
             | select(.created_at > $base)] | length'

# 2) PR body의 baseline 이후 `eyes` (Eyes 신호 1)
gh api --paginate "repos/<owner>/<repo>/issues/<pr>/reactions" \
  | jq -s --arg base "<baseline>" '
      add // []
      | [.[] | select(.content=="eyes")
             | select(.created_at > $base)] | length'

# 3) 이번 baseline 안에 본인이 남긴 review-request 코멘트의 eyes count (Eyes 신호 2)
#    self_login은 `gh api user -q .login`으로 얻는다.
gh api --paginate "repos/<owner>/<repo>/issues/<pr>/comments" \
  | jq -s --arg base "<baseline>" \
         --arg me "<self_login>" \
         --arg req "$CODEX_REVIEW_REQUEST_BODY" '
      add // []
      | [.[] | select(.user.login==$me)
             | select(.body==$req)
             | select(.created_at > $base)
             | (.reactions.eyes // 0)] | add // 0'
```

세 query 모두 `--paginate`를 기본으로 둔다. GitHub REST는 페이지당 30개가 기본이라 활성 PR에서는 후속 페이지에 codex의 신호가 위치할 수 있고, 누락 시 분기가 잘못된다.

결과 분기:

- 1번이 > 0 → **Pass**. "Merge 처리"로 진행 후 `mcp__github__unsubscribe_pr_activity` 호출.
- Pass 신호 없음 + (2번 또는 3번 > 0) → codex가 작업 중이라는 신호. 추가 행동 없이 turn 종료해 다음 이벤트를 기다린다.
- 셋 다 0 → entry에서 이미 `@codex review`를 남겼는데도 codex가 어떤 reaction도 달지 않은 상태. 사용자에게 codex 미응답을 보고하고 turn 종료. probe에서 `@codex review`를 다시 게시하지 않는다 — 동일 baseline 동안 entry의 1회로 한정한다. 진입 시 stale `eyes`가 있어 entry가 게시를 건너뛴 드문 경우라도 같다.

probe 시점은 자연스러운 wake-up에 piggyback한다. 자체 timer나 sleep loop는 만들지 않는다. wake-up이 5분보다 일찍 도착해서 probe 조건을 만족하지 못하면 그냥 wake-up 1번 항목 흐름만 처리하고 종료한다 — 다음 wake-up에서 조건이 충족되면 그때 probe한다. push 후 5분이 지났는데 wake-up이 전혀 오지 않는 무이벤트 케이스는 사용자가 다시 명령을 줄 때 처리하며, 그 호출 시점에 probe 1회를 실행한다.

### feedback 수정 후 push

1. 수정 → commit → push.
2. `last_push_at`을 새 push timestamp로 갱신한다 (baseline 재계산: 위 "진입" 2단계와 동일).
3. **review-request gate를 재실행한다** (entry 3단계와 동일): 새 baseline 기준으로 PR body / 본인 코멘트의 `eyes` reaction을 다시 확인하고 둘 다 없으면 `$CODEX_REVIEW_REQUEST_BODY` (기본 `@codex review`) 코멘트를 1회 남긴다. 자동으로 codex 리뷰가 트리거되지 않는 repo에서는 이 재요청이 없으면 무한 대기에 빠진다. 이 게시도 동일 baseline 1회 한정.
4. subscribe는 이미 active이므로 재호출하지 않는다 (idempotent이지만 불필요).
5. turn 종료.

### 종료

- merge 또는 PR close 직후 `mcp__github__unsubscribe_pr_activity` 호출.
- 사용자가 watch 중단을 지시하면 즉시 unsubscribe하고 추가 push를 중단한다.

## Path B: 폴링 스크립트

각 대기 사이클은 `wait-codex-review.sh`를 foreground로 1회 실행해 처리한다. 스크립트가 내부 polling을 담당하고 종료 시점에 필요한 결과만 반환한다. GitHub app으로 즉시 확인 가능한 상태가 있어도 대기/polling은 스크립트에 맡긴다. feedback을 수정하고 push한 뒤에는 다음 대기 사이클로 보고 스크립트를 다시 실행한다.

```bash
CODEX_REVIEW_HELPER=".agents/scripts/wait-codex-review.sh"
[ -x "$CODEX_REVIEW_HELPER" ] || CODEX_REVIEW_HELPER="$HOME/.agents/scripts/wait-codex-review.sh"
[ -x "$CODEX_REVIEW_HELPER" ] || { echo "Missing wait-codex-review.sh"; exit 1; }
bash "$CODEX_REVIEW_HELPER"
```

기본 stdout은 사람이 읽는 feedback 출력이다. 구조화된 관찰이 필요하면 동일한 foreground 호출에 `--json`을 붙이거나 `CODEX_REVIEW_OUTPUT=json`을 설정한다. 이 모드는 exit code를 바꾸지 않고 stdout에 compact `schema_version:1`, `kind:"codex_review_observation"` JSON 1개를 출력한다.

다음 패턴은 금지한다.

- `bash ... &` 로 background polling
- background 실행 후 주기적 output 확인
- 매 sleep 사이에 PR 상태를 다시 polling
- 별도 monitor 도구로 stream watch

### 절차

1. PR 만든 직후, 또는 push 직후, 스크립트를 foreground로 1회 실행한다.
2. 종료될 때까지 기다린다. 스크립트가 PR 감지, baseline 계산, feedback/reaction polling을 처리한다.
3. 종료 코드에 따라 처리한다.

| exit | 의미 | 다음 행동 |
| ---- | ---- | --------- |
| 0 | Codex pass reaction 감지 | checks 확인 후 PR merge |
| 1 | 새 comment/review가 stdout에 출력됨 | 분석 -> 수정 -> commit -> push -> 스크립트 재실행 |
| 2 | 두 번째 timeout 또는 review 요청 미확인 | loop 종료, 사용자에게 타임아웃 보고 |
| 3 | PR 감지 실패 | PR 번호 또는 URL 요청 후 스크립트 인자로 재실행 |
| 4 | 진행을 막는 API 오류 | 인증/권한/네트워크 문제 보고 |

첫 successful 조회에서 PR의 comment/review/reaction이 모두 비어 있으면 helper는 한 번만 `CODEX_INITIAL_EMPTY_DELAY`초, 기본 300초를 쉰 뒤 기존 `CODEX_POLL_INTERVAL`로 계속 조회한다.

각 polling iter에서 helper는 PR 본문 reaction과 인증 사용자 comment의 reaction을 확인한다.

- `eyes` reaction이 PR 본문 또는 내 comment에 있으면 계속 대기한다.
- `eyes` reaction이 없고 아직 review 요청을 남기지 않았으면 PR에 `@codex review` comment를 1회 남긴다.
- review 요청 comment 자체는 새 feedback으로 처리하지 않는다.
- comment를 남긴 뒤 다음 3번의 polling iter 안에 PR 본문 또는 내 comment에 `eyes` reaction이 생기지 않으면 exit 2로 종료한다.
- 일반 polling timeout은 한 번 더 대기하고, 두 번째 timeout에서 exit 2로 종료한다.

## Feedback 처리

- codex review 결과를 그대로 작업 목록으로 받아들이지 말고 적대적/비판적으로 재평가한다. 각 comment/review item마다 주장, 근거, 재현 가능성, 실제 영향, severity, 범위 적합성을 먼저 판정한다.
- Opus reviewer를 사용할 경우 raw PR 전체를 넘기지 말고 새 feedback, 관련 diff, helper-generated review dossier 또는 수동 risk summary, 재현/검증 출력, 이전 finding 요약만 전달한다.
- 유효한 item은 가장 합리적인 해결 방식을 선택한다: root-cause code fix, test 보강, 문서/계약 정정, 요구사항 clarification, 또는 사용자 결정 요청. 리뷰를 만족시키려고 보안/검증/계약을 약화하거나 symptom-only patch를 만들지 않는다.
- 코멘트가 모호하거나 우선순위 판단이 필요하면 코드 수정 전 사용자에게 확인한다.
- 이미 처리된 이슈, 재현 불가 항목, 범위 밖 요구는 근거를 남기고 제외할 수 있다.
- 수정은 최소 diff로 하고, 관련 테스트와 repo가 정의한 검증 명령을 다시 실행한다.
- push 후:
  - **Path A**: `last_push_at`/baseline을 갱신하고 turn을 종료한다. subscription은 그대로 유효하다. silent-approval probe는 wake-up 시점의 조건 충족 여부에 따라 자동 실행된다.
  - **Path B**: 스크립트를 foreground로 다시 실행한다.

## Merge 처리

Path A의 pass reaction 감지 또는 Path B의 exit 0은 Codex pass를 의미한다. 사용자의 추가 확인을 기다리지 말고 PR을 merge한다. 단, merge 전 다음을 확인한다.

1. PR이 draft가 아니어야 한다.
2. required checks가 통과해야 한다.
3. 새 actionable comment/review가 없어야 한다.
4. repo-local guidance 또는 GitHub repo 설정이 정한 merge 방식을 따른다.

권장 확인:

```bash
gh pr view --json number,url,isDraft,baseRefName,headRefName,mergeStateStatus,reviewDecision
gh pr checks <PR_NUMBER> --watch
gh api "repos/<owner>/<repo>" --jq '{allow_merge_commit, allow_squash_merge, allow_rebase_merge}'
```

merge 방식 선택:

- repo-local guidance가 `squash merge`를 요구하면 `gh pr merge <PR_NUMBER> --squash --delete-branch`.
- repo가 merge commit만 허용하면 `gh pr merge <PR_NUMBER> --merge --delete-branch`.
- repo가 rebase merge만 허용하면 `gh pr merge <PR_NUMBER> --rebase --delete-branch`.
- 명시 정책이 없고 여러 방식이 허용되면 기존 repo 관례를 따른다. 관례가 불명확하면 `--squash`를 기본값으로 사용한다.

branch protection, merge queue, required check pending 때문에 즉시 merge가 막히면 같은 방식에 `--auto`를 붙여 auto-merge를 걸 수 있다. 그래도 막히면 차단 사유와 PR URL을 사용자에게 보고한다.

Path A에서는 merge 또는 `--auto` 설정 후 `mcp__github__unsubscribe_pr_activity`를 호출해 구독을 해제한다.

## 환경변수

Path A·B 공통 (둘 다 동일 시맨틱으로 사용):

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `CODEX_PASS_ACTOR` | `chatgpt-codex-connector[bot]` | 통과 reaction을 다는 봇 login |
| `CODEX_PASS_REACTION` | `+1` | 통과를 의미하는 reaction content |
| `CODEX_REVIEW_REQUEST_BODY` | `@codex review` | `eyes` acknowledgement가 없을 때 1회 남기는 comment |

Path A 전용:

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `CODEX_SILENT_PROBE_DELAY` | 300 | 마지막 push 이후 이 시간(초) 이상 경과하고 codex 코멘트가 도착하지 않았을 때만 reaction probe를 실행 |

Path B (`wait-codex-review.sh`) 전용:

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `CODEX_POLL_INTERVAL` | 20 | 폴링 간격 (초) |
| `CODEX_POLL_TIMEOUT` | 600 | 전체 대기 한도 (초) |
| `CODEX_INITIAL_EMPTY_DELAY` | 300 | 첫 successful 조회에서 comment/review/reaction이 모두 없을 때 한 번만 쉬는 시간 (초) |
| `CODEX_BASELINE` | (auto) | 이 ISO timestamp 이전 활동 무시 |
| `CODEX_REPO` | (auto) | fork 워크플로 시 base repo 명시 (`owner/repo`) |
| `CODEX_REVIEW_OUTPUT` | `human` | `json`이면 structured observation을 stdout에 출력 |

## 인자 형식 (Path B)

- 인자 없음: 현재 브랜치의 PR 자동 감지
- PR 번호: `bash "$CODEX_REVIEW_HELPER" 42`
- PR URL: `bash "$CODEX_REVIEW_HELPER" https://github.com/owner/repo/pull/42`
- structured observation: `bash "$CODEX_REVIEW_HELPER" --json 42`

## Structured Observation (Path B)

`--json` 출력은 DevDeck 같은 projection layer가 나중에 읽을 수 있는 작은 상태 스냅샷이다. 한 줄 compact JSON이므로 필요하면 호출자가 그대로 JSONL log에 append할 수 있다. 필드는 versioned envelope, repo/PR/baseline, pass reaction 관찰 상태, feedback items, timeout 상태, review request/eyes acknowledgement 상태, API error classification, `next_allowed_actions`를 포함한다. 이 JSON은 machine state이고, Markdown/stdout human feedback을 대체하지 않는다. codex-loop 자체는 기존 exit code 기반 분기를 유지한다.
