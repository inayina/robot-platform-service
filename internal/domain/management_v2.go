// Package domain — Management Plane v2 本地草案对象模型。
//
// v2 按 docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md 把 Phase 1 混合 Device 拆成 Robot、
// Device、Runtime 三个不同寿命的对象，并引入 RuntimeSession/RuntimeHeartbeat
// 解决"身份需要稳定"与"seq 重启归零"的冲突。
//
// v1 类型(Device/Heartbeat/Task/Run/Alert/SoftwareVersion)在 domain.go 中保持不变。
package domain

import "encoding/json"

// ---------------------------------------------------------------------------
// 枚举
// ---------------------------------------------------------------------------

// RobotLifecycleState 资产生命周期：active 表示在位可引用，retired 表示退役但保留历史。
type RobotLifecycleState string

const (
	RobotActive  RobotLifecycleState = "active"
	RobotRetired RobotLifecycleState = "retired"
)

// DeviceClass 粗粒度设备类别。Platform 只登记，不据此推断行为。
type DeviceClass string

const (
	DeviceCompute    DeviceClass = "compute"
	DeviceController DeviceClass = "controller"
	DeviceSensor     DeviceClass = "sensor"
	DeviceActuator   DeviceClass = "actuator"
	DeviceBusNode    DeviceClass = "bus_node"
	DeviceComposite  DeviceClass = "composite"
)

// RuntimeRole 粗粒度 Runtime 角色。
type RuntimeRole string

const (
	RuntimeControlRuntime RuntimeRole = "control_runtime"
	RuntimeDomainExecutor RuntimeRole = "domain_executor"
	RuntimeDeviceBridge   RuntimeRole = "device_bridge"
	RuntimeReplayExecutor RuntimeRole = "replay_executor"
)

// SessionState RuntimeSession 的服务器投影状态。
type SessionState string

const (
	SessionCurrent    SessionState = "current"
	SessionEnded      SessionState = "ended"
	SessionSuperseded SessionState = "superseded"
)

// RuntimeLiveness 管理面 Runtime 活性(unknown/online/stale/offline)。
// 与 v1 DeviceStatus(ok/stale/missing)语义不同：DeviceStatus 应用于混合 Device，
// RuntimeLiveness 只表达管理面心跳活性，不代表 Robot readiness、Device condition
// 或控制安全。
type RuntimeLiveness string

const (
	LivenessUnknown RuntimeLiveness = "unknown" // active Runtime 从未建立 session 或尚无 Heartbeat
	LivenessOnline  RuntimeLiveness = "online"  // age <= stale 阈值
	LivenessStale   RuntimeLiveness = "stale"   // stale < age <= offline 阈值
	LivenessOffline RuntimeLiveness = "offline" // age > offline 阈值，或 session 明确结束
)

// ---------------------------------------------------------------------------
// 值对象
// ---------------------------------------------------------------------------

// ExternalRef 外部系统的 namespaced identity 映射。
// canonical ID 与 external ref 分开；外部系统不能覆盖 Platform ID。
type ExternalRef struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
}

// ExternalRefs 是 []ExternalRef 的 JSON 序列化辅助类型。
// API 层保持结构化数组；D2 存储层使用按对象种类分开的关系映射表，
// 不再把 Registry identity mapping 塞进不可约束的 JSON TEXT。
type ExternalRefs []ExternalRef

// MarshalJSON 实现 json.Marshaler。
func (r ExternalRefs) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("[]"), nil
	}
	type alias ExternalRefs
	return json.Marshal(alias(r))
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (r *ExternalRefs) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = nil
		return nil
	}
	var v []ExternalRef
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*r = ExternalRefs(v)
	return nil
}

// ArtifactRef 指向域内产物的 typed reference。Platform 不保存产物本体。
type ArtifactRef struct {
	Type            string `json:"type"`             // episode/checkpoint/evaluation/release/log
	URI             string `json:"uri"`              // 域内定位符
	HashSHA256      string `json:"hash_sha256"`      // 内容校验 hash
	ProducerRepo    string `json:"producer_repo"`    // 产生此产物的仓库
	ProducerVersion string `json:"producer_version"` // 产生此产物的版本
}

// ---------------------------------------------------------------------------
// 顶层实体
// ---------------------------------------------------------------------------

// Robot 是可接受领域 Task 的 physical 或 simulation 机器人资产。
// 身份跨 Runtime 重启、软件升级、Device 固件升级保持稳定。
type Robot struct {
	ID             string              `json:"id"`
	DisplayName    string              `json:"display_name"`
	Domain         string              `json:"domain"`
	Embodiment     string              `json:"embodiment"`
	LifecycleState RobotLifecycleState `json:"lifecycle_state"`
	ExternalRefs   ExternalRefs        `json:"external_refs,omitempty"`
	CreatedAt      int64               `json:"created_at"`
	UpdatedAt      int64               `json:"updated_at"`
}

// DeviceV2 是 Robot 下具有独立资产身份的硬件或仿真端点。
// 命名为 DeviceV2 以避免与 v1 domain.Device 冲突。
type DeviceV2 struct {
	ID             string       `json:"id"`
	RobotID        string       `json:"robot_id"`
	ParentDeviceID string       `json:"parent_device_id,omitempty"`
	DisplayName    string       `json:"display_name"`
	DeviceClass    DeviceClass  `json:"device_class"`
	DomainType     string       `json:"domain_type,omitempty"`
	Manufacturer   string       `json:"manufacturer,omitempty"`
	Model          string       `json:"model,omitempty"`
	SerialNumber   string       `json:"serial_number,omitempty"`
	LifecycleState string       `json:"lifecycle_state"` // active/retired
	ExternalRefs   ExternalRefs `json:"external_refs,omitempty"`
	CreatedAt      int64        `json:"created_at"`
	UpdatedAt      int64        `json:"updated_at"`
}

// Runtime 是部署到一台 Robot、需要独立登记和观测的软件实例。
// 同一部署实例的进程重启保持 runtime_id，并创建新 session_id。
type Runtime struct {
	ID                  string       `json:"id"`
	RobotID             string       `json:"robot_id"`
	DisplayName         string       `json:"display_name"`
	RuntimeRole         RuntimeRole  `json:"runtime_role"`
	Component           string       `json:"component"`
	HostDeviceID        string       `json:"host_device_id,omitempty"`
	HeartbeatIntervalMs int64        `json:"heartbeat_interval_ms"`
	LifecycleState      string       `json:"lifecycle_state"` // active/retired
	ExternalRefs        ExternalRefs `json:"external_refs,omitempty"`
	CreatedAt           int64        `json:"created_at"`
	UpdatedAt           int64        `json:"updated_at"`
}

// RuntimeSession 是一次进程执行的不可复用代次。
// session_id 由 Runtime producer 每次启动生成，在该 runtime_id 下唯一。
// LastHeartbeatAtMs 是持久化的最新 received_at，用于 Platform 重启后恢复 liveness
// 投影(合同 7.2：不能依赖内存计时器作为唯一事实)。
type RuntimeSession struct {
	SessionID          string       `json:"session_id"`
	RuntimeID          string       `json:"runtime_id"`
	SoftwareVersionRef string       `json:"software_version_ref"`
	StartedAtReported  int64        `json:"started_at_reported,omitempty"`
	StartedAtReceived  int64        `json:"started_at_received"`
	EndedAtReported    int64        `json:"ended_at_reported,omitempty"`
	EndedAtReceived    int64        `json:"ended_at_received,omitempty"`
	SessionState       SessionState `json:"session_state"`
	LastHeartbeatAtMs  int64        `json:"last_heartbeat_at_ms"`
}

// RuntimeHeartbeat 是 RuntimeSession 向管理面报告存活的一次观测。
// received_at 是唯一 liveness 时钟来源；reported_at 只用于审计。
type RuntimeHeartbeat struct {
	RuntimeID  string `json:"runtime_id"`
	SessionID  string `json:"session_id"`
	Seq        int64  `json:"seq"`
	ReportedAt int64  `json:"reported_at,omitempty"`
	ReceivedAt int64  `json:"received_at"`
}

// RunV2 是一次 Task 执行的关联台账。
// 引用 Robot(执行资产)和 RuntimeSession(authoritative executor)。
// RuntimeID + SessionID 组成对 runtime_sessions 复合 PK 的引用。
// 命名为 RunV2 以避免与 v1 domain.Run 冲突。
type RunV2 struct {
	ID          string       `json:"id"`
	TaskID      string       `json:"task_id"`
	RobotID     string       `json:"robot_id"`
	RuntimeID   string       `json:"runtime_id"`
	SessionID   string       `json:"session_id"`
	StartedMs   int64        `json:"started_ms"`
	EndedMs     int64        `json:"ended_ms,omitempty"`
	Result      string       `json:"result,omitempty"`
	ArtifactRef *ArtifactRef `json:"artifact_ref,omitempty"`
}

// ---------------------------------------------------------------------------
// 默认值
// ---------------------------------------------------------------------------

const (
	DefaultRuntimeHeartbeatIntervalMs int64 = 5000
)

// Runtime liveness 阈值乘数(与 Phase 1 3x/6x 策略一致，但只应用于 Runtime)。
const (
	RuntimeStaleFactor   int64 = 3
	RuntimeOfflineFactor int64 = 6
)

// ---------------------------------------------------------------------------
// RuntimeLivenessEvaluator
// ---------------------------------------------------------------------------

// RuntimeLivenessEvaluator 判定 Runtime 的管理面活性。
//
// 时钟通过 Now 注入：生产用 time.Now().UnixMilli()，测试用固定时钟。
// 与 v1 StatusEvaluator 共享 NowFunc 类型，但输出 RuntimeLiveness 而非 DeviceStatus。
// 合同依据：docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md §7.3。
type RuntimeLivenessEvaluator struct {
	Now           NowFunc
	StaleFactor   int64
	OfflineFactor int64
}

// NewRuntimeLivenessEvaluator 构造判定器，未显式指定的因子回落默认值。
func NewRuntimeLivenessEvaluator(now NowFunc) *RuntimeLivenessEvaluator {
	return &RuntimeLivenessEvaluator{
		Now:           now,
		StaleFactor:   RuntimeStaleFactor,
		OfflineFactor: RuntimeOfflineFactor,
	}
}

// Evaluate 根据 Runtime 配置和当前 session 状态计算管理面活性。
//
// 规则(合同 §7.3)：
//   - Runtime nil 或从未建立 session → unknown
//   - current session 尚无 Heartbeat → unknown
//   - session 已 ended 或 superseded → offline
//   - age <= StaleFactor × interval → online
//   - age <= OfflineFactor × interval → stale
//   - 其余 → offline
//
// reported_at 不参与判定；唯一时钟来源是 LastHeartbeatAtMs(received_at)。
func (e *RuntimeLivenessEvaluator) Evaluate(rt *Runtime, sess *RuntimeSession) RuntimeLiveness {
	if rt == nil {
		return LivenessUnknown
	}
	// 无 session 或 session 非 current 且非正常结束
	if sess == nil {
		return LivenessUnknown
	}
	if sess.SessionState == SessionEnded || sess.SessionState == SessionSuperseded {
		return LivenessOffline
	}
	// current session 但从未收到心跳
	if sess.LastHeartbeatAtMs == 0 {
		return LivenessUnknown
	}
	interval := rt.HeartbeatIntervalMs
	if interval <= 0 {
		interval = DefaultRuntimeHeartbeatIntervalMs
	}
	age := e.Now() - sess.LastHeartbeatAtMs
	switch {
	case age <= interval*e.StaleFactor:
		return LivenessOnline
	case age <= interval*e.OfflineFactor:
		return LivenessStale
	default:
		return LivenessOffline
	}
}
