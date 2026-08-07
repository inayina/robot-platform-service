# Go 语言面试背景知识(基于 robot-platform-service 实操)

> 面向第一次接触 Go、正在准备机器人软件工程师面试的读者。每个知识点都链接到本仓库的实际代码,让你能用自己写过的项目解释。

## 目录

1. [为什么这个项目用 Go](#1-为什么这个项目用-go)
2. [Go 模块与依赖管理](#2-go-模块与依赖管理)
3. [类型系统](#3-类型系统)
4. [错误处理](#4-错误处理)
5. [Context 与超时/取消](#5-context-与超时取消)
6. [net/http 服务端与客户端](#6-nethttp-服务端与客户端)
7. [SQLite 集成(无 CGO)](#7-sqlite-集成无-cgo)
8. [测试](#8-测试)
9. [项目目录结构惯例](#9-项目目录结构惯例)
10. [并发模型(goroutine + channel)](#10-并发模型goroutine--channel)
11. [常见面试追问](#11-常见面试追问)

---

## 1. 为什么这个项目用 Go

### 项目上下文

`robot-platform-service` 是跨七仓的管理面汇聚服务——只需要低频 HTTP API + SQLite 持久化,不参与实时控制(CAN/ROS2/MoveIt)。

### Go 的理由(面试这样说)

**第一:单静态二进制。**`go build` 产出一个独立可执行文件——无需运行时、共享库或容器。Edge Agent 就是这么交叉编译 ARM64 后 scp 到 Orange Pi 的:

```bash
GOARCH=arm64 GOOS=linux go build -o edge-agent ./cmd/edge-agent
scp edge-agent orangepi@192.168.1.22:~/
```

**第二:无 CGO。**`modernc.org/sqlite` 是纯 Go 的 SQLite 驱动——不需要 gcc、不需要 target 编译链。这意味着同一个代码在 x86 笔记本和 ARM64 单板机上都能 `go build`。

**第三:并发模型。**goroutine + channel 让"主循环 + ticker + 信号监听"变得简洁(见 `internal/edgeagent/agent.go`)。

**但 Go 不是所有场景都合适:**
- 实时控制闭环(CAN/μs 级)→ C++/Rust
- 数据科学/ML(Python 生态)→ Python
- 系统层驱动 → C + Linux 内核模块

> **关键表述**:我们选 Go 不是为了性能炫技,而是为了"零运维依赖的单二进制 + 正确级别的类型安全 + 足够好的并发模型"。

---

## 2. Go 模块与依赖管理

### go.mod

本项目的 `go.mod`(仓库根目录):

```
module github.com/inayina/robot-platform-service

go 1.26.5

require modernc.org/sqlite v1.56.0
```

- `module` 声明导入路径,决定了所有内部包的 import 路径
- `require` 列出直接依赖;`go mod tidy` 自动补全间接依赖

### 导入规则

```go
// 导入标准库
import "net/http"

// 导入本模块的其他包
import "github.com/inayina/robot-platform-service/internal/domain"
```

所有导入路径从 module 名开始。同一个 module 下的包互相可见(只要名字匹配)。

### 国内网络

```bash
# 必须设置,否则 proxy.golang.org 极慢
export GOPROXY=https://goproxy.cn,direct
```

> **面试被问"依赖管理"时的回答**:Go modules 是 1.11 引入的。`go.mod` 声明依赖和版本,`go.sum` 锁定哈希值。`go mod tidy` 自动清理未使用的依赖。国内用 goproxy.cn 代理。

---

## 3. 类型系统

### Struct 与 JSON

Go 用 struct 定义数据结构,struct tag 控制 JSON 序列化:

```go
// internal/domain/domain.go
type Device struct {
    ID                  string `json:"id"`
    Name                string `json:"name"`
    Kind                string `json:"kind"`
    Version             string `json:"version,omitempty"`  // 空值不输出
    HeartbeatIntervalMs int64  `json:"heartbeat_interval_ms"`
    FirstSeenMs         int64  `json:"first_seen_ms"`
    LastSeenMs          int64  `json:"last_seen_ms"`
}
```

- `json:"field_name"` 映射 JSON 字段名
- `json:"field,omitempty"` 值类型的零值时不输出(指针类型 nil 也不输出)
- 大写字段 = 导出(包外可见),小写 = 私有

### 指针与零值

```go
type HostMetricsPayload struct {
    TemperatureCelsius *float64 `json:"temperature_celsius,omitempty"`
    CanAvailable       *bool    `json:"can_available,omitempty"`
}
```

- `*float64` 是 float64 的指针——`nil` 表示"未采集",JSON 序列化时 omit
- Go 的零值: `int` = 0, `string` = "", `bool` = false, `*T` = nil

### 常量与 iota

```go
// internal/domain/platform.go
type RuntimeLiveness string
const (
    LivenessUnknown RuntimeLiveness = "unknown"
    LivenessOnline  RuntimeLiveness = "online"
    LivenessStale   RuntimeLiveness = "stale"
    LivenessOffline RuntimeLiveness = "offline"
)
```

Go 没有 enum 关键字;用 const + 自定义类型模拟。

### 方法接收者

```go
func (s *Store) CreateRobot(ctx context.Context, r *domain.Robot) error { ... }
```

- `(s *Store)` 是接收者——用指针(而非值)接收者,因为 Store 内部有状态(db 连接)
- 值接收者不修改原对象,指针接收者可以修改

---

## 4. 错误处理

### Sentinel errors

```go
var (
    ErrNotFound        = errors.New("not found")
    ErrConflict        = errors.New("conflict")
    ErrSeqRegression   = errors.New("heartbeat seq regression")
)
```

### 错误包装与 errors.Is

```go
func (s *Store) GetDevice(ctx context.Context, id string) (*Device, error) {
    row := s.db.QueryRowContext(ctx, "...")
    // ...
    if errors.Is(err, sql.ErrNoRows) {
        return nil, fmt.Errorf("device %s: %w", id, ErrNotFound)
    }
    // ...
}

// 调用方:
dev, err := s.store.GetDevice(ctx, "dev-1")
if errors.Is(err, store.ErrNotFound) {
    // 设备不存在
}
```

- `%w` 而非 `%v` 包装错误——保留 `errors.Is` 和 `errors.As` 能力
- `errors.Is(err, target)` 沿错误链查找匹配(err == target 或 wrapped)

### 常见模式:错误按类型分支

```go
switch {
case errors.Is(err, store.ErrConflict):
    writeJSON(w, http.StatusConflict, ...)
case errors.Is(err, store.ErrNotFound):
    writeJSON(w, http.StatusNotFound, ...)
default:
    writeJSON(w, http.StatusInternalServerError, ...)
}
```

> **面试被问"Go 怎么处理异常"时**:Go 没有 try-catch。函数返回 `(value, error)`,调用方必须处理。ide 用 `:=` 赋值同时检查 error。用 `errors.Is` 和 `errors.As` 替代类型断言。

---

## 5. Context 与超时/取消

### 生命周期控制

```go
func New(cfg *Config) *Agent {
    ctx, cancel := context.WithCancel(context.Background())
    return &Agent{..., cancel: cancel, ctx: ctx}
}

func (a *Agent) Run() error {
    // ...
    select {
    case <-ticker.C:
        a.sendHeartbeat(a.ctx)
    case <-a.ctx.Done():   // Shutdown() → cancel() → 触发这里
        return nil
    }
}

func (a *Agent) Shutdown() {
    a.cancel()
}
```

### HTTP Client + Context

```go
func (c *Client) RegisterDevice(ctx context.Context, ...) error {
    req, err := http.NewRequestWithContext(ctx, "POST", url, body)
    resp, err := c.hc.Do(req)
    // ctx 超时或取消→Do 自动返回
}
```

### 三个关键概念

| 函数 | 用途 |
|---|---|
| `context.Background()` | 根 context,用于 main 或 init |
| `context.WithCancel(p)` | 手动取消(Shutdown 模式) |
| `context.WithTimeout(p, d)` | 自动超时取消(HTTP 请求) |

> **面试被问"Context 是什么"时**:context 是 Go 的请求范围值传递机制——挂载超时、取消和元数据。核心规则:Context 是第一个参数、只传递不保存、用 defer cancel、不把 nil 当 context 传入。

---

## 6. net/http 服务端与客户端

### 服务端(mux + handler)

```go
// cmd/platformd/main.go
mux := http.NewServeMux()
mux.Handle("/v1/", api.NewHandler(st, evalV1))
mux.Handle("/v2/", http.StripPrefix("/v2", api.NewHandlerV2(st, evalV2)))

func (s *Server) handleCreateRobot(w http.ResponseWriter, r *http.Request) {
    robotID := r.PathValue("id")              // Go 1.22+ 路径参数
    // decode JSON → validate → store → write response
}
```

### 客户端(Edge Agent)

```go
// internal/edgeagent/platformclient/client.go
type Client struct {
    baseURL string
    hc      *http.Client  // 有 timeout 的客户端
}

func (c *Client) do(ctx context.Context, method, path string, body any, ...) error {
    req, _ := http.NewRequestWithContext(ctx, method, c.baseURL+path, ...)
    resp, _ := c.hc.Do(req)
    // 检查 status→返回 *Error
}
```

> **面试被问"用 Go 写 HTTP 服务"时**:标准库 net/http 够用——自带 ServeMux、HTTP/1 和 HTTP/2、超时控制。不依赖 Express/Gin 等第三方框架。关键实践:打 Server Header、读 body 限制大小(1MB)、在 context 上设置超时。

---

## 7. SQLite 集成(无 CGO)

### 为什么纯 Go 驱动

```go
import "modernc.org/sqlite" // 纯 Go,不带 .c 文件
```

- 不依赖 CGO(gcc/c 编译链接)——交叉编译 ARM64 时无需 target toolchain
- 单静态二进制包含了整个 SQLite——部署时只需一个文件

### 事务 + 回滚

```go
tx, err := s.db.BeginTx(ctx, nil)
defer tx.Rollback() // 如果没 Commit,自动回滚

// 一系列操作...
// 如果全部成功:
return tx.Commit()
```

### id 生成

```go
func NewID(prefix string) string {
    b := make([]byte, 8); rand.Read(b); return prefix + hex.EncodeToString(b)
}
```

### migration 模式

```go
func migrate(db *sql.DB) error {
    // 创建表(IF NOT EXISTS 幂等)
    // ALTER TABLE 加列(catch duplicate column)
    // PRAGMA user_version = 2
}
```

> **面试参考**:SQLite 选型理由:单文件、零运维、纯 Go 驱动。迁移策略:禁止自动迁移,需要时会显式返回 error 并停止。v1+v2 表共存在同一库,共享 sql.DB 连接池。

---

## 8. 测试

### 表驱动测试(Go 惯例)

```go
func TestHeartbeatSeq(t *testing.T) {
    tests := []struct {
        name string; seq int64; wantErr error
    }{
        {"first", 1, nil},
        {"increment", 2, nil},
        {"regression", 1, store.ErrSeqRegression},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := doHeartbeat(tt.seq)
            if !errors.Is(err, tt.wantErr) { t.Errorf(...) }
        })
    }
}
```

### httptest

```go
func TestAgent(t *testing.T) {
    mux := http.NewServeMux()
    mux.HandleFunc("POST /v1/devices", handleRegister)
    srv := httptest.NewServer(mux)  // 真实 HTTP 服务器,localhost 端口
    defer srv.Close()
    // 测试用的 Agent 连接 srv.URL
}
```

### Race detector

```bash
go test -race ./...
```

`-race` 编译时注入 C 运行时检测器,在并发访问时报警。本仓库 `edgeagent` 测试 2.9s 通过(goroutine 安全)。

### 我们覆盖了什么

| 测试 | 方式 |
|---|---|
| 注册成功 | httptest |
| 重复注册 409 | fakePlatform + 断言 |
| Heartbeat seq 递增/回归/幂等 | 事务内 SELECT + INSERT |
| HTTP 4xx 不重试 | 硬编码 400 的 handler |
| Context 取消后退出 | Shutdown() 触发 ctx.Done() |
| Host metrics 采集失败降级 | 空 Snapshot + omitempty |
| session_id 重启后变化 | 两次 makeAgent + 断言 |

---

## 9. 项目目录结构惯例

```
cmd/               # 可执行程序入口,每个子目录 = 一个 main 包
  platformd/       # Platform 服务入口
  edge-agent/      # Orange Pi Agent 入口
internal/          # 内部包(外部 module 禁止导入)
  domain/          # 实体定义(Data/Value Object)
  store/           # 持久化层
  api/             # HTTP 路由 + handler
  edgeagent/       # Agent 核心逻辑(内部再分 hostmetrics/platformclient)
    hostmetrics/   # /proc + /sys 采集
    platformclient/ # HTTP 客户端(零 domain 依赖)
scripts/           # 部署/演示/验收脚本
.env.example       # 环境变量模板(不含密钥)
```

> **面试说"为什么不用 pkg/ 目录"时**:`pkg/` 语义模糊——什么人可以导入？如果内部/外部都允许,那就该是独立 module。本仓库用 `internal/` 强制包私有——更安全,符合"最小暴露面"原则。

---

## 10. 并发模型(goroutine + channel)

### Ticker(定时任务)

```go
ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
defer ticker.Stop()
for {
    select {
    case <-ticker.C:
        sendHeartbeat()
    case <-sigCh:
        return // graceful shutdown
    }
}
```

### Channel + select(信号复用)

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

for {
    select {
    case <-ticker.C:
        // do work
    case sig := <-sigCh:
        // handle signal
    case <-ctx.Done():
        // context cancellation
    }
}
```

### Mutex(保护共享状态)

```go
type Agent struct {
    mu  sync.Mutex
    seq int64
}

func (a *Agent) sendHeartbeat() {
    a.mu.Lock()
    a.seq++
    seq := a.seq
    a.mu.Unlock()
    // ...
}
```

> **面试重点**:goroutine 不是线程——是 Go 运行时管理的 light-weight 协程(栈初始 2KB,按需增长)。channel 用于 goroutine 间通信,无锁管道。Mutex 用于保护共享状态。Go 的"不通过共享内存通信,而通过通信共享内存"是核心哲学。

---

## 11. 常见面试追问

### Q1: Go 的 GC 停顿对实时系统的影响?

**回答**:Go 的 GC 是并发标记-清除,1ms 以内的停顿。对管理面 HTTP 服务(低频心跳)完全不是问题。但对 CAN/μs 级控制闭环——Go 不合适,改用 C++。

### Q2: 为什么不用 Gin/Echo/Fiber?为什么不加 ORM?

**回答**:Platform 的 HTTP API 是 REST 风格、低频(每几秒一次心跳)、响应体简单(JSON)。标准库 `net/http` 就够——减少依赖=减少攻击面。SQLite 的表结构简单(外键、索引)——用裸 SQL 比 ORM 更透明,SQL 语句有类型检查但没有魔法隐藏。

### Q3: 你这个项目里 Go 的 CGO 怎么处理的?

**回答**:`CGO_ENABLED=0`——pure Go SQLite 驱动,无 CGO。Edge Agent 是一个 ARM64 可执行文件,scp 到 Orange Pi 直接跑。不需要在目标板上安装任何依赖。

### Q4: 如果 Platform 要支持 PostgreSQL 怎么改?

**回答**:用 `database/sql` 接口——driver 替换即可。`modernc.org/sqlite` 和 `lib/pq` 实现相同的 `database/sql` 接口。迁移 SQL 语句(SQLite→Postgres)需要小幅调整(BLOB→BYTEA,datetime→timestamp 等),但应用层代码不变。

### Q5: Context 的超时和 HTTP Client 的 Timeout 什么关系?

**回答**:两层:HTTP Client Timeout 是连接+读取的总超时(底层),Context 是逻辑超时(上层)。例如:

```go
hc := &http.Client{Timeout: 5 * time.Second}
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, _ := hc.Do(req)
// 3 秒后 ctx 先超时→Do 返回,"3 秒的 ctx 短于 5 秒的 client",更严格的那个生效
```

### Q6: Go 的 side-effect-free 错误处理方式 vs try-catch,你更习惯哪个?

**回答**(诚实):Go 的要求在调用点显式处理 error——不能忽略,不能上抛。这虽然会产生重复的 `if err != nil` 代码,但在平台层的 HTTP handler 中,这种模式让"谁出错、谁背锅"一清二楚——不像 try-catch 可能把错误吞没或被 unknown handler 抓走。

---

## 学练对照表

| 概念 | 本仓库对应文件 |
|---|---|
| Go 类型系统 | `internal/domain/domain.go` (v1 struct + json tag) |
| 指针 + omitempty | `internal/edgeagent/platformclient/client.go:HostMetricsPayload` |
| Context 超时/取消 | `internal/edgeagent/agent.go:New()` + `Run()` |
| HTTP Server | `cmd/platformd/main.go` + `internal/api/api.go` |
| HTTP Client | `internal/edgeagent/platformclient/client.go` |
| SQLite 事务 | `internal/store/platform.go:CreateRunV2` |
| Ticker + Signal | `internal/edgeagent/agent.go:Run()` |
| httptest + 假 Platform | `internal/edgeagent/agent_test.go` |
| Table driven test | `internal/store/store_test.go` |
| Race detector | 运行 `go test -race ./...` 自己看 |

---

## 其他面试方向参考资料

- [机器人软件职位分层与技能地图](../resume-portfolio/02_capability_and_project_narrative.md)
- [七仓项目总览与架构叙事](../robot-control-runtime/docs/portfolio/SEVEN_REPOS_OVERVIEW.md)
- [平台架构设计](../portfolio-audit/ARCHITECTURE_DESIGN.md)
