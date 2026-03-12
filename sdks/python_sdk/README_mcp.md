# Sleigh MCP Integration

Install SDK with MCP extras:

```bash
cd sdks/python_sdk/sdk
pip install ".[mcp]"
```

Run stdio MCP server:

```python
from sdk import run_stdio_server

run_stdio_server(base_url="http://127.0.0.1:10122")
```

Or build explicitly:

```python
from sdk import build_mcp_server

mcp = build_mcp_server(base_url="http://127.0.0.1:10122")
mcp.run()
```

AI coding focused tools are included:

- `create_session_token`
- `run_workflow`
- `read_sandbox`
- `code_write`
- `list_mount_workspaces`
- `copy_environment`

`create_sandbox` supports `confirm_low_memory` for low-memory confirmation flow.
Use `create_session_token` first and reuse returned `session_token` in subsequent calls.
For `run_workflow`, every step in `steps` should include `sandbox_id` (SDK performs pre-validation).
`code_write` supports two modes: `write_mode=context_edit` (server-side context locate+replace) and `write_mode=replace_file` (raw full overwrite).
`mount_path` is server-enforced read-only (`ro`).
