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

	appErr "sleigh-runtime/server/internal/errors"
	"sleigh-runtime/server/internal/sandbox"
)

const (
	defaultCreateImage            = "alpine:3.20"
	defaultCreateMemoryLimitMB    = 256
	preExecExpandThresholdPercent = 85.0
	emergencyExpandFactor         = 2.0
)

type Backend struct {
	imagePullTimeout  time.Duration
	memoryExpandMaxMB int64
}

// NewBackend creates a Docker sandbox backend. memoryExpandMaxMB caps agent-driven
// memory expansion when > 0; if <= 0, there is no software-side absolute MB cap (host
// memory checks in the HTTP layer and Docker still apply).
func NewBackend(imagePullTimeoutSeconds int, memoryExpandMaxMB int64) *Backend {
	timeout := time.Duration(imagePullTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Backend{
		imagePullTimeout:  timeout,
		memoryExpandMaxMB: memoryExpandMaxMB,
	}
}

func (b *Backend) Kind() string {
	return "docker"
}

func (b *Backend) ImageExists(ctx context.Context, image string) (bool, error) {
	return b.imageExists(ctx, strings.TrimSpace(image))
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
	pullTriggered := false
	pullDurationMS := int64(0)

	exists, err := b.imageExists(ctx, image)
	if err != nil {
		return sandbox.Metadata{}, err
	}
	if !exists {
		pullTriggered = true
		pullStarted := time.Now()
		if err := b.pullImage(ctx, image); err != nil {
			return sandbox.Metadata{}, err
		}
		pullDurationMS = time.Since(pullStarted).Milliseconds()
	}

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
	meta.ImagePullTriggered = pullTriggered
	if pullTriggered {
		meta.ImagePullStatus = "completed"
		meta.ImagePullDurationMS = pullDurationMS
	}

	return meta, nil
}

func (b *Backend) imageExists(ctx context.Context, image string) (bool, error) {
	_, err := dockerJSON(ctx, "image", "inspect", image)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "No such image") {
		return false, nil
	}
	return false, fmt.Errorf("inspect image %q failed: %w", image, err)
}

func (b *Backend) pullImage(ctx context.Context, image string) error {
	pullCtx, cancel := context.WithTimeout(ctx, b.imagePullTimeout)
	defer cancel()
	if _, err := dockerJSON(pullCtx, "pull", image); err != nil {
		if errors.Is(pullCtx.Err(), context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded") {
			return fmt.Errorf(
				"image pull timed out after %ds for %q; check server network connectivity or configure proxy (HTTP_PROXY/HTTPS_PROXY)",
				int(b.imagePullTimeout/time.Second),
				image,
			)
		}
		return fmt.Errorf(
			"image pull failed for %q: %w; check server network connectivity or configure proxy (HTTP_PROXY/HTTPS_PROXY)",
			image,
			err,
		)
	}
	return nil
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
	if _, err := dockerCommitWithRetry(ctx, container, tmpImage); err != nil {
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

func (b *Backend) DeleteImageIfUnused(ctx context.Context, image string) (bool, string, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return false, "empty_image", nil
	}
	// Skip images still referenced by any container to avoid host-side collateral deletion.
	referenced, err := dockerJSON(
		ctx,
		"ps",
		"-a",
		"--filter",
		fmt.Sprintf("ancestor=%s", image),
		"--format",
		"{{.ID}}",
	)
	if err != nil {
		return false, "check_referenced_failed", err
	}
	if strings.TrimSpace(referenced) != "" {
		return false, "referenced_by_container", nil
	}
	_, inspectErr := dockerJSON(ctx, "image", "inspect", image)
	if inspectErr != nil {
		if isDockerNotFoundError(inspectErr) || strings.Contains(inspectErr.Error(), "No such image") {
			return true, "already_deleted", nil
		}
		return false, "inspect_failed", inspectErr
	}
	if _, err := dockerJSON(ctx, "image", "rm", image); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "being used") || strings.Contains(msg, "conflict") {
			return false, "referenced_by_container", nil
		}
		if isDockerNotFoundError(err) || strings.Contains(err.Error(), "No such image") {
			return true, "already_deleted", nil
		}
		return false, "remove_failed", err
	}
	return true, "deleted", nil
}

// PruneDanglingImages removes untagged Docker images (REPOSITORY "<none>").
func (b *Backend) PruneDanglingImages(ctx context.Context) error {
	_, err := dockerJSON(ctx, "image", "prune", "-f")
	return err
}

// PreExecAutoExpand is a no-op at the Docker layer; pre-exec automatic expansion is
// implemented in sandbox.Service (label auto_expand_memory + pressure + policy).
func (b *Backend) PreExecAutoExpand(ctx context.Context, sandboxID string) (sandbox.AutoExpandResult, error) {
	_ = ctx
	_ = sandboxID
	return sandbox.AutoExpandResult{}, nil
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
	if b.memoryExpandMaxMB > 0 && target > b.memoryExpandMaxMB {
		target = b.memoryExpandMaxMB
	}
	if target <= currentMB {
		reason := "cannot increase memory limit further"
		if b.memoryExpandMaxMB > 0 {
			reason = fmt.Sprintf("already at or above docker memory expand cap (%d MB)", b.memoryExpandMaxMB)
		}
		return sandbox.AutoExpandResult{
			Triggered: false,
			FromMB:    currentMB,
			ToMB:      currentMB,
			Reason:    reason,
		}, nil
	}

	container := containerName(sandboxID)
	targetStr := fmt.Sprintf("%dm", target)
	if _, err := dockerJSON(ctx, "update", "--memory", targetStr, "--memory-swap", targetStr, container); err != nil {
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
	if b.memoryExpandMaxMB > 0 && targetMB > b.memoryExpandMaxMB {
		targetMB = b.memoryExpandMaxMB
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

	var (
		usagePercent float64
		limitBytes   int64
		currentBytes int64
	)

	if stats.usagePercentFromDocker != nil {
		usagePercent = *stats.usagePercentFromDocker
		if meta.MemoryLimitMB > 0 {
			limitBytes = meta.MemoryLimitMB * 1024 * 1024
			currentBytes = int64(float64(limitBytes) * usagePercent / 100.0)
		}
	} else {
		limitBytes = stats.limitBytes
		if limitBytes <= 0 && meta.MemoryLimitMB > 0 {
			limitBytes = meta.MemoryLimitMB * 1024 * 1024
		}
		currentBytes = stats.currentBytes
		if limitBytes <= 0 {
			return sandbox.MemoryPressure{
				NearLimit:      false,
				UsagePercent:   0,
				CurrentBytes:   stats.currentBytes,
				LimitBytes:     stats.limitBytes,
				CurrentLimitMB: 0,
				Reason:         "memory limit unavailable (no cgroup limit and docker memory unset)",
			}, nil
		}
		usagePercent = (float64(stats.currentBytes) / float64(limitBytes)) * 100
	}

	near := usagePercent >= preExecExpandThresholdPercent
	reason := ""
	if near {
		reason = fmt.Sprintf("memory usage %.2f%% exceeded threshold %.2f%%", usagePercent, preExecExpandThresholdPercent)
	}

	currentLimitMB := int64(0)
	if limitBytes > 0 {
		currentLimitMB = limitBytes / (1024 * 1024)
	}

	return sandbox.MemoryPressure{
		NearLimit:      near,
		UsagePercent:   usagePercent,
		CurrentBytes:   currentBytes,
		LimitBytes:     limitBytes,
		CurrentLimitMB: currentLimitMB,
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

	_, err := dockerCommitWithRetry(ctx, container, snapshot.ImageRef)
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

func dockerCommitWithRetry(ctx context.Context, container, imageRef string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		out, err := dockerJSON(ctx, "commit", container, imageRef)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isDockerCommitConflict(err) {
			break
		}
		if err := waitWithContext(ctx, time.Duration(150*(attempt+1))*time.Millisecond); err != nil {
			return "", err
		}
	}
	return "", lastErr
}

func isDockerCommitConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot commit container which is being removed") ||
		strings.Contains(msg, "removal of container") ||
		strings.Contains(msg, "is already in progress")
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	// usagePercentFromDocker is set when cgroup stats are unavailable; Docker's MemPerc
	// is used directly (same basis as `docker stats`).
	usagePercentFromDocker *float64
}

func (b *Backend) readContainerMemoryStats(ctx context.Context, container string) (containerMemoryStats, error) {
	stats, err := b.readContainerMemoryStatsFromCgroupV2(ctx, container)
	if err == nil {
		return stats, nil
	}
	statsV1, errV1 := b.readContainerMemoryStatsFromCgroupV1(ctx, container)
	if errV1 == nil {
		return statsV1, nil
	}
	statsDocker, errDocker := b.readContainerMemoryStatsFromDocker(ctx, container)
	if errDocker == nil {
		return statsDocker, nil
	}
	return containerMemoryStats{}, fmt.Errorf("memory stats: cgroup v2: %v; cgroup v1: %v; docker: %w", err, errV1, errDocker)
}

func (b *Backend) readContainerMemoryStatsFromCgroupV2(ctx context.Context, container string) (containerMemoryStats, error) {
	pid, err := b.runningContainerPID(ctx, container)
	if err != nil {
		return containerMemoryStats{}, err
	}

	cgroupRelPath, err := readProcessCgroupPathV2(pid)
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

func (b *Backend) readContainerMemoryStatsFromCgroupV1(ctx context.Context, container string) (containerMemoryStats, error) {
	pid, err := b.runningContainerPID(ctx, container)
	if err != nil {
		return containerMemoryStats{}, err
	}
	rel, err := readProcessCgroupMemoryPathV1(pid)
	if err != nil {
		return containerMemoryStats{}, err
	}
	base := filepath.Join("/sys/fs/cgroup/memory", strings.TrimPrefix(rel, "/"))

	usageRaw, err := os.ReadFile(filepath.Join(base, "memory.usage_in_bytes"))
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("read memory.usage_in_bytes: %w", err)
	}
	limitRaw, err := os.ReadFile(filepath.Join(base, "memory.limit_in_bytes"))
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("read memory.limit_in_bytes: %w", err)
	}
	currentBytes, err := strconv.ParseInt(strings.TrimSpace(string(usageRaw)), 10, 64)
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("parse usage_in_bytes: %w", err)
	}
	limitBytes, err := strconv.ParseInt(strings.TrimSpace(string(limitRaw)), 10, 64)
	if err != nil {
		return containerMemoryStats{}, fmt.Errorf("parse limit_in_bytes: %w", err)
	}
	// cgroup v1 uses a very large sentinel for "no limit"
	if limitBytes <= 0 || limitBytes > 1<<60 {
		limitBytes = 0
	}

	return containerMemoryStats{
		currentBytes: currentBytes,
		limitBytes:   limitBytes,
	}, nil
}

func (b *Backend) readContainerMemoryStatsFromDocker(ctx context.Context, container string) (containerMemoryStats, error) {
	p, err := b.memoryUsagePercent(ctx, container)
	if err != nil {
		return containerMemoryStats{}, err
	}
	pp := p
	return containerMemoryStats{usagePercentFromDocker: &pp}, nil
}

func (b *Backend) runningContainerPID(ctx context.Context, container string) (int, error) {
	pidText, err := dockerJSON(ctx, "inspect", container, "--format", "{{.State.Pid}}")
	if err != nil {
		if isDockerNotFoundError(err) {
			return 0, appErr.ErrNotFound
		}
		return 0, err
	}
	pidText = strings.TrimSpace(pidText)
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("container is not running")
	}
	return pid, nil
}

func readProcessCgroupPathV2(pid int) (string, error) {
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

func readProcessCgroupMemoryPathV1(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", fmt.Errorf("read process cgroup: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] == "memory" {
			return parts[2], nil
		}
	}
	return "", fmt.Errorf("cgroup v1 memory path not found for pid %d", pid)
}
