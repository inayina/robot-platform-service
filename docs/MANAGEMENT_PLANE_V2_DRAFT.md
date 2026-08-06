# Management Plane v2 本地实现草案

- 状态：`Local draft / D2 Verified (gofmt/go test/go vet) / D3 non-conformant`
- 依据：[Robot / Device / Runtime 拆分合同](ROBOT_DEVICE_RUNTIME_CONTRACT.md)
- 一致性审阅：[Management Plane v2 D2/D3 一致性审阅](MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md)
- v1 保留：`/v1` 九个端点、六张表、现有 domain/store/api 文件全部原样保留
- 实施策略：`/v2` 新端点、新表、新文件；v1 v2 并行运行

本文描述工作区中的本地实现草案，不是已发布能力、跨仓接入证据或架构验收结果。D2 schema/ID enforcement 已实施，且本机已通过 `gofmt`/`go test ./...`/`go vet ./...`；D3 session、heartbeat、source 和 Run result authority 仍不一致，因此 `/v2` 仍只能称为 Management Plane draft。

## 1. 数据模型

### 1.1 Robot

Robot 是 Platform 的资产聚合根。身份跨 Runtime 重启、软件升级、Device 固件升级保持稳定。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | TEXT PK | server-generated `rob-{opaque}`；当前 issuer 使用 32 位小写 hex；strict create body 不接受 caller ID | Platform canonical robot_id |
| `display_name` | TEXT NOT NULL | mutable | 人类可读名称 |
| `domain` | TEXT NOT NULL | required | `amr` / `panda` 等路由命名空间 |
| `embodiment` | TEXT NOT NULL | required | `physical` / `simulation` |
| `lifecycle_state` | TEXT NOT NULL | required | `active` / `retired` |
| `external_refs` | relation | `robot_external_refs`；`(namespace,value)` unique | API 仍投影为结构化数组，不在 Robot row 内保存 JSON |
| `created_at` | INTEGER NOT NULL | server-owned | epoch ms |
| `updated_at` | INTEGER NOT NULL | server-owned | epoch ms |

禁止出现在 Robot：`heartbeat_interval`、`software_version`、`current_pose`、`task_success`、`ready`。

### 1.2 Device

Device 是 Robot 下具有独立资产身份的硬件或仿真端点。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | TEXT PK | server-generated `dev-{opaque}`；当前 issuer 使用 32 位小写 hex；strict create body 不接受 caller ID | Platform canonical device_id |
| `robot_id` | TEXT NOT NULL FK→robots | immutable | 所属 Robot |
| `parent_device_id` | TEXT FK→devices_v2 | optional | 同一 Robot 内的 containment |
| `display_name` | TEXT NOT NULL | mutable | 人类可读名称 |
| `device_class` | TEXT NOT NULL | required | `compute`/`controller`/`sensor`/`actuator`/`bus_node`/`composite` |
| `domain_type` | TEXT | optional | 域内类型名，Platform 不解析 |
| `manufacturer` | TEXT | optional | 静态硬件身份 |
| `model` | TEXT | optional | 静态硬件身份 |
| `serial_number` | TEXT | optional | 静态硬件身份 |
| `lifecycle_state` | TEXT NOT NULL | required | `active` / `retired` |
| `external_refs` | relation | `device_external_refs_v2`；`(namespace,value)` unique | firmware/domain 侧 namespaced identity |
| `created_at` | INTEGER NOT NULL | server-owned | epoch ms |
| `updated_at` | INTEGER NOT NULL | server-owned | epoch ms |

禁止出现在 Device：`heartbeat_interval`、`software_version`、CAN heartbeat seq、sensor sample。

### 1.3 Runtime

Runtime 是部署到一台 Robot、需要独立登记和观测的软件实例。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | TEXT PK | server-generated `rt-{opaque}`；当前 issuer 使用 32 位小写 hex；strict create body 不接受 caller ID | Platform canonical runtime_id，与 Run ID namespace 分离 |
| `robot_id` | TEXT NOT NULL FK→robots | immutable | 所属 Robot |
| `display_name` | TEXT NOT NULL | mutable | 人类可读名称 |
| `runtime_role` | TEXT NOT NULL | required | `control_runtime`/`domain_executor`/`device_bridge`/`replay_executor` |
| `component` | TEXT NOT NULL | required | 稳定组件名，如 `rcrd` |
| `host_device_id` | TEXT FK→devices_v2 | optional | 部署所在 compute Device |
| `heartbeat_interval_ms` | INTEGER NOT NULL | required, default 5000 | 低频管理面期望周期 |
| `lifecycle_state` | TEXT NOT NULL | required | `active` / `retired` |
| `external_refs` | relation | `runtime_external_refs`；`(namespace,value)` unique | deployment 侧 namespaced identity |
| `created_at` | INTEGER NOT NULL | server-owned | epoch ms |
| `updated_at` | INTEGER NOT NULL | server-owned | epoch ms |

### 1.4 RuntimeSession

一次进程执行的不可复用代次。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `session_id` | TEXT | required, producer-generated | 每次进程启动生成，该 runtime 下唯一 |
| `runtime_id` | TEXT FK→runtimes | required, immutable | 所属 Runtime |
| `software_version_ref` | TEXT NOT NULL | 目标：required, resolvable Version ref；当前：自由字符串，默认 `unknown` | 真实 producer 不得用匿名 `unknown` 代替 build 身份 |
| `started_at_reported` | INTEGER | optional | producer wall clock |
| `started_at_received` | INTEGER NOT NULL | server-owned | Platform 首次接收时间 |
| `ended_at_reported` | INTEGER | optional | producer 正常结束上报 |
| `ended_at_received` | INTEGER | optional, server-owned | Platform 接收结束事件时间 |
| `session_state` | TEXT NOT NULL | server projection | `current` / `ended` / `superseded` |

复合主键：`(runtime_id, session_id)`。

### 1.5 RuntimeHeartbeat

管理面存活心跳。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `runtime_id` | TEXT FK→runtime_sessions | required | 稳定 Runtime 身份 |
| `session_id` | TEXT FK→runtime_sessions | required | 本次进程执行代次 |
| `seq` | INTEGER NOT NULL | session 内严格递增 | 正整数，新 session 从 1 重新开始 |
| `reported_at` | INTEGER | optional | producer wall clock，不用于活性判定 |
| `received_at` | INTEGER NOT NULL | server-owned | Platform 接收时间，唯一 liveness 时钟来源 |

UNIQUE: `(runtime_id, session_id, seq)`。

### 1.6 Run (v2)

一次执行关联台账。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | TEXT PK | server-generated `run-{opaque}`；当前 issuer 使用 32 位小写 hex；strict create body 不接受 caller ID | Platform run ID，API 版本不进入产品 identity |
| `task_id` | TEXT NOT NULL FK→tasks | required | 关联 Task |
| `robot_id` | TEXT NOT NULL FK→robots | required | 执行 Robot |
| `runtime_session_id` | TEXT NOT NULL FK→runtime_sessions | required | authoritative executor session |
| `started_ms` | INTEGER NOT NULL | required | 执行开始时间 |
| `ended_ms` | INTEGER | optional | 执行结束时间 |
| `result` | TEXT | 当前：caller-supplied free string，non-conformant | 目标 outcome 必须来自与 `run_kind` 匹配的 source authority |
| `artifact_ref` | TEXT | 当前：单个 caller-supplied JSON，non-conformant | 目标是独立、typed、source-attributed reference，不改变 Run outcome |

### 1.7 ArtifactReference (typed, embedded in Run)

| 字段 | 类型 | 说明 |
|---|---|---|
| `type` | TEXT | `episode` / `checkpoint` / `evaluation` / `release` / `log` |
| `uri` | TEXT | 域内定位符 |
| `hash_sha256` | TEXT | 内容校验 hash |
| `producer_repo` | TEXT | 产生此产物的仓库 |
| `producer_version` | TEXT | 产生此产物的版本 |

### 1.8 ExternalRef

| 字段 | 类型 | 说明 |
|---|---|---|
| `namespace` | TEXT | 如 `amr_wms.robot_id`、`ros2.node_fqn` |
| `value` | TEXT | 该命名空间下的 ID 值 |

## 2. 枚举

### 2.1 RobotLifecycleState

| 值 | 含义 |
|---|---|
| `active` | 资产在位，可被引用 |
| `retired` | 已退役，保留历史引用 |

### 2.2 DeviceClass

`compute` / `controller` / `sensor` / `actuator` / `bus_node` / `composite`

### 2.3 RuntimeRole

`control_runtime` / `domain_executor` / `device_bridge` / `replay_executor`

### 2.4 SessionState

| 值 | 含义 |
|---|---|
| `current` | 当前活跃 session |
| `ended` | 正常结束 |
| `superseded` | 被新 session 取代 |

### 2.5 RuntimeLiveness

| 值 | 含义 |
|---|---|
| `unknown` | active Runtime 从未建立 session 或 current session 尚无 Heartbeat |
| `online` | age ≤ 3 × heartbeat_interval_ms |
| `stale` | 3 × interval < age ≤ 6 × interval |
| `offline` | age > 6 × interval，或 session 明确结束 |

## 3. 强制不变量

1. `device.robot_id` 与 `runtime.robot_id` 不可为空。
2. `parent_device_id` 所属 Robot 必须与 child Device 一致。containment 无环。
3. `host_device_id` 如果存在，必须属于同一 Robot。
4. `runtime_session.runtime_id` 不可变；`session_id` 不可复用。
5. Heartbeat `(runtime_id, session_id, seq)` 唯一，seq 只在 session 内严格递增。
6. 同一 Runtime 最多有一个 current session。新 session 创建时旧 current session → superseded。
7. 非 current session 的 Heartbeat 只保留审计，不更新 liveness。当前 HTTP `accepted` 响应语义尚未与该规则完全对齐，属于已知审计缺口。
8. Run 的 `robot_id` 必须与 authoritative `runtime_session` 所属 Runtime 的 `robot_id` 一致。
9. 被 Run、Fault、Version 或 Artifact Reference 引用的身份只能 retire，不能硬删除。

## 4. API 端点 (v2)

所有 v2 端点以 `/v2/` 为前缀。v1 端点全部保留不变。

### 4.1 Robot

```
POST   /v2/robots                        # 注册 Robot
GET    /v2/robots                        # 列表
GET    /v2/robots/{id}                   # 详情
```

### 4.2 Device

```
POST   /v2/robots/{robot_id}/devices     # 注册 Device
GET    /v2/robots/{robot_id}/devices     # Robot 下 Device 列表
GET    /v2/devices/{id}                  # Device 详情
```

### 4.3 Runtime

```
POST   /v2/robots/{robot_id}/runtimes    # 注册 Runtime
GET    /v2/robots/{robot_id}/runtimes    # Robot 下 Runtime 列表
GET    /v2/runtimes/{id}                 # Runtime 详情
GET    /v2/runtimes/{id}/liveness        # Runtime liveness 查询
```

### 4.4 RuntimeSession

```
POST   /v2/runtimes/{id}/sessions        # 开启新 session
GET    /v2/runtimes/{id}/sessions        # session 历史列表
POST   /v2/runtimes/{id}/sessions/{sid}/end  # 结束 session
```

### 4.5 RuntimeHeartbeat

```
POST   /v2/runtimes/{id}/sessions/{sid}/heartbeats   # 上报心跳
```

### 4.6 Run

```
POST   /v2/runs                          # 创建 Run
GET    /v2/runs                          # Run 列表
GET    /v2/runs/{id}                     # Run 详情
```

### 4.7 Health

```
GET    /v2/health                        # v2 健康检查
```

## 5. 跨仓 Adapter 接口

以下接口在 `internal/adapter/` 定义，当前不接入真实对端，只提供 mock 实现用于测试。

### 5.1 TaskSource

```go
type TaskSource interface {
    SubmitTask(ctx context.Context, task *domain.Task) (accepted bool, err error)
    ReportTaskStatus(ctx context.Context, taskID string, status domain.TaskStatus) error
}
```

### 5.2 FaultSource

```go
type FaultSource interface {
    ReportFault(ctx context.Context, fault Fault) error
    ClearFault(ctx context.Context, faultID string) error
}
```

### 5.3 ArtifactRegistry

```go
type ArtifactRegistry interface {
    RegisterArtifact(ctx context.Context, ref domain.ArtifactRef) error
    ResolveArtifact(ctx context.Context, uri string) (*domain.ArtifactRef, error)
}
```

## 6. 测试计划

### 6.1 Store 层

| 场景 | 验证点 |
|---|---|
| Registry identity | API caller 不能注入 canonical ID；store 内重复 ID/ExternalRef → conflict |
| Device 创建 | robot_id 不存在、parent 不同 Robot、断链或 cycle → fail closed |
| Runtime 创建 | robot_id 不存在或 host_device 不同 Robot → 拒；ID namespace 与 Run 分离 |
| Session 生命周期 | 新建 → current；精确重复 → 幂等；同 ID 不同 version/start/source → conflict；新 ID 原子 supersede |
| Heartbeat | session 内 seq 递增；回归 → ErrSeqRegression；非 current session 可存档但不应用到 liveness |
| Heartbeat last_seen | 仅 current session 的 `last_heartbeat_at_ms` 随首次接收的新心跳更新；重复/迟到不刷新 |
| Run v2 | robot-session 一致；caller 不得在 create 声明结果；错误 source/run_kind 不得写 outcome |
| Liveness | unknown → online → stale → offline；新 session 恢复 online |

### 6.2 API 层

与现有 api_test.go 同模式：`:memory:` SQLite + 注入时钟 + httptest.Server。场景覆盖上述全部端点路径。

## 7. v1/v2 共存

- `/v1` 九个端点：零改动，继续标 Phase 1 prototype
- `/v2` 端点：本文记录的本地 Management Plane 草案端点
- v1 表 (`devices`, `heartbeats`, `tasks`, `runs`, `alerts`, `versions`)：不改
- v2 core 表 (`robots`, `devices_v2`, `runtimes`, `runtime_sessions`, `runtime_heartbeats`, `runs_v2`)：新建
- v2 identity mapping 表 (`robot_external_refs`, `device_external_refs_v2`, `runtime_external_refs`)：D2 新建
- `runs_v2.task_id` FK 引用 v1 `tasks` 表（Task 暂不拆分）
- 两套 handler 注册在同一 http.ServeMux，路径前缀隔离

## 8. D2/D3 一致性结论

- 五项完整判定、authority ledger、truth table 和实施顺序见 [D2/D3 一致性审阅](MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md)。
- D2 schema/ID 代码已实施；SQLite 约束与本机 Go 1.26.5 的 `gofmt`/`go test ./...`/`go vet ./...` 均已通过，D2 verification Gate 为 `Pass`。
- D3 implementation Gate 仍为 `Fail`。
- RuntimeSession 的 producer identity、重用冲突和 Version binding 尚未落实。
- 旧 session 的迟到 Heartbeat 虽不更新 liveness，但 HTTP `accepted` 仍混淆“已存档”和“已用于活性”。
- Run result 与 Artifact Reference 仍可由调用方直接提交，不能证明来自对应 Domain executor 或 Panda Task GT。
- `internal/adapter/` 只是未接线的合同 seam 和本地 mock，不是七仓真实集成。
