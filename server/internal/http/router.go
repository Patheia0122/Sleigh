package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"agent-heavyworks-runtime/server/internal/config"
	appErr "agent-heavyworks-runtime/server/internal/errors"
	"agent-heavyworks-runtime/server/internal/monitor"
	"agent-heavyworks-runtime/server/internal/sandbox"
)

type Router struct {
	config  config.Config
	service *sandbox.Service
	monitor *monitor.Service
}

type healthResponse struct {
	Status      string `json:"status"`
	Time        string `json:"time"`
	Version     string `json:"version"`
	SandboxKind string `json:"sandbox_kind"`
}

type createSandboxRequest struct {
	SessionToken  string            `json:"session_token"`
	Image         string            `json:"image"`
	Labels        map[string]string `json:"labels,omitempty"`
	MemoryLimitMB int64             `json:"memory_limit_mb,omitempty"`
}

type rollbackRequest struct {
	SessionToken string `json:"session_token"`
	SnapshotID   string `json:"snapshot_id"`
}

type execRequest struct {
	SessionToken string `json:"session_token"`
	Command      string `json:"command"`
}

type expandMemoryRequest struct {
	SessionToken string `json:"session_token"`
	TargetMB     int64  `json:"target_mb"`
}

type mountRequest struct {
	SessionToken  string `json:"session_token"`
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	Mode          string `json:"mode"`
}

func NewHandler(cfg config.Config, service *sandbox.Service, monitorService *monitor.Service) stdhttp.Handler {
	router := &Router{
		config:  cfg,
		service: service,
		monitor: monitorService,
	}

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /healthz", router.healthz)
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

	meta, err := r.service.Rollback(req.Context(), sandboxID, body.SnapshotID)
	if err != nil {
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

	writeJSON(w, stdhttp.StatusAccepted, result)
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
	if body.TargetMB <= 0 {
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

	result, err := r.service.ExpandMemory(req.Context(), sandboxID, body.TargetMB)
	if err != nil {
		writeDomainError(w, err)
		return
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
		writeError(w, stdhttp.StatusBadRequest, errors.New("session id is required"))
		return
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

	result, err := r.service.MountPath(req.Context(), sandboxID, sandbox.MountRequest{
		HostPath:      body.HostPath,
		ContainerPath: body.ContainerPath,
		Mode:          body.Mode,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusCreated, result)
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
