# Sleigh Python SDK

Python SDK for the Sleigh runtime server.

Two client variants are included:

- **LangChain Tool variant**: one-call `as_langchain_tool()` integration
- **MCP variant**: expose runtime APIs as MCP tools over stdio

---

## 1. Install

### Base client

```bash
pip install .
```

### With LangChain support

```bash
pip install ".[langchain]"
```

### With MCP support

```bash
pip install ".[mcp]"
```

---

## 2. Base Python Client

```python
from sleigh_sdk import SleighClient

client = SleighClient(base_url="http://127.0.0.1:8080")
session_token = "sess_demo"

created = client.create_sandbox(session_token=session_token, image="alpine:3.20")
sandbox_id = created["id"]

result = client.exec_command(
    session_token=session_token,
    sandbox_id=sandbox_id,
    command="echo hello",
)
print(result)
```

The server enforces session-scoped visibility using `session_token`.

---

## 3. LangChain Tool Variant

The SDK preconfigures:

- `args_schema` (Pydantic)
- `name` and `description`
- `return_direct=True`
- `handle_tool_error=True`

```python
from sleigh_sdk import SleighLangChainClient

lc_client = SleighLangChainClient(base_url="http://127.0.0.1:8080")
tool = lc_client.as_langchain_tool()
tools = [tool]
```

Input uses one unified schema:

- `session_token` (required)
- `action` (required)
- other fields based on action (`sandbox_id`, `command`, etc.)

---

## 4. MCP Variant

### Run MCP server (stdio)

```python
from sleigh_sdk import run_stdio_server

run_stdio_server(base_url="http://127.0.0.1:8080")
```

Or construct explicitly:

```python
from sleigh_sdk import build_mcp_server

mcp = build_mcp_server(base_url="http://127.0.0.1:8080")
mcp.run()
```

Included tools:

- `create_sandbox`
- `list_sandboxes`
- `get_sandbox`
- `delete_sandbox`
- `exec_command`
- `get_exec`
- `create_snapshot`
- `rollback_snapshot`
- `list_mounts`
- `mount_path`
- `unmount_path`
- `list_session_exec_tasks`

---

## 5. Session Token Rule

Current server contract:

- accepts `session_token` in body or query
- does not accept header token

The SDK follows this contract automatically.

---

## 6. Error Handling

The base client raises on HTTP `>=400`:

- `SleighClientError`

Error message includes:

- HTTP method
- path
- status code
- server payload
