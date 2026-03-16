"""Sleigh LangChain tool example (zero wrapper).

Only one prerequisite:
- Call action=create_session_token first.
- Reuse session_token for all other actions.
"""

import os

from sdk import SleighLangChainClient


def build_tool():
    base_url = (os.getenv("SLEIGH_RUNTIME_BASE_URL") or "http://127.0.0.1:10122").strip()
    timeout_seconds = float(os.getenv("SLEIGH_RUNTIME_TIMEOUT_SECONDS", "30"))
    client = SleighLangChainClient(base_url=base_url, timeout_seconds=timeout_seconds)
    return client.get_sleigh_runtime_tool()


if __name__ == "__main__":
    tool = build_tool()
    print("Sleigh tool ready:", tool.name)
