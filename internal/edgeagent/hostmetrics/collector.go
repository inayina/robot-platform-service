// Package hostmetrics 提供 Linux 主机指标采集(CPU/内存/温度/CAN/OS 信息)。
//
// 所有采集方法在读取失败时降级:返回 nil 指针或空字符串,调用方应视为 "unavailable"。
// 不引入第三方库(/proc + /sys 直接读,net/http 不加额外依赖)。
package hostmetrics

import (
	"context"
	"math"
	"os"
	"strings"
)

// Snapshot 一次主机指标快照。
type Snapshot struct {
	Hostname           string
	Arch               string
	OS                 string
	CPUPercent         *float64
	MemoryPercent      *float64
	TemperatureCelsius *float64
	CanAvailable       *bool
}

// Collector 主机指标采集器。
type Collector struct{}

// New 构建采集器。
func New() *Collector { return &Collector{} }

// Collect 执行一次全量采集(ctx 用于超时取消,当前为同步阻塞采集)。
func (c *Collector) Collect(ctx context.Context) (*Snapshot, error) {
	s := &Snapshot{
		Hostname: hostname(),
		Arch:     arch(),
		OS:       osVersion(),
	}

	// 各指标独立采集:一个失败不影响其他
	if v, err := cpuPercent(ctx); err == nil {
		s.CPUPercent = &v
	}
	if v, err := memoryPercent(ctx); err == nil {
		s.MemoryPercent = &v
	}
	if v, err := temperatureCelsius(); err == nil {
		s.TemperatureCelsius = &v
	}
	can := canAvailable()
	s.CanAvailable = &can

	return s, nil
}

// hostname 返回主机名(读不到时返回空字符串)。
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		if b, e := os.ReadFile("/proc/sys/kernel/hostname"); e == nil {
			return strings.TrimSpace(string(b))
		}
		return ""
	}
	return h
}

// arch 返回架构(uname -m 等价)。
func arch() string {
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "CPU architecture") || strings.HasPrefix(line, "Architecture") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	// 回退:试 /proc/version (仅 32-bit / 64-bit 判断)
	if b, err := os.ReadFile("/proc/version"); err == nil && strings.Contains(string(b), "aarch64") {
		return "aarch64"
	}
	if b, err := os.ReadFile("/proc/version"); err == nil && strings.Contains(string(b), "x86_64") {
		return "x86_64"
	}
	return "unknown"
}

// osVersion 返回操作系统信息(kernel release + 发行版名称)。
func osVersion() string {
	kernel := ""
	if b, err := os.ReadFile("/proc/version"); err == nil {
		// e.g., "Linux version 6.1....."
		kernel = strings.TrimSpace(strings.SplitN(string(b), " ", 3)[2])
	}
	// 发行版名(可选):读 /etc/os-release
	distro := ""
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				distro = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}
	if distro != "" {
		return distro + " (" + kernel + ")"
	}
	return kernel
}

// cpuPercent 读取瞬时 CPU 使用率,范围 0-100。
// 从 /proc/stat 第一行取 user+nice+system+idle,计算 busy/(busy+idle)。
func cpuPercent(_ context.Context) (float64, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	if len(fields) < 5 {
		return 0, nil
	}
	var busy, idle uint64
	// [cpu, user, nice, system, idle, ...]
	for i, v := range fields[1:] {
		n := parseUint(v)
		if i <= 2 {
			busy += n
		} else if i == 3 {
			idle = n
			break
		}
	}
	total := busy + idle
	if total == 0 {
		return 0, nil
	}
	pct := (float64(busy) / float64(total)) * 100.0
	return math.Round(pct*10) / 10, nil
}

// memoryPercent 返回内存使用率,范围 0-100。
// /proc/meminfo: (MemTotal - MemAvailable) / MemTotal * 100。
func memoryPercent(ctx context.Context) (float64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	var total, avail uint64
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			total = parseMemLine(line)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			avail = parseMemLine(line)
		}
	}
	_ = ctx
	if total == 0 {
		return 0, nil
	}
	used := total - avail
	pct := (float64(used) / float64(total)) * 100.0
	return math.Round(pct*10) / 10, nil
}

// temperatureCelsius 返回 SoC 温度(摄氏度),读不到返回 error。
//
// Orange Pi / ARM SBC 常用路径:
//
//	/sys/class/thermal/thermal_zone0/temp (毫摄氏度)
//	/sys/devices/virtual/thermal/thermal_zone0/temp
func temperatureCelsius() (float64, error) {
	paths := []string{
		"/sys/class/thermal/thermal_zone0/temp",
		"/sys/class/thermal/thermal_zone1/temp",
		"/sys/devices/virtual/thermal/thermal_zone0/temp",
		"/sys/devices/platform/soc@3000000/thermal/thermal_zone0/temp",
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v := parseUint(strings.TrimSpace(string(b)))
		if v > 0 {
			return float64(v) / 1000.0, nil // 毫摄氏度→摄氏度
		}
	}
	return 0, os.ErrNotExist
}

// canAvailable 判断系统是否具备 SocketCAN 能力(例如 /sys/class/net/can0 存在)。
//
// 当前 Orange Pi 厂商镜像 # CONFIG_CAN is not set→can0 不存在→返回 false。
// 不检查 vcan(软件虚拟接口不计入"真实 CAN 可用")。
func canAvailable() bool {
	// 方法:读取 /proc/net/can 目录存在性;或者检查 /sys/class/net/can* 存在。
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "can") {
			return true
		}
	}
	return false
}

// --- helpers ---

func parseUint(s string) uint64 {
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			continue
		}
		v = v*10 + uint64(c-'0')
	}
	return v
}

func parseMemLine(s string) uint64 {
	// "MemTotal:       16384000 kB"
	f := strings.Fields(s)
	if len(f) < 2 {
		return 0
	}
	v := parseUint(f[1])
	// kB → B
	return v * 1024
}
