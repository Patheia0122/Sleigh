package monitor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent-heavyworks-runtime/server/internal/notifier"
)

type Service struct {
	reporter notifier.Reporter
	mu       sync.Mutex
	lastRisk string
	lastSent time.Time
}

type ResourceReport struct {
	Time    string      `json:"time"`
	CPU     CPUReport   `json:"cpu"`
	Memory  MemoryStats `json:"memory"`
	Docker  DockerInfo  `json:"docker"`
	LoadAvg LoadAvg     `json:"load_avg"`
}

type CPUReport struct {
	LogicalCores int `json:"logical_cores"`
}

type LoadAvg struct {
	Avg1  float64 `json:"avg1"`
	Avg5  float64 `json:"avg5"`
	Avg15 float64 `json:"avg15"`
}

type MemoryStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	SwapTotalBytes uint64  `json:"swap_total_bytes"`
	SwapFreeBytes  uint64  `json:"swap_free_bytes"`
	AvailableRatio float64 `json:"available_ratio"`
}

type DockerInfo struct {
	Available     bool   `json:"available"`
	ServerVersion string `json:"server_version,omitempty"`
	Error         string `json:"error,omitempty"`
}

type OOMDiagnostics struct {
	Time            string      `json:"time"`
	RiskLevel       string      `json:"risk_level"`
	Memory          MemoryStats `json:"memory"`
	Pressure        PSIReport   `json:"pressure"`
	Signals         []string    `json:"signals"`
	Recommendations []string    `json:"recommendations"`
	Alert           AlertStatus `json:"alert"`
}

type AlertStatus struct {
	Target     string `json:"target,omitempty"`
	Dispatched bool   `json:"dispatched"`
	Error      string `json:"error,omitempty"`
}

type PSIReport struct {
	SomeAvg10  float64 `json:"some_avg10"`
	SomeAvg60  float64 `json:"some_avg60"`
	SomeAvg300 float64 `json:"some_avg300"`
	FullAvg10  float64 `json:"full_avg10"`
	FullAvg60  float64 `json:"full_avg60"`
	FullAvg300 float64 `json:"full_avg300"`
}

func NewService(reporter notifier.Reporter) *Service {
	return &Service{
		reporter: reporter,
	}
}

func (s *Service) GetResources(ctx context.Context) (ResourceReport, error) {
	mem, err := readMemory()
	if err != nil {
		return ResourceReport{}, err
	}

	load, err := readLoadAvg()
	if err != nil {
		load = LoadAvg{}
	}

	return ResourceReport{
		Time: time.Now().UTC().Format(time.RFC3339),
		CPU: CPUReport{
			LogicalCores: runtime.NumCPU(),
		},
		Memory:  mem,
		LoadAvg: load,
		Docker:  readDockerInfo(ctx),
	}, nil
}

func (s *Service) GetOOMDiagnostics(ctx context.Context) (OOMDiagnostics, error) {
	mem, err := readMemory()
	if err != nil {
		return OOMDiagnostics{}, err
	}

	psi, err := readMemoryPSI()
	if err != nil {
		psi = PSIReport{}
	}

	riskLevel := assessOOMRisk(mem.AvailableRatio, psi)
	signals := buildSignals(mem, psi)
	recommendations := buildRecommendations(riskLevel)
	alert := s.dispatchOOMEvent(ctx, riskLevel, mem, psi)

	return OOMDiagnostics{
		Time:            time.Now().UTC().Format(time.RFC3339),
		RiskLevel:       riskLevel,
		Memory:          mem,
		Pressure:        psi,
		Signals:         signals,
		Recommendations: recommendations,
		Alert:           alert,
	}, nil
}

func (s *Service) dispatchOOMEvent(
	ctx context.Context,
	riskLevel string,
	mem MemoryStats,
	psi PSIReport,
) AlertStatus {
	if s.reporter == nil {
		return AlertStatus{}
	}
	if riskLevel != "high" && riskLevel != "critical" {
		return AlertStatus{Target: s.reporter.Target(), Dispatched: false}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.lastRisk == riskLevel && now.Sub(s.lastSent) < 60*time.Second {
		return AlertStatus{Target: s.reporter.Target(), Dispatched: false}
	}

	event := notifier.Event{
		Type:      "oom-risk",
		Severity:  riskLevel,
		Timestamp: now.UTC().Format(time.RFC3339),
		Payload: map[string]any{
			"memory_available_ratio": mem.AvailableRatio,
			"memory_total_bytes":     mem.TotalBytes,
			"memory_available_bytes": mem.AvailableBytes,
			"psi_some_avg10":         psi.SomeAvg10,
			"psi_full_avg10":         psi.FullAvg10,
		},
	}

	if err := s.reporter.Report(ctx, event); err != nil {
		return AlertStatus{
			Target:     s.reporter.Target(),
			Dispatched: false,
			Error:      err.Error(),
		}
	}

	s.lastRisk = riskLevel
	s.lastSent = now
	return AlertStatus{
		Target:     s.reporter.Target(),
		Dispatched: true,
	}
}

func readMemory() (MemoryStats, error) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryStats{}, fmt.Errorf("read /proc/meminfo: %w", err)
	}

	memMap := map[string]uint64{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		valueParts := strings.Fields(strings.TrimSpace(parts[1]))
		if len(valueParts) == 0 {
			continue
		}
		num, convErr := strconv.ParseUint(valueParts[0], 10, 64)
		if convErr != nil {
			continue
		}
		memMap[parts[0]] = num * 1024
	}
	if err := scanner.Err(); err != nil {
		return MemoryStats{}, fmt.Errorf("scan meminfo: %w", err)
	}

	total := memMap["MemTotal"]
	available := memMap["MemAvailable"]
	swapTotal := memMap["SwapTotal"]
	swapFree := memMap["SwapFree"]

	ratio := 0.0
	if total > 0 {
		ratio = float64(available) / float64(total)
	}

	return MemoryStats{
		TotalBytes:     total,
		AvailableBytes: available,
		SwapTotalBytes: swapTotal,
		SwapFreeBytes:  swapFree,
		AvailableRatio: ratio,
	}, nil
}

func readLoadAvg() (LoadAvg, error) {
	content, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return LoadAvg{}, fmt.Errorf("read /proc/loadavg: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(content)))
	if len(parts) < 3 {
		return LoadAvg{}, fmt.Errorf("invalid /proc/loadavg format")
	}
	avg1, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return LoadAvg{}, err
	}
	avg5, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return LoadAvg{}, err
	}
	avg15, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return LoadAvg{}, err
	}

	return LoadAvg{Avg1: avg1, Avg5: avg5, Avg15: avg15}, nil
}

func readMemoryPSI() (PSIReport, error) {
	content, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return PSIReport{}, fmt.Errorf("read /proc/pressure/memory: %w", err)
	}

	report := PSIReport{}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		kind := parts[0]
		values := map[string]float64{}
		for _, kv := range parts[1:] {
			kvParts := strings.SplitN(kv, "=", 2)
			if len(kvParts) != 2 {
				continue
			}
			v, convErr := strconv.ParseFloat(kvParts[1], 64)
			if convErr != nil {
				continue
			}
			values[kvParts[0]] = v
		}

		if kind == "some" {
			report.SomeAvg10 = values["avg10"]
			report.SomeAvg60 = values["avg60"]
			report.SomeAvg300 = values["avg300"]
		}
		if kind == "full" {
			report.FullAvg10 = values["avg10"]
			report.FullAvg60 = values["avg60"]
			report.FullAvg300 = values["avg300"]
		}
	}

	return report, nil
}

func readDockerInfo(ctx context.Context) DockerInfo {
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DockerInfo{
			Available: false,
			Error:     strings.TrimSpace(string(out)),
		}
	}

	return DockerInfo{
		Available:     true,
		ServerVersion: strings.TrimSpace(string(out)),
	}
}

func assessOOMRisk(availableRatio float64, psi PSIReport) string {
	switch {
	case availableRatio < 0.08 || psi.FullAvg10 > 0.2:
		return "critical"
	case availableRatio < 0.15 || psi.SomeAvg10 > 0.5:
		return "high"
	case availableRatio < 0.3 || psi.SomeAvg10 > 0.2:
		return "medium"
	default:
		return "low"
	}
}

func buildSignals(mem MemoryStats, psi PSIReport) []string {
	signals := []string{
		fmt.Sprintf("memory.available_ratio=%.4f", mem.AvailableRatio),
		fmt.Sprintf("psi.some.avg10=%.4f", psi.SomeAvg10),
		fmt.Sprintf("psi.full.avg10=%.4f", psi.FullAvg10),
	}
	return signals
}

func buildRecommendations(riskLevel string) []string {
	switch riskLevel {
	case "critical":
		return []string{
			"暂停新任务调度，优先释放资源",
			"提升沙箱内存阈值或触发扩容策略",
			"定位高内存任务并创建快照后重试",
		}
	case "high":
		return []string{
			"限制并发任务数并观察 PSI 变化",
			"优先回收长时间空闲沙箱",
		}
	case "medium":
		return []string{
			"持续监控 memory PSI 和可用内存比率",
			"准备下一步自动扩容或降载策略",
		}
	default:
		return []string{
			"当前风险较低，保持周期性采样",
		}
	}
}
