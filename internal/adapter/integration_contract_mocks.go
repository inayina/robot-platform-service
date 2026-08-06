// Package adapter — 尚未接线的集成合同 mock。
//
// 这些 mock 仅用于 Platform 本仓测试，不模拟对端仓库的真实行为。
// 真实接入时替换为对端提供的 adapter 实现。
package adapter

import (
	"context"
	"sync"

	"github.com/inayina/robot-platform-service/internal/domain"
)

// MockTaskSource 记录调用历史，用于验证 Platform 侧的 Task 提交合同。
type MockTaskSource struct {
	mu               sync.Mutex
	SubmittedTasks   []*domain.Task
	ReportedStatuses []struct {
		TaskID string
		Status domain.TaskStatus
	}
	// SubmitResult 控制 AcceptTask 返回值(默认 true)。
	SubmitResult bool
}

func (m *MockTaskSource) SubmitTask(_ context.Context, task *domain.Task) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SubmittedTasks = append(m.SubmittedTasks, task)
	return m.SubmitResult, nil
}

func (m *MockTaskSource) ReportTaskStatus(_ context.Context, taskID string, status domain.TaskStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReportedStatuses = append(m.ReportedStatuses, struct {
		TaskID string
		Status domain.TaskStatus
	}{taskID, status})
	return nil
}

// SubmittedCount 返回提交记录数。
func (m *MockTaskSource) SubmittedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.SubmittedTasks)
}

// MockFaultSource 记录上报和清除调用。
type MockFaultSource struct {
	mu             sync.Mutex
	ReportedFaults []Fault
	ClearedFaults  []string
}

func (m *MockFaultSource) ReportFault(_ context.Context, fault Fault) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReportedFaults = append(m.ReportedFaults, fault)
	return nil
}

func (m *MockFaultSource) ClearFault(_ context.Context, faultID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ClearedFaults = append(m.ClearedFaults, faultID)
	return nil
}

func (m *MockFaultSource) ReportedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ReportedFaults)
}

// MockArtifactRegistry 记录登记和反查调用。
type MockArtifactRegistry struct {
	mu             sync.Mutex
	RegisteredRefs []domain.ArtifactRef
	ResolvedURIs   []string
	// ResolveResult 控制 ResolveArtifact 返回值(nil = 未找到)。
	ResolveResult *domain.ArtifactRef
}

func (m *MockArtifactRegistry) RegisterArtifact(_ context.Context, ref domain.ArtifactRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RegisteredRefs = append(m.RegisteredRefs, ref)
	return nil
}

func (m *MockArtifactRegistry) ResolveArtifact(_ context.Context, uri string) (*domain.ArtifactRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ResolvedURIs = append(m.ResolvedURIs, uri)
	return m.ResolveResult, nil
}

func (m *MockArtifactRegistry) RegisteredCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RegisteredRefs)
}

// 编译时接口满足检查。
var (
	_ TaskSource       = (*MockTaskSource)(nil)
	_ FaultSource      = (*MockFaultSource)(nil)
	_ ArtifactRegistry = (*MockArtifactRegistry)(nil)
)
