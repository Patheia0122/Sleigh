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

For `patch_workspace`, default to `write_mode=context_edit`: provide `target_file_path`, `old_text`, `new_text`, and optionally `before_context`/`after_context`/`occurrence`.
The server performs snippet locate+replace and returns semantic errors like `no_match` / `ambiguous_match`.
If full overwrite is intended, use `write_mode=replace_file` with `target_file_path` and raw `content`.

When creating sandbox under low memory pressure, pass `confirm_low_memory=True`.
