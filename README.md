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

## Install (Host Service Mode)

Run:

```bash
./install_server.sh
```

The installer will:

- ask language at startup (English / Chinese)
- configure mount allowlist root interactively
- build server binary on host
- install and start `systemd` service `sleigh.service`

Useful commands after installation:

```bash
sudo systemctl status sleigh.service
sudo journalctl -u sleigh.service -f
```

## Local Development

For local debugging, a docker-compose setup is available:

```bash
docker compose up --build
```

> Production recommendation: use host service mode via `install_server.sh`.

## API Highlights

- `POST /sandboxes` create sandbox
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

All protected endpoints require `session_token` (body or query).

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

## Python SDK

Python SDK is located in `sdks/python_sdk/` and includes:

- **LangChain Tool adapter**: `sdk.SleighLangChainClient`
- **MCP adapter**: `sdk.run_stdio_server`

See `sdks/python_sdk/README.md` for installation and usage details.

## Status

Current implementation focuses on a stable, minimal production loop:

- host-service deployment
- Docker sandbox execution
- session isolation
- recovery and observability primitives

Roadmap items continue in incremental iterations.
