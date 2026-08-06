# Management Plane v2 D2/D3 一致性审阅

- 状态：`D2 Verified (gofmt/go test/go vet); D3 implementation not accepted`
- 审阅日期：2026-08-06
- 输入：[Robot / Device / Runtime 拆分合同](ROBOT_DEVICE_RUNTIME_CONTRACT.md)、[Registry 对象分类账](REGISTRY_OBJECT_CLASSIFICATION.md)、本地 `/v2` schema/store/API/tests
- 本轮边界：D2 schema/ID enforcement 已实施；D3 只保留审阅结论，不实现、不新增 endpoint，也不接入真实 producer

## 1. 审阅结论

本地 `/v2` 已经具备 Robot、Device、Runtime、RuntimeSession、Heartbeat 和 Run 的代码骨架，但五个关键问题均未达到可接入 producer 的标准。结论不是删除草案，而是把它保留为 conformance target 之前的本地 spike：

| 审阅项 | 设计结论 | 当前实现结论 | Gate |
|---|---|---|---|
| identity constraint | canonical identity、外部映射和数据库不变量已冻结 | D2 已实施；SQLite 约束与 `gofmt`/`go test ./...`/`go vet ./...` 均已通过 | `Pass` |
| session conflict | 精确重复幂等；同 ID 不同不可变 payload/source 必须冲突 | 只按复合主键判重复，不比较 payload | `Blocked` |
| late heartbeat | 可以留审计，但必须明确没有应用到 current liveness | API 对所有成功写入都返回 `accepted: true` | `Blocked` |
| source authority | source 必须由服务端根据受信 transport/auth binding 注入 | 任意 HTTP caller 都能写 session、heartbeat、Run result | `Blocked` |
| Run result authority | Domain executor/Task GT 只对其 run kind 的结果有权威 | Run 创建请求可直接声明 result 和 artifact producer | `Blocked` |

D2/D3 的设计问题在本文件中逐项收敛。D2 schema/ID enforcement 已进入代码，并且本机 Go 1.26.5 已通过 `gofmt`/`go test ./...`/`go vet ./...`；D3 仍未实现。因此整体 `/v2` **实现验收仍未通过**，不能进入 D4 producer 接入。

## 2. Identity constraint

### 2.1 身份签发和映射

| 身份 | 谁生成 | 目标形式 | 冻结规则 |
|---|---|---|---|
| `robot_id` | Platform Registry | opaque，示例 `rob-{random}` | caller 不能指定、覆盖或复用 |
| `device_id` | Platform Registry | opaque，示例 `dev-{random}` | caller 不能指定；必须绑定一个 Robot |
| `runtime_id` | Platform Registry | opaque，示例 `rt-{random}` | 与 Run ID 使用不同 namespace；必须绑定一个 Robot |
| `run_id` | Platform Run ledger | opaque，示例 `run-{random}` | `/v2` 只能是 API 代际，不能进入产品 identity |
| `session_id` | 已绑定该 Runtime 的 producer | producer opaque ID | 只在 `runtime_id` 下唯一且永不复用 |
| `source_event_id` | 已绑定 source | producer opaque ID | 与 `source_id` 组成管理事件幂等键 |

Robot、Device、Runtime 的 POST body 不接受 canonical `id`。域侧 ID、serial、ROS name 或 deployment name 只能进入 namespaced `external_refs`，不能变成 Platform 主键。

每个 ExternalRef 必须满足：

- `namespace` 和 `value` 非空，namespace 说明来源和对象语义；
- `(object_kind, namespace, value)` 最多映射到一个 canonical object；
- 同一映射重复写入可以幂等，映射到另一个 canonical ID 必须冲突；
- JSON 编解码失败必须使请求或读操作失败，不能静默替换为 `[]` 或 `nil`；
- v1 demo row 不自动转成 ExternalRef，也不自动生成 v2 identity。

### 2.2 数据库和 store 不变量

D2 实现至少必须同时满足以下约束，不能只依赖 handler 校验：

1. SQLite 每个连接启用 foreign key enforcement；Robot、Device、Runtime、Session 和 Run 引用不能产生孤儿。
2. `embodiment`、`lifecycle_state`、`device_class`、`runtime_role`、`session_state` 使用 DB `CHECK` 或等价的持久化层强约束。
3. `heartbeat_interval_ms > 0`；server-owned timestamps 不接受 caller 覆盖。
4. `parent_device_id` 与 child 属于同一 Robot，并且 cycle 检查遇到已有环、断链或查询错误时 fail closed。
5. `host_device_id` 如果存在，必须和 Runtime 属于同一 Robot。
6. 同一 `runtime_id` 最多一个 `current` session，由数据库唯一约束和同一事务中的 store 逻辑共同保证。
7. identity 被历史对象引用后只能 retire，不能复用或硬删除。

本轮 D2 实施证据：

- [`schema.sql`](../internal/store/schema.sql) 对 canonical ID namespace、枚举、timestamp、heartbeat interval 和 session state 增加 `CHECK`，并以 partial unique index 强制每个 Runtime 最多一个 current session。
- [`store.go`](../internal/store/store.go) 对每个 SQLite connection 启用并验证 foreign keys；`foreign_key_check` 失败时拒绝启动。
- Robot/Device/Runtime/Run ID 由服务端生成 `rob/dev/rt/run` opaque ID；v2 identity create command 使用 strict JSON，caller `id`、`lifecycle_state` 和 server-owned timestamp 会被拒绝。
- ExternalRef 从不可索引 JSON 收敛到三张带 owner FK 和 `(namespace, value)` 主键的 mapping table；同 payload 精确重复折叠，跨 canonical object 冲突回滚。
- containment cycle/断链改为 fail-closed；same-Robot parent/host 和 Run-RuntimeSession ownership 同时由 store 与 trigger 保护。
- 数据库使用 `user_version=2`。发现 pre-D2 v2 表时保留原数据并返回 `ErrMigrationRequired`，不做猜义迁移；v1-only 数据库仍可 additive 建表。

本机 `sqlite3` 已验证 schema parse、integrity、FK orphan、ExternalRef uniqueness、单 current session 和递归 containment cycle 均按预期拒绝。本机 Go 1.26.5（`~/.local/go`）已执行 `gofmt`、`go test ./...`、`go vet ./...` 并通过，因此 D2 verification Gate 标为 `Pass`。D3 仍为 Fail。

## 3. Session conflict

SessionStarted 的不可变比较字段冻结为：

```text
runtime_id + session_id
software_version_ref
started_at_reported
server-bound source_id
```

`started_at_received` 是 Platform 首次收到事件的时间，不参与 producer payload 相等判断，也不能在重试时刷新。

| 输入情况 | 结果 | 是否改变 current session |
|---|---|---|
| 新 `session_id`，Runtime 存在且 source 有权 | `201 Created` | 原 current 原子地变为 superseded；新 session 成为 current |
| 完全相同的 SessionStarted 重试 | `200 OK`, `idempotent=true` | 不改变；不刷新 received time |
| 同 ID、不同 version/start/source | `409 Conflict` | 不改变任何 session |
| 对 ended/superseded ID 的完全相同重试 | `200 OK`, 返回原状态 | 不重新激活 |
| 对 ended/superseded ID 换 payload 重用 | `409 Conflict` | 不重新激活 |
| supersede 或 insert 任一步失败 | 整个事务失败 | 保留事务前状态 |

`software_version_ref` 必须最终指向可解析的 Version record。当前自由字符串 `unknown` 只能作为 Mock 草案数据；真实 producer 接入前必须有显式、可追溯的 Version binding，不能用匿名 `unknown` 代替 build 身份。

当前 store 仍只要主键已存在就返回旧行，没有比较不可变 payload；API 也没有 source，且无法稳定区分新建与幂等 current-session 重试。D2 已修复 supersede error handling，并由数据库强制单 current session；其余属于 D3，仍标为 non-conformant。

## 4. Late heartbeat

Heartbeat 的“保存”和“应用到活性”是两件不同的事。响应不再使用含义不清的单一 `accepted`：

```json
{
  "recorded": true,
  "applied_to_liveness": false,
  "idempotent": false,
  "session_state": "superseded",
  "reason": "session_not_current"
}
```

| 情况 | HTTP | `recorded` | `applied_to_liveness` | `idempotent` | `reason` |
|---|---:|---:|---:|---:|---|
| current session 的新 seq | 202 | true | true | false | `applied` |
| 完全相同的 heartbeat 重试 | 200 | true | false | true | `duplicate` |
| superseded/ended session 的新 seq | 202 | true | false | false | `session_not_current` |
| 同 key 不同 producer payload | 409 | false | false | false | `payload_conflict` |
| 新 key 但 seq 小于等于已记录最大值 | 409 | false | false | false | `seq_regression` |
| session 不存在或 source 不匹配 | 404/403 | false | false | false | 对应错误 |

补充规则：

- liveness 只使用首次写入的 server-owned `received_at`；producer `reported_at` 只供审计。
- 重试不能延长 liveness；迟到旧 session 事件不能恢复 current、online 或修改 current session 的 `last_heartbeat_at`。
- 是否保存非 current session 事件是审计策略，不表示它“作为当前活性被接受”。
- 读取 current/last session 发生数据库错误时返回 500；不能把 store error 吞掉后伪装成 `unknown` 或 `offline`。

当前 store 在 [`management_v2.go`](../internal/store/management_v2.go) 第 411-490 行已经做到“迟到事件可存档但不更新 current liveness”，这是可保留的部分；API 在 [`management_v2.go`](../internal/api/management_v2.go) 第 454-498 行仍统一返回 `accepted: true`。同文件第 327、348、357、377、379 行忽略 store error，会把读失败降级成错误的 liveness 投影，必须修正后才能通过 D3。

## 5. Source authority

### 5.1 SourceContext

所有事实写入都必须使用由服务端注入的 `SourceContext`：

```text
source_id
source_kind
producer_version
transport_or_auth_binding_ref
```

`source_id` 不能作为普通 JSON 字段被 caller 自报。未来可以由 mTLS、签名 token、本机受限 socket 或受信 adapter 绑定，但本轮不选择认证技术；在绑定机制落地前，curl 和 mock adapter 只能算 Mock contract evidence，不能拥有生产 source authority。

### 5.2 八仓 authority ledger

| Source | 可以写 | 只可投影/引用 | 绝不能写 |
|---|---|---|---|
| Platform Registry | Robot/Device/Runtime identity、retire 状态 | external identity mapping | Runtime heartbeat、Domain result |
| `robot-control-runtime` 的已绑定 Runtime producer | 自身 Session、Heartbeat、runtime/device Fault、Version ref | evidence/artifact ref | AMR/Panda Task success、Fault 根因的平台侧强制 clear |
| AMR domain executor / Mock WMS authority | AMR Task accept/reject/status、AMR domain Run outcome | AMR log/evidence refs | Nav2 控制细节以外域结果、Panda result |
| Panda MuJoCo/Isaac executor + upstream Task GT | Panda domain Run outcome、Task GT result | Episode 和执行证据 refs | PyBullet replay 结论或 DataLab Gate 冒充 Task GT |
| `robot-arm-episode-data-lab` | Release/Checkpoint/Evaluation refs 和权威评测摘要 | provenance、handoff refs | Task success、Runtime liveness |
| PyBullet replay executor | `validation_replay` outcome、risk/replay refs | readiness/risk summary | Panda `domain_execution` success、Sim2Real 声明 |
| Digital Twin bridge/device source | Device condition/Fault、firmware/bridge Version | device evidence refs | Task outcome、Robot ready、motor command 经 Platform |
| Dashboard/operator | 高层 Task intent、Alert ack | 查询 Platform facts/history | canonical identity、Heartbeat、Fault clear、Run outcome |

同一个 source 只能写绑定对象和允许的事实类型。repository 名、request body 中的 `producer_repo` 或 RuntimeRole 字符串都不能单独证明 authority。

当前 [`integration_contracts.go`](../internal/adapter/integration_contracts.go) 只是未接线 interface/mock；公开 `/v2` handler 也没有 SourceContext。因此所有 source-authoritative write path 仍是 `Not accepted`。

## 6. Run result authority

### 6.1 Run 是关联台账，不是 caller 声明

Run 创建只建立执行关联：

```text
run_id + run_kind + task_id + robot_id
authoritative runtime_id/session_id
started_at_reported/received
source_id + source_event_id + producer_version
```

caller 不能在创建 Run 时同时提交 terminal `result`，也不能靠提交一个 ArtifactReference 推导成功。Run outcome 是 source-attributed event 的只读投影，至少保留 outcome、source、producer version、reported/received time 和 source event ID。

### 6.2 Run kind 与结果权威

| `run_kind` | 可用结果范围 | 唯一结果权威 | 明确禁止 |
|---|---|---|---|
| `domain_execution` / AMR | AMR 接受、执行和终态 | 对应 AMR domain executor | Dashboard、`rcrd` heartbeat、Platform 自行判成功 |
| `domain_execution` / Panda | Panda 执行状态和 Task GT 终态 | 对应 Panda executor + upstream Task GT | DataLab Offline Pass、PyBullet replay/risk 改写 Task GT |
| `validation_replay` | replay started/completed/failed 和风险摘要 | 对应 replay producer | 使用 `succeeded` 冒充真实 Panda Task success 或 Sim2Real |

结果写入规则：

1. source 必须与 Run 的 authoritative producer 和 `run_kind` 匹配。
2. `(source_id, source_event_id)` 完全相同的重试幂等；不同 payload 冲突。
3. terminal outcome 一旦由合法 authority 确认，不允许另一个 source 覆盖。
4. ArtifactReference 是独立、typed、source-attributed 的外部引用；添加 Evaluation、Episode 或 risk report 不改变 Run outcome。
5. `rcrd` 可以提供 lifecycle、Fault 和 evidence，但 `control_runtime` 角色本身不授予 Task result authority。

当前 [`RunV2`](../internal/domain/management_v2.go) 第 192-205 行只有自由字符串 `result` 和单个 artifact；[`handleCreateRunV2`](../internal/api/management_v2.go) 第 526-581 行允许任意 caller 同时提交 result、结束时间和 artifact producer；store 第 518-575 行只检查引用和 Robot 一致性。该 endpoint 可以保留作本地草案，但在 source/result contract 落地前不得作为真实 Run ingestion API。

本轮不新增 endpoint，也不决定最终 command/event URL。后续实现可以拆分 create 与 outcome event，也可以在既有路径内收紧，但必须先满足上述 authority，而不是为了 REST 形式增加更多 CRUD。

## 7. 测试合同必须如何改变

当前测试是本地 contract fixture，不是资产或集成证据。[API 测试](../internal/api/management_v2_test.go) 第 193-200、238-243 行组合了 fictional physical Robot 与 `rcrd`；第 255-263 行把 late heartbeat 写成 HTTP accepted；第 313-344 行又允许同一 caller 经 `control_runtime` 直接提交 `succeeded` 和任意 producer artifact。这些断言只能记录当前代码，不能被提升为目标语义。

D2/D3 实现验收至少需要这些负向测试：

- canonical Robot/Device/Runtime ID 不能由 caller 注入；非法枚举、孤儿 FK、跨 Robot parent/host、containment cycle 全部失败；
- ExternalRef 重复映射冲突，序列化/反序列化错误不能静默丢失；
- 同 session ID 的精确重复为幂等，不同 version/start/source 为 409，旧 ID 永不重新激活；
- 并发启动 session 后数据库中仍至多一个 current，supersede 失败必须回滚；
- heartbeat duplicate 不刷新 received time，late heartbeat recorded 但不 applied，DB read error 不生成伪 liveness；
- 未绑定 source、错误 Runtime source、Dashboard caller 均不能上报 Session/Heartbeat/Run result；
- AMR authority 不能写 Panda outcome，PyBullet replay 不能写 `domain_execution` Task success，DataLab Evaluation 不能覆盖 Task GT；
- 第一个合法 terminal outcome 可重试但不能被不同 source 或不同 payload 覆盖。

## 8. Gate 与后续实施顺序

| Gate | 设计审阅 | 当前代码一致性 | 结论 |
|---|---|---|---|
| D2：目标数据模型 | `Complete` | `Pass`（`gofmt`/`go test`/`go vet` 已通过） | 不登记真实资产；D2 关闭后仍须等 D3 |
| D3：管理面事件与权威 | `Complete` | `Fail` | 禁止真实 producer 和 Dashboard 接入 |
| D4：首个真实 producer | `Not started` | 不适用 | 不能开始 |

获得明确实施授权后，代码工作必须按以下顺序进行：

1. ~~在可用 Go 1.26 环境执行 `gofmt`、`go test ./...`、`go vet ./...`，关闭 D2 verification。~~ **已完成（本机 Go 1.26.5）。**
2. D3 trusted SourceContext seam：先能拒绝无权 source，再谈真实认证或 adapter。
3. Session conflict：不可变 payload 比较、Version binding 和幂等响应。
4. Heartbeat admission result：recorded/applied/idempotent 分离，并修复 liveness read error handling。
5. Run authority：`run_kind`、correlation-only create、source-attributed outcome projection。
6. 重写并通过 D3 source/outcome 负向测试。

停止线：在 2-6 全部通过前，不新增 endpoint、不接 Dashboard、不选择 D4 producer、不迁移 v1 demo row、不把测试 fixture 写成真实 physical Robot 或跨仓证据。
