# Sleigh Python SDK

Python SDK for the Sleigh runtime server.

Two client variants are included:

- **LangChain Tool variant**: one-call `as_langchain_tool()` (returns `StructuredTool`)
- **MCP variant**: expose runtime APIs as MCP tools over stdio

---

## 1. Install

```bash
pip install .
```

Optional extras:

```bash
pip install ".[langchain]"
pip install ".[mcp]"
```

---

## 2. Base Python Client

```python
from sdk import SleighClient

client = SleighClient(base_url="http://127.0.0.1:8080")
session_token = "sess_demo"
created = client.create_sandbox(session_token=session_token, image="alpine:3.20")
sandbox_id = created["id"]
```

---

## 3. Ordered Workflow (AI Coding)

Run multiple steps in one request and stop early on failure/timeout:

```python
result = client.run_workflow(
    session_token=session_token,
    steps=[
        {"action": "create_sandbox", "image": "alpine:3.20", "memory_limit_mb": 512},
        {"action": "exec_command", "command": "echo hello", "wait": True, "wait_timeout_seconds": 10},
        {"action": "create_snapshot"},
        {"action": "exec_command", "command": "uname -a", "wait": True},
    ],
)
print(result["stopped_early"], result["steps"])
```

---

## 4. Sandbox Read API (AI Coding)

```python
read_result = client.read_sandbox(
    session_token=session_token,
    sandbox_id=sandbox_id,
    command="rg",
    args=["TODO", "/workspace"],
    timeout_seconds=10,
    max_output_bytes=65536,
    max_lines=200,
)
print(read_result)
```

---

## 5. Patch API (Sandbox Semantic)

`patch_workspace` targets:

- `POST /sandboxes/{id}/ops/patch`
- validates sandbox auth + mounted workspace path ownership

```python
result = client.patch_workspace(
    session_token=session_token,
    sandbox_id=sandbox_id,
    cwd="/home/hxluo/sleigh_env",
    patch="*** Begin Patch\n*** End Patch\n",
)
```

---

## 6. Low-Memory Create Guard

When host available memory ratio:

- `< 5%`: create is blocked
- `>= 5%` and `< 8%`: create requires `confirm_low_memory=True`

```python
created = client.create_sandbox(
    session_token=session_token,
    image="alpine:3.20",
    confirm_low_memory=True,
)
```

---

## 7. More Examples

- LangChain integration: `../README_langchain.md`
- MCP integration: `../README_mcp.md`
