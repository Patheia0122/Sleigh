# Sleigh LangChain Tool Integration

Install SDK with LangChain extras:

```bash
cd sdks/python_sdk/sdk
pip install ".[langchain]"
```

Usage:

```python
from sdk import SleighLangChainClient

client = SleighLangChainClient(base_url="http://127.0.0.1:8080")
tool = client.as_langchain_tool()
```

The returned tool uses `StructuredTool` and unified `SleighToolInput`.

AI coding related actions included in `SleighToolInput.action`:

- `create_session_token`
- `run_workflow`
- `read_sandbox`
- `patch_workspace`

Call `create_session_token` first, then pass the returned `session_token` to other actions.

For `patch_workspace`, the agent should provide complete git patch text (prefer full `diff --git` format), not raw source code.
For create/delete/rename operations, include metadata headers such as `new file mode`/`deleted file mode`/`rename from`/`rename to` and `index`.

When creating sandbox under low memory pressure, pass `confirm_low_memory=True`.
