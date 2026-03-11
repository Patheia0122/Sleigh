# Sleigh

[中文版本](README_zh.md)

![Sleigh Logo](docs/assets/Sleigh_logo.png)

**Sleigh — Agent-native elastic sandbox runtime.**

Sleigh is an execution runtime for long-running, stateful, and resource-volatile agent workloads.
It provides the control-plane primitives required to run sandboxed tasks safely, recover from failures,
and keep execution loops stable at scale.

## What Sleigh Solves

- Isolates each agent session with sandbox-level access boundaries
- Supports command execution with async status and cancellation
- Supports synchronous wait mode for exec requests
- Enables snapshot and rollback for failure recovery
- Exposes memory pressure and in-place expansion controls
- Supports host-path mounting with permission boundaries
- Supports ordered multi-step workflow execution in one request
- Supports sandbox-scoped read operations with command allowlist and truncation
- Supports sandbox-scoped patch pipeline (`git apply` + `pre-commit` + optional build)
- Uses OTEL tracing for runtime observability
- Keeps execution history queryable with cursor pagination and TTL cleanup

## Runtime Model

- **Server runs on host machine** (system service mode)
- **Sandboxes run in Docker containers**
- **Session-scoped visibility** via `session_token`
- **Workspace-first snapshot semantics** (with container fallback)

## Install

| Component | Recommended command |
| --- | --- |
| Server (host service) | `./install_server.sh` |
| Python client (base) | `pip install sleigh-sdk` |
| Python client + LangChain | `pip install "sleigh-sdk[langchain]"` |
| Python client + MCP | `pip install "sleigh-sdk[mcp]"` |

### Install Server (Host Service Mode)

```bash
./install_server.sh
```

Installer behavior:

- prompts language (English / Chinese)
- configures mount allowlist root interactively
- builds server binary on host
- installs and starts `systemd` service `sleigh.service`

Useful service checks:

```bash
sudo systemctl status sleigh.service
sudo journalctl -u sleigh.service -f
```

### Install Python Client (pip)

```bash
pip install sleigh-sdk
```

Import path:

```python
from sdk import SleighClient
```

More usage details: `sdks/python_sdk/README.md`.

## Local Development

For local debugging, a docker-compose setup is available:

```bash
docker compose up --build
```

> Production recommendation: use host service mode via `install_server.sh`.

## API Highlights

- `POST /sandboxes` create sandbox
- `POST /sessions/token` issue a server-generated session token
- `GET /sandboxes` list sandboxes in current session
- `POST /sandboxes/{id}/exec` execute command
- `POST /workflow/run` run ordered workflow steps in one call
- `POST /sandboxes/{id}/snapshots` create snapshot
- `POST /sandboxes/{id}/rollback` rollback snapshot
- `GET /sandboxes/{id}/memory/pressure` query pressure
- `POST /sandboxes/{id}/memory/expand` request memory expansion
- `POST /sandboxes/{id}/ops/read` sandbox read operation (sync, allowlisted commands)
- `POST /sandboxes/{id}/ops/patch` sandbox-scoped patch pipeline (mounted workspace)
- `GET /sessions/{sessionId}/exec-tasks` paginated history

For mount writes, client input uses `workspace_path` (relative to `SERVER_MOUNT_ALLOWED_ROOT`, leading `/` allowed) and the server resolves it to host absolute paths internally.  
For patch writes, client input uses `sandbox_path` (absolute path inside sandbox), and the server performs host-side patch by exporting/syncing that sandbox directory.
Patch also supports `write_mode=replace_file` for full overwrite with raw source content.
Patch quality checks run `pre-commit` when `.pre-commit-config.yaml` exists; otherwise language-detected fallback checks are executed.
The `patch` field must be unified diff text (not raw source code), using headers like `*** Begin Patch` or `diff --git`.

All protected endpoints require `session_token` (body or query).  
Recommended flow: first call `POST /sessions/token`, then reuse returned token for the whole task/session.

Read/patch style endpoints return an AI-friendly envelope:

- `status`, `duration_ms`, `timed_out`, `truncated`
- `stdout`, `stderr`, `error`
- optional `omitted_bytes`, `next_offset`, and endpoint-specific artifacts

## Key Runtime Config

Configured through `install_server.sh` interactive prompts and written to `sleigh.env`.

- `SERVER_ADDR` HTTP listen address
- `SERVER_MOUNT_ALLOWED_ROOT` host mount allowlist root
- `WARM_POOL_SIZE` / `WARM_POOL_IMAGE` / `WARM_POOL_MEMORY_MB`
- `EXEC_TASK_TTL_DAYS` and `EXEC_CLEANUP_INTERVAL_SECONDS`
- `SANDBOX_IDLE_TTL_DAYS` idle sandbox recycle threshold (default `14`)
- `SERVER_OTEL_EXPORTER_OTLP_ENDPOINT` optional OTLP gRPC endpoint (empty disables OTEL)
- `IMAGE_PULL_TIMEOUT_SECONDS` image pull timeout for sandbox create

## Observability And Stability

- `create_sandbox` response includes `startup_latency_ms`
- image pull metadata is included in create response:
  - `image_pull_triggered`
  - `image_pull_status`
  - `image_pull_duration_ms`
- optional OTEL tracing over OTLP gRPC for sandbox lifecycle spans
- periodic idle sandbox cleanup removes stale session sandboxes and writes audit logs

## SDK Integrations

- **LangChain Tool adapter**: `sdk.SleighLangChainClient`
- **MCP adapter**: `sdk.run_stdio_server`
- docs: `sdks/python_sdk/README.md`

## Status

Current implementation focuses on a stable, minimal production loop:

- host-service deployment
- Docker sandbox execution
- session isolation
- recovery and observability primitives

Roadmap items continue in incremental iterations.
