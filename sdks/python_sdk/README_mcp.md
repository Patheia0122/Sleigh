# Sleigh MCP Integration

Install SDK with MCP extras:

```bash
cd sdks/python_sdk/sdk
pip install ".[mcp]"
```

Run stdio MCP server:

```python
from sdk import run_stdio_server

run_stdio_server(base_url="http://127.0.0.1:8080")
```

Or build explicitly:

```python
from sdk import build_mcp_server

mcp = build_mcp_server(base_url="http://127.0.0.1:8080")
mcp.run()
```

AI coding focused tools are included:

- `create_session_token`
- `run_workflow`
- `read_sandbox`
- `patch_workspace`

`create_sandbox` supports `confirm_low_memory` for low-memory confirmation flow.
Use `create_session_token` first and reuse returned `session_token` in subsequent calls.
`patch_workspace` supports two modes: `write_mode=patch` (git patch) and `write_mode=replace_file` (raw full overwrite).
