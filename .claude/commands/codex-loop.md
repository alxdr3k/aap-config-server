---
name: codex-loop
description: 현재 PR의 codex 리뷰를 기다리고 코멘트 수정 후 push, 통과 reaction을 받으면 정책에 맞춰 merge
---

현재 작업 중인 PR에 대해 codex 리뷰를 기다리고, 코멘트가 달리면 수정 후 push. 통과 reaction까지 반복한 뒤 PR을 정책에 맞춰 merge한다.

사용자에게 보이는 보고, feedback 정리, 질문은 한국어로 작성한다. 코드, 명령, 파일명, 원문 인용은 원문 언어를 유지한다.

## 핵심 원칙: 대기 사이클마다 foreground sync 1회

각 대기 사이클은 `wait-codex-review.sh`를 foreground로 1회 실행해 처리한다.
스크립트는 polling sleep 동안 idle이라 그 시간 동안 토큰을 거의 쓰지 않는다.
GitHub app으로 즉시 확인 가능한 상태가 있어도 대기/polling은 스크립트에 맡긴다.
feedback을 수정하고 push한 뒤에는 다음 대기 사이클로 보고 스크립트를 다시 실행한다.

```bash
CODEX_REVIEW_HELPER=".agents/scripts/wait-codex-review.sh"
[ -x "$CODEX_REVIEW_HELPER" ] || CODEX_REVIEW_HELPER="$HOME/.agents/scripts/wait-codex-review.sh"
[ -x "$CODEX_REVIEW_HELPER" ] || { echo "Missing wait-codex-review.sh"; exit 1; }
bash "$CODEX_REVIEW_HELPER"
```

다음 패턴은 **금지** — 매 cycle마다 stdout/상태를 확인하면 토큰을 그대로 다 쓰게 되어 스크립트의 의미가 사라짐:
- ❌ `bash ... &` (background로 띄우고 polling)
- ❌ `run_in_background: true` 후 매 cycle output check
- ❌ Monitor 도구로 stream watch
- ❌ 매 sleep 사이에 상태 polling

## 절차

1. PR 만든 직후, 또는 push 직후, 위 명령을 **foreground로 1회** 실행한다.
2. 종료될 때까지 기다린다 (스크립트가 알아서 polling).
3. 종료 코드에 따라 처리:

   | exit | 의미 | 다음 행동 |
   |------|------|-----------|
   | 0 | codex가 👍 reaction 추가 — 리뷰 통과 | check 확인 후 PR merge |
   | 1 | 새 코멘트/리뷰가 stdout에 출력됨 | 분석 → 코드 수정 → commit → push → 1번부터 재시도 |
   | 2 | 타임아웃 (default 10분) | 사용자에게 보고 |
   | 3 | PR 감지 실패 | 첫 인자로 PR 번호 또는 URL 전달 |
   | 4 | 영구 API 에러 (401/403/404) | 사용자에게 인증·권한 점검 요청 |

4. exit 1 후 push가 끝나면 다시 1번부터.

## Merge 처리

exit 0은 "Codex가 thumbs-up pass reaction을 달았다"는 뜻이다. 이 경우 사용자의 추가 확인을 기다리지 말고 PR을 merge한다. 단, merge 전 다음을 확인한다.

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

## 환경변수

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `CODEX_POLL_INTERVAL` | 20 | 폴링 간격 (초) |
| `CODEX_POLL_TIMEOUT` | 600 | 전체 대기 한도 (초) |
| `CODEX_BASELINE` | (auto) | 이 ISO timestamp 이전 활동 무시 |
| `CODEX_REPO` | (auto) | fork 워크플로 시 base repo 명시 (`owner/repo`) |
| `CODEX_PASS_ACTOR` | `chatgpt-codex-connector[bot]` | 통과 reaction을 다는 봇 login |
| `CODEX_PASS_REACTION` | `+1` | 통과를 의미하는 reaction content |

## 인자 형식

- 인자 없음: 현재 브랜치의 PR 자동 감지
- PR 번호: `bash "$CODEX_REVIEW_HELPER" 42`
- PR URL: `bash "$CODEX_REVIEW_HELPER" https://github.com/owner/repo/pull/42`

## 작업 지시 시 주의

코멘트가 모호하거나 우선순위 판단이 필요하면 코드 수정 전 사용자에게 확인. 합리적이지 않은 트집(이미 처리된 이슈, 비현실적 엣지케이스)은 근거를 남기고 제외할 수 있다. thumbs-up pass reaction을 받았고 checks가 통과하면 PR은 merge까지 진행한다.
