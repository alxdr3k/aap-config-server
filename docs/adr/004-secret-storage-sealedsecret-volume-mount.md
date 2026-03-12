# ADR-004: 시크릿 저장 — SealedSecret + Volume Mount

> **상태**: Accepted
> **일자**: 2026-03-12

## 컨텍스트

Config Server는 서비스별 시크릿(API 키, DB 비밀번호 등)을 관리해야 한다. 시크릿은 다음 요건을 충족해야 한다:

- **저장 시**: 평문이 Git에 노출되면 안 됨
- **전송 시**: Config Server → Config Agent 구간에서 보호
- **운영 시**: Config Server가 시크릿 값을 resolve하여 API 응답에 포함할 수 있어야 함
- **인프라 의존성**: 추가 인프라(Vault 클러스터 등) 없이 동작해야 함

## 결정 동인

- Config Server의 "No Database" 원칙 — 외부 상태 저장소 의존 최소화
- K8s 클러스터 내부에서만 동작 (외부 서비스 의존 불가)
- Git을 source of truth로 유지하되 시크릿 평문은 Git에 저장 안 함
- 운영 복잡도를 최소화

## 선택지

### 1. HashiCorp Vault — 미채택

별도 Vault 클러스터를 운영하여 시크릿을 저장. Config Server가 Vault API로 시크릿을 조회.

- **장점**: 업계 표준, 동적 시크릿, 세밀한 접근 제어, 감사 로깅
- **단점**:
  - **추가 인프라**: Vault 클러스터 운영 필요 (HA 구성 시 Consul/Raft 백엔드 포함)
  - **"No Database" 원칙 위반**: Vault 자체가 상태를 가진 외부 시스템
  - **운영 복잡도**: unseal 관리, 토큰 갱신, 백업/복원 등
  - **과잉 설계**: AAP 규모에서 Vault의 동적 시크릿, 리스 관리 등 고급 기능이 불필요

### 2. External Secrets Operator (ESO) — 미채택

ESO를 설치하여 외부 시크릿 저장소(AWS Secrets Manager, GCP Secret Manager 등)에서 K8s Secret을 동기화.

- **장점**: 클라우드 네이티브, 관리형 서비스 활용
- **단점**:
  - **클라우드 의존**: 특정 클라우드 프로바이더에 종속
  - **온프레미스 불가**: AAP가 온프레미스 환경에서도 동작해야 할 경우 사용 불가
  - **추가 컴포넌트**: ESO 설치 및 운영 필요

### 3. Git 암호화 (SOPS, git-crypt) — 미채택

시크릿 파일을 SOPS나 git-crypt로 암호화하여 Git에 저장.

- **장점**: Git에 모든 데이터가 있어 완전한 GitOps
- **단점**:
  - **키 관리**: 암호화 키를 어디에 저장할 것인가 — 결국 K8s Secret이나 KMS 필요
  - **Config Server 복잡도**: 복호화 로직, 키 접근 권한 관리
  - **atomic commit 어려움**: 시크릿 파일 암호화/복호화가 Git 워크플로우에 간섭

### 4. SealedSecret + Volume Mount — 채택

Bitnami SealedSecret으로 시크릿을 암호화하여 Git에 저장. SealedSecret Controller가 K8s Secret으로 복호화. Config Server는 K8s Secret을 Volume Mount로 읽기.

- **장점**:
  - **추가 인프라 없음**: SealedSecret Controller만 설치 (단일 Deployment)
  - **K8s 네이티브**: 표준 K8s Secret + etcd encryption at rest 활용
  - **Git 안전**: SealedSecret은 클러스터 비밀키 없이 복호화 불가
  - **atomic commit**: config.yaml + SealedSecret YAML을 단일 Git commit으로 처리 가능
  - **자동 갱신**: kubelet이 Volume Mount를 자동 sync (~60초)
- **단점**:
  - SealedSecret Controller에 의존 (단일 장애점, 복제 가능)
  - Volume Mount 갱신 지연 (~60초, kubelet sync 주기)
  - 서비스 추가 시 Config Server Deployment에 volume mount 추가 필요

## 결정

**SealedSecret + Volume Mount 채택**.

### 시크릿 흐름

```
Console
  │ POST /admin/changes (secrets: {평문})
  ▼
Config Server
  ├─ kubeseal: 평문 → SealedSecret YAML (공개키 암호화)
  ├─ Git commit: SealedSecret YAML + secrets.yaml(메타데이터)
  ├─ K8s API: SealedSecret apply
  └─ API 응답 (200 OK)
       ▼
SealedSecret Controller
  └─ SealedSecret 복호화 → K8s Secret 생성/업데이트
       ▼
kubelet
  └─ Volume Mount 자동 갱신 (~60초)
       ▼
Config Server Pod
  └─ /secrets/{namespace}/{name}/{key} 파일에서 평문 읽기
```

### Git에 저장되는 것 vs 저장되지 않는 것

| 저장소 | 내용 | 형태 |
|--------|------|------|
| Git (`secrets.yaml`) | 시크릿 메타데이터 (id, description, K8s Secret 경로) | **평문** (민감하지 않음) |
| Git (`sealed-secrets/`) | SealedSecret YAML | **암호화** (클러스터 비밀키 없이 복호화 불가) |
| K8s etcd | Secret 객체 | **암호화** (encryption at rest) |
| Config Server Pod | Volume Mount 파일 | **평문** (Pod 내부, 읽기 전용) |

## 영향

### 긍정적

- Vault 등 외부 시크릿 인프라 불필요 — "No Database" 원칙 유지
- Git 저장소 유출 시에도 시크릿 안전 (SealedSecret은 클러스터 비밀키 없이 복호화 불가)
- 설정과 시크릿의 단일 atomic Git commit 보장
- K8s 네이티브 도구만 사용하여 운영 복잡도 최소
- Volume Mount로 Pod 재시작 없이 시크릿 갱신 가능

### 부정적

- Volume Mount 갱신 지연 (~60초) — 설정 서버 특성상 허용 가능
- 서비스/시크릿 추가 시 Config Server Deployment manifest에 volume 선언 추가 필요
  - 완화: Helm chart values.yaml에서 시크릿 목록을 관리하여 자동화 가능
- SealedSecret Controller 단일 장애점
  - 완화: Controller 장애 시 기존 K8s Secret은 영향 없음, 새 시크릿 생성만 불가
- `subPath` 마운트 시 자동 갱신 불가 — 반드시 디렉토리 단위 마운트 필요

### 향후 고려사항

- 서비스 수가 크게 증가하면 Volume Mount 선언이 많아질 수 있음 → CSI Secret Store Driver 등으로 전환 검토
- 멀티 클러스터 환경에서는 클러스터별 SealedSecret 키가 다르므로, 클러스터별 SealedSecret YAML 생성이 필요할 수 있음

## 관련 문서

- [PRD FR-7: 시크릿 관리](../PRD.md)
- [PRD FR-17: 시크릿 보안](../PRD.md)
