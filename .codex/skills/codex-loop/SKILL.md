---
name: codex-loop
description: 현재 PR의 codex 리뷰를 기다리고 코멘트 수정 후 push, 통과 reaction을 받으면 정책에 맞춰 merge
---
<!-- my-skill:generated
skill: codex-loop
base-sha256: 90aa303001e7be2eb547282a329cb30f18d0efc554a9851f3c8d22a2adda77bc
overlay-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
output-sha256: 90aa303001e7be2eb547282a329cb30f18d0efc554a9851f3c8d22a2adda77bc
do-not-edit: edit .codex/skill-overrides/codex-loop.md instead
-->

현재 작업 중인 PR에 대해 codex 리뷰를 기다리고, 코멘트가 달리면 수정 후 push. 통과 reaction까지 반복한 뒤 PR을 정책에 맞춰 merge한다.

사용자에게 보이는 보고, feedback 정리, 질문은 한국어로 작성한다. 코드, 명령, 파일명, 원문 인용은 원문 언어를 유지한다.

## 핵심 원칙: 대기 사이클마다 foreground script 1회

각 대기 사이클은 `wait-codex-review.sh`를 foreground로 1회 실행해 처리한다.
스크립트가 내부 polling을 담당하고 종료 시점에 필요한 결과만 반환한다. GitHub app으로
즉시 확인 가능한 상태가 있어도 대기/polling은 스크립트에 맡긴다. feedback을 수정하고
push한 뒤에는 다음 대기 사이클로 보고 스크립트를 다시 실행한다.

```bash
CODEX_REVIEW_HELPER=".agents/scripts/wait-codex-review.sh"
[ -x "$CODEX_REVIEW_HELPER" ] || CODEX_REVIEW_HELPER="$HOME/.agents/scripts/wait-codex-review.sh"
[ -x "$CODEX_REVIEW_HELPER" ] || { echo "Missing wait-codex-review.sh"; exit 1; }
bash "$CODEX_REVIEW_HELPER"
```

다음 패턴은 금지한다.

- `bash ... &` 로 background polling
- background 실행 후 주기적 output 확인
- 매 sleep 사이에 PR 상태를 다시 polling
- 별도 monitor 도구로 stream watch

## 절차

1. PR 만든 직후, 또는 push 직후, 스크립트를 foreground로 1회 실행한다.
2. 종료될 때까지 기다린다. 스크립트가 PR 감지, baseline 계산, feedback/reaction
   polling을 처리한다.
3. 종료 코드에 따라 처리한다.

| exit | 의미 | 다음 행동 |
| ---- | ---- | --------- |
| 0 | Codex pass reaction 감지 | checks 확인 후 PR merge |
| 1 | 새 comment/review가 stdout에 출력됨 | 분석 -> 수정 -> commit -> push -> 스크립트 재실행 |
| 2 | 두 번째 timeout 또는 review 요청 미확인 | loop 종료, 사용자에게 타임아웃 보고 |
| 3 | PR 감지 실패 | PR 번호 또는 URL 요청 후 스크립트 인자로 재실행 |
| 4 | 영구 API 오류 | 인증/권한 문제 보고 |

첫 successful 조회에서 PR의 comment/review/reaction이 모두 비어 있으면 helper는 한 번만
`CODEX_INITIAL_EMPTY_DELAY`초, 기본 300초를 쉰 뒤 기존 `CODEX_POLL_INTERVAL`로
계속 조회한다. PR 생성 직후 Codex/GitHub 쪽 초기 처리 지연 때문에 빈 PR을 너무 촘촘하게
polling하지 않기 위한 동작이다.

각 polling iter에서 helper는 PR 본문 reaction과 인증 사용자 comment의 reaction을
확인한다.

- `eyes` reaction이 PR 본문 또는 내 comment에 있으면 계속 대기한다.
- `eyes` reaction이 없고 아직 review 요청을 남기지 않았으면 PR에 `@codex review`
  comment를 1회 남긴다.
- review 요청 comment 자체는 새 feedback으로 처리하지 않는다.
- comment를 남긴 뒤 다음 3번의 polling iter 안에 PR 본문 또는 내 comment에 `eyes`
  reaction이 생기지 않으면 exit 2로 종료한다.
- 일반 polling timeout은 한 번 더 대기하고, 두 번째 timeout에서 exit 2로 종료한다.

인자 형식:

- 인자 없음: 현재 브랜치 PR 자동 감지
- PR 번호: `bash "$CODEX_REVIEW_HELPER" 42`
- PR URL: `bash "$CODEX_REVIEW_HELPER" https://github.com/owner/repo/pull/42`

## Feedback 처리

- 코멘트가 모호하거나 우선순위 판단이 필요하면 코드 수정 전 사용자에게 확인한다.
- 이미 처리된 이슈, 재현 불가 항목, 범위 밖 요구는 근거를 남기고 제외할 수 있다.
- 수정은 최소 diff로 하고, 관련 테스트와 repo가 정의한 검증 명령을 다시 실행한다.
- push 후 스크립트를 다시 foreground로 실행한다.

## Merge 처리

exit 0은 Codex pass reaction을 감지했다는 뜻이다. 이 경우 사용자의 추가 확인을
기다리지 말고 PR을 merge한다. 단, merge 전 다음을 확인한다.

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

## 완료

PR URL, merge 방식, check 결과, 처리한 feedback, 남은 리스크를 보고한다.
