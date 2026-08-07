// Package domain 定义平台核心实体。
//
// 设计原则(见 ARCHITECTURE_DESIGN.md 第二部分):
// 平台只统一"信封"(身份/时间/状态/上报结构),不统一"内容"(域 schema)。
// 因此这里只有 Device/Task/Run/Heartbeat/Alert/SoftwareVersion 六个平台实体,
// Episode/Dataset/Checkpoint/Evaluation 等域对象由平台以 artifact_ref 指针引用,不建表。
package domain

// DeviceStatus 设备状态枚举,平台级标准(与 dashboard 状态桥 ok/stale/missing 口径一致)。
type DeviceStatus string

const (
	StatusUnknown DeviceStatus = "unknown" // 已注册但从未收到心跳
	StatusOK      DeviceStatus = "ok"      // 最近心跳在 stale 窗口内
	StatusStale   DeviceStatus = "stale"   // 心跳超时(stale 窗口 < age <= missing 窗口)
	StatusMissing DeviceStatus = "missing" // 心跳严重超时
)

// Device 受管设备。kind 表达域(amr/panda/dev),version 为软件版本(信封字段)。
type Device struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Kind                string       `json:"kind"`
	Version             string       `json:"version"`
	HeartbeatIntervalMs int64        `json:"heartbeat_interval_ms"`
	FirstSeenMs         int64        `json:"first_seen_ms"`
	LastSeenMs          int64        `json:"last_seen_ms"`
	Status              DeviceStatus `json:"status"`
	// Edge Agent 宿主信息(Phase 2:Edge Agent 集成新增)
	Hostname string `json:"hostname,omitempty"`
	Arch     string `json:"arch,omitempty"`
	OS       string `json:"os,omitempty"`
}

// Heartbeat 心跳上报结构:seq 必须严格递增(与主仓命令合同的理念一致)。
type Heartbeat struct {
	DeviceID string `json:"device_id"`
	Seq      int64  `json:"seq"`
	TsMs     int64  `json:"ts_ms"`
	// Edge Agent 扩展(Phase 2)
	SessionID string       `json:"session_id,omitempty"` // Agent 每次重启后变化
	Metrics   *HostMetrics `json:"metrics,omitempty"`    // 主机实时指标
}

// HostMetrics 主机实时指标(Edge Agent 周期性上报)。
// 这是心跳携带的可选扩展字段,不由平台核心模型长期存储(当前随 heartbeat 记录)。
type HostMetrics struct {
	CPUPercent         *float64 `json:"cpu_percent,omitempty"`         // 0-100,Nil = 未采集
	MemoryPercent      *float64 `json:"memory_percent,omitempty"`      // 0-100
	TemperatureCelsius *float64 `json:"temperature_celsius,omitempty"` // 读不到温度时明确为 nil→unavailable
	CanAvailable       *bool    `json:"can_available,omitempty"`       // Nil = 未判断(Agent 启动前)
	RuntimeState       string   `json:"runtime_state,omitempty"`       // "idle"/"shutdown"/"degraded"
}

// TaskStatus 任务状态枚举,平台级标准(与 amr Mock WMS 5 态口径一致)。
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

// Task 跨域任务统一视图。domain 区分 amr/panda 等域;target 是域语义的透传字段,
// 平台不解析其内容(信封原则)。
type Task struct {
	ID        string     `json:"id"`
	Domain    string     `json:"domain"`
	Kind      string     `json:"kind"`
	Target    string     `json:"target"`
	Status    TaskStatus `json:"status"`
	CreatedMs int64      `json:"created_ms"`
	UpdatedMs int64      `json:"updated_ms"`
}

// Run 一次运行记录:一个任务的一次执行(amr 任务执行 / panda episode 采集 / 回放评测)。
// 这是各域都有、但当前没有任何仓库统一建模的对象。
type Run struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	DeviceID    string `json:"device_id"`
	StartedMs   int64  `json:"started_ms"`
	EndedMs     int64  `json:"ended_ms,omitempty"`
	Result      string `json:"result,omitempty"`
	ArtifactRef string `json:"artifact_ref,omitempty"` // 指向域内产物(manifest/episode 目录),平台不解析
}

// Alert 告警记录。v1 仅建表,由心跳/状态判定派生,端点 reserved。
type Alert struct {
	ID       int64  `json:"id"`
	DeviceID string `json:"device_id"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	TsMs     int64  `json:"ts_ms"`
	Acked    bool   `json:"acked"`
}

// SoftwareVersion 组件版本登记(信封元数据)。v1 仅建表,端点 reserved。
type SoftwareVersion struct {
	ID           int64  `json:"id"`
	Component    string `json:"component"`
	Repo         string `json:"repo"`
	GitSHA       string `json:"git_sha"`
	ReleasedAtMs int64  `json:"released_at_ms"`
}

// 默认心跳窗口参数:interval 来自设备注册;ok/stale/missing 按倍数判定。
const (
	DefaultHeartbeatIntervalMs int64 = 5000
	StaleFactor                int64 = 3 // age > 3×interval → stale
	MissingFactor              int64 = 6 // age > 6×interval → missing
)
