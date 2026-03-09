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

- `run_workflow`
- `read_sandbox`
- `patch_workspace`

`create_sandbox` supports `confirm_low_memory` for low-memory confirmation flow.
