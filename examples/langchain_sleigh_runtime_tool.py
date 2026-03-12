"""Sleigh LangChain runtime tool example (complete action template).

Only one prerequisite:
- Call action=create_session_token first.
- All other actions require session_token.
"""

from __future__ import annotations

import json
import logging
import os
from typing import Any

logger = logging.getLogger(__name__)


def _import_sleigh_modules():
    """Import Sleigh modules from published pip package."""
    from sdk import SleighLangChainClient  # type: ignore
    from sdk.langchain_tool import SleighToolInput  # type: ignore

    return SleighLangChainClient, SleighToolInput


def get_sleigh_runtime_tool() -> Any | None:
    """Create a StructuredTool with complete action coverage."""
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
            "Sleigh runtime unified tool.\n"
            "First call action=create_session_token. All other actions require session_token.\n"
            "Supports: create/list/get/delete sandbox, exec/get_exec/cancel_exec, "
            "create/list/rollback snapshot, list/mount/unmount, list_mount_workspaces, "
            "list_environment_workspaces, copy_environment, get/expand memory, "
            "list_session_exec_tasks, run_workflow, read_sandbox, "
            "code_write, code_write_context_edit, code_write_replace_file.\n"
            "Optional for code_write*: build_language=go|python|node|rust|java.\n"
            "If the required language image is missing on host, runtime will pull it first, "
            "which can increase latency for that request."
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


def build_action_examples(session_token: str, sandbox_id: str = "sbx_example") -> dict[str, dict[str, Any]]:
    """Return copy-paste payload templates for major actions."""
    return {
        "create_session_token": {"action": "create_session_token"},
        "create_sandbox": {
            "action": "create_sandbox",
            "session_token": session_token,
            "image": "python:3.11-slim",
            "memory_limit_mb": 512,
            "confirm_low_memory": True,
        },
        "list_sandboxes": {"action": "list_sandboxes", "session_token": session_token},
        "get_sandbox": {"action": "get_sandbox", "session_token": session_token, "sandbox_id": sandbox_id},
        "delete_sandbox": {"action": "delete_sandbox", "session_token": session_token, "sandbox_id": sandbox_id},
        "exec_command": {
            "action": "exec_command",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "command": "python -V",
            "wait": True,
            "wait_timeout_seconds": 20,
        },
        "get_exec": {
            "action": "get_exec",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "exec_id": "exec_example",
        },
        "cancel_exec": {
            "action": "cancel_exec",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "exec_id": "exec_example",
        },
        "create_snapshot": {"action": "create_snapshot", "session_token": session_token, "sandbox_id": sandbox_id},
        "list_snapshots": {"action": "list_snapshots", "session_token": session_token, "sandbox_id": sandbox_id},
        "rollback_snapshot": {
            "action": "rollback_snapshot",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "snapshot_id": "snap_example",
        },
        "list_mount_workspaces": {"action": "list_mount_workspaces", "session_token": session_token},
        "mount_path": {
            "action": "mount_path",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "workspace_path": "/project-a",
            "container_path": "/workspace",
        },
        "list_mounts": {"action": "list_mounts", "session_token": session_token, "sandbox_id": sandbox_id},
        "unmount_path": {
            "action": "unmount_path",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "mount_id": "mnt_example",
        },
        "list_environment_workspaces": {"action": "list_environment_workspaces", "session_token": session_token},
        "copy_environment": {
            "action": "copy_environment",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "environment_path": "/env-a",
            "sandbox_path": "/app",
        },
        "get_memory_pressure": {
            "action": "get_memory_pressure",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
        },
        "expand_memory": {
            "action": "expand_memory",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "target_mb": 1024,
        },
        "read_sandbox": {
            "action": "read_sandbox",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "read_command": "ls",
            "read_args": ["-la", "/app"],
            "timeout_seconds": 10,
        },
        "code_write_context_edit": {
            "action": "code_write_context_edit",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "sandbox_path": "/app/main.py",
            "old_text": "print('hello')\n",
            "new_text": "print('hello world')\n",
            "build_language": "python",
        },
        "code_write_replace_file": {
            "action": "code_write_replace_file",
            "session_token": session_token,
            "sandbox_id": sandbox_id,
            "sandbox_path": "/app/main.py",
            "content": "print('fresh file')\n",
            "build_language": "python",
        },
        "run_workflow": {
            "action": "run_workflow",
            "session_token": session_token,
            "workflow_steps": [
                {"action": "exec_command", "sandbox_id": sandbox_id, "command": "echo workflow", "wait": True},
                {"action": "create_snapshot", "sandbox_id": sandbox_id},
            ],
        },
        "list_session_exec_tasks": {
            "action": "list_session_exec_tasks",
            "session_token": session_token,
            "limit": 20,
        },
    }


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    tool = get_sleigh_runtime_tool()
    if tool is None:
        print("Sleigh tool disabled. Set SLEIGH_RUNTIME_ENABLED=true and SLEIGH_RUNTIME_BASE_URL.")
    else:
        print("Sleigh tool ready:", tool.name)
        print("Call action=create_session_token first. All other actions require session_token.")
        examples = build_action_examples("sess_xxx", "sbx_xxx")
        print(json.dumps(examples, ensure_ascii=False, indent=2))
