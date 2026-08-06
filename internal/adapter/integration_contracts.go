// Package adapter 定义尚未接线的跨仓集成合同。
//
// 这些接口是 Platform 对其它七仓的集成缝(seam)。
// 当前只定义接口和本地 mock；它们不构成任何真实跨仓接入证据。
// 真实接入时，各仓提供满足这些接口的 adapter，Platform 不关心对端内部实现。
//
// 合同依据：SYSTEM_ARCHITECTURE.md §10 逐仓集成合同。
package adapter

import (
	"context"

	"github.com/inayina/robot-platform-service/internal/domain"
)

// Fault 是 adapter 包内保留的最小 Fault 投影。
// 完整 Fault 模型在 Platform Fault/Alert 拆分时再进入 domain 层。
type Fault struct {
	ID       string `json:"id"`
	Source   string `json:"source"`   // runtime_id / device_id / domain
	Code     string `json:"code"`     // 来源系统定义的错误码
	Severity string `json:"severity"` // info / warning / critical
	Message  string `json:"message"`  // 人类可读描述
	TsMs     int64  `json:"ts_ms"`    // 来源系统上报时间(reported time)
}

// TaskSource 是 Robot Domain(Task 权威)的管理面合同。
// Platform 通过此接口提交 Task intent 并接收终态，但不参与 Task 校验和执行。
type TaskSource interface {
	// SubmitTask 向 Domain 提交高层 Task intent。Domain 决定接受或拒绝。
	SubmitTask(ctx context.Context, task *domain.Task) (accepted bool, err error)

	// ReportTaskStatus 由 Domain 回调，报告 Task 终态。
	// Platform 据此更新 Task.Status，但不覆盖 Domain 结论。
	ReportTaskStatus(ctx context.Context, taskID string, status domain.TaskStatus) error
}

// FaultSource 是 Runtime/Device/Domain 的 Fault 上报合同。
// Platform 只保存来源事实投影；确认和清除由来源系统执行。
type FaultSource interface {
	// ReportFault 上报一个新 Fault。Platform 保存投影并可能派生 Alert。
	ReportFault(ctx context.Context, fault Fault) error

	// ClearFault 来源系统确认 Fault 已清除。
	ClearFault(ctx context.Context, faultID string) error
}

// ArtifactRegistry 是 Data & Validation 层的产物登记合同。
// Platform 只保存 typed Artifact Reference，不搬运产物本体。
type ArtifactRegistry interface {
	// RegisterArtifact 登记一个外部产物引用。
	RegisterArtifact(ctx context.Context, ref domain.ArtifactRef) error

	// ResolveArtifact 按 URI 反查产物引用。
	ResolveArtifact(ctx context.Context, uri string) (*domain.ArtifactRef, error)
}
