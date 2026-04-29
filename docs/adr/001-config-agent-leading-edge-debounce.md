# ADR-001: Config Agent Leading-edge Debounce

> **상태**: Accepted
> **일자**: 2026-03-12

## 컨텍스트

Config Agent는 Config Server를 polling하여 설정 변경을 감지하고, K8s ConfigMap/Secret 업데이트 후 대상 Deployment의 rolling restart를 트리거한다.

여러 사용자가 짧은 간격으로 설정을 변경하면, 매 변경마다 rolling restart가 트리거되어 **재시작 폭풍(restart storm)** 이 발생한다:

```
T=0s    User A: config 변경
T=30s   Config Agent: A 감지 → rolling restart 시작 (~2-3분 소요)
T=45s   User B: config 변경
T=60s   Config Agent: B 감지 → 진행 중 rollout 중단, 새 rollout 시작
T=90s   User C: config 변경
T=120s  Config Agent: C 감지 → 또 rollout 중단, 새 rollout 시작
```

K8s Deployment controller는 overlapping rollout을 네이티브로 처리하지만(진행 중인 rollout 중단 → 최신 template으로 전환), litellm이 안정 상태에 도달하지 못하고 계속 cycling된다.

## 결정 동인

- 단일 변경 시 사용자가 불필요하게 기다리면 안 됨
- 연속 변경 시 불필요한 재시작 횟수를 줄여야 함
- 변경이 끊임없이 들어와도 무한 대기하면 안 됨

## 선택지

### 1. 즉시 적용 (No Debounce) — 미채택

변경 감지 시 항상 즉시 ConfigMap 업데이트 + rolling restart.

- **장점**: 단일 변경 시 지연 없음
- **단점**: 연속 변경 시 재시작 폭풍, litellm이 안정화되지 못함

### 2. Trailing-edge Debounce — 미채택

변경 감지 후 quiet period(30초) 동안 추가 변경이 없을 때 적용. 변경이 계속 들어오면 maxWait(5분) 후 강제 적용.

- **장점**: 연속 변경을 효과적으로 배칭
- **단점**: **단일 변경에도 30초 추가 지연** 발생. maxWait 5분은 사용자 체감상 너무 김

### 3. Leading-edge Debounce — 채택

첫 변경은 즉시 적용, cooldown 기간 내 후속 변경만 배칭.

- **장점**: 단일 변경 시 즉시 반영. 연속 변경 시에만 debounce 작동
- **단점**: 첫 변경 직후 연이은 변경은 2번의 rolling restart 발생 (첫 번째 즉시 + 배칭분 1회)

## 결정

**Leading-edge Debounce 채택**.

```
한가한 시간: 변경 → 즉시 적용 → cooldown 10초 → 다시 대기 상태
바쁜 시간:   변경 → 즉시 적용 → 10초 내 또 변경 → debounce 모드 진입
             → 10초간 조용하면 적용, 안 조용하면 최대 2분 후 강제 적용
```

파라미터:

| 파라미터 | 기본값 | 설명 |
|----------|--------|------|
| `--debounce-cooldown` | `10s` | 적용 직후 재적용 방지 기간 |
| `--debounce-quiet-period` | `10s` | 마지막 변경 후 이 시간 동안 추가 변경 없으면 적용 |
| `--debounce-max-wait` | `2m` | debounce 시작 후 최대 대기. 초과 시 강제 적용 |

## 영향

### 긍정적

- 단일 변경: polling 주기(~30초) 후 즉시 반영, 추가 지연 없음
- 연속 변경: 불필요한 재시작 횟수 대폭 감소
- maxWait로 최악의 경우에도 2분 안에 반영 보장
- 29초 간격 변경: cooldown(10초)을 넘으므로 매번 즉시 적용됨 (debounce 미발동)

### 부정적

- 10초 이내 연타 시 첫 변경 직후의 rolling restart가 완료되기 전에 두 번째 배칭분이 또 restart를 트리거할 수 있음 (최대 2회). 하지만 debounce 없는 경우(매번 restart)보다 낫다.

### 주의사항

- debounce는 Config Agent 레벨의 메커니즘. Console 사용자의 API 응답 시간(~1-2초)에는 영향 없음
- 파라미터는 Config Agent Deployment args로 설정 가능하여 운영 중 조정 가능

## 관련 문서

- [PRD FR-9: Config Agent](../01_PRD.md)
- [ADR-002: 중앙 집중형 Config Agent vs Sidecar](./002-central-config-agent-vs-sidecar.md)
