# Sleigh Python SDK

Python SDK for the Sleigh runtime server.

Two client variants are included:

- **LangChain Tool variant**: one-call `as_langchain_tool()` (returns `StructuredTool`)
- **MCP variant**: expose runtime APIs as MCP tools over stdio

---

## 1. Install

```bash
pip install sleigh-sdk
```

Optional extras:

```bash
pip install "sleigh-sdk[langchain]"
pip install "sleigh-sdk[mcp]"
```

---

## 2. Base Python Client

```python
from sdk import SleighClient

client = SleighClient(base_url="http://127.0.0.1:10122")
session_token = client.create_session_token()["session_token"]
created = client.create_sandbox(session_token=session_token, image="python:3.11-slim")
sandbox_id = created["sandbox_id"]
```

---

## 3. Ordered Workflow (AI Coding)

Run multiple steps in one request and stop early on failure/timeout:

```python
sandbox_id = created["sandbox_id"]
result = client.run_workflow(
    session_token=session_token,
    steps=[
        {"action": "exec_command", "sandbox_id": sandbox_id, "command": "echo hello", "wait": True, "wait_timeout_seconds": 10},
        {"action": "create_snapshot", "sandbox_id": sandbox_id},
        {"action": "exec_command", "sandbox_id": sandbox_id, "command": "uname -a", "wait": True},
    ],
)
print(result["stopped_early"], result["steps"])
```

Note: in SDK validation, every workflow step must include `sandbox_id`.

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

## 5. Mount + Environment Copy

List available mount workspace directories first:

```python
dirs = client.list_mount_workspaces(session_token=session_token)
print(dirs["items"])
```

Mount is now server-enforced read-only:

```python
mount_result = client.mount_path(
    session_token=session_token,
    sandbox_id=sandbox_id,
    workspace_path="/project-a",
    container_path="/workspace",
)
print(mount_result)
```

Copy one allowlisted workspace directory into sandbox filesystem (non-mount path, via `docker cp`):

```python
copy_result = client.copy_environment(
    session_token=session_token,
    sandbox_id=sandbox_id,
    workspace_path="/project-a",
    sandbox_path="/app",
)
print(copy_result)
```

---

## 6. Code Write API (Sandbox Semantic)

`code_write` targets:

- `POST /sandboxes/{id}/ops/code/write`
- validates sandbox auth and targets file inside sandbox filesystem
- `sandbox_path` is required and must be an absolute file path in sandbox
- service exports target file directory to host temp workspace, applies edit, and syncs back
- quality checks: run `pre-commit` when config exists; otherwise auto-detect language for fallback checks
- `write_mode=context_edit` is default for partial edits; pass raw snippets with `old_text`, `new_text`, and optional `before_context`/`after_context`/`occurrence`
- `write_mode=replace_file` is supported for full overwrite by raw source content

```python
result = client.code_write(
    session_token=session_token,
    sandbox_id=sandbox_id,
    sandbox_path="/app/calculator.py",
    write_mode="context_edit",
    before_context="    def multiply(self, a, b):\n        return a * b\n\n",
    old_text="    def multiply(self, a, b):\n        return a * b\n",
    new_text="    def multiply(self, a, b):\n        return a * b\n\n    def sqrt(self, a):\n        if a < 0:\n            raise ValueError('Cannot sqrt negative number!')\n        return a ** 0.5\n",
)

# Full overwrite mode (raw source content)
rewrite_result = client.code_write(
    session_token=session_token,
    sandbox_id=sandbox_id,
    sandbox_path="/app/calculator.py",
    write_mode="replace_file",
    content="print('hello from overwrite mode')\n",
)
```

---

## 7. Low-Memory Guard (Create + Expand)

When host available memory ratio:

- `< 10%`: create/expand is blocked
- `>= 10%` and `< 15%`: create requires `confirm_low_memory=True`; expand proceeds with warning in response `reason`

```python
created = client.create_sandbox(
    session_token=session_token,
    image="python:3.11-slim",
    confirm_low_memory=True,
    request_timeout_seconds=180,
)
```

---

## 8. More Examples

- LangChain integration: `../README_langchain.md`
- MCP integration: `../README_mcp.md`

## 9. Session Exec History

`list_session_exec_tasks` accepts optional `session_id`.
If omitted, SDK uses `session_token` as `session_id` automatically:

```python
history = client.list_session_exec_tasks(
    session_token=session_token,
    limit=20,
)
print(history)
```
