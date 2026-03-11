package sandbox

import "context"

type Metadata struct {
	ID                  string            `json:"sandbox_id"`
	Image               string            `json:"image"`
	Status              string            `json:"status"`
	Labels              map[string]string `json:"labels,omitempty"`
	Created             string            `json:"created"`
	MemoryLimitMB       int64             `json:"memory_limit_mb,omitempty"`
	StartupLatencyMS    int64             `json:"startup_latency_ms,omitempty"`
	ImagePullTriggered  bool              `json:"image_pull_triggered,omitempty"`
	ImagePullStatus     string            `json:"image_pull_status,omitempty"`
	ImagePullDurationMS int64             `json:"image_pull_duration_ms,omitempty"`
}

type Snapshot struct {
	ID        string `json:"id"`
	SandboxID string `json:"sandbox_id"`
	ImageRef  string `json:"image_ref"`
	Type      string `json:"type,omitempty"`
	HostPath  string `json:"host_path,omitempty"`
	BaseID    string `json:"base_snapshot_id,omitempty"`
	Created   string `json:"created"`
}

type CreateRequest struct {
	ID            string            `json:"id"`
	Image         string            `json:"image"`
	Labels        map[string]string `json:"labels,omitempty"`
	MemoryLimitMB int64             `json:"memory_limit_mb,omitempty"`
}

type ExecOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

type AutoExpandResult struct {
	Triggered bool   `json:"triggered"`
	FromMB    int64  `json:"from_mb,omitempty"`
	ToMB      int64  `json:"to_mb,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type MemoryPressure struct {
	NearLimit      bool    `json:"near_limit"`
	UsagePercent   float64 `json:"usage_percent"`
	CurrentBytes   int64   `json:"current_bytes,omitempty"`
	LimitBytes     int64   `json:"limit_bytes,omitempty"`
	CurrentLimitMB int64   `json:"current_limit_mb,omitempty"`
	Reason         string  `json:"reason,omitempty"`
}

type MountSpec struct {
	ID            string `json:"id"`
	SandboxID     string `json:"sandbox_id"`
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	Mode          string `json:"mode"`
	Created       string `json:"created"`
}

type Backend interface {
	Kind() string
	Create(ctx context.Context, req CreateRequest) (Metadata, error)
	Get(ctx context.Context, id string) (Metadata, error)
	Delete(ctx context.Context, id string) error
	UpdateMounts(ctx context.Context, sandboxID string, mounts []MountSpec) error
	EnsureRunning(ctx context.Context, sandboxID string) error
	GetMemoryPressure(ctx context.Context, sandboxID string) (MemoryPressure, error)
	ExpandMemory(ctx context.Context, sandboxID string, targetMB int64) (AutoExpandResult, error)
	PreExecAutoExpand(ctx context.Context, sandboxID string) (AutoExpandResult, error)
	EmergencyExpand(ctx context.Context, sandboxID string) (AutoExpandResult, error)
	Exec(ctx context.Context, sandboxID, command string) (ExecOutput, error)
	CreateSnapshot(ctx context.Context, sandboxID string, snapshot Snapshot) error
	RollbackToSnapshot(ctx context.Context, sandboxID string, snapshot Snapshot) (Metadata, error)
}
