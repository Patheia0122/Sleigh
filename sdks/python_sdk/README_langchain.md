# Sleigh LangChain Tool Integration

Install SDK with LangChain extras:

```bash
cd sdks/python_sdk/sdk
pip install ".[langchain]"
```

Usage:

```python
from sdk import SleighLangChainClient

client = SleighLangChainClient(base_url="http://127.0.0.1:10122")
tool = client.as_langchain_tool()
```

The returned tool uses `StructuredTool` and unified `SleighToolInput`.

AI coding related actions included in `SleighToolInput.action`:

- `create_session_token`
- `run_workflow`
- `read_sandbox`
- `code_write`
- `code_write_context_edit`
- `code_write_replace_file`
- `list_mount_workspaces`
- `list_environment_workspaces`
- `copy_environment`

Call `create_session_token` first, then pass the returned `session_token` to other actions.
For `run_workflow`, every step in `workflow_steps` must include `sandbox_id` (SDK enforces this before request).

For `code_write`, default to `write_mode=context_edit`: provide `sandbox_path` (absolute file path), `old_text`, `new_text`, and optionally `before_context`/`after_context`/`occurrence`.
To avoid ambiguous flat parameters, prefer explicit actions:
- `code_write_context_edit`: `sandbox_path + old_text + new_text (+ before_context/after_context/occurrence)`
- `code_write_replace_file`: `sandbox_path + content`
The server performs snippet locate+replace and returns semantic errors like `no_match` / `ambiguous_match`.
If full overwrite is intended, use `write_mode=replace_file` with `sandbox_path` and raw `content`.

For `copy_environment`, use `environment_path` (relative to `SERVER_ENV_ALLOWED_ROOT`) and target `sandbox_path`.

When creating sandbox under low memory pressure, pass `confirm_low_memory=True`.
`mount_path` is server-enforced read-only (`ro`).
