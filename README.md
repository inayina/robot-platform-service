# robot-platform-service

管理系统汇聚服务——统一 Robot/Device/Runtime 身份与运行时状态。v1(Phase 1 原型)和 v2(拆分合同草案)双路由同端口并行挂载。

**当前真实集成链路：**

```
Orange Pi (192.168.1.22, aarch64, 无 SocketCAN)
        │
        │  Edge Agent: 主机指标 + 心跳   HTTP POST /v1/devices/*/heartbeats
        ▼
robot-platform-service  ◀────  curl / 脚本查询
        │
        └── HTTP API (v1 + v2) → SQLite
```

## 边界声明(2026-08-07 实施状态)

| 声明 | 事实 |
|---|---|
| 集成链路 | **仅 Orange Pi Edge Host → Platform**。Panda / AMR / Digital Twin 尚未接入 Platform |
| Dashboard | **尚未接入** Platform v1/v2 API。`robot-ops-dashboard` 当前走自有 backend |
| robot-control-runtime Core | **尚未接入**。rcrd 未在 Orange Pi 常驻运行；Edge Agent 只上报管理面,不进控制链路 |
| SocketCAN | **Orange Pi 内核 `# CONFIG_CAN is not set`**——`can_available` 如实上报 `false`,无伪装 |
| Platform 角色 | **不参与实时控制**。Platform 离线时,本地 Runtime/watchdog/E-stop 必须安全运行 |
| 部署规模 | **单机/少量设备实验**,不是生产级 Fleet Platform |
| OTA / Version 管理 | **不做**。`versions` 表 reserved |
| 网络中断 | **不影响本地控制安全**。Agent 退避重试;Platform 数据有缺口但不回填 |
| 控制消息 | **不发不收**。Edge Agent 没有重放过期命令的逻辑,因为没有命令可发 |

## Edge Agent(Orange Pi)

### 职责

- 只做管理面:设备注册 + 心跳 + 主机指标上报
- 不进控制链路(不接 CAN/Panda/AMR)
- Agent 重启 → 新 `session_id`(代理生命周期)
- 网络/平台故障 → 退避重试(5xx)或终止(4xx)

### 上报内容

```
robot_id / device_id   → orname Pi 主机身份(注册时声明)
runtime_type           → "orangepi-edge"
runtime_version        → 构建时注入(默认 0.1.0-dev)
session_id             → 每次 Agent 启动生成新 UUID
heartbeat seq          → 严格递增
hostname / arch / os   → 从 /proc 采集
cpu_percent            → /proc/stat 瞬时值
memory_percent         → /proc/meminfo (MemAvailable 法)
temperature_celsius    → /sys/class/thermal/thermal_zone0/temp(不可用时 nil→omit)
can_available          → 检查 /sys/class/net/can* 存在性(真实判断)
runtime_state          → "idle" / "shutdown"
last_fault             → 无故障时为空
```

### 用法

```bash
# 构建(交叉编译 ARM64)
GOARCH=arm64 GOOS=linux go build -o edge-agent ./cmd/edge-agent

# 复制到 Orange Pi
scp edge-agent orangepi@192.168.1.22:~/

# 在 Orange Pi 上运行(环境变量注入)
export PLATFORM_BASE_URL=http://192.168.1.8:9100
export ROBOT_ID=opi-edge-001
export HEARTBEAT_INTERVAL_MS=3000
~/edge-agent

# 验证(ThinkPad 上)
curl http://localhost:9100/v1/devices               # 设备列表+状态
curl http://localhost:9100/v1/devices/opi-edge-001  # 详情+最近心跳+metrics
```

### 配置

```bash
# 所有配置通过环境变量注入(见 .env.example)
PLATFORM_BASE_URL=http://127.0.0.1:9100
ROBOT_ID=opi-edge-001
DEVICE_ID=            # 空→使用 ROBOT_ID
RUNTIME_TYPE=orangepi-edge
RUNTIME_VERSION=0.1.0-dev
HEARTBEAT_INTERVAL_MS=5000
REQUEST_TIMEOUT_MS=10000
```

### 真机验证记录(2026-08-07 @ Orange Pi 4 Pro)

| 验证项 | 结果 | 说明 |
|---|---|---|
| ARM64 交叉编译 + SCP 部署 | ✅ 8.3 MB 单二进制 | 无 CGO,静态链接 |
| 主机名/架构/OS 采集 | ✅ orangepi4pro / aarch64 / 6.6.98-sun60iw2 | |
| CPU/内存采集 | ✅ 2.4% / 5.5% | /proc 直接读,无第三方 |
| SoC 温度采集 | ✅ 39.7~42.8°C | /sys/class/thermal/thermal_zone0/temp |
| can_available 判断 | ✅ false | 与厂商镜像一致,无 can0 |
| 心跳 seq 递增 | ✅ 1→23(72s 运行) | |
| 新 session_id | ✅ 重启后变化 | sess-1786032878968-d14219ef1825a7f0 ≠ 上次 |
| seq 回归拒绝 | ✅ 409 → agent 退出 | 新 agent seq=1 vs 平台 max=23 |

## Phase 1 快速开始(Platform 服务)

```bash
# 纯 Go SQLite 驱动,无 CGO。国内网络建议:
# export GOPROXY=https://goproxy.cn,direct
go run ./cmd/platformd -addr :9100 -db data/platform.db

# v1 curl 演示
bash scripts/demo.sh
```

## API 概览

### v1(Phase 1,9 端点)

| 方法 | 路径 | 行为 |
|---|---|---|
| POST | `/v1/devices` | 注册设备(含 hostname/arch/os) |
| GET | `/v1/devices` | 设备列表 + 计算后的 status |
| GET | `/v1/devices/{id}` | 设备详情 + 最近心跳(含 metrics + session_id) |
| POST | `/v1/devices/{id}/heartbeats` | 心跳;seq 严格递增;可选 metrics/session_id |
| POST | `/v1/tasks` | 创建任务 |
| GET | `/v1/tasks?status=` | 任务列表 |
| POST | `/v1/runs` | 创建运行记录 |
| GET | `/v1/runs/{id}` | 运行详情 |
| GET | `/v1/health` | 健康检查 |

### v2(拆分合同草案,18 端点)

按 Robot/Device/Runtime 拆分身份;引入 RuntimeSession + RuntimeHeartbeat。

| 方法 | 路径 | 行为 |
|---|---|---|
| POST | `/v2/robots` | 注册 Robot |
| GET | `/v2/robots` | Robot 列表 |
| GET | `/v2/robots/{id}` | Robot 详情 |
| POST | `/v2/robots/{robot_id}/devices` | 注册 Device |
| GET | `/v2/robots/{robot_id}/devices` | Robot 下 Devices |
| GET | `/v2/devices/{id}` | Device 详情 |
| POST | `/v2/robots/{robot_id}/runtimes` | 注册 Runtime |
| GET | `/v2/robots/{robot_id}/runtimes` | Runtime 列表(含 liveness) |
| GET | `/v2/runtimes/{id}` | Runtime + session + liveness |
| GET | `/v2/runtimes/{id}/liveness` | 活性:unknown/online/stale/offline |
| POST | `/v2/runtimes/{id}/sessions` | 创建 session(旧→superseded) |
| GET | `/v2/runtimes/{id}/sessions` | Session 列表 |
| POST | `/v2/runtimes/{id}/sessions/{sid}/heartbeats` | Session 内心跳(seq 严格) |
| POST | `/v2/runtimes/{id}/sessions/{sid}/end` | End session |
| POST | `/v2/runs` | 创建 Run(v2 域) |
| GET | `/v2/runs?robot_id=` | Run 列表 |
| GET | `/v2/runs/{id}` | Run 详情(含 artifact_ref) |
| GET | `/v2/health` | v2 健康检查 |

## 状态判定

| 条件(age = 现在 - 最后心跳) | Device 状态(v1) | Runtime Liveness(v2) |
|---|---|---|
| 从未收到 | `missing` | `unknown` |
| age ≤ 3×interval | `ok` | `online` |
| 3× < age ≤ 6× | `stale` | `stale` |
| age > 6× | `missing` | `offline` |

v1 判定见 `internal/domain/status.go`,v2 见 `internal/domain/platform.go:RuntimeLivenessEvaluator`;均可注入时钟。

## 目录结构

```
cmd/
  platformd/main.go              Platform 服务入口(v1+v2 同端口)
  edge-agent/main.go             Edge Agent 入口(Orange Pi 部署)
internal/
  domain/
    domain.go                    v1: Device/Heartbeat/Task/Run/Alert/SoftwareVersion
    status.go                    v1: ok/stale/missing 判定
    platform.go                  v2: Robot/DeviceV2/Runtime/RuntimeSession/RuntimeHeartbeat/RunV2/liveness
  store/
    store.go                     v1: SQLite 持久化 + 迁移
    schema.sql                   v1+v2 建表(15 张)
    platform.go                   v2: Robot/DeviceV2/Runtime/Session/Heartbeat 持久化
  api/
    api.go                       v1: 9 端点
    platform.go                   v2: 18 端点
  edgeagent/
    agent.go                     Agent 主循环(注册→心跳→信号)
    collector.go                 /proc + /sys 读取(CPU/内存/温度/CAN/OS)
    platformclient/              HTTP 客户端(零依赖,不引 Platform domain)
scripts/
  demo.sh                        v1 curl 演示
.env.example                     Edge Agent 环境变量模板
```

## 跨仓关系(当前态)

| 仓库 | 集成状态 |
|---|---|
| robot-control-runtime | **Not integrated**——Platform 不参与 Runtime Core 控制 |
| ros2-arm-teleoperation-suite | **Not integrated** |
| robot-arm-episode-data-lab | **Not integrated** |
| ros2-moveit-pybullet-bridge | **Not integrated** |
| robot-ops-dashboard | **Not integrated**——走自有 backend |
| amr_warehouse_navigation | **Not integrated** |
| ros2-robot-digital-twin | **Not integrated** |

唯一已验证链路:Orange Pi Edge Agent → Platform(v1 心跳+指标)。

## 测试

```bash
go test ./...        # v1 + edge-agent + platform 全绿(33/33)
go vet ./...
go test -race ./...
```

## 背景知识(面试准备)

[📖 Go 语言面试背景知识](docs/GO_INTERVIEW_GUIDE.md)——基于本项目实操,覆盖类型系统、错误处理、Context、net/http、SQLite、测试、并发模型和常见面试追问。

## 文件命名

| 文件 | 内容 |
|---|---|
| `internal/domain/domain.go` | v1 实体定义 |
| `internal/domain/platform.go` | 平台实体定义(Robot/DeviceV2/Runtime/RuntimeSession 等) |
| `internal/store/store.go` | v1 持久化 |
| `internal/store/platform.go` | 平台持久化 |
| `internal/api/api.go` | v1 HTTP 路由 |
| `internal/api/platform.go` | 平台 HTTP 路由 |
| `internal/store/platform_test.go` | 平台存储层测试 |
| `internal/api/platform_test.go` | 平台 API 集成测试 |
