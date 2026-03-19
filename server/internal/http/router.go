package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sleigh-runtime/server/internal/config"
	appErr "sleigh-runtime/server/internal/errors"
	"sleigh-runtime/server/internal/id"
	"sleigh-runtime/server/internal/monitor"
	"sleigh-runtime/server/internal/sandbox"
)

type Router struct {
	config  config.Config
	service *sandbox.Service
	monitor *monitor.Service
}

const (
	lowMemoryBlockThreshold   = 0.10
	lowMemoryWarningThreshold = 0.15
)

type healthResponse struct {
	Status      string `json:"status"`
	Time        string `json:"time"`
	Version     string `json:"version"`
	SandboxKind string `json:"sandbox_kind"`
}

type sessionTokenResponse struct {
	SessionToken string `json:"session_token"`
	IssuedAt     string `json:"issued_at"`
}

type createSandboxRequest struct {
	SessionToken     string            `json:"session_token"`
	Image            string            `json:"image"`
	Labels           map[string]string `json:"labels,omitempty"`
	MemoryLimitMB    int64             `json:"memory_limit_mb,omitempty"`
	ConfirmLowMemory bool              `json:"confirm_low_memory,omitempty"`
	AutoExpandMemory bool              `json:"auto_expand_memory,omitempty"`
	ImagePullPolicy  string            `json:"image_pull_policy,omitempty"`
}

type rollbackRequest struct {
	SessionToken string `json:"session_token"`
	SnapshotID   string `json:"snapshot_id"`
	AutoExpand   bool   `json:"auto_expand,omitempty"`
}

type execRequest struct {
	SessionToken       string `json:"session_token"`
	Command            string `json:"command"`
	Wait               bool   `json:"wait,omitempty"`
	WaitTimeoutSeconds int    `json:"wait_timeout_seconds,omitempty"`
}

type expandMemoryRequest struct {
	SessionToken string `json:"session_token"`
	TargetMB     int64  `json:"target_mb"`
	AutoExpand   bool   `json:"auto_expand,omitempty"`
}

type mountRequest struct {
	SessionToken  string `json:"session_token"`
	WorkspacePath string `json:"workspace_path"`
	ContainerPath string `json:"container_path"`
}

type copyEnvironmentRequest struct {
	SessionToken    string `json:"session_token"`
	EnvironmentPath string `json:"environment_path"`
	WorkspacePath   string `json:"workspace_path,omitempty"`
	SandboxPath     string `json:"sandbox_path"`
}

type readOpRequest struct {
	SessionToken   string   `json:"session_token"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
	MaxLines       int      `json:"max_lines,omitempty"`
	OutputOffset   int64    `json:"output_offset,omitempty"`
}

type readOpResponse struct {
	Status       string `json:"status"`
	DurationMS   int64  `json:"duration_ms"`
	TimedOut     bool   `json:"timed_out"`
	Truncated    bool   `json:"truncated"`
	ExecID       string `json:"exec_id,omitempty"`
	Stdout       string `json:"stdout,omitempty"`
	Stderr       string `json:"stderr,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Error        string `json:"error,omitempty"`
	OmittedBytes int64  `json:"omitted_bytes,omitempty"`
	NextOffset   int64  `json:"next_offset,omitempty"`
}

type codeWriteRequest struct {
	SessionToken   string `json:"session_token"`
	SandboxPath    string `json:"sandbox_path"`
	WriteMode      string `json:"write_mode,omitempty"`
	BeforeContext  string `json:"before_context,omitempty"`
	OldText        string `json:"old_text,omitempty"`
	NewText        string `json:"new_text,omitempty"`
	AfterContext   string `json:"after_context,omitempty"`
	Occurrence     int    `json:"occurrence,omitempty"`
	Content        string `json:"content,omitempty"`
	BuildLanguage  string `json:"build_language,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	MaxLines       int    `json:"max_lines,omitempty"`
}

type codeWriteResponse struct {
	Status       string   `json:"status"`
	DurationMS   int64    `json:"duration_ms"`
	TimedOut     bool     `json:"timed_out"`
	Truncated    bool     `json:"truncated"`
	Stdout       string   `json:"stdout,omitempty"`
	Stderr       string   `json:"stderr,omitempty"`
	Error        string   `json:"error,omitempty"`
	OmittedBytes int64    `json:"omitted_bytes,omitempty"`
	AppliedFiles []string `json:"applied_files,omitempty"`
	FormatIssues []string `json:"format_issues,omitempty"`
	LintIssues   []string `json:"lint_issues,omitempty"`
	BuildStatus  string   `json:"build_status,omitempty"`
}

type workflowRunRequest struct {
	SessionToken string                `json:"session_token"`
	Steps        []workflowStepRequest `json:"steps"`
}

type workflowStepRequest struct {
	Action             string            `json:"action"`
	SandboxID          string            `json:"sandbox_id,omitempty"`
	Image              string            `json:"image,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	MemoryLimitMB      int64             `json:"memory_limit_mb,omitempty"`
	Command            string            `json:"command,omitempty"`
	Wait               *bool             `json:"wait,omitempty"`
	WaitTimeoutSeconds int               `json:"wait_timeout_seconds,omitempty"`
	SnapshotID         string            `json:"snapshot_id,omitempty"`
}

type workflowStepResult struct {
	Index      int    `json:"index"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	SandboxID  string `json:"sandbox_id,omitempty"`
	ExecID     string `json:"exec_id,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Error      string `json:"error,omitempty"`
	Result     any    `json:"result,omitempty"`
}

func NewHandler(cfg config.Config, service *sandbox.Service, monitorService *monitor.Service) stdhttp.Handler {
	router := &Router{
		config:  cfg,
		service: service,
		monitor: monitorService,
	}

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /healthz", router.healthz)
	mux.HandleFunc("POST /sessions/token", router.createSessionToken)
	mux.HandleFunc("GET /resources", router.getResources)
	mux.HandleFunc("GET /diagnostics/oom", router.getOOMDiagnostics)
	mux.HandleFunc("POST /sandboxes", router.createSandbox)
	mux.HandleFunc("GET /sandboxes", router.listSandboxes)
	mux.HandleFunc("GET /sandboxes/{id}", router.getSandbox)
	mux.HandleFunc("DELETE /sandboxes/{id}", router.deleteSandbox)
	mux.HandleFunc("POST /sandboxes/{id}/snapshots", router.createSnapshot)
	mux.HandleFunc("GET /sandboxes/{id}/snapshots", router.listSnapshots)
	mux.HandleFunc("POST /sandboxes/{id}/rollback", router.rollbackSandbox)
	mux.HandleFunc("POST /sandboxes/{id}/exec", router.execSandbox)
	mux.HandleFunc("GET /sandboxes/{id}/exec/{execId}", router.getExecResult)
	mux.HandleFunc("POST /sandboxes/{id}/exec/{execId}/cancel", router.cancelExec)
	mux.HandleFunc("GET /sandboxes/{id}/memory/pressure", router.getMemoryPressure)
	mux.HandleFunc("POST /sandboxes/{id}/memory/expand", router.expandMemory)
	mux.HandleFunc("GET /sandboxes/{id}/mounts", router.listMounts)
	mux.HandleFunc("POST /sandboxes/{id}/mounts", router.mountPath)
	mux.HandleFunc("DELETE /sandboxes/{id}/mounts/{mountId}", router.unmountPath)
	mux.HandleFunc("GET /mounts/workspaces", router.listMountWorkspaces)
	mux.HandleFunc("GET /environments/workspaces", router.listEnvironmentWorkspaces)
	mux.HandleFunc("POST /sandboxes/{id}/environment/copy", router.copyEnvironment)
	mux.HandleFunc("POST /sandboxes/{id}/ops/read", router.readOp)
	mux.HandleFunc("POST /sandboxes/{id}/ops/code/write", router.codeWrite)
	mux.HandleFunc("POST /workflow/run", router.runWorkflow)
	mux.HandleFunc("GET /sessions/{sessionId}/exec-tasks", router.listSessionExecTasks)
	mux.HandleFunc("POST /maintenance/exec-tasks/cleanup", router.cleanupExecTasks)
	mux.HandleFunc("GET /warm-pool", router.getWarmPool)
	mux.HandleFunc("POST /warm-pool/refill", router.refillWarmPool)

	return mux
}

func (r *Router) healthz(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	sandboxKind := "unknown"
	if r.service != nil {
		sandboxKind = r.service.Kind()
	}

	response := healthResponse{
		Status:      "ok",
		Time:        time.Now().UTC().Format(time.RFC3339),
		Version:     r.config.Version,
		SandboxKind: sandboxKind,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(stdhttp.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Router) createSessionToken(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	token, err := id.New("sess_")
	if err != nil {
		writeError(w, stdhttp.StatusInternalServerError, fmt.Errorf("create session token failed: %w", err))
		return
	}
	writeJSON(w, stdhttp.StatusCreated, sessionTokenResponse{
		SessionToken: token,
		IssuedAt:     time.Now().UTC().Format(time.RFC3339),
	})
}

func (r *Router) getResources(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	if r.monitor == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, errors.New("monitor service is unavailable"))
		return
	}

	report, err := r.monitor.GetResources(req.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusOK, report)
}

func (r *Router) getOOMDiagnostics(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	if r.monitor == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, errors.New("monitor service is unavailable"))
		return
	}

	report, err := r.monitor.GetOOMDiagnostics(req.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusOK, report)
}

func (r *Router) createSandbox(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	var body createSandboxRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	if body.AutoExpandMemory {
		body.Labels["auto_expand_memory"] = "true"
	}
	imagePullPolicy := strings.ToLower(strings.TrimSpace(body.ImagePullPolicy))
	if imagePullPolicy == "" {
		imagePullPolicy = "wait"
	}
	if imagePullPolicy != "wait" && imagePullPolicy != "notify" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("image_pull_policy must be one of: wait, notify"))
		return
	}
	if imagePullPolicy == "notify" {
		cacheCheckCtx, cacheCheckCancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cacheCheckCancel()
		cached, resolvedImage, cacheErr := r.service.IsCreateImageCached(cacheCheckCtx, body.Image)
		if strings.TrimSpace(resolvedImage) == "" {
			resolvedImage = strings.TrimSpace(body.Image)
		}
		if cacheErr != nil {
			if errors.Is(cacheCheckCtx.Err(), context.DeadlineExceeded) || strings.Contains(strings.ToLower(cacheErr.Error()), "context deadline exceeded") {
				writeJSON(w, stdhttp.StatusConflict, map[string]any{
					"error":                 fmt.Sprintf("image cache check for %q timed out; treating image pull as needed", resolvedImage),
					"image_pull_needed":     true,
					"resolved_image":        resolvedImage,
					"cache_check_timed_out": true,
					"next_action":           "retry create_sandbox with image_pull_policy=wait to perform pull",
				})
				return
			}
			writeDomainError(w, cacheErr)
			return
		}
		if !cached {
			writeJSON(w, stdhttp.StatusConflict, map[string]any{
				"error":             fmt.Sprintf("image %q is not cached on host and needs pull", resolvedImage),
				"image_pull_needed": true,
				"resolved_image":    resolvedImage,
				"next_action":       "retry create_sandbox with image_pull_policy=wait to perform pull",
			})
			return
		}
	}
	if r.monitor != nil {
		report, monitorErr := r.monitor.GetResources(req.Context())
		if monitorErr == nil {
			ratio := report.Memory.AvailableRatio
			if ratio < lowMemoryBlockThreshold {
				writeError(
					w,
					stdhttp.StatusServiceUnavailable,
					fmt.Errorf(
						"host memory available ratio %.2f%% is below %.0f%%; sandbox creation is blocked",
						ratio*100,
						lowMemoryBlockThreshold*100,
					),
				)
				return
			}
			if ratio < lowMemoryWarningThreshold && !body.ConfirmLowMemory {
				writeError(
					w,
					stdhttp.StatusConflict,
					fmt.Errorf(
						"host memory available ratio %.2f%% is below %.0f%%; resend with confirm_low_memory=true to continue",
						ratio*100,
						lowMemoryWarningThreshold*100,
					),
				)
				return
			}
		}
	}
	// Session token is the source of truth for sandbox ownership.
	body.Labels["session_id"] = sessionID

	meta, err := r.service.Create(req.Context(), sandbox.CreateRequest{
		Image:         body.Image,
		Labels:        body.Labels,
		MemoryLimitMB: body.MemoryLimitMB,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusCreated, meta)
}

func (r *Router) listSandboxes(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items, err := r.service.ListBySession(req.Context(), sessionID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if len(items) == 0 {
		select {
		case <-req.Context().Done():
		case <-time.After(120 * time.Millisecond):
			if retryItems, retryErr := r.service.ListBySession(req.Context(), sessionID); retryErr == nil {
				items = retryItems
			}
		}
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"items": items})
}

func (r *Router) getSandbox(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	meta, err := r.service.Get(req.Context(), sandboxID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusOK, meta)
}

func (r *Router) deleteSandbox(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	if err := r.service.Delete(req.Context(), sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	w.WriteHeader(stdhttp.StatusNoContent)
}

func (r *Router) createSnapshot(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	snapshot, err := r.service.CreateSnapshot(req.Context(), sandboxID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusCreated, snapshot)
}

func (r *Router) listSnapshots(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	snapshots, err := r.service.ListSnapshots(req.Context(), sandboxID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if len(snapshots) == 0 {
		select {
		case <-req.Context().Done():
		case <-time.After(120 * time.Millisecond):
			if retryItems, retryErr := r.service.ListSnapshots(req.Context(), sandboxID); retryErr == nil {
				snapshots = retryItems
			}
		}
	}

	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"items": snapshots,
	})
}

func (r *Router) rollbackSandbox(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}

	var body rollbackRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	autoExpandEnabled := body.AutoExpand
	if !autoExpandEnabled {
		if meta, getErr := r.service.Get(req.Context(), sandboxID); getErr == nil {
			autoExpandEnabled = strings.EqualFold(strings.TrimSpace(meta.Labels["auto_expand_memory"]), "true")
		}
	}
	if autoExpandEnabled {
		if allow, reason := r.allowAutoExpand(req.Context(), "rollback"); allow {
			if _, expandErr := r.service.AutoExpandMemory(req.Context(), sandboxID); expandErr != nil && !errors.Is(expandErr, appErr.ErrBadRequest) {
				writeDomainError(w, expandErr)
				return
			}
		} else if reason != "" {
			// Keep rollback available even when auto expand is blocked by host pressure.
			_ = reason
		}
	}

	meta, err := r.service.Rollback(req.Context(), sandboxID, body.SnapshotID)
	if err != nil {
		if errors.Is(err, appErr.ErrNotFound) {
			snapshots, listErr := r.service.ListSnapshots(req.Context(), sandboxID)
			if listErr == nil {
				candidates := make([]string, 0, len(snapshots))
				for _, item := range snapshots {
					candidates = append(candidates, item.ID)
				}
				writeError(
					w,
					stdhttp.StatusNotFound,
					fmt.Errorf("snapshot not found: %s; available snapshot ids: %s", body.SnapshotID, strings.Join(candidates, ", ")),
				)
				return
			}
		}
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusOK, meta)
}

func (r *Router) execSandbox(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}

	var body execRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := r.service.Execute(req.Context(), sandboxID, sandbox.ExecRequest{
		Command: body.Command,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	result = clampExecResultOutput(result)
	if !body.Wait {
		writeJSON(w, stdhttp.StatusAccepted, result)
		return
	}

	waitSeconds := body.WaitTimeoutSeconds
	if waitSeconds <= 0 {
		waitSeconds = 10
	}
	current, timedOut, err := r.waitExecResult(req.Context(), sandboxID, result, waitSeconds)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if timedOut {
		current.Error = fmt.Sprintf(
			"still running after wait timeout (%ds); call get_exec with exec_id=%s to continue polling",
			waitSeconds,
			current.ID,
		)
	}
	current = clampExecResultOutput(current)
	writeJSON(w, stdhttp.StatusOK, current)
}

func (r *Router) runWorkflow(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	var body workflowRunRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" || len(body.Steps) == 0 {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}

	currentSandboxID := ""
	steps := make([]workflowStepResult, 0, len(body.Steps))
	stoppedEarly := false

	for idx, step := range body.Steps {
		started := time.Now()
		action := normalizeWorkflowAction(strings.TrimSpace(step.Action))
		item := workflowStepResult{
			Index:  idx,
			Action: action,
			Status: "succeeded",
		}

		markFailed := func(err error) {
			item.Status = "failed"
			item.Error = err.Error()
			item.DurationMS = time.Since(started).Milliseconds()
			steps = append(steps, item)
			stoppedEarly = true
		}

		switch action {
		case "create_sandbox":
			labels := map[string]string{}
			for key, value := range step.Labels {
				labels[key] = value
			}
			labels["session_id"] = sessionID
			meta, err := r.service.Create(req.Context(), sandbox.CreateRequest{
				Image:         step.Image,
				Labels:        labels,
				MemoryLimitMB: step.MemoryLimitMB,
			})
			if err != nil {
				markFailed(err)
				break
			}
			currentSandboxID = meta.ID
			item.SandboxID = meta.ID
			item.Result = meta

		case "exec_command":
			sandboxID := strings.TrimSpace(step.SandboxID)
			if sandboxID == "" {
				sandboxID = currentSandboxID
			}
			if sandboxID == "" {
				markFailed(errors.New("workflow step requires sandbox_id or a previously created sandbox"))
				break
			}
			if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
				markFailed(err)
				break
			}
			command := strings.TrimSpace(step.Command)
			if command == "" {
				markFailed(errors.New("workflow exec_command step requires non-empty command"))
				break
			}
			execResult, err := r.service.Execute(req.Context(), sandboxID, sandbox.ExecRequest{Command: command})
			if err != nil {
				markFailed(err)
				break
			}
			wait := true
			if step.Wait != nil {
				wait = *step.Wait
			}
			if wait {
				waitSeconds := step.WaitTimeoutSeconds
				if waitSeconds <= 0 {
					waitSeconds = 10
				}
				execResult, timedOut, waitErr := r.waitExecResult(req.Context(), sandboxID, execResult, waitSeconds)
				if waitErr != nil {
					markFailed(waitErr)
					break
				}
				item.TimedOut = timedOut
				if timedOut {
					execResult = clampExecResultOutput(execResult)
					item.Status = "timed_out"
					item.Error = fmt.Sprintf(
						"exec step timed out after %ds; call get_exec with exec_id=%s to continue polling",
						waitSeconds,
						execResult.ID,
					)
					item.Result = execResult
					item.ExecID = execResult.ID
					item.SandboxID = sandboxID
					item.DurationMS = time.Since(started).Milliseconds()
					steps = append(steps, item)
					stoppedEarly = true
					break
				}
			}
			execResult = clampExecResultOutput(execResult)
			item.ExecID = execResult.ID
			item.SandboxID = sandboxID
			item.Result = execResult

		case "create_snapshot":
			sandboxID := strings.TrimSpace(step.SandboxID)
			if sandboxID == "" {
				sandboxID = currentSandboxID
			}
			if sandboxID == "" {
				markFailed(errors.New("workflow create_snapshot step requires sandbox_id or a previously created sandbox"))
				break
			}
			if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
				markFailed(err)
				break
			}
			snapshot, err := r.service.CreateSnapshot(req.Context(), sandboxID)
			if err != nil {
				markFailed(err)
				break
			}
			item.SandboxID = sandboxID
			item.SnapshotID = snapshot.ID
			item.Result = snapshot

		case "rollback_snapshot":
			sandboxID := strings.TrimSpace(step.SandboxID)
			if sandboxID == "" {
				sandboxID = currentSandboxID
			}
			if sandboxID == "" || strings.TrimSpace(step.SnapshotID) == "" {
				markFailed(errors.New("workflow rollback_snapshot step requires sandbox_id and snapshot_id"))
				break
			}
			if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
				markFailed(err)
				break
			}
			meta, err := r.service.Rollback(req.Context(), sandboxID, step.SnapshotID)
			if err != nil {
				markFailed(err)
				break
			}
			item.SandboxID = sandboxID
			item.SnapshotID = step.SnapshotID
			item.Result = meta

		case "delete_sandbox":
			sandboxID := strings.TrimSpace(step.SandboxID)
			if sandboxID == "" {
				sandboxID = currentSandboxID
			}
			if sandboxID == "" {
				markFailed(errors.New("workflow delete_sandbox step requires sandbox_id or a previously created sandbox"))
				break
			}
			if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
				markFailed(err)
				break
			}
			if err := r.service.Delete(req.Context(), sandboxID); err != nil {
				markFailed(err)
				break
			}
			if sandboxID == currentSandboxID {
				currentSandboxID = ""
			}
			item.SandboxID = sandboxID
			item.Result = map[string]any{"ok": true}

		default:
			markFailed(fmt.Errorf("unsupported workflow action: %s", strings.TrimSpace(step.Action)))
		}

		if stoppedEarly {
			break
		}
		item.DurationMS = time.Since(started).Milliseconds()
		steps = append(steps, item)
	}

	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"session_token": sessionID,
		"stopped_early": stoppedEarly,
		"steps":         steps,
	})
}

func normalizeWorkflowAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "create_sandbox", "create", "createsandbox":
		return "create_sandbox"
	case "exec_command", "exec", "execcommand":
		return "exec_command"
	case "create_snapshot", "snapshot", "createsnapshot":
		return "create_snapshot"
	case "rollback_snapshot", "rollback", "rollbacksnapshot":
		return "rollback_snapshot"
	case "delete_sandbox", "delete", "deletesandbox":
		return "delete_sandbox"
	default:
		return strings.TrimSpace(action)
	}
}

func resolveWorkspacePath(allowedRoot, workspacePath string) (string, error) {
	root := strings.TrimSpace(allowedRoot)
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("server allowed root is invalid")
	}

	raw := strings.TrimSpace(workspacePath)
	if raw == "" {
		return "", errors.New("workspace_path is required")
	}

	clean := filepath.Clean(raw)
	if filepath.IsAbs(clean) {
		clean = strings.TrimPrefix(clean, string(filepath.Separator))
	}
	if clean == "." || clean == "" {
		return root, nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace_path cannot escape allowed root")
	}

	resolved := filepath.Join(root, clean)
	if !isPathWithinRoot(resolved, root) {
		return "", errors.New("workspace_path is outside allowed host root")
	}
	return resolved, nil
}

type workspaceEntry struct {
	Path               string `json:"path"`
	EnvReadme          string `json:"env_readme,omitempty"`
	EnvReadmeTruncated bool   `json:"env_readme_truncated,omitempty"`
}

const (
	maxWorkspaceListEntries = 200
	maxEnvReadmeChars       = 1200
)

func listWorkspaceDirectories(root string, includeEnvReadme bool) ([]string, []workspaceEntry, bool, int, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return nil, nil, false, 0, errors.New("server allowed root is invalid")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil, false, 0, fmt.Errorf("mount root not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, false, 0, errors.New("mount root is not a directory")
	}

	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, false, 0, fmt.Errorf("read mount root failed: %w", err)
	}
	items := []string{"/"}
	entries := []workspaceEntry{{Path: "/"}}
	truncated := false
	omittedCount := 0

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if len(entries) >= maxWorkspaceListEntries {
			truncated = true
			omittedCount++
			continue
		}
		rel := "/" + strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(d.Name())), "/")
		items = append(items, rel)
		entry := workspaceEntry{Path: rel}
		if includeEnvReadme {
			readmePath := filepath.Join(root, d.Name(), "env_readme.md")
			readmeBytes, readErr := os.ReadFile(readmePath)
			if readErr == nil {
				readmeText := strings.TrimSpace(string(readmeBytes))
				if readmeText != "" {
					readmeText, readmeTruncated := truncateWithEllipsis(readmeText, maxEnvReadmeChars)
					entry.EnvReadme = readmeText
					entry.EnvReadmeTruncated = readmeTruncated
				}
			}
		}
		entries = append(entries, entry)
	}
	return items, entries, truncated, omittedCount, nil
}

func truncateWithEllipsis(input string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return "", input != ""
	}
	runes := []rune(input)
	if len(runes) <= maxChars {
		return input, false
	}
	return string(runes[:maxChars]) + "...", true
}

func normalizeSandboxCopyPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", errors.New("sandbox_path is required")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("sandbox_path must be an absolute path inside sandbox")
	}
	path = filepath.Clean(path)
	if path == "/" {
		return "", errors.New("sandbox_path cannot be root")
	}
	if strings.HasPrefix(path, "/proc") || strings.HasPrefix(path, "/sys") || strings.HasPrefix(path, "/dev") {
		return "", errors.New("sandbox_path is not writable")
	}
	return filepath.ToSlash(path), nil
}

func (r *Router) readOp(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	var body readOpRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	command := strings.TrimSpace(body.Command)
	if command == "" || !isAllowedReadCommand(command) {
		writeError(w, stdhttp.StatusBadRequest, errors.New("unsupported read command"))
		return
	}

	timeoutSeconds := body.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	maxOutputBytes := body.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = 128 * 1024
	}

	commandLine := buildReadCommandLine(command, body.Args, body.Cwd)
	started := time.Now()
	execResult, err := r.service.Execute(req.Context(), sandboxID, sandbox.ExecRequest{Command: commandLine})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	current, timedOut, err := r.waitExecResult(req.Context(), sandboxID, execResult, timeoutSeconds)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	durationMS := time.Since(started).Milliseconds()
	stdout, stdoutOmitted, stdoutTruncated := applyOutputLimits(current.Stdout, maxOutputBytes, body.MaxLines)
	stderr, stderrOmitted, stderrTruncated := applyOutputLimits(current.Stderr, maxOutputBytes, body.MaxLines)
	truncated := stdoutTruncated || stderrTruncated

	resp := readOpResponse{
		Status:       "ok",
		DurationMS:   durationMS,
		TimedOut:     timedOut,
		Truncated:    truncated,
		ExecID:       current.ID,
		Stdout:       stdout,
		Stderr:       stderr,
		ExitCode:     current.ExitCode,
		OmittedBytes: stdoutOmitted + stderrOmitted,
	}
	if truncated {
		resp.NextOffset = body.OutputOffset + int64(len(stdout)+len(stderr))
	}
	if timedOut {
		resp.Status = "running"
		resp.Error = fmt.Sprintf(
			"read command still running after timeout (%ds); poll get_exec with exec_id=%s",
			timeoutSeconds,
			current.ID,
		)
	} else if current.Status == "failed" || current.Status == "cancelled" {
		resp.Status = "error"
		resp.Error = current.Error
	}
	writeJSON(w, stdhttp.StatusOK, resp)
}

func (r *Router) codeWrite(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	var body codeWriteRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.SessionToken) == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), strings.TrimSpace(body.SessionToken), sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxFilePath, err := normalizeSandboxCodePath(body.SandboxPath)
	if err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sandboxDirPath := filepath.Dir(sandboxFilePath)
	writeMode, err := normalizePatchWriteMode(body.WriteMode)
	if err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	if writeMode == "replace_file" && strings.TrimSpace(body.Content) == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("content is required for write_mode=replace_file"))
		return
	}
	if writeMode == "context_edit" && strings.TrimSpace(body.OldText) == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("old_text is required for write_mode=context_edit"))
		return
	}
	if writeMode == "context_edit" && strings.TrimSpace(body.NewText) == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("new_text is required for write_mode=context_edit"))
		return
	}
	if err := r.service.EnsureRunning(req.Context(), sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	timeoutSeconds := body.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 20
	}
	maxOutputBytes := body.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = 128 * 1024
	}
	maxLines := body.MaxLines
	if maxLines <= 0 {
		maxLines = 500
	}

	started := time.Now()
	runCtx, cancel := context.WithTimeout(req.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	workDir, err := os.MkdirTemp("", "sleigh-sbx-code-write-*")
	if err != nil {
		writeError(w, stdhttp.StatusInternalServerError, fmt.Errorf("create temp workspace: %w", err))
		return
	}
	defer os.RemoveAll(workDir)

	cwd := filepath.Join(workDir, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		writeError(w, stdhttp.StatusInternalServerError, fmt.Errorf("prepare temp workspace: %w", err))
		return
	}

	ensureOut, ensureErr := ensureSandboxDirectory(runCtx, sandboxID, sandboxDirPath)
	if ensureErr != nil {
		stdout, omittedOut, truncOut := applyOutputLimits(ensureOut.stdout, maxOutputBytes, maxLines)
		stderr, omittedErr, truncErr := applyOutputLimits(ensureOut.stderr, maxOutputBytes, maxLines)
		writeJSON(w, stdhttp.StatusOK, codeWriteResponse{
			Status:       "error",
			DurationMS:   time.Since(started).Milliseconds(),
			TimedOut:     errors.Is(runCtx.Err(), context.DeadlineExceeded),
			Truncated:    truncOut || truncErr,
			Stdout:       stdout,
			Stderr:       stderr,
			Error:        "ensure sandbox path failed",
			BuildStatus:  "not_run",
			OmittedBytes: omittedOut + omittedErr,
		})
		return
	}
	copyOut, copyErr := copySandboxDirectoryToHost(runCtx, sandboxID, sandboxDirPath, cwd)
	if copyErr != nil {
		stdout, omittedOut, truncOut := applyOutputLimits(copyOut.stdout, maxOutputBytes, maxLines)
		stderr, omittedErr, truncErr := applyOutputLimits(copyOut.stderr, maxOutputBytes, maxLines)
		writeJSON(w, stdhttp.StatusOK, codeWriteResponse{
			Status:       "error",
			DurationMS:   time.Since(started).Milliseconds(),
			TimedOut:     errors.Is(runCtx.Err(), context.DeadlineExceeded),
			Truncated:    truncOut || truncErr,
			Stdout:       stdout,
			Stderr:       stderr,
			Error:        "copy sandbox directory to host failed",
			BuildStatus:  "not_run",
			OmittedBytes: omittedOut + omittedErr,
		})
		return
	}

	editOut := commandOutput{}
	editErr := error(nil)
	appliedFiles := []string{}
	if writeMode == "context_edit" {
		var targetFile string
		targetFile, editOut, editErr = applyContextEditInWorkspace(
			cwd,
			sandboxFilePath,
			body.BeforeContext,
			body.OldText,
			body.NewText,
			body.AfterContext,
			body.Occurrence,
		)
		if editErr != nil {
			stdout, omittedOut, truncOut := applyOutputLimits(editOut.stdout, maxOutputBytes, maxLines)
			stderr, omittedErr, truncErr := applyOutputLimits(editOut.stderr, maxOutputBytes, maxLines)
			writeJSON(w, stdhttp.StatusOK, codeWriteResponse{
				Status:       "error",
				DurationMS:   time.Since(started).Milliseconds(),
				TimedOut:     errors.Is(runCtx.Err(), context.DeadlineExceeded),
				Truncated:    truncOut || truncErr,
				Stdout:       stdout,
				Stderr:       stderr,
				Error:        editErr.Error(),
				BuildStatus:  "not_run",
				OmittedBytes: omittedOut + omittedErr,
			})
			return
		}
		appliedFiles = []string{targetFile}
	} else {
		var targetFile string
		targetFile, err = rewriteFileInWorkspace(cwd, sandboxFilePath, body.Content)
		if err != nil {
			writeJSON(w, stdhttp.StatusOK, codeWriteResponse{
				Status:      "error",
				DurationMS:  time.Since(started).Milliseconds(),
				TimedOut:    errors.Is(runCtx.Err(), context.DeadlineExceeded),
				Error:       err.Error(),
				BuildStatus: "not_run",
			})
			return
		}
		appliedFiles = []string{targetFile}
	}
	stdout, omittedOut, truncOut := applyOutputLimits(editOut.stdout, maxOutputBytes, maxLines)
	stderr, omittedErr, truncErr := applyOutputLimits(editOut.stderr, maxOutputBytes, maxLines)
	resp := codeWriteResponse{
		Status:       "ok",
		DurationMS:   time.Since(started).Milliseconds(),
		TimedOut:     errors.Is(runCtx.Err(), context.DeadlineExceeded),
		Truncated:    truncOut || truncErr,
		Stdout:       stdout,
		Stderr:       stderr,
		AppliedFiles: appliedFiles,
		FormatIssues: []string{},
		LintIssues:   []string{},
		BuildStatus:  "not_run",
		OmittedBytes: omittedOut + omittedErr,
	}
	if editErr != nil {
		resp.Status = "error"
		resp.Error = "context edit failed"
		writeJSON(w, stdhttp.StatusOK, resp)
		return
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		resp.Status = "error"
		resp.Error = "code_write operation timed out"
		writeJSON(w, stdhttp.StatusOK, resp)
		return
	}
	syncOut, syncErr := copyHostDirectoryToSandbox(runCtx, sandboxID, sandboxDirPath, cwd)
	if syncErr != nil {
		resp.Status = "error"
		resp.Error = "copy code changes back to sandbox failed"
		combinedStdout := editOut.stdout + syncOut.stdout
		combinedStderr := editOut.stderr + syncOut.stderr
		stdout, omittedOut, truncOut := applyOutputLimits(combinedStdout, maxOutputBytes, maxLines)
		stderr, omittedErr, truncErr := applyOutputLimits(combinedStderr, maxOutputBytes, maxLines)
		resp.Stdout = stdout
		resp.Stderr = stderr
		resp.Truncated = truncOut || truncErr
		resp.OmittedBytes = omittedOut + omittedErr
		writeJSON(w, stdhttp.StatusOK, resp)
		return
	}

	qualityOutput := commandOutput{}
	qualityErr := error(nil)
	if hasPreCommitConfig(cwd) {
		qualityOutput, qualityErr = runPreCommit(runCtx, cwd, appliedFiles)
		if qualityErr != nil {
			resp.FormatIssues, resp.LintIssues = classifyPreCommitIssues(qualityOutput.stderr, qualityOutput.stdout, 30)
			resp.Status = "error"
			resp.Error = "pre-commit checks failed; fix issues and retry"
			resp.BuildStatus = "not_run"
		}
	} else if detectedLanguage := detectWorkspaceLanguage(cwd, appliedFiles); detectedLanguage != "" {
		qualityOutput, qualityErr = runLanguageQualityChecks(runCtx, cwd, detectedLanguage)
		if qualityErr != nil {
			resp.FormatIssues = []string{}
			resp.LintIssues = []string{}
			resp.Status = "error"
			resp.Error = formatLanguageCheckError(detectedLanguage, qualityErr, runCtx.Err())
			resp.BuildStatus = "not_run"
		}
	}

	buildOutput := commandOutput{}
	buildLang := strings.TrimSpace(body.BuildLanguage)
	if resp.Status == "ok" {
		if buildLang == "" {
			resp.BuildStatus = "not_run"
		} else {
			var buildErr error
			buildOutput, buildErr = runContainerBuild(runCtx, cwd, buildLang)
			if buildErr != nil {
				resp.Status = "error"
				resp.Error = formatBuildError(buildLang, buildErr, runCtx.Err())
				resp.BuildStatus = "failed"
			} else {
				resp.BuildStatus = "passed"
			}
		}
	}

	combinedStdout := ensureOut.stdout + copyOut.stdout + editOut.stdout + syncOut.stdout + qualityOutput.stdout + buildOutput.stdout
	combinedStderr := ensureOut.stderr + copyOut.stderr + editOut.stderr + syncOut.stderr + qualityOutput.stderr + buildOutput.stderr
	stdout, omittedOut, truncOut = applyOutputLimits(combinedStdout, maxOutputBytes, maxLines)
	stderr, omittedErr, truncErr = applyOutputLimits(combinedStderr, maxOutputBytes, maxLines)
	resp.Stdout = stdout
	resp.Stderr = stderr
	resp.Truncated = truncOut || truncErr
	resp.OmittedBytes = omittedOut + omittedErr
	writeJSON(w, stdhttp.StatusOK, resp)
}

func (r *Router) getExecResult(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	execID := strings.TrimSpace(req.PathValue("execId"))
	if sandboxID == "" || execID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id and exec id are required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := r.service.GetExec(req.Context(), sandboxID, execID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	result = clampExecResultOutput(result)
	writeJSON(w, stdhttp.StatusOK, result)
}

func (r *Router) cancelExec(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	execID := strings.TrimSpace(req.PathValue("execId"))
	if sandboxID == "" || execID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id and exec id are required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := r.service.CancelExec(req.Context(), sandboxID, execID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	result = clampExecResultOutput(result)
	writeJSON(w, stdhttp.StatusOK, result)
}

func (r *Router) getMemoryPressure(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	pressure, err := r.service.GetMemoryPressure(req.Context(), sandboxID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, stdhttp.StatusOK, pressure)
}

func (r *Router) expandMemory(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}

	var body expandMemoryRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	autoExpandEnabled := body.AutoExpand
	if !autoExpandEnabled && body.TargetMB <= 0 {
		if meta, getErr := r.service.Get(req.Context(), sandboxID); getErr == nil {
			autoExpandEnabled = strings.EqualFold(strings.TrimSpace(meta.Labels["auto_expand_memory"]), "true")
		}
	}
	if body.TargetMB <= 0 && !autoExpandEnabled {
		writeError(w, stdhttp.StatusBadRequest, errors.New("target_mb must be greater than 0"))
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	allowExpand, lowMemoryWarning := r.allowAutoExpand(req.Context(), "memory expand")
	if !allowExpand {
		writeError(w, stdhttp.StatusServiceUnavailable, errors.New(lowMemoryWarning))
		return
	}

	var (
		result sandbox.AutoExpandResult
		err    error
	)
	if autoExpandEnabled {
		result, err = r.service.AutoExpandMemory(req.Context(), sandboxID)
	} else {
		result, err = r.service.ExpandMemory(req.Context(), sandboxID, body.TargetMB)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if lowMemoryWarning != "" {
		if strings.TrimSpace(result.Reason) == "" {
			result.Reason = lowMemoryWarning
		} else {
			result.Reason = result.Reason + "; " + lowMemoryWarning
		}
	}

	writeJSON(w, stdhttp.StatusOK, result)
}

func (r *Router) listSessionExecTasks(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionIDToken, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sessionID := strings.TrimSpace(req.PathValue("sessionId"))
	if sessionID == "" {
		sessionID = sessionIDToken
	}
	if sessionIDToken != sessionID {
		writeDomainError(w, appErr.ErrForbidden)
		return
	}

	limit := 20
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	cursor := strings.TrimSpace(req.URL.Query().Get("cursor"))

	page, err := r.service.ListExecHistory(req.Context(), sessionID, limit, cursor)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	for i := range page.Items {
		page.Items[i] = clampExecResultOutput(page.Items[i])
	}
	writeJSON(w, stdhttp.StatusOK, page)
}

func (r *Router) cleanupExecTasks(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	result, err := r.service.CleanupExecHistory(req.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, result)
}

func (r *Router) listMounts(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	items, err := r.service.ListMounts(req.Context(), sandboxID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"items": items})
}

func (r *Router) mountPath(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}

	var body mountRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	hostPath, err := resolveWorkspacePath(r.config.MountAllowedRoot, body.WorkspacePath)
	if err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	containerPath := strings.TrimSpace(body.ContainerPath)
	if containerPath == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("container_path is required"))
		return
	}
	if !filepath.IsAbs(containerPath) {
		writeError(w, stdhttp.StatusBadRequest, errors.New("container_path must be an absolute path"))
		return
	}
	result, err := r.service.MountPath(req.Context(), sandboxID, sandbox.MountRequest{
		HostPath:      hostPath,
		ContainerPath: containerPath,
		Mode:          "ro",
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusCreated, result)
}

func (r *Router) listMountWorkspaces(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	if _, err := sessionTokenFromRequest(req); err != nil {
		writeDomainError(w, err)
		return
	}
	root := strings.TrimSpace(r.config.MountAllowedRoot)
	items, entries, truncated, omittedCount, err := listWorkspaceDirectories(root, false)
	if err != nil {
		writeError(w, stdhttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"allowed_root":  root,
		"items":         items,
		"entries":       entries,
		"truncated":     truncated,
		"omitted_count": omittedCount,
		"suggested_next_actions": []map[string]any{
			{
				"action": "mount_path",
				"required_fields": []string{
					"session_token",
					"sandbox_id",
					"workspace_path",
					"container_path",
				},
			},
		},
	})
}

func (r *Router) listEnvironmentWorkspaces(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	if _, err := sessionTokenFromRequest(req); err != nil {
		writeDomainError(w, err)
		return
	}
	root := strings.TrimSpace(r.config.EnvironmentAllowedRoot)
	items, entries, truncated, omittedCount, err := listWorkspaceDirectories(root, true)
	if err != nil {
		writeError(w, stdhttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"allowed_root":  root,
		"items":         items,
		"entries":       entries,
		"truncated":     truncated,
		"omitted_count": omittedCount,
		"suggested_next_actions": []map[string]any{
			{
				"action": "copy_environment",
				"required_fields": []string{
					"session_token",
					"sandbox_id",
					"environment_path",
					"sandbox_path",
				},
			},
		},
	})
}

func (r *Router) copyEnvironment(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	if sandboxID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id is required"))
		return
	}
	var body copyEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	sessionID := strings.TrimSpace(body.SessionToken)
	if sessionID == "" {
		writeDomainError(w, appErr.ErrBadRequest)
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	environmentPath := strings.TrimSpace(body.EnvironmentPath)
	if environmentPath == "" {
		environmentPath = strings.TrimSpace(body.WorkspacePath)
	}
	if environmentPath == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("environment_path is required"))
		return
	}
	hostPath, err := resolveWorkspacePath(r.config.EnvironmentAllowedRoot, environmentPath)
	if err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	hostInfo, hostErr := os.Stat(hostPath)
	if hostErr != nil {
		writeError(w, stdhttp.StatusBadRequest, fmt.Errorf("workspace_path not found: %w", hostErr))
		return
	}
	if !hostInfo.IsDir() {
		writeError(w, stdhttp.StatusBadRequest, errors.New("workspace_path must be a directory"))
		return
	}
	sandboxPath, err := normalizeSandboxCopyPath(body.SandboxPath)
	if err != nil {
		writeError(w, stdhttp.StatusBadRequest, err)
		return
	}
	if err := r.service.EnsureRunning(req.Context(), sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	if _, ensureErr := ensureSandboxDirectory(req.Context(), sandboxID, sandboxPath); ensureErr != nil {
		writeError(w, stdhttp.StatusInternalServerError, fmt.Errorf("ensure sandbox path failed: %w", ensureErr))
		return
	}
	out, copyErr := copyHostDirectoryToSandbox(req.Context(), sandboxID, sandboxPath, hostPath)
	if copyErr != nil {
		writeError(w, stdhttp.StatusInternalServerError, fmt.Errorf("copy environment directory failed: %s%s", out.stderr, copyErr))
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{
		"status":           "ok",
		"sandbox_id":       sandboxID,
		"environment_path": environmentPath,
		"sandbox_path":     sandboxPath,
	})
}

func (r *Router) unmountPath(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	sessionID, err := sessionTokenFromRequest(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sandboxID := strings.TrimSpace(req.PathValue("id"))
	mountID := strings.TrimSpace(req.PathValue("mountId"))
	if sandboxID == "" || mountID == "" {
		writeError(w, stdhttp.StatusBadRequest, errors.New("sandbox id and mount id are required"))
		return
	}
	if err := r.service.AuthorizeSandboxAccess(req.Context(), sessionID, sandboxID); err != nil {
		writeDomainError(w, err)
		return
	}

	if err := r.service.UnmountPath(req.Context(), sandboxID, mountID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}

func (r *Router) getWarmPool(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	stats, err := r.service.WarmPoolStatus(req.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, stats)
}

func (r *Router) refillWarmPool(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	stats, err := r.service.RefillWarmPool(req.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, stats)
}

func decodeJSON(req *stdhttp.Request, dst any) error {
	if req.Body == nil {
		return errors.New("empty request body")
	}
	defer req.Body.Close()

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return errors.New("empty request body")
	}

	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func writeJSON(w stdhttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w stdhttp.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}

func writeDomainError(w stdhttp.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appErr.ErrNotFound):
		writeError(w, stdhttp.StatusNotFound, err)
	case errors.Is(err, appErr.ErrBadRequest):
		writeError(w, stdhttp.StatusBadRequest, err)
	case errors.Is(err, appErr.ErrForbidden):
		writeError(w, stdhttp.StatusForbidden, err)
	default:
		writeError(w, stdhttp.StatusInternalServerError, err)
	}
}

func isExecTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func (r *Router) waitExecResult(
	ctx context.Context,
	sandboxID string,
	initial sandbox.ExecResult,
	waitSeconds int,
) (sandbox.ExecResult, bool, error) {
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	current := initial
	for time.Now().Before(deadline) {
		if isExecTerminalStatus(current.Status) {
			return current, false, nil
		}
		time.Sleep(250 * time.Millisecond)
		next, err := r.service.GetExec(ctx, sandboxID, current.ID)
		if err != nil {
			return sandbox.ExecResult{}, false, err
		}
		current = next
	}
	if isExecTerminalStatus(current.Status) {
		return current, false, nil
	}
	return current, true, nil
}

func (r *Router) allowAutoExpand(ctx context.Context, operation string) (bool, string) {
	if r.monitor == nil {
		return true, ""
	}
	report, monitorErr := r.monitor.GetResources(ctx)
	if monitorErr != nil {
		return true, ""
	}
	ratio := report.Memory.AvailableRatio
	if ratio < lowMemoryBlockThreshold {
		return false, fmt.Sprintf(
			"host memory available ratio %.2f%% is below %.0f%%; %s is blocked",
			ratio*100,
			lowMemoryBlockThreshold*100,
			operation,
		)
	}
	if ratio < lowMemoryWarningThreshold {
		return true, fmt.Sprintf(
			"host memory available ratio %.2f%% is below %.0f%%; operation succeeded but host is under pressure",
			ratio*100,
			lowMemoryWarningThreshold*100,
		)
	}
	return true, ""
}

func truncateLines(content string, maxLines int) (string, bool) {
	if maxLines <= 0 || content == "" {
		return content, false
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content, false
	}
	return strings.Join(lines[:maxLines], "\n"), true
}

const maxExecOutputTailLines = 100

func truncateTailLinesWithNotice(content string, maxLines int, streamLabel string) (string, bool) {
	if maxLines <= 0 || content == "" {
		return content, false
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content, false
	}
	omitted := len(lines) - maxLines
	tail := strings.Join(lines[omitted:], "\n")
	prefix := fmt.Sprintf("...(truncated %s: omitted %d lines, showing last %d lines)\n", streamLabel, omitted, maxLines)
	return prefix + tail, true
}

func clampExecResultOutput(result sandbox.ExecResult) sandbox.ExecResult {
	stdout, stdoutCut := truncateTailLinesWithNotice(result.Stdout, maxExecOutputTailLines, "stdout")
	stderr, stderrCut := truncateTailLinesWithNotice(result.Stderr, maxExecOutputTailLines, "stderr")
	if stdoutCut || stderrCut {
		result.Stdout = stdout
		result.Stderr = stderr
	}
	return result
}

func applyOutputLimits(content string, maxBytes int, maxLines int) (string, int64, bool) {
	if content == "" {
		return "", 0, false
	}
	omitted := int64(0)
	truncated := false
	if maxBytes > 0 && len(content) > maxBytes {
		omitted += int64(len(content) - maxBytes)
		content = content[:maxBytes]
		truncated = true
	}
	if maxLines > 0 {
		limited, cut := truncateLines(content, maxLines)
		if cut && len(limited) < len(content) {
			omitted += int64(len(content) - len(limited))
			truncated = true
		}
		content = limited
	}
	return content, omitted, truncated
}

func buildReadCommandLine(command string, args []string, cwd string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, shQuote(command))
	for _, arg := range args {
		parts = append(parts, shQuote(arg))
	}
	cmdLine := strings.Join(parts, " ")
	if strings.TrimSpace(cwd) != "" {
		return "cd " + shQuote(strings.TrimSpace(cwd)) + " && " + cmdLine
	}
	return cmdLine
}

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isAllowedReadCommand(cmd string) bool {
	switch strings.TrimSpace(cmd) {
	case "cat", "sed", "head", "tail", "ls", "tree", "grep", "rg", "find", "fd", "wc", "stat":
		return true
	default:
		return false
	}
}

type commandOutput struct {
	stdout string
	stderr string
}

func runCommand(ctx context.Context, cwd string, command string, args ...string) (commandOutput, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	return commandOutput{
		stdout: stdoutBuf.String(),
		stderr: stderrBuf.String(),
	}, err
}

func runGitApplyCommand(ctx context.Context, cwd string, args ...string) (commandOutput, error) {
	return runCommand(ctx, "", "git", append([]string{"-C", cwd}, args...)...)
}

func normalizeSandboxCodePath(raw string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(raw))
	if path == "." || path == "" {
		return "", errors.New("sandbox_path is required")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("sandbox_path must be an absolute file path inside sandbox")
	}
	if path == "/" {
		return "", errors.New("sandbox_path cannot be root")
	}
	if strings.HasPrefix(path, "/proc") || strings.HasPrefix(path, "/sys") || strings.HasPrefix(path, "/dev") {
		return "", errors.New("sandbox_path is not writable")
	}
	return path, nil
}

func normalizePatchWriteMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "context_edit":
		return "context_edit", nil
	case "replace_file", "rewrite", "overwrite":
		return "replace_file", nil
	default:
		return "", errors.New("write_mode must be one of: context_edit, replace_file")
	}
}

func rewriteFileInWorkspace(cwd, sandboxFilePath, content string) (string, error) {
	relativeTarget, err := resolveTargetRelativePath(sandboxFilePath)
	if err != nil {
		return "", fmt.Errorf("invalid sandbox_path for write_mode=replace_file: %w", err)
	}
	localTarget := filepath.Join(cwd, relativeTarget)
	if err := os.MkdirAll(filepath.Dir(localTarget), 0o755); err != nil {
		return "", fmt.Errorf("create target directory failed: %w", err)
	}
	if err := os.WriteFile(localTarget, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write target file failed: %w", err)
	}
	return relativeTarget, nil
}

func applyContextEditInWorkspace(
	cwd, sandboxFilePath, beforeContext, oldText, newText, afterContext string, occurrence int,
) (string, commandOutput, error) {
	relativeTarget, err := resolveTargetRelativePath(sandboxFilePath)
	if err != nil {
		return "", commandOutput{}, err
	}
	localTarget := filepath.Join(cwd, relativeTarget)
	contentBytes, err := os.ReadFile(localTarget)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", commandOutput{}, errors.New("context_edit no_match: sandbox_path not found")
		}
		return "", commandOutput{}, fmt.Errorf("read target file failed: %w", err)
	}
	current := string(contentBytes)
	searchBlock := beforeContext + oldText + afterContext
	replaceBlock := beforeContext + newText + afterContext
	if strings.TrimSpace(beforeContext) == "" && strings.TrimSpace(afterContext) == "" {
		searchBlock = oldText
		replaceBlock = newText
	}
	matchIndexes := findAllIndexes(current, searchBlock)
	if len(matchIndexes) == 0 {
		return "", commandOutput{}, errors.New("context_edit no_match: snippet not found; read latest file and provide tighter before_context/after_context")
	}
	if occurrence <= 0 {
		occurrence = 1
	}
	if len(matchIndexes) > 1 && occurrence == 1 {
		return "", commandOutput{}, errors.New("context_edit ambiguous_match: snippet matched multiple locations; provide more context or set occurrence")
	}
	if occurrence > len(matchIndexes) {
		return "", commandOutput{}, fmt.Errorf("context_edit ambiguous_match: occurrence=%d exceeds match count=%d", occurrence, len(matchIndexes))
	}
	start := matchIndexes[occurrence-1]
	updated := current[:start] + replaceBlock + current[start+len(searchBlock):]
	if err := os.WriteFile(localTarget, []byte(updated), 0o644); err != nil {
		return "", commandOutput{}, fmt.Errorf("write context edit result failed: %w", err)
	}
	return relativeTarget, commandOutput{
		stdout: fmt.Sprintf("context edit applied: file=%s occurrence=%d matches=%d\n", relativeTarget, occurrence, len(matchIndexes)),
	}, nil
}

func resolveTargetRelativePath(sandboxFilePath string) (string, error) {
	cleanTarget := filepath.Clean(strings.TrimSpace(sandboxFilePath))
	if cleanTarget == "." || cleanTarget == "" {
		return "", errors.New("sandbox_path is required")
	}
	relativeTarget := filepath.Base(cleanTarget)
	if relativeTarget == "." || relativeTarget == "" {
		return "", errors.New("sandbox_path must point to a file")
	}
	if relativeTarget == "/" {
		return "", errors.New("sandbox_path must point to a file")
	}
	return relativeTarget, nil
}

func findAllIndexes(content, needle string) []int {
	if needle == "" {
		return nil
	}
	indexes := []int{}
	offset := 0
	for {
		idx := strings.Index(content[offset:], needle)
		if idx < 0 {
			return indexes
		}
		real := offset + idx
		indexes = append(indexes, real)
		offset = real + 1
	}
}

func sandboxContainerName(sandboxID string) string {
	return "hwr-sbx-" + strings.TrimSpace(sandboxID)
}

func ensureSandboxDirectory(ctx context.Context, sandboxID, sandboxPath string) (commandOutput, error) {
	container := sandboxContainerName(sandboxID)
	return runCommand(
		ctx,
		"",
		"docker",
		"exec",
		container,
		"sh",
		"-lc",
		"mkdir -p "+shQuote(sandboxPath),
	)
}

func copySandboxDirectoryToHost(ctx context.Context, sandboxID, sandboxPath, hostDir string) (commandOutput, error) {
	container := sandboxContainerName(sandboxID)
	return runCommand(
		ctx,
		"",
		"docker",
		"cp",
		fmt.Sprintf("%s:%s/.", container, sandboxPath),
		hostDir,
	)
}

func copyHostDirectoryToSandbox(ctx context.Context, sandboxID, sandboxPath, hostDir string) (commandOutput, error) {
	container := sandboxContainerName(sandboxID)
	return runCommand(
		ctx,
		"",
		"docker",
		"cp",
		hostDir+"/.",
		fmt.Sprintf("%s:%s", container, sandboxPath),
	)
}

func hasPreCommitConfig(cwd string) bool {
	info, err := os.Stat(filepath.Join(cwd, ".pre-commit-config.yaml"))
	return err == nil && !info.IsDir()
}

func detectWorkspaceLanguage(cwd string, files []string) string {
	byExt := make(map[string]int)
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(strings.TrimSpace(file)))
		if ext != "" {
			byExt[ext]++
		}
	}
	score := map[string]int{
		"python": byExt[".py"],
		"go":     byExt[".go"],
		"node":   byExt[".js"] + byExt[".jsx"] + byExt[".ts"] + byExt[".tsx"],
		"rust":   byExt[".rs"],
		"java":   byExt[".java"],
	}
	if fileExists(filepath.Join(cwd, "pyproject.toml")) || fileExists(filepath.Join(cwd, "requirements.txt")) {
		score["python"] += 3
	}
	if fileExists(filepath.Join(cwd, "go.mod")) {
		score["go"] += 3
	}
	if fileExists(filepath.Join(cwd, "package.json")) {
		score["node"] += 3
	}
	if fileExists(filepath.Join(cwd, "Cargo.toml")) {
		score["rust"] += 3
	}
	if fileExists(filepath.Join(cwd, "pom.xml")) || fileExists(filepath.Join(cwd, "build.gradle")) || fileExists(filepath.Join(cwd, "build.gradle.kts")) {
		score["java"] += 3
	}

	bestLang := ""
	bestScore := 0
	for lang, val := range score {
		if val > bestScore {
			bestScore = val
			bestLang = lang
		}
	}
	if bestScore == 0 {
		return ""
	}
	return bestLang
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

type qualityProfile struct {
	image   string
	command string
}

func resolveQualityProfile(language string) (qualityProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "python", "py":
		return qualityProfile{
			image:   "python:3.12",
			command: "python -m compileall -q .",
		}, true
	case "go", "golang":
		return qualityProfile{
			image: "golang:1.26",
			command: "gofmt_out=$(gofmt -l .); " +
				"if [ -n \"$gofmt_out\" ]; then echo \"$gofmt_out\"; exit 1; fi",
		}, true
	case "node", "javascript", "js", "typescript", "ts":
		return qualityProfile{
			image: "node:20",
			command: "if [ -f package.json ]; then " +
				"npm -s run lint --if-present && npm -s run format:check --if-present; " +
				"else echo 'package.json not found, skip node quality checks'; fi",
		}, true
	case "rust":
		return qualityProfile{
			image:   "rust:1.80",
			command: "if [ -f Cargo.toml ]; then cargo fmt --check; else echo 'Cargo.toml not found, skip rust quality checks'; fi",
		}, true
	case "java":
		return qualityProfile{
			image: "maven:3.9-eclipse-temurin-17",
			command: "if [ -f pom.xml ]; then mvn -q -DskipTests verify; " +
				"else echo 'pom.xml not found, skip java quality checks'; fi",
		}, true
	default:
		return qualityProfile{}, false
	}
}

func runLanguageQualityChecks(ctx context.Context, cwd string, language string) (commandOutput, error) {
	profile, ok := resolveQualityProfile(language)
	if !ok {
		return commandOutput{}, nil
	}
	pullOut, pullErr := runCommand(ctx, "", "docker", "pull", profile.image)
	if pullErr != nil {
		return pullOut, fmt.Errorf("docker pull failed for quality checks image %q: %w", profile.image, pullErr)
	}
	runOut, runErr := runCommand(
		ctx,
		"",
		"docker",
		"run",
		"--rm",
		"-v",
		cwd+":/workspace",
		"-w",
		"/workspace",
		profile.image,
		"sh",
		"-lc",
		profile.command,
	)
	return combineCommandOutputs(pullOut, runOut), runErr
}

func runPreCommit(ctx context.Context, cwd string, files []string) (commandOutput, error) {
	args := []string{"run"}
	if len(files) > 0 {
		args = append(args, "--files")
		args = append(args, files...)
	}
	preCommitBin := strings.TrimSpace(os.Getenv("PRE_COMMIT_BIN"))
	if preCommitBin == "" {
		preCommitBin = "pre-commit"
	}
	return runCommand(ctx, cwd, preCommitBin, args...)
}

func classifyPreCommitIssues(stderr string, stdout string, maxItems int) ([]string, []string) {
	joined := strings.TrimSpace(stderr + "\n" + stdout)
	if joined == "" {
		return []string{}, []string{}
	}
	lines := strings.Split(joined, "\n")
	formatIssues := make([]string, 0)
	lintIssues := make([]string, 0)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "format") ||
			strings.Contains(lower, "reformat") ||
			strings.Contains(lower, "black") ||
			strings.Contains(lower, "prettier") ||
			strings.Contains(lower, "gofmt") {
			formatIssues = append(formatIssues, line)
		}
		if strings.Contains(lower, "fail") ||
			strings.Contains(lower, "error") ||
			strings.Contains(lower, "lint") ||
			strings.Contains(lower, "ruff") ||
			strings.Contains(lower, "eslint") {
			lintIssues = append(lintIssues, line)
		}
	}
	if len(formatIssues) == 0 {
		formatIssues = summarizeLines(lines, maxItems)
	}
	if len(lintIssues) == 0 {
		lintIssues = summarizeLines(lines, maxItems)
	}
	return summarizeLines(formatIssues, maxItems), summarizeLines(lintIssues, maxItems)
}

type buildProfile struct {
	image   string
	command string
}

func resolveBuildProfile(language string) (buildProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go", "golang":
		return buildProfile{
			image:   "golang:1.26",
			command: "go build ./...",
		}, true
	case "python", "py":
		return buildProfile{
			image:   "python:3.12",
			command: "python -m compileall -q .",
		}, true
	case "node", "javascript", "js", "typescript", "ts":
		return buildProfile{
			image: "node:20",
			command: "if [ -f package-lock.json ]; then npm ci --ignore-scripts && npm run -s build; " +
				"elif [ -f pnpm-lock.yaml ]; then corepack enable && pnpm install --frozen-lockfile && pnpm -s build; " +
				"elif [ -f yarn.lock ]; then corepack enable && yarn install --frozen-lockfile && yarn -s build; " +
				"elif [ -f package.json ]; then npm install --ignore-scripts && npm run -s build; " +
				"else echo 'package.json not found' >&2; exit 2; fi",
		}, true
	case "rust":
		return buildProfile{
			image:   "rust:1.80",
			command: "cargo build --locked || cargo build",
		}, true
	case "java":
		return buildProfile{
			image:   "maven:3.9-eclipse-temurin-17",
			command: "mvn -q -DskipTests package",
		}, true
	default:
		return buildProfile{}, false
	}
}

func runContainerBuild(ctx context.Context, cwd string, language string) (commandOutput, error) {
	profile, ok := resolveBuildProfile(language)
	if !ok {
		return commandOutput{}, fmt.Errorf("unsupported build language: %s", language)
	}
	pullOut, pullErr := runCommand(ctx, "", "docker", "pull", profile.image)
	if pullErr != nil {
		return pullOut, fmt.Errorf("docker pull failed for build image %q: %w", profile.image, pullErr)
	}
	buildOut, buildErr := runCommand(
		ctx,
		"",
		"docker",
		"run",
		"--rm",
		"-v",
		cwd+":/workspace",
		"-w",
		"/workspace",
		profile.image,
		"sh",
		"-lc",
		profile.command,
	)
	return combineCommandOutputs(pullOut, buildOut), buildErr
}

func combineCommandOutputs(parts ...commandOutput) commandOutput {
	var out commandOutput
	for _, part := range parts {
		out.stdout += part.stdout
		out.stderr += part.stderr
	}
	return out
}

func formatLanguageCheckError(language string, err error, ctxErr error) string {
	if isNetworkUnreachableError(err) {
		return fmt.Sprintf("language quality checks failed for %q: failed to download required Docker image or dependencies due to network issues. Check server Docker registry/mirror or proxy settings.", language)
	}
	if isTimeoutLikeError(err, ctxErr) {
		return fmt.Sprintf("language quality checks timed out for %q: initial image/dependency download may take longer. Please retry later and check server logs.", language)
	}
	return fmt.Sprintf("language quality checks failed for %q", language)
}

func formatBuildError(language string, err error, ctxErr error) string {
	if isNetworkUnreachableError(err) {
		return fmt.Sprintf("build failed for language %q: failed to download build image or dependencies due to network issues. Check server Docker registry/mirror or proxy settings.", language)
	}
	if isTimeoutLikeError(err, ctxErr) {
		return fmt.Sprintf("build timed out for language %q: initial image/dependency download may take longer. Please retry later and check server logs.", language)
	}
	return fmt.Sprintf("build failed for language %q", language)
}

func isTimeoutLikeError(err error, ctxErr error) bool {
	if errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "timed out")
}

func isNetworkUnreachableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "proxyconnect") ||
		strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "dial tcp")
}

func summarizeLines(lines []string, maxItems int) []string {
	if maxItems <= 0 || len(lines) <= maxItems {
		return lines
	}
	return lines[:maxItems]
}

func parseApplyNumstatFiles(stdout string) []string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	files := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		file := parts[len(parts)-1]
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}
	return files
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

func isPathWithinAnyMount(path string, mounts []sandbox.MountSpec) bool {
	for _, mount := range mounts {
		if isPathWithinRoot(path, mount.HostPath) {
			return true
		}
	}
	return false
}

func sessionTokenFromRequest(req *stdhttp.Request) (string, error) {
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return "", appErr.ErrBadRequest
		}
		req.Body = io.NopCloser(bytes.NewBuffer(raw))
		if len(strings.TrimSpace(string(raw))) > 0 {
			var body struct {
				SessionToken string `json:"session_token"`
			}
			if err := json.Unmarshal(raw, &body); err == nil {
				token := strings.TrimSpace(body.SessionToken)
				if token != "" {
					return token, nil
				}
			}
		}
	}
	token := strings.TrimSpace(req.URL.Query().Get("session_token"))
	if token != "" {
		return token, nil
	}
	return "", appErr.ErrBadRequest
}
