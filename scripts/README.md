# Scripts

## sync-origin-to-samsung

origin 브랜치의 커밋을 samsung main으로 옮기면서 author/committer를 지정 사용자로 바꾸고, 커밋 메시지의 URL을 제거합니다.

**실행 (PowerShell):**
```powershell
.\scripts\sync-origin-to-samsung.ps1 [origin_브랜치명] [--full]
```

**실행 (Git Bash):**
```bash
./scripts/sync-origin-to-samsung.sh [origin_브랜치명] [--full]
```

- `origin_브랜치명`: 생략 시 `claude/config-server-setup-4kjXZ`
- `--full`: 전체 히스토리 재작성 (첫 실행, 또는 증분 동기화 초기화 시)
- 기본 author/committer: `ygdg.kim <ygdg.kim@samsung.com>`
- **증분 모드**: 이전 동기화 지점(`refs/sync-state/`)을 저장해 **새 커밋만** cherry-pick + amend. samsung에만 있는 커밋도 유지됨.
- **전체 모드**: reset + filter-branch (느림). `--full` 시 samsung main을 origin으로 완전 대체 → **samsung 전용 커밋 손실**

---

## remove-commit-urls

현재 브랜치 커밋 메시지에서 URL을 제거하고, author/committer를 지정한 사용자로 변경합니다. 새 커밋이 생겼을 때 다시 실행할 수 있습니다.

**실행 (PowerShell):**
```powershell
.\scripts\remove-commit-urls.ps1 [브랜치명]
```

**실행 (Git Bash):**
```bash
./scripts/remove-commit-urls.sh [브랜치명]
```

- `브랜치명`: 생략 시 현재 브랜치
- 기본 author/committer: `ygdg.kim <ygdg.kim@samsung.com>`. 변경 시 `AUTHOR_NAME`, `AUTHOR_EMAIL` 환경변수 설정 후 실행
- 실행 후 히스토리가 변경되므로 force push 필요: `git push samsung <브랜치> --force`
- (선택) 백업 refs 정리: `git for-each-ref --format='%(refname)' refs/original/ | xargs -n 1 git update-ref -d`
