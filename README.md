# Sleigh

[中文版本](README_zh.md)

![PyPI](https://img.shields.io/pypi/v/sleigh-sdk)
![Python](https://img.shields.io/badge/python-3.10%2B-3776AB?logo=python&logoColor=white)
![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go)

## Sleigh is a self-hosted sandbox runtime for Agents with elasticity and strong filesystem state

Run the Sleigh server on a single high-resource machine, then let multiple Agents use the Sleigh client to get sandboxes with strong filesystem state and elastic memory expansion.

---

![Sleigh Logo](docs/assets/Sleigh_logo.png)

## What Sleigh Provides

Sleigh is for teams that want cloud-sandbox-like capabilities but already have a high-resource local server, want to avoid cloud lock-in, or need to keep data in-network.

- Session-level sandbox isolation
- Elastic controls for resource-volatile workloads
- Command execution (async + sync wait)
- Snapshot and rollback
- Strong filesystem state for long-running tasks
- Read/write APIs for AI coding loops
- Read-only host-path mount for safe dataset/code reuse
- Environment-zone directory copy for fast runtime bootstrap
- Memory pressure observation and expansion controls
- OTEL observability support

Sleigh is open-source and self-hosted. It runs on your own infrastructure.

## Who It Is For / Not For

**Good fit:**

- Individuals or small teams with an existing Linux server
- Teams needing long-running, high-resource, or stateful Agent execution
- Teams wanting more predictable cost on owned hardware

**Probably not a fit:**

- You only want fully managed SaaS and do not want to run server components
- Your workload is lightweight and cloud sandbox costs are negligible

## Self-hosted vs Cloud Sandbox

| Dimension | Sleigh (self-hosted) | Typical cloud sandbox |
| --- | --- | --- |
| Deployment | Your own server | Vendor-managed |
| Lock-in risk | Lower (open source) | Usually higher |
| Product usage fee | No fee | Usually usage-based |
| Control | Full control | Constrained by platform |

---

## 2-Minute Quickstart

### Prerequisites

- Linux host
- `systemd` available
- Docker installed and running
- `git`, `bash`, and network access for dependencies/images

### 1) Install server (host mode)

```bash
git clone git@github.com:Patheia0122/Sleigh.git
cd Sleigh
./install_server.sh
```

The installer builds the server binary and starts `sleigh.service`.

### 2) Check service health

```bash
sudo systemctl status sleigh.service
curl -sS http://127.0.0.1:10122/healthz
```

### 3) Install Python SDK

```bash
pip install sleigh-sdk
```

---

## Minimal End-to-End Flow (Token -> Sandbox -> Exec)

### Option A: curl

1) Create session token:

```bash
TOKEN=$(curl -sS -X POST http://127.0.0.1:10122/sessions/token | python3 -c "import sys,json;print(json.load(sys.stdin)['session_token'])")
```

2) Create sandbox:

```bash
SANDBOX_ID=$(curl -sS -X POST http://127.0.0.1:10122/sandboxes \
  -H "Content-Type: application/json" \
  -d "{\"session_token\":\"$TOKEN\",\"image\":\"python:3.11-slim\"}" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['sandbox_id'])")
```

3) Execute command:

```bash
curl -sS -X POST "http://127.0.0.1:10122/sandboxes/$SANDBOX_ID/exec" \
  -H "Content-Type: application/json" \
  -d "{\"session_token\":\"$TOKEN\",\"command\":\"python -V\",\"wait\":true}"
```

### Option B: Python SDK

```python
from sleigh_sdk import SleighClient

client = SleighClient(base_url="http://127.0.0.1:10122")
token = client.create_session_token()["session_token"]
sandbox_id = client.create_sandbox(session_token=token, image="python:3.11-slim")["sandbox_id"]
result = client.exec_command(
    session_token=token,
    sandbox_id=sandbox_id,
    command="python -V",
    wait=True,
)
print(result)
```

---

## Typical Agent Use Cases

- Run coding Agent tasks in isolated containers
- Add checkpoint/rollback for long Agent workflows
- Serve multiple Agents from one local high-resource server
- Keep sensitive workloads inside your own network boundary
- Run memory-heavy tasks (for example, metagenomic alignment in computational biology, where a single task may consume hundreds of GBs or even 1TB of memory)
- Mount large reference datasets as read-only into multiple sandboxes to avoid accidental host data mutation
- Copy prebuilt toolchain/environment templates from environment zone into sandbox to shorten cold start

## Core API

Core control-plane endpoints exposed by the Sleigh server.
- `POST /sessions/token`: issue session token
- `POST /sandboxes`: create sandbox
- `GET /sandboxes`: list session sandboxes
- `POST /sandboxes/{id}/exec`: execute command
- `POST /webhooks/exec/subscribe`: subscribe exec completion webhook callback
- `POST /workflow/run`: ordered multi-step workflow execution
- `POST /sandboxes/{id}/snapshots`: create snapshot
- `POST /sandboxes/{id}/rollback`: rollback snapshot
- `POST /sandboxes/{id}/ops/read`: read operation (allowlisted commands)
- `POST /sandboxes/{id}/ops/code/write`: AI coding endpoint with formatting/lint checks and optional build verification
- `POST /sandboxes/{id}/environment/copy`: copy environment-zone directory into sandbox

## Runtime Model

- Server runs on host machine (`systemd`)
- Sandboxes run in Docker containers
- Protected endpoints require `session_token`

## Python Integration Install

```bash
pip install sleigh-sdk
pip install "sleigh-sdk[langchain]"
pip install "sleigh-sdk[mcp]"
```

## SDK and Agent Integration

Sleigh SDK is designed to expose runtime capabilities directly as a LangChain Tool for Agents.
That means Agents do not need to orchestrate raw HTTP calls manually. They can use one unified tool interface for sandbox lifecycle, command execution, read/write coding, and workflows.

Benefits:

- Tool semantics cover core operations (create/exec/read/write/rollback/workflow)
- Parameter validation before dispatch reduces Agent ambiguity
- Agent-friendly action design (including explicit code_write actions)
- MCP adapter is available when your platform prefers MCP transport

Minimal LangChain Tool example:

```python
from sleigh_sdk import SleighLangChainClient

client = SleighLangChainClient(base_url="http://127.0.0.1:10122")
tool = client.as_langchain_tool()

# Inject `tool` into your Agent tool list.
```

More complete Agent-friendly example:
`examples/langchain_sleigh_runtime_tool.py`

## Notes and Limits

- `build_language` in code_write is optional; if the server lacks the required image, it will pull first and increase latency.
- Exec webhook callbacks are HMAC signed (`X-Timestamp`, `X-Signature`) with server-side `WEBHOOK_HMAC_SECRET`.
- You can pass `webhook_url` on `POST /sandboxes/{id}/exec` to subscribe in one shot (no separate `exec_id` round-trip), or use `POST /webhooks/exec/subscribe` after `exec` returns.
- Mount mode is read-only (`ro`) by design.
- Environment copy is guarded by allowlisted root boundaries.
- Requires Linux host
- Currently Docker runtime only
- Sandboxes share host kernel

## More Docs

- Full API reference: `docs/api_reference.md`
- SDK docs: `sdks/python_sdk/README.md`
- LangChain example: `examples/langchain_sleigh_runtime_tool.py`
- MCP stdio example: `examples/mcp_sleigh_runtime_server.py`
