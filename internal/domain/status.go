package domain

// StatusEvaluator 设备状态判定器。
//
// 时钟通过 Now 注入:生产用 time.Now().UnixMilli(),测试用固定时钟,
// 使 ok/stale/missing 判定可确定性测试(这是本服务最核心的判定逻辑)。
type StatusEvaluator struct {
	Now           NowFunc
	StaleFactor   int64 // 默认 StaleFactor
	MissingFactor int64 // 默认 MissingFactor
}

// NowFunc 返回当前 epoch 毫秒。
type NowFunc func() int64

// NewStatusEvaluator 构造判定器,未显式指定的因子回落默认值。
func NewStatusEvaluator(now NowFunc) *StatusEvaluator {
	return &StatusEvaluator{
		Now:           now,
		StaleFactor:   StaleFactor,
		MissingFactor: MissingFactor,
	}
}

// Evaluate 根据设备最后心跳时间计算当前状态。
//
// 规则:
//   - 从未收到心跳 → missing(视为失联,而不是 ok)
//   - age <= staleFactor×interval → ok
//   - age <= missingFactor×interval → stale
//   - 其余 → missing
func (e *StatusEvaluator) Evaluate(d *Device) DeviceStatus {
	if d == nil {
		return StatusUnknown
	}
	if d.LastSeenMs == 0 {
		return StatusMissing
	}
	interval := d.HeartbeatIntervalMs
	if interval <= 0 {
		interval = DefaultHeartbeatIntervalMs
	}
	age := e.Now() - d.LastSeenMs
	switch {
	case age <= interval*e.StaleFactor:
		return StatusOK
	case age <= interval*e.MissingFactor:
		return StatusStale
	default:
		return StatusMissing
	}
}
