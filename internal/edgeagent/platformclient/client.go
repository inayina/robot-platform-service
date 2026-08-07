// Package platformclient 提供 Edge Agent 与 robot-platform-service 的 HTTP 客户端。
//
// 只依赖 net/http + encoding/json,不导入 Platform 的 domain/store 包(Agent 零 Platform 依赖)。
package platformclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Error 封装 HTTP 错误响应用于区分 4xx/5xx/网络错误。
type Error struct {
	StatusCode int
	Body       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("platform HTTP %d: %s", e.StatusCode, e.Body)
}

// Client 与 Platform 的 HTTP 交互。
type Client struct {
	baseURL string
	hc      *http.Client
}

// New 构建 Client。
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		hc: &http.Client{Timeout: timeout},
	}
}

// RegisterDeviceRequest 设备注册载荷。
type RegisterDeviceRequest struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	Version             string `json:"version"`
	Hostname            string `json:"hostname,omitempty"`
	Arch                string `json:"arch,omitempty"`
	OS                  string `json:"os,omitempty"`
	HeartbeatIntervalMs int64  `json:"heartbeat_interval_ms"`
}

// HeartbeatRequest 心跳载荷。
type HeartbeatRequest struct {
	Seq       int64              `json:"seq"`
	SessionID string             `json:"session_id,omitempty"`
	Metrics   HostMetricsPayload `json:"metrics,omitempty"`
}

// HostMetricsPayload 主机指标载荷。
type HostMetricsPayload struct {
	CPUPercent         *float64 `json:"cpu_percent,omitempty"`
	MemoryPercent      *float64 `json:"memory_percent,omitempty"`
	TemperatureCelsius *float64 `json:"temperature_celsius,omitempty"`
	CanAvailable       *bool    `json:"can_available,omitempty"`
	RuntimeState       string   `json:"runtime_state,omitempty"`
}

// RegisterDevice 向 Platform 注册设备。
// 返回 ErrConflict(409)时调用方应视为幂等成功。
func (c *Client) RegisterDevice(ctx context.Context, req RegisterDeviceRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/devices", req, nil)
}

// SendHeartbeat 发送心跳。
func (c *Client) SendHeartbeat(ctx context.Context, deviceID string, req HeartbeatRequest) error {
	path := "/v1/devices/" + deviceID + "/heartbeats"
	return c.do(ctx, http.MethodPost, path, req, nil)
}

// do 统一请求路径:序列化 JSON,发送,解析响应。
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &Error{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			// 平台返回非 JSON(不应发生);接受 2xx 无 body
			if !errors.Is(err, io.EOF) && len(respBody) > 0 {
				return fmt.Errorf("unmarshal response: %w", err)
			}
		}
	}
	return nil
}
