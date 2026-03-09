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

- `run_workflow`
- `read_sandbox`
- `patch_workspace`

When creating sandbox under low memory pressure, pass `confirm_low_memory=True`.
