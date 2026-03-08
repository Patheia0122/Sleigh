package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appErr "agent-heavyworks-runtime/server/internal/errors"
	"agent-heavyworks-runtime/server/internal/sandbox"
)

const (
	defaultCreateImage                  = "alpine:3.20"
	defaultCreateMemoryLimitMB          = 256
	defaultMaxMemoryLimitMB       int64 = 4096
	preExecExpandThresholdPercent       = 85.0
	emergencyExpandFactor               = 2.0
)

type Backend struct{}

func NewBackend() *Backend {
	return &Backend{}
}

func (b *Backend) Kind() string {
	return "docker"
}

func (b *Backend) Create(ctx context.Context, req sandbox.CreateRequest) (sandbox.Metadata, error) {
	image := strings.TrimSpace(req.Image)
	if image == "" {
		image = defaultCreateImage
	}
	limitMB := req.MemoryLimitMB
	if limitMB <= 0 {
		limitMB = defaultCreateMemoryLimitMB
	}
	container := containerName(req.ID)

	args := []string{"create", "--name", container}
	args = append(args, "--memory", fmt.Sprintf("%dm", limitMB))
	args = append(args, "--label", "heavyworks.sandbox_id="+req.ID)
	for key, value := range req.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", key, value))
	}
	// Keep the sandbox container alive so it can accept exec commands.
	args = append(args, image, "sh", "-lc", "while true; do sleep 3600; done")

	if _, err := dockerJSON(ctx, args...); err != nil {
		return sandbox.Metadata{}, err
	}

	meta, err := b.Get(ctx, req.ID)
	if err != nil {
		return sandbox.Metadata{}, err
	}

	return meta, nil
}

func (b *Backend) Get(ctx context.Context, id string) (sandbox.Metadata, error) {
	container := containerName(id)
	output, err := dockerJSON(
		ctx,
		"inspect",
		container,
		"--format",
		"{{json .}}",
	)
	if err != nil {
		if isDockerNotFoundError(err) {
			return sandbox.Metadata{}, appErr.ErrNotFound
		}
		return sandbox.Metadata{}, err
	}

	var inspected dockerInspect
	if err := json.Unmarshal([]byte(output), &inspected); err != nil {
		return sandbox.Metadata{}, fmt.Errorf("unmarshal docker inspect: %w", err)
	}

	return sandbox.Metadata{
		ID:            id,
		Image:         inspected.Config.Image,
		Status:        inspected.State.Status,
		Created:       inspected.Created,
		MemoryLimitMB: inspected.HostConfig.Memory / (1024 * 1024),
	}, nil
}

func (b *Backend) Delete(ctx context.Context, id string) error {
	container := containerName(id)
	_, err := dockerJSON(ctx, "rm", "-f", container)
	if err != nil {
		if isDockerNotFoundError(err) {
			return appErr.ErrNotFound
		}
		return err
	}

	return nil
}

func (b *Backend) UpdateMounts(ctx context.Context, sandboxID string, mounts []sandbox.MountSpec) error {
	container := containerName(sandboxID)

	inspectText, err := dockerJSON(ctx, "inspect", container, "--format", "{{json .}}")
	if err != nil {
		if isDockerNotFoundError(err) {
			return appErr.ErrNotFound
		}
		return err
	}
	var inspected dockerInspect
	if err := json.Unmarshal([]byte(inspectText), &inspected); err != nil {
		return fmt.Errorf("unmarshal inspect for mount update: %w", err)
	}

	wasRunning := inspected.State.Status == "running"
	tmpImage := fmt.Sprintf("hwr-rebuild:%s-%d", sandboxID, time.Now().UnixNano())
	if _, err := dockerJSON(ctx, "commit", container, tmpImage); err != nil {
		return fmt.Errorf("commit before mount update: %w", err)
	}
	if _, err := dockerJSON(ctx, "rm", "-f", container); err != nil {
		return err
	}

	args := []string{"create", "--name", container}
	if inspected.HostConfig.Memory > 0 {
		mb := inspected.HostConfig.Memory / (1024 * 1024)
		if mb > 0 {
			args = append(args, "--memory", fmt.Sprintf("%dm", mb), "--memory-swap", fmt.Sprintf("%dm", mb))
		}
	}
	for key, value := range inspected.Config.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", key, value))
	}
	for _, mount := range mounts {
		bind := fmt.Sprintf("%s:%s:%s", mount.HostPath, mount.ContainerPath, mount.Mode)
		args = append(args, "-v", bind)
	}
	args = append(args, tmpImage)

	if _, err := dockerJSON(ctx, args...); err != nil {
		return fmt.Errorf("recreate container with mounts: %w", err)
	}
	if wasRunning {
		if _, err := dockerJSON(ctx, "start", container); err != nil {
			return fmt.Errorf("start container after mount update: %w", err)
		}
	}
	return nil
}

func (b *Backend) EnsureRunning(ctx context.Context, sandboxID string) error {
	meta, err := b.Get(ctx, sandboxID)
	if err != nil {
		return err
	}
	if meta.Status == "running" {
		return nil
	}

	container := containerName(sandboxID)
	if _, err := dockerJSON(ctx, "start", container); err != nil {
		return err
	}
	return nil
}

func (b *Backend) Exec(ctx context.Context, sandboxID, command string) (sandbox.ExecOutput, error) {
	if err := b.EnsureRunning(ctx, sandboxID); err != nil {
		return sandbox.ExecOutput{}, err
	}

	container := containerName(sandboxID)
	args := []string{"exec", container, "sh", "-lc", command}
	output, err := dockerRun(ctx, args...)
	if err != nil {
		if isDockerNotFoundError(err) {
			return output, appErr.ErrNotFound
		}
		if errors.Is(err, context.Canceled) {
			return output, context.Canceled
		}
		return output, err
	}

	return output, nil
}

func (b *Backend) PreExecAutoExpand(ctx context.Context, sandboxID string) (sandbox.AutoExpandResult, error) {
	pressure, err := b.GetMemoryPressure(ctx, sandboxID)
	if err != nil {
		return sandbox.AutoExpandResult{}, nil
	}
	if !pressure.NearLimit {
		return sandbox.AutoExpandResult{}, nil
	}

	return sandbox.AutoExpandResult{
		Triggered: true,
		FromMB:    pressure.CurrentLimitMB,
		ToMB:      pressure.CurrentLimitMB,
		Reason:    pressure.Reason,
	}, nil
}

func (b *Backend) EmergencyExpand(ctx context.Context, sandboxID string) (sandbox.AutoExpandResult, error) {
	currentMB, err := b.currentMemoryLimitMB(ctx, sandboxID)
	if err != nil {
		return sandbox.AutoExpandResult{}, err
	}
	if currentMB <= 0 {
		return sandbox.AutoExpandResult{}, nil
	}

	target := int64(float64(currentMB) * emergencyExpandFactor)
	if target <= currentMB {
		target = currentMB + 256
	}
	if target > defaultMaxMemoryLimitMB {
		target = defaultMaxMemoryLimitMB
	}
	if target <= currentMB {
		return sandbox.AutoExpandResult{}, nil
	}

	container := containerName(sandboxID)
	if _, err := dockerJSON(ctx, "update", "--memory", fmt.Sprintf("%dm", target), container); err != nil {
		return sandbox.AutoExpandResult{}, err
	}

	return sandbox.AutoExpandResult{
		Triggered: true,
		FromMB:    currentMB,
		ToMB:      target,
		Reason:    "emergency expansion after OOM signal",
	}, nil
}

func (b *Backend) ExpandMemory(ctx context.Context, sandboxID string, targetMB int64) (sandbox.AutoExpandResult, error) {
	if targetMB <= 0 {
		return sandbox.AutoExpandResult{}, appErr.ErrBadRequest
	}
	if targetMB > defaultMaxMemoryLimitMB {
		targetMB = defaultMaxMemoryLimitMB
	}

	currentMB, err := b.currentMemoryLimitMB(ctx, sandboxID)
	if err != nil {
		return sandbox.AutoExpandResult{}, err
	}
	if targetMB <= currentMB {
		return sandbox.AutoExpandResult{
			Triggered: false,
			FromMB:    currentMB,
			ToMB:      currentMB,
			Reason:    "target memory is not greater than current limit",
		}, nil
	}

	container := containerName(sandboxID)
	target := fmt.Sprintf("%dm", targetMB)
	if _, err := dockerJSON(ctx, "update", "--memory", target, "--memory-swap", target, container); err != nil {
		return sandbox.AutoExpandResult{}, err
	}

	return sandbox.AutoExpandResult{
		Triggered: true,
		FromMB:    currentMB,
		ToMB:      targetMB,
		Reason:    "memory expanded by agent decision",
	}, nil
}

func (b *Backend) GetMemoryPressure(ctx context.Context, sandboxID string) (sandbox.MemoryPressure, error) {
	meta, err := b.Get(ctx, sandboxID)
	if err != nil {
		return sandbox.MemoryPressure{}, err
	}
	if meta.Status != "running" {
		return sandbox.MemoryPressure{
			NearLimit:      false,
			UsagePercent:   0,
			CurrentBytes:   0,
			LimitBytes:     meta.MemoryLimitMB * 1024 * 1024,
			CurrentLimitMB: meta.MemoryLimitMB,
			Reason:         "container not running; start sandbox to collect live pressure",
		}, nil
	}

	container := containerName(sandboxID)
	stats, err := b.readContainerMemoryStats(ctx, container)
	if err != nil {
		return sandbox.MemoryPressure{}, err
	}

	if stats.limitBytes <= 0 {
		return sandbox.MemoryPressure{
			NearLimit:      false,
			UsagePercent:   0,
			CurrentBytes:   stats.currentBytes,
			LimitBytes:     stats.limitBytes,
			CurrentLimitMB: 0,
			Reason:         "memory limit unavailable",
		}, nil
	}

	usagePercent := (float64(stats.currentBytes) / float64(stats.limitBytes)) * 100
	near := usagePercent >= preExecExpandThresholdPercent
	reason := ""
	if near {
		reason = fmt.Sprintf("memory usage %.2f%% exceeded threshold %.2f%%", usagePercent, preExecExpandThresholdPercent)
	}

	return sandbox.MemoryPressure{
		NearLimit:      near,
		UsagePercent:   usagePercent,
		CurrentBytes:   stats.currentBytes,
		LimitBytes:     stats.limitBytes,
		CurrentLimitMB: stats.limitBytes / (1024 * 1024),
		Reason:         reason,
	}, nil
}

func (b *Backend) CreateSnapshot(ctx context.Context, sandboxID string, snapshot sandbox.Snapshot) error {
	container := containerName(sandboxID)
	if _, err := dockerJSON(ctx, "inspect", container, "--format", "{{.Id}}"); err != nil {
		if isDockerNotFoundError(err) {
			return appErr.ErrNotFound
		}
		return err
	}

	_, err := dockerJSON(ctx, "commit", container, snapshot.ImageRef)
	if err != nil {
		return err
	}

	return nil
}

func (b *Backend) RollbackToSnapshot(
	ctx context.Context,
	sandboxID string,
	snapshot sandbox.Snapshot,
) (sandbox.Metadata, error) {
	container := containerName(sandboxID)
	if _, err := dockerJSON(ctx, "rm", "-f", container); err != nil {
		if !isDockerNotFoundError(err) {
			return sandbox.Metadata{}, err
		}
	}

	if _, err := dockerJSON(ctx, "create", "--name", container, snapshot.ImageRef); err != nil {
		return sandbox.Metadata{}, err
	}

	meta, err := b.Get(ctx, sandboxID)
	if err != nil {
		return sandbox.Metadata{}, err
	}

	return meta, nil
}

func containerName(id string) string {
	return "hwr-sbx-" + id
}

func dockerJSON(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func dockerRun(ctx context.Context, args ...string) (sandbox.ExecOutput, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := sandbox.ExecOutput{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		return result, fmt.Errorf("docker %s failed: %w", strings.Join(args, " "), err)
	}

	result.ExitCode = 0
	return result, nil
}

func isDockerNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "No such container")
}

type dockerInspect struct {
	Created string `json:"Created"`
	State   struct {
		Status string `json:"Status"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Memory int64 `json:"Memory"`
	} `json:"HostConfig"`
}

func (b *Backend) currentMemoryLimitMB(ctx context.Context, sandboxID string) (int64, error) {
	meta, err := b.Get(ctx, sandboxID)
	if err != nil {
		return 0, err
	}
	return meta.MemoryLimitMB, nil
}

func (b *Backend) memoryUsagePercent(ctx context.Context, container string) (float64, error) {
	out, err := dockerJSON(ctx, "stats", "--no-stream", "--format", "{{.MemPerc}}", container)
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(strings.TrimSuffix(out, "%"))
	percent, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return percent, nil
}

type containerMemoryStats struct {
	currentBytes int64
	limitBytes   int64
}

func (b *Backend) readContainerMemoryStats(ctx context.Context, container string) (containerMemoryStats, error) {
	pidText, err := dockerJSON(ctx, "inspect", container, "--format", "{{.State.Pid}}")
	if err != nil {
		if isDockerNotFoundError(err) {
			return containerMemoryStats{}, appErr.ErrNotFound
		}
		return containerMemoryStats{}, err
	}
	pidText = strings.TrimSpace(pidText)
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return containerMemoryStats{}, fmt.Errorf("container is not running")
	}

	cgroupRelPath, err := readProcessCgroupPath(pid)
	if err != nil {
		return containerMemoryStats{}, err
	}
	base := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(cgroupRelPath, "/"))

	currentRaw, err := os.ReadFile(filepath.Join(base, "memory.current"))
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("read memory.current: %w", err)
	}
	limitRaw, err := os.ReadFile(filepath.Join(base, "memory.max"))
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("read memory.max: %w", err)
	}

	currentBytes, err := strconv.ParseInt(strings.TrimSpace(string(currentRaw)), 10, 64)
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("parse memory.current: %w", err)
	}
	limitText := strings.TrimSpace(string(limitRaw))
	var limitBytes int64
	if limitText == "max" {
		limitBytes = 0
	} else {
		limitBytes, err = strconv.ParseInt(limitText, 10, 64)
		if err != nil {
			return containerMemoryStats{}, fmt.Errorf("parse memory.max: %w", err)
		}
	}

	return containerMemoryStats{
		currentBytes: currentBytes,
		limitBytes:   limitBytes,
	}, nil
}

func readProcessCgroupPath(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", fmt.Errorf("read process cgroup: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] == "" {
			return parts[2], nil
		}
	}
	return "", fmt.Errorf("cgroup v2 path not found for pid %d", pid)
}
