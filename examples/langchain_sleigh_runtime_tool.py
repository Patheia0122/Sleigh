"""Sleigh runtime LangChain tool integration (optional)."""

from __future__ import annotations

import json
import logging
import os
import sys
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)


def _import_sleigh_modules():
    """Import SleighLangChainClient from local SDK package."""
    try:
        from sdk import SleighLangChainClient  # type: ignore
        from sdk.langchain_tool import SleighToolInput  # type: ignore

        return SleighLangChainClient, SleighToolInput
    except Exception:
        pass

    # Fallback: add local SDK directory to sys.path.
    project_root = Path(__file__).resolve().parents[1]
    sdk_root = project_root / "sdks" / "python_sdk" / "sdk" / "src"
    if sdk_root.exists():
        sdk_path = str(sdk_root)
        if sdk_path not in sys.path:
            sys.path.insert(0, sdk_path)
        from sdk import SleighLangChainClient  # type: ignore
        from sdk.langchain_tool import SleighToolInput  # type: ignore

        return SleighLangChainClient, SleighToolInput
    raise ImportError("sdk package is not available")


def get_sleigh_runtime_tool() -> Any | None:
    """Create Sleigh runtime LangChain tool when enabled.

    Env:
    - SLEIGH_RUNTIME_ENABLED: true/false (default false)
    - SLEIGH_RUNTIME_BASE_URL: runtime server base url
    - SLEIGH_RUNTIME_TIMEOUT_SECONDS: optional float, default 30
    """
    enabled = os.getenv("SLEIGH_RUNTIME_ENABLED", "false").lower() in ("true", "1", "yes", "y")
    if not enabled:
        return None

    base_url = (os.getenv("SLEIGH_RUNTIME_BASE_URL") or "").strip()
    if not base_url:
        logger.warning("SLEIGH_RUNTIME_ENABLED=true but SLEIGH_RUNTIME_BASE_URL is empty; skip Sleigh tool")
        return None

    timeout_seconds = float(os.getenv("SLEIGH_RUNTIME_TIMEOUT_SECONDS", "30"))
    try:
        SleighLangChainClient, SleighToolInput = _import_sleigh_modules()
        client = SleighLangChainClient(base_url=base_url, timeout_seconds=timeout_seconds)
        description = (
            "Sleigh runtime unified tool. Use action to call sandbox APIs "
            "(create/list/get/delete sandbox, exec command, snapshot, mount, memory, history, "
            "run_workflow, read_sandbox, code_write). "
            "session_token is required and should usually use current session/thread id."
        )
        from langchain_core.tools import StructuredTool

        def _runtime_tool(**kwargs) -> str:
            payload = SleighToolInput(**kwargs)
            result = client._dispatch(payload)  # noqa: SLF001 - SDK internal dispatcher reuse
            return json.dumps(result, ensure_ascii=False)

        tool = StructuredTool.from_function(
            func=_runtime_tool,
            name="sleigh_runtime",
            description=description,
            args_schema=SleighToolInput,
            return_direct=False,
            handle_tool_error=True,
        )
        logger.info("Sleigh runtime tool enabled: %s", base_url)
        return tool
    except Exception as exc:
        logger.warning("Failed to initialize Sleigh runtime tool: %s", exc)
        return None
