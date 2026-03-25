"""Sleigh runtime tool (minimal wrapper, tool-decorator style)."""

from __future__ import annotations

import asyncio
import os
from typing import Any

from langchain.tools import tool
from sleigh_sdk import SleighLangChainClient

_BASE_URL = (os.getenv("SLEIGH_RUNTIME_BASE_URL") or "http://127.0.0.1:10122").strip()
_TIMEOUT_SECONDS = float(os.getenv("SLEIGH_RUNTIME_TIMEOUT_SECONDS", "30"))

_client = SleighLangChainClient(base_url=_BASE_URL, timeout_seconds=_TIMEOUT_SECONDS)
_sdk_tool = _client.as_langchain_tool(
    name="sleigh_runtime",
    return_direct=False,
    handle_tool_error=True,
)


@tool("sleigh_runtime", args_schema=_sdk_tool.args_schema, return_direct=False)
async def sleigh_runtime(**kwargs: Any) -> str:
    """Unified Sleigh runtime tool for session, sandbox, exec, read/write."""
    return await asyncio.to_thread(_sdk_tool.invoke, kwargs)
