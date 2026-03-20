# Sleigh Runtime API Reference

This document is the detailed English reference for the Sleigh HTTP API.
It covers all public endpoints currently registered by the server router.

## Base URL

- Default local address: `http://127.0.0.1:10122`

## Authentication Model

- Most control-plane endpoints require `session_token`.
- `session_token` can be provided in either:
  - request JSON body as `session_token`, or
  - query string as `?session_token=...` (mainly for GET endpoints).
- `POST /sessions/token` is the token-issuing endpoint.
- Authorization is sandbox-scoped: access to a `sandbox_id` is validated against the token owner.

## Response and Error Conventions

- Success responses are JSON unless noted (for example `204 No Content`).
- Errors follow:

```json
{
  "error": "human-readable message"
}
```

- Typical status codes:
  - `200 OK`: successful read/update/list operations
  - `201 Created`: token/sandbox/snapshot creation, mount create
  - `204 No Content`: successful delete/unmount
  - `400 Bad Request`: malformed JSON, missing required fields, invalid params
  - `403 Forbidden`: token does not own the requested sandbox/session resource
  - `404 Not Found`: resource missing
  - `409 Conflict`: semantic conflicts (for example low-memory confirmation required, image pull notify mode)
  - `503 Service Unavailable`: blocked by host constraints (for example low memory)

## Endpoint Index

### Health and Diagnostics

- `GET /healthz`
- `GET /resources`
- `GET /diagnostics/oom`

### Session

- `POST /sessions/token`
- `GET /sessions/{sessionId}/exec-tasks`
- `POST /maintenance/exec-tasks/cleanup`

### Sandbox Lifecycle

- `POST /sandboxes`
- `GET /sandboxes`
- `GET /sandboxes/{id}`
- `DELETE /sandboxes/{id}`

### Snapshot

- `POST /sandboxes/{id}/snapshots`
- `GET /sandboxes/{id}/snapshots`
- `POST /sandboxes/{id}/rollback`

### Command Execution

- `POST /sandboxes/{id}/exec`
- `GET /sandboxes/{id}/exec/{execId}`
- `POST /sandboxes/{id}/exec/{execId}/cancel`
- `POST /webhooks/exec/subscribe`

### Memory

- `GET /sandboxes/{id}/memory/pressure`
- `POST /sandboxes/{id}/memory/expand`

### Mount and Environment

- `GET /mounts/workspaces`
- `GET /environments/workspaces`
- `GET /sandboxes/{id}/mounts`
- `POST /sandboxes/{id}/mounts`
- `DELETE /sandboxes/{id}/mounts/{mountId}`
- `POST /sandboxes/{id}/environment/copy`

### Agent-Oriented Ops

- `POST /sandboxes/{id}/ops/read`
- `POST /sandboxes/{id}/ops/code/write`
- `POST /workflow/run`

### Warm Pool

- `GET /warm-pool`
- `POST /warm-pool/refill`

---

## Detailed Endpoints

### 1) `GET /healthz`

Returns server liveness and runtime metadata.

Response fields:
- `status`
- `time` (RFC3339 UTC)
- `version`
- `sandbox_kind`

### 2) `GET /resources`

Returns host resource report (from monitor service), including memory/cpu/disk metrics used by admission checks.

### 3) `GET /diagnostics/oom`

Returns OOM diagnostics report from monitor service.

### 4) `POST /sessions/token`

Issues a new session token.

Response example:

```json
{
  "session_token": "sess_xxx",
  "issued_at": "2026-03-16T09:00:00Z"
}
```

### 5) `POST /sandboxes`

Creates a sandbox.

Request body:
- `session_token` (required)
- `image` (required by semantics; defaults are SDK-side)
- `labels` (optional map)
- `memory_limit_mb` (optional)
- `confirm_low_memory` (optional, required when host free-memory ratio is in warning range)
- `auto_expand_memory` (optional, stores auto-expand intent via sandbox label)
- `image_pull_policy` (optional: `wait` or `notify`)

Special behavior:
- Host memory guard:
  - `<10%` available: blocked with `503`
  - `10%~15%`: returns conflict unless `confirm_low_memory=true`
- `image_pull_policy=notify`:
  - if image is not cached, returns `409` with:
    - `image_pull_needed=true`
    - `resolved_image`
    - `next_action`

### 6) `GET /sandboxes`

Lists sandboxes owned by the current session.

Auth:
- `session_token` in query or JSON body.

Response:
- `{ "items": [...] }`

### 7) `GET /sandboxes/{id}`

Returns metadata for one sandbox (authorized by session ownership).

### 8) `DELETE /sandboxes/{id}`

Deletes a sandbox.

Response:
- `204 No Content` on success.

### 9) `POST /sandboxes/{id}/snapshots`

Creates a snapshot for sandbox filesystem/runtime state.

### 10) `GET /sandboxes/{id}/snapshots`

Lists snapshots for the sandbox.

### 11) `POST /sandboxes/{id}/rollback`

Rolls back sandbox to a snapshot.

Request body:
- `session_token` (required)
- `snapshot_id` (required)
- `auto_expand` (optional; triggers auto-expand flow when enabled)

### 12) `POST /sandboxes/{id}/exec`

Runs a command in sandbox.

Request body:
- `session_token` (required)
- `command` (required)
- `wait` (optional bool)
- `wait_timeout_seconds` (optional int)

Behavior:
- If `wait=true`, endpoint can poll and return terminal result inline (or timeout envelope).
- Otherwise returns async execution metadata (`exec_id` etc).

### 13) `GET /sandboxes/{id}/exec/{execId}`

Fetches execution status/result for one exec task.

### 14) `POST /sandboxes/{id}/exec/{execId}/cancel`

Cancels a running exec task.

### 14.1) `POST /webhooks/exec/subscribe`

Subscribes a webhook callback for one exec task. Server sends callback when the task reaches terminal state.

Request body:
- `session_token` (required)
- `sandbox_id` (required)
- `exec_id` (required)
- `webhook_url` (required; `http://` or `https://`)

Webhook delivery:
- Method: `POST`
- Headers: `Content-Type: application/json`, `X-Timestamp`, `X-Signature`
- Signature: `X-Signature=sha256=<hex(hmac_sha256(WEBHOOK_HMAC_SECRET, "<timestamp>.<raw_body>"))>`
- Body:
  - `status`: `ok` (succeeded), `err` (failed/cancelled), `timeout` (delivery timeout/network timeout classification)
  - `sandbox_id`
  - `exec_id`

### 15) `GET /sessions/{sessionId}/exec-tasks`

Lists historical exec tasks for the session.

Auth and ownership:
- Caller token must match `{sessionId}` (or use same value when omitted/derived).

Query params:
- `limit` (optional, default `20`)
- `cursor` (optional)

### 16) `POST /maintenance/exec-tasks/cleanup`

Runs cleanup for stale execution task records.

### 17) `GET /sandboxes/{id}/memory/pressure`

Returns memory pressure details for the sandbox/host context.

### 18) `POST /sandboxes/{id}/memory/expand`

Expands sandbox memory.

Request body:
- `session_token` (required)
- `target_mb` (required in manual mode; may be omitted/<=0 when `auto_expand=true`)
- `auto_expand` (optional)

Behavior:
- Auto-expand still respects host guard:
  - hard block below 10% free memory
  - warning band below 15% can be surfaced in reason fields

### 19) `GET /mounts/workspaces`

Lists directories available under mount allowlist root.

Response:
- `allowed_root`
- `items`

### 20) `GET /environments/workspaces`

Lists directories available under environment allowlist root.

Response:
- `allowed_root`
- `items`

### 21) `GET /sandboxes/{id}/mounts`

Lists active mounts for sandbox.

Response:
- `{ "items": [...] }`

### 22) `POST /sandboxes/{id}/mounts`

Creates a mount into sandbox.

Request body:
- `session_token` (required)
- `workspace_path` (required, resolved under mount allowlist root)
- `container_path` (required absolute path in sandbox)

Notes:
- Server enforces read-only mode (`ro`) regardless of client preference.

### 23) `DELETE /sandboxes/{id}/mounts/{mountId}`

Unmounts one mount entry.

Response:
- `204 No Content`

### 24) `POST /sandboxes/{id}/environment/copy`

Copies a host directory (from environment allowlist zone) into sandbox path.

Request body:
- `session_token` (required)
- `environment_path` (required; `workspace_path` accepted as backward-compatible alias)
- `sandbox_path` (required absolute path inside sandbox)

Behavior:
- Validates source directory under `SERVER_ENV_ALLOWED_ROOT`.
- Ensures sandbox is running and destination path exists (creates as needed).
- Performs copy via controlled host-to-container flow.

### 25) `POST /sandboxes/{id}/ops/read`

Runs allowlisted read-only commands and returns AI-friendly output envelope.

Request body:
- `session_token` (required)
- `command` (required, allowlisted)
- `args` (optional)
- `cwd` (optional)
- `timeout_seconds` (optional)
- `max_output_bytes` (optional)
- `max_lines` (optional)
- `output_offset` (optional)

Response fields:
- `status`, `duration_ms`, `timed_out`, `truncated`
- `stdout`, `stderr`, `exit_code`, `error`
- `omitted_bytes`, `next_offset`

### 26) `POST /sandboxes/{id}/ops/code/write`

AI-oriented code write endpoint for sandbox files, with optional quality/build checks.

Request body:
- Common:
  - `session_token` (required)
  - `sandbox_path` (required absolute sandbox file path)
  - `write_mode` (`context_edit` default, or `replace_file`)
  - `build_language` (optional: `go`, `python`, `node`, `rust`, `java`, ...)
  - `timeout_seconds`, `max_output_bytes`, `max_lines` (optional)
- `context_edit` mode:
  - `old_text`, `new_text` (required)
  - `before_context`, `after_context`, `occurrence` (optional)
- `replace_file` mode:
  - `content` (required; empty string is allowed)

Response fields:
- `status`, `duration_ms`, `timed_out`, `truncated`
- `stdout`, `stderr`, `error`
- `applied_files`
- `format_issues`, `lint_issues`
- `build_status` (`not_run`, `passed`, `failed`)

Quality/build semantics:
- If `.pre-commit-config.yaml` exists in workspace, pre-commit is preferred.
- If unavailable, language-specific fallback checks can run.
- If `build_language` is provided, language build validation is executed.
- Image/dependency pull failures are wrapped with actionable network/timeout error messages.

### 27) `POST /workflow/run`

Runs an ordered multi-step workflow with fail-fast behavior.

Request body:
- `session_token` (required)
- `steps` (required array)

Each step supports:
- `action` (required)
- `sandbox_id` (required for non-create actions)
- optional fields per action:
  - `image`, `labels`, `memory_limit_mb`
  - `command`, `wait`, `wait_timeout_seconds`
  - `snapshot_id`

Supported actions:
- `create_sandbox`
- `exec_command`
- `create_snapshot`
- `rollback_snapshot`
- `delete_sandbox`

Response includes:
- global workflow status and duration
- ordered per-step result entries (`index`, `action`, `status`, `duration_ms`, optional ids, error/result payload)

### 28) `GET /warm-pool`

Returns warm pool status/counters.

### 29) `POST /warm-pool/refill`

Triggers warm pool refill and returns updated warm pool status/counters.

---

## Agent Calling Recommendations

- Create and reuse one `session_token` per conversational/task context.
- Prefer `ops/read` before `ops/code/write` for context-edit reliability.
- Use small, incremental `context_edit` writes instead of large risky rewrites.
- Use `build_language` only when compile/build signal is needed (to avoid extra image pull latency).
- For create flows, handle `image_pull_policy=notify` conflict by retrying with `wait` when appropriate.
