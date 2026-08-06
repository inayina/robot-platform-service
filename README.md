# robot-platform-service

机器人平台管理面汇聚服务(Phase 1 最小闭环)。

> **定位一句话**:单机/少量设备的管理面——设备注册、心跳汇聚、ok/stale/missing 状态判定、任务与运行记录。
> **信封原则**:平台只统一"信封"(身份/时间/状态/上报结构),不统一"内容"(域 schema 冻结在各仓,平台不解析)。
> 架构依据:[ARCHITECTURE_DESIGN.md](../../portfolio-audit/ARCHITECTURE_DESIGN.md)

## 边界

| 负责 | 绝不负责 |
|---|---|
| 设备注册 + 心跳汇聚 + 状态判定 | 控制闭环、实时控制、策略推理、导航 |
| Task/Run 跨域统一视图(amr/panda 等域并存) | 数据本体存储(episode/图像/点云),只存元数据指针 |
| 健康检查 | 训练/评测判定(Gate 权威在中游,平台只引用) |
| (v1 reserved)告警/版本登记表已建,端点未开 | 多机器人调度、OTA 实际刷写、认证 |

## 快速开始

```bash
# 需要 Go 1.22+(纯 Go SQLite 驱动,无 CGO,单静态二进制)
# 国内网络建议:export GOPROXY=https://goproxy.cn,direct
go run ./cmd/platformd -addr :9100 -db data/platform.db

# 另一个终端:演示脚本
bash scripts/demo.sh
```

## API v1

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/devices` | 注册设备 `{name, kind?, version?, heartbeat_interval_ms?}`;id 缺省自动生成 |
| GET | `/v1/devices` | 设备列表(含计算后的 status) |
| GET | `/v1/devices/{id}` | 设备详情 + 最近心跳 |
| POST | `/v1/devices/{id}/heartbeats` | 心跳上报 `{seq, ts_ms?}`;seq 必须严格递增,否则 409 |
| POST | `/v1/tasks` | 创建任务记录 `{domain, kind?, target?, status?}` |
| GET | `/v1/tasks?status=` | 任务列表(可按状态过滤) |
| POST | `/v1/runs` | 记录一次运行 `{task_id, device_id, result?, artifact_ref?}`;引用不存在返回 422 |
| GET | `/v1/runs/{id}` | 运行详情 |
| GET | `/v1/health` | 健康检查 |

## 状态机:ok / stale / missing

设备注册时声明 `heartbeat_interval_ms`(默认 5000):

| 条件(age = 当前时间 - 最后心跳) | 状态 |
|---|---|
| 从未收到心跳 | `missing` |
| age ≤ 3×interval | `ok` |
| 3×interval < age ≤ 6×interval | `stale` |
| age > 6×interval | `missing` |

判定逻辑在 `internal/domain/status.go`,时钟可注入(测试用假时钟,生产用 `time.Now().UnixMilli()`)。

## 目录结构

```
cmd/platformd/       入口(flag 配置 + 启动)
internal/domain/     核心实体 + 状态判定(信封模型)
internal/store/      SQLite 持久化(schema.sql 内嵌,modernc 纯 Go 驱动)
internal/api/        v1 HTTP 路由
scripts/demo.sh      curl 演示
```

## 数据模型

6 张表:`devices` / `heartbeats` / `tasks` / `runs` / `alerts`(v1 预留) / `versions`(v1 预留)。
域对象(Episode/Dataset/Checkpoint/Evaluation)不入库,由 `runs.artifact_ref` 指针引用。

## 测试

```bash
go test ./...
go vet ./...
```

覆盖:设备 CRUD 与冲突、心跳 seq 严格递增、未知设备、任务过滤、run 引用完整性、**时钟推进下的 ok→stale→missing 判定**。

## 与各域的关系(Phase 1)

- 域 → 平台:HTTP 上报(心跳/任务/运行);Phase 1 用 curl/脚本模拟,第二期对接 twin(MQTT 或心跳脚本)与 amr(现有 HTTP API 消费)。
- 平台 → UI:聚合 API(前端可读);v1 无内置 UI,`GET /v1/devices` 即演示面。
- 平台不反向控制域(命令代理为第二期,且必须走显式受限端点)。
