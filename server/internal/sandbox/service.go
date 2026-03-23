package sandbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	appErr "sleigh-runtime/server/internal/errors"
	"sleigh-runtime/server/internal/id"
	"sleigh-runtime/server/internal/notifier"
	sqlitestore "sleigh-runtime/server/internal/store/sqlite"
)

type Service struct {
	backend  Backend
	store    *sqlitestore.Store
	reporter notifier.Reporter
	policy   Policy
	tracer   trace.Tracer

	mu      sync.RWMutex
	execMap map[string]*execTask

	warmPoolMu          sync.Mutex
	warmPoolHits        int64
	warmPoolMisses      int64
	warmPoolRefillTotal int64
}

type Policy struct {
	MinExpandMB                int64
	MaxExpandMB                int64
	MaxExpandStepMB            int64
	ExecTTLDays                int
	ExecCleanupIntervalSeconds int
	MountAllowedRoot           string
	EnvironmentAllowedRoot     string
	WarmPoolSize               int
	WarmPoolImage              string
	WarmPoolMemoryMB           int64
	SnapshotRootDir            string
	CursorTokenSecret          string
	WebhookHMACSecret          string
	CursorTokenTTLSeconds      int
	SandboxIdleTTLDays         int
}

type ExecRequest struct {
	Command string `json:"command"`
}

type ExecResult struct {
	ID          string `json:"id"`
	SandboxID   string `json:"sandbox_id"`
	Command     string `json:"command"`
	Status      string `json:"status"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	Recovery    string `json:"recovery,omitempty"`
}

type ExecHistoryPage struct {
	Items      []ExecResult `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type CleanupResult struct {
	DeletedRows int64  `json:"deleted_rows"`
	Before      string `json:"before"`
}

type IdleCleanupResult struct {
	DeletedRows int64  `json:"deleted_rows"`
	Before      string `json:"before"`
}

type ImageCleanupResult struct {
	Scanned int64 `json:"scanned"`
	Deleted int64 `json:"deleted"`
}

type MountRequest struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	Mode          string `json:"mode"`
}

type WarmPoolStats struct {
	Enabled     bool   `json:"enabled"`
	TargetSize  int    `json:"target_size"`
	Available   int    `json:"available"`
	Image       string `json:"image"`
	MemoryMB    int64  `json:"memory_mb"`
	Hits        int64  `json:"hits"`
	Misses      int64  `json:"misses"`
	RefillTotal int64  `json:"refill_total"`
}

type execTask struct {
	result ExecResult
	cancel context.CancelFunc
}

const managedImageUnusedTTLDays = 14

const webhookDeliveryTimeout = 8 * time.Second

func NewService(
	backend Backend,
	store *sqlitestore.Store,
	reporter notifier.Reporter,
	policy Policy,
	tracer trace.Tracer,
) *Service {
	if tracer == nil {
		tracer = trace.NewNoopTracerProvider().Tracer("sleigh.sandbox")
	}
	return &Service{
		backend:  backend,
		store:    store,
		reporter: reporter,
		policy:   policy,
		tracer:   tracer,
		execMap:  make(map[string]*execTask),
	}
}

func (s *Service) Kind() string {
	if s.backend == nil {
		return "unknown"
	}
	return s.backend.Kind()
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (Metadata, error) {
	startedAt := time.Now()
	if s.backend == nil || s.store == nil {
		return Metadata{}, fmt.Errorf("sandbox service not initialized")
	}

	ctx, span := s.tracer.Start(ctx, "sandbox.create")
	defer span.End()
	span.SetAttributes(
		attribute.String("sandbox.request.image", strings.TrimSpace(req.Image)),
		attribute.Int64("sandbox.request.memory_limit_mb", req.MemoryLimitMB),
	)

	image, memoryMB := s.normalizeCreateDefaults(req)
	req.Image = image
	req.MemoryLimitMB = memoryMB
	sessionID := sessionIDFromLabels(req.Labels)
	span.SetAttributes(
		attribute.String("sandbox.image", req.Image),
		attribute.Int64("sandbox.memory_limit_mb", req.MemoryLimitMB),
		attribute.String("sandbox.session_id", sessionID),
	)

	meta, allocatedFromPool, err := s.allocateFromWarmPool(ctx, req, sessionID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Metadata{}, err
	}
	span.SetAttributes(attribute.Bool("sandbox.from_warm_pool", allocatedFromPool))
	if !allocatedFromPool {
		sandboxID, idErr := id.New("sbx_")
		if idErr != nil {
			span.RecordError(idErr)
			span.SetStatus(codes.Error, idErr.Error())
			return Metadata{}, idErr
		}
		req.ID = sandboxID
		meta, err = s.backend.Create(ctx, req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return Metadata{}, err
		}

		meta.ID = sandboxID
		if meta.Image == "" {
			meta.Image = req.Image
		}
		if meta.Created == "" {
			meta.Created = time.Now().UTC().Format(time.RFC3339)
		}
		meta.Labels = req.Labels

		if err := s.store.CreateSandbox(ctx, sqlitestore.SandboxRecord{
			ID:           meta.ID,
			SessionID:    sessionID,
			Image:        meta.Image,
			Status:       meta.Status,
			Labels:       meta.Labels,
			Created:      meta.Created,
			LastAccessed: meta.Created,
		}); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return Metadata{}, err
		}
	}

	trigger := "sandbox_created"
	if allocatedFromPool {
		trigger = "sandbox_assigned_from_pool"
	}
	s.emitSessionAggregate(context.Background(), sessionID, meta.ID, trigger, "info")
	go s.ensureWarmPool(context.Background())
	s.recordManagedImageUsageAsync(meta)
	meta.StartupLatencyMS = time.Since(startedAt).Milliseconds()
	span.SetAttributes(
		attribute.String("sandbox.id", meta.ID),
		attribute.String("sandbox.status", meta.Status),
		attribute.Int64("sandbox.create_duration_ms", meta.StartupLatencyMS),
	)
	span.SetStatus(codes.Ok, "sandbox created")

	return meta, nil
}

func (s *Service) IsCreateImageCached(ctx context.Context, image string) (bool, string, error) {
	if s.backend == nil {
		return false, "", fmt.Errorf("sandbox service not initialized")
	}
	normalizedImage, _ := s.normalizeCreateDefaults(CreateRequest{Image: image})
	exists, err := s.backend.ImageExists(ctx, normalizedImage)
	if err != nil {
		return false, normalizedImage, err
	}
	return exists, normalizedImage, nil
}

func (s *Service) Get(ctx context.Context, sandboxID string) (Metadata, error) {
	if s.backend == nil || s.store == nil {
		return Metadata{}, fmt.Errorf("sandbox service not initialized")
	}

	record, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		return Metadata{}, err
	}

	meta, err := s.backend.Get(ctx, sandboxID)
	if err != nil {
		return Metadata{}, err
	}

	meta.ID = record.ID
	meta.Labels = record.Labels
	if meta.Image == "" {
		meta.Image = record.Image
	}
	if meta.Created == "" {
		meta.Created = record.Created
	}

	if updateErr := s.store.UpdateSandboxStatus(ctx, sandboxID, meta.Status); updateErr != nil && updateErr != appErr.ErrNotFound {
		return Metadata{}, updateErr
	}

	return meta, nil
}

func (s *Service) ListBySession(ctx context.Context, sessionID string) ([]Metadata, error) {
	if s.backend == nil || s.store == nil {
		return nil, fmt.Errorf("sandbox service not initialized")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, appErr.ErrBadRequest
	}
	rows, err := s.store.ListSandboxesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0, len(rows))
	for _, row := range rows {
		meta := Metadata{
			ID:      row.ID,
			Image:   row.Image,
			Status:  row.Status,
			Labels:  row.Labels,
			Created: row.Created,
		}
		if live, liveErr := s.backend.Get(ctx, row.ID); liveErr == nil {
			meta.Status = live.Status
			meta.MemoryLimitMB = live.MemoryLimitMB
			if updateErr := s.store.UpdateSandboxStatus(ctx, row.ID, live.Status); updateErr != nil && updateErr != appErr.ErrNotFound {
				return nil, updateErr
			}
		}
		result = append(result, meta)
	}
	return result, nil
}

func (s *Service) AuthorizeSandboxAccess(ctx context.Context, sessionID, sandboxID string) error {
	if s.store == nil {
		return fmt.Errorf("sandbox service not initialized")
	}
	sessionID = strings.TrimSpace(sessionID)
	sandboxID = strings.TrimSpace(sandboxID)
	if sessionID == "" || sandboxID == "" {
		return appErr.ErrBadRequest
	}
	record, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(record.SessionID) != sessionID {
		return appErr.ErrForbidden
	}
	_ = s.store.UpdateSandboxLastAccess(ctx, sandboxID, time.Now().UTC().Format(time.RFC3339))
	return nil
}

func (s *Service) Delete(ctx context.Context, sandboxID string) error {
	if s.backend == nil || s.store == nil {
		return fmt.Errorf("sandbox service not initialized")
	}

	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return err
	}
	record, _ := s.store.GetSandbox(ctx, sandboxID)

	if err := s.backend.Delete(ctx, sandboxID); err != nil {
		return err
	}

	if err := s.store.DeleteSandbox(ctx, sandboxID); err != nil {
		return err
	}
	s.emitSessionAggregate(context.Background(), record.SessionID, sandboxID, "sandbox_deleted", "info")
	return nil
}

func (s *Service) CreateSnapshot(ctx context.Context, sandboxID string) (Snapshot, error) {
	if s.backend == nil || s.store == nil {
		return Snapshot{}, fmt.Errorf("sandbox service not initialized")
	}

	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return Snapshot{}, err
	}

	snapshotID, err := id.New("snap_")
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		ID:        snapshotID,
		SandboxID: sandboxID,
		ImageRef:  "hwr-snapshot:" + strings.TrimPrefix(snapshotID, "snap_"),
		Created:   time.Now().UTC().Format(time.RFC3339),
	}
	workspaceMount, hasWorkspaceMount, err := s.pickWorkspaceMount(ctx, sandboxID)
	if err != nil {
		return Snapshot{}, err
	}
	if hasWorkspaceMount {
		baseSnapshotID, createErr := s.createWorkspaceSnapshot(ctx, sandboxID, snapshotID, workspaceMount.HostPath)
		if createErr != nil {
			return Snapshot{}, createErr
		}
		snapshot.Type = "workspace"
		snapshot.HostPath = workspaceMount.HostPath
		snapshot.BaseID = baseSnapshotID
		snapshot.ImageRef = "workspace:" + snapshotID
	} else {
		snapshot.Type = "container"
		if err := s.backend.CreateSnapshot(ctx, sandboxID, snapshot); err != nil {
			return Snapshot{}, err
		}
	}

	if err := s.store.CreateSnapshot(ctx, sqlitestore.SnapshotRecord{
		ID:        snapshot.ID,
		SandboxID: snapshot.SandboxID,
		ImageRef:  snapshot.ImageRef,
		Type:      snapshot.Type,
		HostPath:  snapshot.HostPath,
		BaseID:    snapshot.BaseID,
		Created:   snapshot.Created,
	}); err != nil {
		return Snapshot{}, err
	}

	return snapshot, nil
}

func (s *Service) ListSnapshots(ctx context.Context, sandboxID string) ([]Snapshot, error) {
	if s.store == nil {
		return nil, fmt.Errorf("sandbox service not initialized")
	}

	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return nil, err
	}

	rows, err := s.store.ListSnapshots(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	result := make([]Snapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, Snapshot{
			ID:        row.ID,
			SandboxID: row.SandboxID,
			ImageRef:  row.ImageRef,
			Type:      row.Type,
			HostPath:  row.HostPath,
			BaseID:    row.BaseID,
			Created:   row.Created,
		})
	}

	return result, nil
}

func (s *Service) Rollback(ctx context.Context, sandboxID, snapshotID string) (Metadata, error) {
	if s.backend == nil || s.store == nil {
		return Metadata{}, fmt.Errorf("sandbox service not initialized")
	}
	if strings.TrimSpace(snapshotID) == "" {
		return Metadata{}, appErr.ErrBadRequest
	}

	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return Metadata{}, err
	}

	snapshot, err := s.store.GetSnapshot(ctx, sandboxID, snapshotID)
	if err != nil {
		return Metadata{}, err
	}

	snapshotMeta := Snapshot{
		ID:        snapshot.ID,
		SandboxID: snapshot.SandboxID,
		ImageRef:  snapshot.ImageRef,
		Type:      snapshot.Type,
		HostPath:  snapshot.HostPath,
		BaseID:    snapshot.BaseID,
		Created:   snapshot.Created,
	}
	if snapshotMeta.Type == "workspace" {
		if err := s.rollbackWorkspaceSnapshot(ctx, snapshotMeta); err != nil {
			return Metadata{}, err
		}
		meta, err := s.Get(ctx, sandboxID)
		if err != nil {
			return Metadata{}, err
		}
		return meta, nil
	}
	meta, err := s.backend.RollbackToSnapshot(ctx, sandboxID, snapshotMeta)
	if err != nil {
		return Metadata{}, err
	}
	meta.ID = sandboxID

	if err := s.store.UpdateSandboxImage(ctx, sandboxID, snapshot.ImageRef); err != nil {
		return Metadata{}, err
	}
	if err := s.store.UpdateSandboxStatus(ctx, sandboxID, meta.Status); err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Service) Execute(ctx context.Context, sandboxID string, req ExecRequest) (ExecResult, error) {
	if s.backend == nil || s.store == nil {
		return ExecResult{}, fmt.Errorf("sandbox service not initialized")
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return ExecResult{}, appErr.ErrBadRequest
	}

	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return ExecResult{}, err
	}
	record, _ := s.store.GetSandbox(ctx, sandboxID)

	execID, err := id.New("exec_")
	if err != nil {
		return ExecResult{}, err
	}

	started := time.Now().UTC().Format(time.RFC3339)
	execCtx, cancel := context.WithCancel(context.Background())
	task := &execTask{
		result: ExecResult{
			ID:        execID,
			SandboxID: sandboxID,
			Command:   command,
			Status:    "running",
			StartedAt: started,
		},
		cancel: cancel,
	}

	s.mu.Lock()
	s.execMap[execID] = task
	s.mu.Unlock()

	if err := s.store.CreateExecTask(ctx, sqlitestore.ExecTaskRecord{
		ID:        task.result.ID,
		SandboxID: task.result.SandboxID,
		Command:   task.result.Command,
		Status:    task.result.Status,
		StartedAt: task.result.StartedAt,
	}); err != nil {
		return ExecResult{}, err
	}
	_ = s.store.UpdateSandboxStatus(ctx, sandboxID, "running")
	s.emitSessionAggregate(context.Background(), record.SessionID, sandboxID, "exec_started", "info")

	go s.runExecTask(execCtx, execID)

	return task.result, nil
}

func (s *Service) GetMemoryPressure(ctx context.Context, sandboxID string) (MemoryPressure, error) {
	if s.backend == nil || s.store == nil {
		return MemoryPressure{}, fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return MemoryPressure{}, err
	}
	return s.backend.GetMemoryPressure(ctx, sandboxID)
}

func (s *Service) ExpandMemory(ctx context.Context, sandboxID string, targetMB int64) (AutoExpandResult, error) {
	if s.backend == nil || s.store == nil {
		return AutoExpandResult{}, fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return AutoExpandResult{}, err
	}
	if targetMB < s.policy.MinExpandMB {
		return AutoExpandResult{}, appErr.ErrBadRequest
	}
	meta, err := s.backend.Get(ctx, sandboxID)
	if err != nil {
		return AutoExpandResult{}, err
	}
	if targetMB <= meta.MemoryLimitMB {
		return AutoExpandResult{}, appErr.ErrBadRequest
	}
	return s.backend.ExpandMemory(ctx, sandboxID, targetMB)
}

func (s *Service) AutoExpandMemory(ctx context.Context, sandboxID string) (AutoExpandResult, error) {
	if s.backend == nil || s.store == nil {
		return AutoExpandResult{}, fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return AutoExpandResult{}, err
	}
	return s.backend.EmergencyExpand(ctx, sandboxID)
}

func (s *Service) EnsureRunning(ctx context.Context, sandboxID string) error {
	if s.backend == nil || s.store == nil {
		return fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return err
	}
	if err := s.backend.EnsureRunning(ctx, sandboxID); err != nil {
		return err
	}
	if err := s.store.UpdateSandboxStatus(ctx, sandboxID, "running"); err != nil && err != appErr.ErrNotFound {
		return err
	}
	return nil
}

func (s *Service) ListExecHistory(
	ctx context.Context,
	sessionID string,
	limit int,
	cursor string,
) (ExecHistoryPage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ExecHistoryPage{}, appErr.ErrBadRequest
	}
	cursorStartedAt, cursorExecID, err := parseCursor(
		sessionID,
		cursor,
		limit,
		s.policy.CursorTokenSecret,
		s.policy.CursorTokenTTLSeconds,
	)
	if err != nil {
		return ExecHistoryPage{}, appErr.ErrBadRequest
	}
	rows, err := s.store.ListExecTasksBySession(ctx, sessionID, limit, cursorStartedAt, cursorExecID)
	if err != nil {
		return ExecHistoryPage{}, err
	}

	items := make([]ExecResult, 0, len(rows))
	for _, row := range rows {
		items = append(items, ExecResult{
			ID:          row.ID,
			SandboxID:   row.SandboxID,
			Command:     row.Command,
			Status:      row.Status,
			Stdout:      row.Stdout,
			Stderr:      row.Stderr,
			ExitCode:    row.ExitCode,
			Error:       row.Error,
			StartedAt:   row.StartedAt,
			CompletedAt: row.CompletedAt,
			Recovery:    row.Recovery,
		})
	}

	page := ExecHistoryPage{Items: items}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = buildCursorToken(
			sessionID,
			last.StartedAt,
			last.ID,
			limit,
			s.policy.CursorTokenSecret,
			s.policy.CursorTokenTTLSeconds,
		)
		if err != nil {
			return ExecHistoryPage{}, err
		}
	}
	return page, nil
}

func (s *Service) CleanupExecHistory(ctx context.Context) (CleanupResult, error) {
	ttlDays := s.policy.ExecTTLDays
	if ttlDays <= 0 {
		ttlDays = 14
	}
	beforeTime := time.Now().UTC().AddDate(0, 0, -ttlDays)
	before := beforeTime.Format(time.RFC3339)

	deleted, err := s.store.CleanupExecTasksBefore(ctx, before)
	if err != nil {
		return CleanupResult{}, err
	}

	return CleanupResult{
		DeletedRows: deleted,
		Before:      before,
	}, nil
}

func (s *Service) StartExecCleanupLoop(
	ctx context.Context,
	interval time.Duration,
	onComplete func(CleanupResult, error),
) {
	if interval <= 0 {
		return
	}
	go func() {
		// Run one cleanup shortly after startup.
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()

		run := func() {
			runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := s.CleanupExecHistory(runCtx)
			if onComplete != nil {
				onComplete(result, err)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			run()
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func (s *Service) WarmPoolStatus(ctx context.Context) (WarmPoolStats, error) {
	if s.backend == nil || s.store == nil {
		return WarmPoolStats{}, fmt.Errorf("sandbox service not initialized")
	}
	image, memoryMB, enabled := s.warmPoolConfig()
	available, err := s.store.CountWarmPoolAvailable(ctx, image, memoryMB)
	if err != nil {
		return WarmPoolStats{}, err
	}

	s.warmPoolMu.Lock()
	stats := WarmPoolStats{
		Enabled:     enabled,
		TargetSize:  s.policy.WarmPoolSize,
		Available:   available,
		Image:       image,
		MemoryMB:    memoryMB,
		Hits:        s.warmPoolHits,
		Misses:      s.warmPoolMisses,
		RefillTotal: s.warmPoolRefillTotal,
	}
	s.warmPoolMu.Unlock()
	return stats, nil
}

func (s *Service) RefillWarmPool(ctx context.Context) (WarmPoolStats, error) {
	if err := s.ensureWarmPool(ctx); err != nil {
		return WarmPoolStats{}, err
	}
	return s.WarmPoolStatus(ctx)
}

func (s *Service) ListMounts(ctx context.Context, sandboxID string) ([]MountSpec, error) {
	if s.backend == nil || s.store == nil {
		return nil, fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return nil, err
	}

	rows, err := s.store.ListMounts(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	result := make([]MountSpec, 0, len(rows))
	for _, row := range rows {
		result = append(result, MountSpec{
			ID:            row.ID,
			SandboxID:     row.SandboxID,
			HostPath:      row.HostPath,
			ContainerPath: row.ContainerPath,
			Mode:          row.Mode,
			Created:       row.Created,
		})
	}
	return result, nil
}

func (s *Service) MountPath(ctx context.Context, sandboxID string, req MountRequest) (MountSpec, error) {
	if s.backend == nil || s.store == nil {
		return MountSpec{}, fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return MountSpec{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "rw"
	}
	if mode != "rw" && mode != "ro" {
		return MountSpec{}, appErr.ErrBadRequest
	}
	hostPath := strings.TrimSpace(req.HostPath)
	containerPath := strings.TrimSpace(req.ContainerPath)
	if hostPath == "" || containerPath == "" {
		return MountSpec{}, appErr.ErrBadRequest
	}
	if !filepath.IsAbs(hostPath) || !filepath.IsAbs(containerPath) {
		return MountSpec{}, appErr.ErrBadRequest
	}
	mountRoot := strings.TrimSpace(s.policy.MountAllowedRoot)
	if mountRoot == "" || !filepath.IsAbs(mountRoot) || !isPathWithinRoot(hostPath, mountRoot) {
		return MountSpec{}, appErr.ErrBadRequest
	}

	mountID, err := id.New("mnt_")
	if err != nil {
		return MountSpec{}, err
	}
	mount := MountSpec{
		ID:            mountID,
		SandboxID:     sandboxID,
		HostPath:      hostPath,
		ContainerPath: containerPath,
		Mode:          mode,
		Created:       time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.store.CreateMount(ctx, sqlitestore.MountRecord{
		ID:            mount.ID,
		SandboxID:     mount.SandboxID,
		HostPath:      mount.HostPath,
		ContainerPath: mount.ContainerPath,
		Mode:          mount.Mode,
		Created:       mount.Created,
	}); err != nil {
		return MountSpec{}, err
	}

	mounts, err := s.ListMounts(ctx, sandboxID)
	if err != nil {
		return MountSpec{}, err
	}
	if err := s.backend.UpdateMounts(ctx, sandboxID, mounts); err != nil {
		_ = s.store.DeleteMount(context.Background(), sandboxID, mountID)
		return MountSpec{}, err
	}

	record, _ := s.store.GetSandbox(ctx, sandboxID)
	s.emitSessionAggregate(context.Background(), record.SessionID, sandboxID, "mount_added", "info")
	return mount, nil
}

func (s *Service) UnmountPath(ctx context.Context, sandboxID, mountID string) error {
	if s.backend == nil || s.store == nil {
		return fmt.Errorf("sandbox service not initialized")
	}
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return err
	}

	if err := s.store.DeleteMount(ctx, sandboxID, mountID); err != nil {
		return err
	}
	mounts, err := s.ListMounts(ctx, sandboxID)
	if err != nil {
		return err
	}
	if err := s.backend.UpdateMounts(ctx, sandboxID, mounts); err != nil {
		return err
	}
	record, _ := s.store.GetSandbox(ctx, sandboxID)
	s.emitSessionAggregate(context.Background(), record.SessionID, sandboxID, "mount_removed", "info")
	return nil
}

func (s *Service) GetExec(ctx context.Context, sandboxID, execID string) (ExecResult, error) {
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return ExecResult{}, err
	}

	s.mu.RLock()
	task, ok := s.execMap[execID]
	s.mu.RUnlock()
	if ok && task.result.SandboxID == sandboxID {
		return task.result, nil
	}

	record, err := s.store.GetExecTask(ctx, sandboxID, execID)
	if err != nil {
		return ExecResult{}, err
	}

	return ExecResult{
		ID:          record.ID,
		SandboxID:   record.SandboxID,
		Command:     record.Command,
		Status:      record.Status,
		Stdout:      record.Stdout,
		Stderr:      record.Stderr,
		ExitCode:    record.ExitCode,
		Error:       record.Error,
		StartedAt:   record.StartedAt,
		CompletedAt: record.CompletedAt,
		Recovery:    record.Recovery,
	}, nil
}

func (s *Service) CancelExec(ctx context.Context, sandboxID, execID string) (ExecResult, error) {
	if _, err := s.store.GetSandbox(ctx, sandboxID); err != nil {
		return ExecResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.execMap[execID]
	if !ok || task.result.SandboxID != sandboxID {
		return ExecResult{}, appErr.ErrNotFound
	}
	if task.result.Status != "running" {
		return task.result, nil
	}

	task.result.Status = "cancelling"
	if task.cancel != nil {
		task.cancel()
	}
	_ = s.store.UpdateExecTask(ctx, sqlitestore.ExecTaskRecord{
		ID:          task.result.ID,
		SandboxID:   task.result.SandboxID,
		Status:      task.result.Status,
		Stdout:      task.result.Stdout,
		Stderr:      task.result.Stderr,
		ExitCode:    task.result.ExitCode,
		Error:       task.result.Error,
		CompletedAt: task.result.CompletedAt,
	})

	return task.result, nil
}

func (s *Service) SubscribeExecWebhook(
	ctx context.Context,
	sessionID string,
	sandboxID string,
	execID string,
	webhookURL string,
) (bool, ExecResult, bool, error) {
	ctx, span := s.tracer.Start(ctx, "webhook.subscribe.exec")
	defer span.End()
	if s.store == nil {
		err := fmt.Errorf("sandbox service not initialized")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return false, ExecResult{}, false, err
	}
	sessionID = strings.TrimSpace(sessionID)
	sandboxID = strings.TrimSpace(sandboxID)
	execID = strings.TrimSpace(execID)
	webhookURL = strings.TrimSpace(webhookURL)
	span.SetAttributes(
		attribute.String("webhook.session_id", sessionID),
		attribute.String("webhook.sandbox_id", sandboxID),
		attribute.String("webhook.exec_id", execID),
		attribute.String("webhook.url", webhookURL),
	)
	if sessionID == "" || sandboxID == "" || execID == "" || webhookURL == "" {
		span.RecordError(appErr.ErrBadRequest)
		span.SetStatus(codes.Error, appErr.ErrBadRequest.Error())
		return false, ExecResult{}, false, appErr.ErrBadRequest
	}
	parsedURL, err := neturl.ParseRequestURI(webhookURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		span.RecordError(appErr.ErrBadRequest)
		span.SetStatus(codes.Error, appErr.ErrBadRequest.Error())
		return false, ExecResult{}, false, appErr.ErrBadRequest
	}
	span.SetAttributes(
		attribute.String("webhook.scheme", parsedURL.Scheme),
		attribute.String("webhook.host", parsedURL.Host),
	)
	if err := s.AuthorizeSandboxAccess(ctx, sessionID, sandboxID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return false, ExecResult{}, false, err
	}
	execResult, err := s.GetExec(ctx, sandboxID, execID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return false, ExecResult{}, false, err
	}
	subscriptionID, err := id.New("whs_")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return false, ExecResult{}, false, err
	}
	span.SetAttributes(attribute.String("webhook.subscription_id", subscriptionID))
	now := time.Now().UTC().Format(time.RFC3339)
	created, err := s.store.CreateExecWebhookSubscription(ctx, sqlitestore.ExecWebhookSubscriptionRecord{
		ID:              subscriptionID,
		SessionID:       sessionID,
		SandboxID:       sandboxID,
		ExecID:          execID,
		WebhookURL:      webhookURL,
		DeliveredStatus: "",
		DeliveredAt:     "",
		LastError:       "",
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return false, ExecResult{}, false, err
	}
	isTerminal := isExecTerminalStatus(execResult.Status)
	span.SetAttributes(
		attribute.Bool("webhook.subscription_created", created),
		attribute.String("webhook.exec_status", execResult.Status),
		attribute.Bool("webhook.deliver_immediately", isTerminal),
	)
	span.SetStatus(codes.Ok, "exec webhook subscribed")
	if isTerminal {
		go s.deliverExecWebhookNotifications(context.Background(), sandboxID, execID, execResult)
	}
	return created, execResult, isTerminal, nil
}

func (s *Service) runExecTask(ctx context.Context, execID string) {
	s.mu.RLock()
	task, ok := s.execMap[execID]
	s.mu.RUnlock()
	if !ok {
		return
	}

	expandResult, _ := s.backend.PreExecAutoExpand(ctx, task.result.SandboxID)
	if expandResult.Triggered {
		task.result.Recovery = expandResult.Reason
		if record, err := s.store.GetSandbox(context.Background(), task.result.SandboxID); err == nil {
			s.emitSessionAggregate(context.Background(), record.SessionID, task.result.SandboxID, "memory_pressure_warning", "warn")
		}
	}
	output, err := s.backend.Exec(ctx, task.result.SandboxID, task.result.Command)
	if isOOMFailure(output, err) {
		recoveryNote := s.autoRecoverOOM(ctx, task.result.SandboxID)
		if recoveryNote != "" {
			task.result.Recovery = recoveryNote
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.execMap[execID]
	if !ok {
		return
	}

	current.result.Stdout = output.Stdout
	current.result.Stderr = output.Stderr
	wasCancelling := current.result.Status == "cancelling"
	if output.ExitCode != 0 || err == nil {
		code := output.ExitCode
		current.result.ExitCode = &code
	}
	current.result.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	current.cancel = nil

	if err == nil {
		current.result.Status = "succeeded"
	} else if wasCancelling {
		current.result.Status = "cancelled"
		current.result.Error = ""
	} else {
		switch {
		case errors.Is(err, context.Canceled):
			current.result.Status = "cancelled"
		case errors.Is(err, appErr.ErrNotFound):
			current.result.Status = "failed"
			current.result.Error = appErr.ErrNotFound.Error()
		default:
			current.result.Status = "failed"
			current.result.Error = err.Error()
		}
	}

	_ = s.store.UpdateExecTask(context.Background(), sqlitestore.ExecTaskRecord{
		ID:          current.result.ID,
		SandboxID:   current.result.SandboxID,
		Status:      current.result.Status,
		Stdout:      current.result.Stdout,
		Stderr:      current.result.Stderr,
		ExitCode:    current.result.ExitCode,
		Error:       current.result.Error,
		CompletedAt: current.result.CompletedAt,
		Recovery:    current.result.Recovery,
	})
	if record, err := s.store.GetSandbox(context.Background(), current.result.SandboxID); err == nil {
		severity := "info"
		if current.result.Status == "failed" || current.result.Status == "cancelled" {
			severity = "warn"
		}
		s.emitSessionAggregate(context.Background(), record.SessionID, current.result.SandboxID, "exec_finished", severity)
	}
	go s.deliverExecWebhookNotifications(context.Background(), current.result.SandboxID, current.result.ID, current.result)
}

func (s *Service) autoRecoverOOM(ctx context.Context, sandboxID string) string {
	notes := make([]string, 0, 2)

	snapshots, err := s.store.ListSnapshots(ctx, sandboxID)
	if err == nil && len(snapshots) > 0 {
		latest := snapshots[0]
		_, rollbackErr := s.backend.RollbackToSnapshot(ctx, sandboxID, Snapshot{
			ID:        latest.ID,
			SandboxID: latest.SandboxID,
			ImageRef:  latest.ImageRef,
			Created:   latest.Created,
		})
		if rollbackErr == nil {
			notes = append(notes, "rolled back to latest snapshot")
		}
	}
	notes = append(notes, "oom detected; agent should decide new memory target via expand API")

	return strings.Join(notes, "; ")
}

func isOOMFailure(output ExecOutput, err error) bool {
	if output.ExitCode == 137 {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error() + " " + output.Stderr)
	return strings.Contains(msg, "out of memory") || strings.Contains(msg, "cannot allocate memory")
}

func isExecTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func execResultToWebhookStatus(result ExecResult) string {
	switch strings.TrimSpace(result.Status) {
	case "succeeded":
		return "ok"
	case "failed", "cancelled":
		return "err"
	default:
		return "timeout"
	}
}

func (s *Service) deliverExecWebhookNotifications(
	ctx context.Context,
	sandboxID string,
	execID string,
	result ExecResult,
) {
	if s.store == nil {
		return
	}
	items, err := s.store.ListPendingExecWebhookSubscriptions(ctx, sandboxID, execID)
	if err != nil || len(items) == 0 {
		return
	}
	for _, sub := range items {
		deliveryStatus, deliveryErr := s.sendExecWebhook(ctx, sub.WebhookURL, result)
		lastErr := ""
		if deliveryErr != nil {
			lastErr = deliveryErr.Error()
		}
		_ = s.store.MarkExecWebhookSubscriptionDelivered(context.Background(), sub.ID, deliveryStatus, lastErr)
	}
}

func (s *Service) sendExecWebhook(ctx context.Context, webhookURL string, result ExecResult) (string, error) {
	ctx, span := s.tracer.Start(ctx, "webhook.delivery.exec")
	defer span.End()
	span.SetAttributes(
		attribute.String("webhook.url", strings.TrimSpace(webhookURL)),
		attribute.String("webhook.sandbox_id", result.SandboxID),
		attribute.String("webhook.exec_id", result.ID),
		attribute.String("webhook.exec_status", result.Status),
	)
	mapped := execResultToWebhookStatus(result)
	payload := map[string]any{
		"exec_id":      result.ID,
		"sandbox_id":   result.SandboxID,
		"exec_status":  result.Status,
		"command":      result.Command,
		"started_at":   result.StartedAt,
		"completed_at": result.CompletedAt,
	}
	if result.ExitCode != nil {
		payload["exit_code"] = *result.ExitCode
	}
	if strings.TrimSpace(result.Error) != "" {
		payload["error"] = result.Error
	}
	bodyMap := map[string]any{
		"status":  mapped,
		"payload": payload,
	}
	span.SetAttributes(attribute.String("webhook.delivery_status", mapped))
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "err", fmt.Errorf("marshal webhook payload failed: %w", err)
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signPayload := timestamp + "." + string(bodyBytes)
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(s.policy.WebhookHMACSecret)))
	_, _ = mac.Write([]byte(signPayload))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	sendCtx, cancel := context.WithTimeout(ctx, webhookDeliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, webhookURL, bytes.NewReader(bodyBytes))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "err", fmt.Errorf("build webhook request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", signature)
	resp, err := (&http.Client{Timeout: webhookDeliveryTimeout}).Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "timeout", fmt.Errorf("send webhook failed: %w", err)
	}
	defer resp.Body.Close()
	span.SetAttributes(attribute.Int("webhook.http_status_code", resp.StatusCode))
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("webhook returned status %d", resp.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "err", err
	}
	finalStatus := execResultToWebhookStatus(result)
	span.SetAttributes(attribute.String("webhook.delivery_result", finalStatus))
	span.SetStatus(codes.Ok, "webhook delivered")
	return finalStatus, nil
}

func sessionIDFromLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	if value := strings.TrimSpace(labels["session_id"]); value != "" {
		return value
	}
	if value := strings.TrimSpace(labels["sessionId"]); value != "" {
		return value
	}
	return ""
}

func (s *Service) emitSessionAggregate(
	ctx context.Context,
	sessionID string,
	triggerSandboxID string,
	trigger string,
	severity string,
) {
	if s.reporter == nil || strings.TrimSpace(sessionID) == "" {
		return
	}

	sandboxes, err := s.store.ListSandboxesBySession(ctx, sessionID)
	if err != nil {
		return
	}

	items := make([]map[string]any, 0, len(sandboxes))
	for _, sb := range sandboxes {
		entry := map[string]any{
			"sandbox_id": sb.ID,
			"status":     sb.Status,
			"image":      sb.Image,
		}

		if execTask, execErr := s.store.GetLatestExecTaskBySandbox(ctx, sb.ID); execErr == nil {
			entry["last_exec"] = map[string]any{
				"exec_id":    execTask.ID,
				"status":     execTask.Status,
				"command":    execTask.Command,
				"started_at": execTask.StartedAt,
			}
		}

		items = append(items, entry)
	}
	seq, err := s.store.NextSessionSequence(ctx, sessionID)
	if err != nil {
		return
	}

	_ = s.reporter.Report(ctx, notifier.Event{
		EventID:        eventID(sessionID, seq),
		SchemaVersion:  "v1",
		SessionSeq:     seq,
		IdempotencyKey: fmt.Sprintf("%s:%d", sessionID, seq),
		Type:           "session-sandbox-update",
		Severity:       severity,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Payload: map[string]any{
			"session_id":         sessionID,
			"trigger":            trigger,
			"trigger_sandbox_id": triggerSandboxID,
			"sandboxes":          items,
		},
	})
}

func eventID(sessionID string, seq int64) string {
	return fmt.Sprintf("evt_%s_%d", sessionID, seq)
}

func (s *Service) normalizeCreateDefaults(req CreateRequest) (string, int64) {
	image := strings.TrimSpace(req.Image)
	memoryMB := req.MemoryLimitMB
	warmImage, warmMemory, _ := s.warmPoolConfig()
	if image == "" {
		image = warmImage
	}
	if memoryMB <= 0 {
		memoryMB = warmMemory
	}
	return image, memoryMB
}

func (s *Service) warmPoolConfig() (string, int64, bool) {
	image := strings.TrimSpace(s.policy.WarmPoolImage)
	if image == "" {
		image = "python:3.11-slim"
	}
	memoryMB := s.policy.WarmPoolMemoryMB
	if memoryMB <= 0 {
		memoryMB = 256
	}
	enabled := s.policy.WarmPoolSize > 0
	return image, memoryMB, enabled
}

func (s *Service) allocateFromWarmPool(
	ctx context.Context,
	req CreateRequest,
	sessionID string,
) (Metadata, bool, error) {
	image, memoryMB, enabled := s.warmPoolConfig()
	if !enabled {
		return Metadata{}, false, nil
	}
	if req.Image != image || req.MemoryLimitMB != memoryMB {
		return Metadata{}, false, nil
	}

	sandboxID, ok, err := s.store.AcquireWarmPoolEntry(ctx, image, memoryMB)
	if err != nil {
		return Metadata{}, false, err
	}
	if !ok {
		s.warmPoolMu.Lock()
		s.warmPoolMisses++
		s.warmPoolMu.Unlock()
		return Metadata{}, false, nil
	}

	meta, err := s.backend.Get(ctx, sandboxID)
	if err != nil {
		return Metadata{}, false, err
	}
	meta.ID = sandboxID
	if meta.Image == "" {
		meta.Image = req.Image
	}
	if meta.Created == "" {
		meta.Created = time.Now().UTC().Format(time.RFC3339)
	}
	meta.Labels = req.Labels

	if err := s.store.UpdateSandboxAssignment(ctx, sandboxID, sessionID, req.Labels); err != nil {
		return Metadata{}, false, err
	}
	if err := s.store.UpdateSandboxStatus(ctx, sandboxID, meta.Status); err != nil {
		return Metadata{}, false, err
	}

	s.warmPoolMu.Lock()
	s.warmPoolHits++
	s.warmPoolMu.Unlock()
	return meta, true, nil
}

func (s *Service) ensureWarmPool(ctx context.Context) error {
	image, memoryMB, enabled := s.warmPoolConfig()
	if !enabled {
		return nil
	}

	s.warmPoolMu.Lock()
	defer s.warmPoolMu.Unlock()

	available, err := s.store.CountWarmPoolAvailable(ctx, image, memoryMB)
	if err != nil {
		return err
	}

	for available < s.policy.WarmPoolSize {
		sandboxID, err := id.New("sbx_")
		if err != nil {
			return err
		}
		req := CreateRequest{
			ID:            sandboxID,
			Image:         image,
			MemoryLimitMB: memoryMB,
			Labels: map[string]string{
				"warm_pool": "true",
			},
		}
		meta, err := s.backend.Create(ctx, req)
		if err != nil {
			return err
		}
		if meta.Created == "" {
			meta.Created = time.Now().UTC().Format(time.RFC3339)
		}
		if err := s.store.CreateSandbox(ctx, sqlitestore.SandboxRecord{
			ID:           sandboxID,
			SessionID:    "",
			Image:        image,
			Status:       meta.Status,
			Labels:       req.Labels,
			Created:      meta.Created,
			LastAccessed: meta.Created,
		}); err != nil {
			return err
		}
		if err := s.store.CreateWarmPoolEntry(ctx, sqlitestore.WarmPoolEntry{
			SandboxID: sandboxID,
			Image:     image,
			MemoryMB:  memoryMB,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		s.warmPoolRefillTotal++
		available++
	}
	return nil
}

func (s *Service) pickWorkspaceMount(ctx context.Context, sandboxID string) (MountSpec, bool, error) {
	mounts, err := s.store.ListMounts(ctx, sandboxID)
	if err != nil {
		return MountSpec{}, false, err
	}
	for _, mount := range mounts {
		if strings.EqualFold(mount.Mode, "rw") {
			return MountSpec{
				ID:            mount.ID,
				SandboxID:     mount.SandboxID,
				HostPath:      mount.HostPath,
				ContainerPath: mount.ContainerPath,
				Mode:          mount.Mode,
				Created:       mount.Created,
			}, true, nil
		}
	}
	return MountSpec{}, false, nil
}

func (s *Service) createWorkspaceSnapshot(
	ctx context.Context,
	sandboxID string,
	snapshotID string,
	sourceHostPath string,
) (string, error) {
	if strings.TrimSpace(s.policy.SnapshotRootDir) == "" {
		return "", fmt.Errorf("%w: snapshot root dir is empty", appErr.ErrBadRequest)
	}
	if err := s.ensureSnapshotSafe(sandboxID); err != nil {
		return "", err
	}
	baseSnapshotID := ""
	baseDir := ""
	if latest, err := s.store.GetLatestWorkspaceSnapshot(ctx, sandboxID); err == nil {
		baseSnapshotID = latest.ID
		baseDir = snapshotDir(s.policy.SnapshotRootDir, sandboxID, latest.ID)
	}

	targetDir := snapshotDir(s.policy.SnapshotRootDir, sandboxID, snapshotID)
	if err := ensureDir(targetDir); err != nil {
		return "", fmt.Errorf("prepare snapshot dir: %w", err)
	}
	if err := cloneSnapshotBase(baseDir, targetDir); err != nil {
		return "", fmt.Errorf("clone base snapshot: %w", err)
	}
	if err := syncTree(sourceHostPath, targetDir); err != nil {
		return "", fmt.Errorf("sync workspace into snapshot: %w", err)
	}
	if err := writeSnapshotMeta(targetDir, sandboxID, sourceHostPath, baseSnapshotID); err != nil {
		return "", fmt.Errorf("write snapshot metadata: %w", err)
	}
	return baseSnapshotID, nil
}

func (s *Service) rollbackWorkspaceSnapshot(ctx context.Context, snapshot Snapshot) error {
	if strings.TrimSpace(snapshot.HostPath) == "" {
		return fmt.Errorf("%w: snapshot host path is empty", appErr.ErrBadRequest)
	}
	if err := s.ensureSnapshotSafe(snapshot.SandboxID); err != nil {
		return err
	}
	srcDir := snapshotDir(s.policy.SnapshotRootDir, snapshot.SandboxID, snapshot.ID)
	if err := ensureDir(snapshot.HostPath); err != nil {
		return fmt.Errorf("prepare workspace dir before rollback: %w", err)
	}
	if _, err := os.Stat(srcDir); err != nil {
		if os.IsNotExist(err) {
			return appErr.ErrNotFound
		}
		return err
	}
	if err := syncTree(srcDir, snapshot.HostPath); err != nil {
		return fmt.Errorf("restore snapshot to workspace: %w", err)
	}
	record, _ := s.store.GetSandbox(ctx, snapshot.SandboxID)
	s.emitSessionAggregate(context.Background(), record.SessionID, snapshot.SandboxID, "workspace_rollback", "info")
	return nil
}

func (s *Service) ensureSnapshotSafe(sandboxID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, task := range s.execMap {
		if task.result.SandboxID != sandboxID {
			continue
		}
		if task.result.Status == "running" || task.result.Status == "cancelling" {
			return fmt.Errorf("%w: exec task is running", appErr.ErrBadRequest)
		}
	}
	return nil
}

func (s *Service) CleanupIdleSandboxes(ctx context.Context) (IdleCleanupResult, error) {
	ttlDays := s.policy.SandboxIdleTTLDays
	if ttlDays <= 0 {
		ttlDays = 14
	}
	beforeTime := time.Now().UTC().AddDate(0, 0, -ttlDays)
	before := beforeTime.Format(time.RFC3339)

	idle, err := s.store.ListIdleSandboxesBefore(ctx, before)
	if err != nil {
		return IdleCleanupResult{}, err
	}
	var deleted int64
	for _, item := range idle {
		if err := s.backend.Delete(ctx, item.ID); err != nil && !errors.Is(err, appErr.ErrNotFound) {
			continue
		}
		if err := s.store.DeleteSandbox(ctx, item.ID); err != nil && !errors.Is(err, appErr.ErrNotFound) {
			continue
		}
		deleted++
	}
	return IdleCleanupResult{
		DeletedRows: deleted,
		Before:      before,
	}, nil
}

func (s *Service) StartIdleCleanupLoop(
	ctx context.Context,
	interval time.Duration,
	onComplete func(IdleCleanupResult, error),
) {
	if interval <= 0 {
		return
	}
	go func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()

		run := func() {
			runCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			result, err := s.CleanupIdleSandboxes(runCtx)
			if onComplete != nil {
				onComplete(result, err)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			run()
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func (s *Service) CleanupUnusedManagedImages(ctx context.Context) (ImageCleanupResult, error) {
	if s.backend == nil || s.store == nil {
		return ImageCleanupResult{}, fmt.Errorf("sandbox service not initialized")
	}
	before := time.Now().UTC().Add(-managedImageUnusedTTLDays * 24 * time.Hour).Format(time.RFC3339)
	candidates, err := s.store.ListManagedImagesUnusedBefore(ctx, before)
	if err != nil {
		return ImageCleanupResult{}, err
	}
	result := ImageCleanupResult{Scanned: int64(len(candidates))}
	for _, rec := range candidates {
		deleted, _, delErr := s.backend.DeleteImageIfUnused(ctx, rec.Image)
		if delErr != nil {
			continue
		}
		if deleted {
			if err := s.store.DeleteManagedImage(ctx, rec.Image); err != nil {
				continue
			}
			result.Deleted++
		}
	}
	return result, nil
}

func (s *Service) StartImageCleanupLoop(
	ctx context.Context,
	interval time.Duration,
	hook func(ImageCleanupResult, error),
) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			runCtx, cancel := context.WithTimeout(ctx, interval)
			result, err := s.CleanupUnusedManagedImages(runCtx)
			cancel()
			if hook != nil {
				hook(result, err)
			}
		}
	}()
}

func (s *Service) recordManagedImageUsageAsync(meta Metadata) {
	if s.store == nil {
		return
	}
	image := strings.TrimSpace(meta.Image)
	if image == "" {
		return
	}
	usedAt := time.Now().UTC().Format(time.RFC3339)
	go func() {
		if meta.ImagePullTriggered {
			_ = s.store.RecordManagedImagePull(context.Background(), image, usedAt)
			return
		}
		_ = s.store.TouchManagedImageIfTracked(context.Background(), image, usedAt)
	}()
}

func isPathWithinRoot(targetPath, rootPath string) bool {
	cleanTarget := filepath.Clean(targetPath)
	cleanRoot := filepath.Clean(rootPath)
	if resolvedRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = resolvedRoot
	}
	if resolvedTarget, err := filepath.EvalSymlinks(cleanTarget); err == nil {
		cleanTarget = resolvedTarget
	}
	if cleanTarget == cleanRoot {
		return true
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}
