# ADR-002: 중앙 집중형 Config Agent vs Sidecar

> **상태**: Accepted
> **일자**: 2026-03-12

## 컨텍스트

litellm 등 대부분의 서비스는 임의 HTTP URL에서 설정을 로드하는 기능이 없다. litellm은 `--config /path/to/config.yaml` 또는 `CONFIG_FILE_PATH` 환경변수로 **로컬 파일만** 읽는다. 환경변수 역시 프로세스 시작 시점에 이미 설정되어 있어야 한다.

따라서 Config Server의 REST API와 클라이언트 서비스 사이를 연결하는 **Config Agent**가 필요하다. 설계 시 "각 Pod에 사이드카로 붙이는 방식"과 "Deployment 단위로 독립 배포하는 중앙 집중형" 두 가지 옵션이 있었다.

## 결정 동인

- litellm은 클러스터당 32 replica로 운영됨
- 동일 Deployment의 모든 replica는 동일한 설정을 공유함
- 설정 변경 시 서비스 가용성을 유지해야 함
- Config Server에 대한 불필요한 부하를 줄여야 함

## 선택지

### 1. Sidecar 패턴 — 미채택

각 litellm Pod에 Config Agent 사이드카 컨테이너를 추가. 사이드카가 Config Server를 polling하고, 변경 감지 시 로컬 파일을 업데이트한 뒤 메인 컨테이너를 재시작.

```
┌─ litellm Pod 1 ──────────────────┐
│  ┌─ sidecar ─┐  ┌─ litellm ───┐ │
│  │ Config    │  │ --config    │ │
│  │ Agent     │──│ /config/... │ │
│  │ (polling) │  │             │ │
│  └───────────┘  └─────────────┘ │
└──────────────────────────────────┘
  × 32 replicas = 32개의 Agent가 각각 polling
```

- **장점**: 구현이 단순함. 각 Pod가 독립적으로 설정을 관리
- **단점**:
  - **Thundering Herd**: Config 변경 시 32개 사이드카가 거의 동시에 감지 → 32개 Pod 동시 재시작 → 순간 서비스 불가
  - **Redundant Polling**: 32개 사이드카가 각각 Config Server에 polling → 요청 32배 증폭
  - **사이드카 재시작 복잡성**: 사이드카에서 메인 컨테이너를 재시작하려면 `shareProcessNamespace`나 시그널 전달 등 추가 구성 필요

### 2. 중앙 집중형 Config Agent — 채택

대상 Deployment별로 독립된 Config Agent Deployment(replica=2)를 배포. Config Agent가 Config Server를 polling → K8s ConfigMap/Secret 업데이트 → Deployment annotation 패치로 rolling restart 트리거.

```
┌─ Config Agent Deployment (replica=2) ─┐
│  Config Server polling (1개만)         │
│  → ConfigMap/Secret 업데이트           │
│  → Deployment annotation 패치         │
│  → K8s rolling restart (maxUnavailable: 25%) │
└────────────────┬──────────────────────┘
                 │
┌────────────────▼──────────────────────┐
│  litellm Deployment (32 replicas)      │
│  Pod 1..32: ConfigMap volume mount    │
│  → rolling restart로 점진적 반영       │
└────────────────────────────────────────┘
```

- **장점**:
  - Polling 요청 1개 (32배 → 1개로 감소)
  - K8s rolling update로 점진적 반영 (maxUnavailable: 25% → 동시 최대 8개만 재시작)
  - Config Agent가 K8s API로 ConfigMap을 업데이트하므로, 메인 컨테이너에 대한 침투적 변경 불필요
- **단점**:
  - Config Agent Deployment 자체를 관리해야 함 (추가 리소스)
  - 대상 Deployment별로 Config Agent를 배포해야 함

## 결정

**중앙 집중형 Config Agent 채택**.

| 지표 | Sidecar | 중앙 집중형 |
|------|---------|-----------|
| Config Server 부하 | N개 replica × polling | 1개 polling |
| 동시 재시작 위험 | N개 동시 재시작 | maxUnavailable: 25% |
| 추가 컨테이너 | N개 사이드카 | 2개 Agent Pod |
| 복잡도 | shareProcessNamespace 등 | K8s API (ConfigMap, Deployment 패치) |

## 영향

### 긍정적

- 32 replica 기준: polling 요청 97% 감소 (32 → 1)
- rolling restart로 서비스 가용성 보장

### 부정적

- 서비스별 Config Agent Deployment을 추가로 관리해야 함
- Config Agent 장애 시 해당 서비스의 설정 업데이트가 중단됨 (replica=2로 완화)

### 주의사항

- Config Agent replica=2는 HA를 위한 것이며, leader election으로 하나만 active하게 동작해야 함 (이중 적용 방지)
- Config Agent의 RBAC은 대상 ConfigMap/Secret/Deployment에만 최소 권한으로 제한

## 관련 문서

- [PRD FR-9: Config Agent](../PRD.md)
- [ADR-001: Config Agent Leading-edge Debounce](./adr-001-config-agent-leading-edge-debounce.md)
