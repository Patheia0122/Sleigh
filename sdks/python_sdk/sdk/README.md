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
session_token = client.create_session_token()["session_token"]
created = client.create_sandbox(session_token=session_token, image="alpine:3.20")
sandbox_id = created["sandbox_id"]
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
- validates sandbox auth and targets directory inside sandbox filesystem
- `sandbox_path` is required and must be an absolute directory path in sandbox
- service exports sandbox dir to host temp workspace, applies edit, and syncs back
- quality checks: run `pre-commit` when config exists; otherwise auto-detect language for fallback checks
- `write_mode=context_edit` is default for partial edits; pass raw snippets with `target_file_path`, `old_text`, `new_text`, and optional `before_context`/`after_context`/`occurrence`
- `write_mode=replace_file` is supported for full overwrite by raw source content

```python
result = client.patch_workspace(
    session_token=session_token,
    sandbox_id=sandbox_id,
    sandbox_path="/app",
    write_mode="context_edit",
    target_file_path="calculator.py",
    before_context="    def multiply(self, a, b):\n        return a * b\n\n",
    old_text="    def multiply(self, a, b):\n        return a * b\n",
    new_text="    def multiply(self, a, b):\n        return a * b\n\n    def sqrt(self, a):\n        if a < 0:\n            raise ValueError('Cannot sqrt negative number!')\n        return a ** 0.5\n",
)

# Full overwrite mode (raw source content)
rewrite_result = client.patch_workspace(
    session_token=session_token,
    sandbox_id=sandbox_id,
    sandbox_path="/app",
    write_mode="replace_file",
    target_file_path="calculator.py",
    content="print('hello from overwrite mode')\n",
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
    request_timeout_seconds=180,
)
```

---

## 7. More Examples

- LangChain integration: `../README_langchain.md`
- MCP integration: `../README_mcp.md`

## 8. Session Exec History

`list_session_exec_tasks` accepts optional `session_id`.
If omitted, SDK uses `session_token` as `session_id` automatically:

```python
history = client.list_session_exec_tasks(
    session_token=session_token,
    limit=20,
)
print(history)
```
