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
- Enables snapshot and rollback for failure recovery
- Exposes memory pressure and in-place expansion controls
- Supports host-path mounting with permission boundaries
- Pushes runtime events with finite retry/backoff
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
- `POST /sandboxes/{id}/snapshots` create snapshot
- `POST /sandboxes/{id}/rollback` rollback snapshot
- `GET /sandboxes/{id}/memory/pressure` query pressure
- `POST /sandboxes/{id}/memory/expand` request memory expansion
- `GET /sessions/{sessionId}/exec-tasks` paginated history

All protected endpoints require `session_token` (body or query).

## Python SDK

Python SDK is located in `python_sdk/` and includes:

- **LangChain Tool adapter**: `sleigh_sdk.SleighLangChainClient`
- **MCP adapter**: `sleigh_sdk.run_stdio_server`

See `python_sdk/README.md` for installation and usage details.

## Status

Current implementation focuses on a stable, minimal production loop:

- host-service deployment
- Docker sandbox execution
- session isolation
- recovery and observability primitives

Roadmap items continue in incremental iterations.
