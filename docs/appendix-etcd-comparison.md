# 부록: etcd 직접 사용 검토

> **Date**: 2026-03-10
> **Status**: 검토 완료 — 채택하지 않음

현재 설계(Git + K8s Secret)에서 etcd를 직접 사용하는 방안을 검토한 결과를 정리한다.

---

## 1. Git 대신 etcd를 설정 저장소로 사용

| | Git (현재) | etcd 직접 사용 |
|--|-----------|---------------|
| **변경 이력** | 자동 (commit history) | 없음 — 직접 구현해야 함 |
| **리뷰/승인** | PR 워크플로우 그대로 | 별도 UI/워크플로우 필요 |
| **변경 감지** | webhook/poll | watch API (실시간, 훨씬 빠름) |
| **스키마 검증** | CI/CD에서 가능 | 별도 validation 레이어 필요 |
| **운영 부담** | Git 서버 (이미 있음) | etcd 클러스터 별도 운영 필요 |
| **장애 영향** | Git 장애 시 캐시로 서빙 | etcd 장애 = K8s 장애 (같은 etcd 쓸 경우) |

### 채택하지 않는 이유

1. **K8s etcd 공유 시 위험** — Config Server의 부하가 K8s control plane에 직접 영향
2. **별도 etcd 운영 부담** — etcd 클러스터 운영이 까다로움 (quorum, compaction, defrag 등). Git 서버는 이미 존재
3. **Git의 이력/리뷰 기능 상실** — 설정 변경의 감사 추적이 PRD 핵심 원칙(Git as Source of Truth)인데, 이를 etcd 위에서 직접 구현해야 함
4. **No Database 원칙 위반** — PRD 1.3절의 핵심 원칙과 충돌

### etcd의 유일한 장점

etcd watch API는 변경을 실시간으로 감지할 수 있어 Git webhook/poll 대비 지연이 적다. 그러나 Git webhook으로도 수 초 이내 반영이 가능하므로, 설정 서버 용도에서는 충분하다.

---

## 2. K8s Secret 대신 etcd에 시크릿 직접 저장

| | K8s Secret + Volume Mount (현재) | etcd 직접 |
|--|----------------------------------|-----------|
| **접근** | kubelet이 파일로 마운트 | etcd client로 직접 read |
| **자동 갱신** | kubelet sync (~60초) | watch API (실시간) |
| **암호화** | etcd encryption at rest (K8s 관리) | 직접 encryption 설정 |
| **접근 제어** | K8s RBAC | etcd auth (별도 관리) |
| **운영** | kubectl로 관리 | etcdctl 또는 별도 도구 |
| **생태계** | Sealed Secrets, External Secrets Operator 등 | 없음 |

### 채택하지 않는 이유

K8s Secret이 이미 etcd를 래핑하고 있다. 그 래핑을 벗겨내면:

- K8s RBAC 접근 제어를 못 씀
- Sealed Secrets, External Secrets Operator 같은 K8s 생태계를 전부 못 씀
- encryption at rest를 직접 설정/관리해야 함
- 시크릿 관리 도구(kubectl, ArgoCD 등)와의 호환성 상실

---

## 3. 고려할 만한 시나리오

Config Server가 **K8s 외부에서도 동작**해야 하는 경우 (bare metal, multi-cluster 등)에는 etcd를 별도 저장소로 쓰는 것이 의미 있을 수 있다. 그러나 현재 PRD 범위는 K8s 단일 클러스터 환경이므로 해당하지 않는다.

---

## 4. 결론

현재 설계(Git + K8s Secret)가 운영 부담 대비 가장 효율적이다.

```
[현재 설계]
설정 저장: Git          → 이력 추적, PR 리뷰, CI/CD 검증 무료로 제공
시크릿 저장: K8s Secret  → etcd encryption at rest, RBAC, 생태계 호환
변경 감지: webhook/poll  → 수 초 이내 반영, 충분한 실시간성

[etcd 직접 사용 시]
설정 저장: etcd          → 이력/리뷰/검증을 직접 구현해야 함
시크릿 저장: etcd         → encryption/RBAC/생태계를 직접 구현해야 함
변경 감지: watch API      → 실시간이지만, 설정 서버에서 그 정도의 실시간성은 불필요
```
