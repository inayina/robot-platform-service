# robot-platform-service

`robot-platform-service` 是这个机器人作品集的跨域管理面。它统一 Robot、Device 与 Runtime 的身份关系，关联 Task 与 Run，并汇聚 Heartbeat、Fault、Version 和 Artifact Reference，为 `robot-ops-dashboard` 提供可审计的系统视图。它不参与 ROS 2/CAN 控制、Nav2、策略执行、Task GT 或训练评测，也不保存 Episode、Checkpoint 和 Evaluation 本体；各域仓库仍是业务、安全与验证事实的权威来源。当前代码是 Phase 1 合同原型，尚未完成跨仓实时接入。

完整的八仓职责划分、对象权威、当前态/目标态架构和验收条件见 [八仓系统架构](docs/SYSTEM_ARCHITECTURE.md)。身份拆分语义见 [Robot / Device / Runtime 拆分合同](docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md)，八仓实际候选及排除项见 [Registry 对象分类账](docs/REGISTRY_OBJECT_CLASSIFICATION.md)，本地代码偏差见 [D2/D3 一致性审阅](docs/MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md) 和 [Management Plane v2 本地草案](docs/MANAGEMENT_PLANE_V2_DRAFT.md)。目标图、字段合同、分类候选和本地草案都不代表当前已经完成跨仓集成。

## 最终定位

Platform 是跨机器人域的 Management Plane，也是 identity、correlation 和 operational projection 的 system of record：

- 登记 Robot、Device、Runtime 及其版本关系；
- 接收高层 Task intent，并为 Task/Run 分配跨域关联 ID；
- 汇聚低频 Runtime Heartbeat、Fault 和运维 Alert；
- 索引 Episode、Checkpoint、Evaluation、日志和报告等外部产物；
- 保留 producer、Git SHA、hash 和权威来源，使结果可追溯。

Platform 不是控制系统、数据湖、训练平台或 Dashboard backend。Platform 离线时，本地 Runtime、watchdog、Hold/E-stop 和设备安全链仍必须安全运行；丢失的是全局管理、关联与审计能力。

## 当前实现状态

| 状态 | 当前事实 |
|---|---|
| `Verified` | Phase 1 Device/Heartbeat/Task/Run 的最小 SQLite 持久化与 HTTP API |
| `Verified` | Phase 1 `ok/stale/missing` 判定支持注入时钟 |
| `Verified` | D2 schema/ID enforcement：server-issued `rob/dev/rt/run` ID、FK/CHECK、单 current session、ExternalRef mapping、显式 migration stop；`gofmt`/`go test ./...`/`go vet ./...` 已通过 |
| `Design only / Implementation fail` | D3 session payload conflict、late heartbeat response、SourceContext 和 Run result authority 尚未实现 |
| `Local draft / Not integrated` | Management Plane v2 仍不能接入真实 producer，也不是跨仓集成证据 |
| `Reserved` | `alerts`、`versions` 表已建，但没有开放端点 |
| `Prototype` | Phase 1 `Device` 暂时混合了 Robot、Device、Runtime 的含义（v1 保留） |
| `Not integrated` | 当前没有连接其它七个仓库；演示只使用 curl/脚本 |
| `Proposed` | Fault/Alert 完整模型、跨仓 integration contract 真实接入仅在架构文档中定义 |

工作区中 v1（Phase 1 原型）与 Management Plane v2 本地草案并行挂载在同一端口。v1 端点保持不变；v2 以 [拆分合同](docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md) 为目标。D2 Go verification Gate 已关闭；D3 仍不一致，因此整体 `/v2` 不能称为已验收实现。

## 边界

| Platform 应负责 | Platform 绝不负责 |
|---|---|
| Robot、Device、Runtime 的全局身份与关系 | CAN/UART/ROS 2 topic、MoveIt、Nav2、PID 和实时命令循环 |
| Task 信封和 Run 关联台账 | 域任务校验、执行、Task GT 和成功判定 |
| 低频 Heartbeat、Fault 投影和 Alert | watchdog、deadline、Hold/E-stop、故障恢复和 Fault 根因清除 |
| 软件/固件 Version 登记 | OTA 刷写和部署执行 |
| typed Artifact Reference 与 provenance | Episode、图像、点云、Checkpoint、Evaluation 报告本体 |
| 为 Operations UI 提供事实与历史 API | 页面、图表、视频代理、WebSocket 客户端和 UI 状态 |

特别注意：确认 Alert 不等于清除 Fault；Offline Pass 不等于 Task Success；Replay Complete 不等于 Sim2Real。

## Phase 1 快速开始

```bash
# go.mod 当前声明 Go 1.26；纯 Go SQLite 驱动，无 CGO。
# 国内网络可按本机环境配置 GOPROXY。
go run ./cmd/platformd -addr :9100 -db data/platform.db

# 另一个终端运行 Phase 1 合同演示
bash scripts/demo.sh

# 运行 Management Plane v2 本地合同演示（不代表跨仓集成）
bash scripts/demo-management-plane.sh
```

## Phase 1 API (/v1)

以下九个端点只描述当前原型，不代表最终对象模型：

| 方法 | 路径 | 当前行为 |
|---|---|---|
| POST | `/v1/devices` | 注册原型 Device 信封 |
| GET | `/v1/devices` | Device 列表和计算后的 status |
| GET | `/v1/devices/{id}` | Device 详情与最近心跳 |
| POST | `/v1/devices/{id}/heartbeats` | 上报心跳；`seq` 必须严格递增 |
| POST | `/v1/tasks` | 创建原型 Task 记录 |
| GET | `/v1/tasks?status=` | Task 列表，可按状态过滤 |
| POST | `/v1/runs` | 创建原型 Run，检查 Task/Device 引用 |
| GET | `/v1/runs/{id}` | Run 详情 |
| GET | `/v1/health` | 服务进程健康检查 |

## Management Plane draft API (/v2)

工作区草案注册了 18 个 v2 handler，用于演示 Robot/Device/Runtime 拆分合同：

| 方法 | 路径 | 行为 |
|---|---|---|
| POST | `/v2/robots` | 注册 Robot |
| GET | `/v2/robots` | Robot 列表 |
| GET | `/v2/robots/{id}` | Robot 详情 |
| POST | `/v2/robots/{robot_id}/devices` | 注册 Device(含 parent containment) |
| GET | `/v2/robots/{robot_id}/devices` | Robot 下 Device 列表 |
| GET | `/v2/devices/{id}` | Device 详情 |
| POST | `/v2/robots/{robot_id}/runtimes` | 注册 Runtime |
| GET | `/v2/robots/{robot_id}/runtimes` | Robot 下 Runtime 列表(含 liveness) |
| GET | `/v2/runtimes/{id}` | Runtime 详情 + current session + liveness |
| GET | `/v2/runtimes/{id}/liveness` | Runtime liveness(unknown/online/stale/offline) |
| POST | `/v2/runtimes/{id}/sessions` | 开启新 RuntimeSession(旧 session → superseded) |
| GET | `/v2/runtimes/{id}/sessions` | Session 历史列表 |
| POST | `/v2/runtimes/{id}/sessions/{sid}/heartbeats` | 上报心跳(session 内 seq 严格递增) |
| POST | `/v2/runtimes/{id}/sessions/{sid}/end` | 结束 session |
| POST | `/v2/runs` | 创建 Run(引用 Robot + RuntimeSession) |
| GET | `/v2/runs?robot_id=` | Run 列表，可按 robot 过滤 |
| GET | `/v2/runs/{id}` | Run 详情(含 typed artifact_ref) |
| GET | `/v2/health` | v2 健康检查 |

## 当前状态判定

Device 注册时声明 `heartbeat_interval_ms`（默认 5000）：

| 条件（age = 当前时间 - 最后心跳） | 状态 |
|---|---|
| 从未收到心跳 | `missing` |
| age <= 3 x interval | `ok` |
| 3 x interval < age <= 6 x interval | `stale` |
| age > 6 x interval | `missing` |

Phase 1 判定逻辑位于 `internal/domain/status.go`，时钟可注入。Management Plane v2 本地草案已经引入 RuntimeSession、reported time 和 received time；目标语义见 [D2/D3 一致性审阅](docs/MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md)。D2 identity 与 Go verification 已通过；迟到事件响应、source authority 和 Run result authority 仍未通过 D3 Gate。

## 当前目录与数据模型

```text
cmd/platformd/                         入口：flag 配置与服务启动，v1/v2 双路由挂载
internal/domain/management_v2.go       Management Plane v2 草案对象模型
internal/store/management_v2.go        Management Plane v2 草案持久化
internal/store/schema_d2_test.go       D2 FK/CHECK/current-session/migration 负向测试
internal/api/management_v2.go          Management Plane v2 草案 HTTP 路由
internal/adapter/integration_contracts.go  尚未接线的跨仓合同 seam
scripts/demo.sh                        Phase 1 curl 合同演示
scripts/demo-management-plane.sh       Management Plane v2 本地合同演示
docs/SYSTEM_ARCHITECTURE.md            八仓系统职责与 current/target 架构
docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md  身份、寿命、权威和不变量合同
docs/REGISTRY_OBJECT_CLASSIFICATION.md 八仓 Registry 候选、排除项与 D1 Gate
docs/MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md D2/D3 决策、代码偏差和实施 Gate
docs/MANAGEMENT_PLANE_V2_DRAFT.md      未验收实现与已知缺口记录
```

当前 schema 有 15 张表：
- v1(6 张): `devices`、`heartbeats`、`tasks`、`runs`、`alerts`(reserved)、`versions`(reserved)
- v2 core(6 张): `robots`、`devices_v2`、`runtimes`、`runtime_sessions`、`runtime_heartbeats`、`runs_v2`
- v2 identity mapping(3 张): `robot_external_refs`、`device_external_refs_v2`、`runtime_external_refs`

ExternalRef mapping 使用独立表落实 `(object_kind, namespace, value)` 唯一性。Episode、Dataset/Release、Checkpoint、Evaluation 的本体不入库；v2 `runs_v2.artifact_ref` 仍是 D3 未收敛的 typed JSON 草案。

D2 schema 使用 SQLite `user_version=2`。v1-only 数据库可以 additive 创建 v2 表；如果数据库已经包含 pre-D2 v2 表，服务会返回 `ErrMigrationRequired` 并拒绝启动，不删除、不覆盖、也不根据 demo 字段猜测迁移。当前仓库没有需要迁移的真实 v2 资产数据。

## 测试

```bash
go test ./...
go vet ./...
```

仓库包含 Phase 1、Management Plane v2 和 D2 schema/ID 负向测试。本机 Go 1.26.5 已执行 `gofmt`、`go test ./...`、`go vet ./...` 并通过；本机 SQLite 3 另验证过 schema 可解析、`user_version=2`、FK/orphan、单 current session、ExternalRef uniqueness 和 containment cycle 约束。这些证据只关闭 D2 verification Gate，不证明七仓已接入、真实机器人、实时性、安全认证或任务成功；D3 仍为 Fail。

## 文件命名规则

| 命名 | 表达的事实 |
|---|---|
| `SYSTEM_ARCHITECTURE.md` | 作品集级职责、边界和 current/target 关系 |
| `ROBOT_DEVICE_RUNTIME_CONTRACT.md` | 已冻结的对象语义与权威，不等于代码已验收 |
| `MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md` | D2/D3 目标语义、代码偏差、Gate 和后续实施顺序 |
| `MANAGEMENT_PLANE_V2_DRAFT.md` | 工作区内未重新验证的实现草案 |
| `management_v2.go` | Management Plane 职责下的 `/v2` 兼容代际 |

数据库表名 `devices_v2`、`runs_v2` 和 Go 类型名 `DeviceV2`、`RunV2` 暂时保留为内部兼容标记。它们不是产品层命名，也不会在本次文件收敛中被包装成新能力。

## 跨仓关系

当前状态：`robot-platform-service` 与其它七仓均为 **Not integrated**。

- `robot-ops-dashboard` 仍通过自己的 backend 直接连接 AMR、Digital Twin 和 mock 数据；这属于当前 Operations 演示路径，不是 Platform 集成。
- Panda 三仓继续通过文件/产物合同完成采集、训练评估与 replay/risk 交接。
- `robot-control-runtime` 继续独立运行本地状态机、watchdog 和 Evidence Plane。
- 本仓的 curl/script 只模拟 HTTP 合同，没有真实 Runtime、AMR、Panda 或 Device producer。

低级命令代理不是 Platform 的后续目标。未来接入也只允许高层 Task 信封和低频管理事件，不把控制闭环迁入本服务。
