from __future__ import annotations

import json
from typing import Any, Literal

from pydantic import BaseModel, Field, model_validator

from .client import SleighClient


class SleighToolInput(BaseModel):
    session_token: str | None = Field(
        None,
        description="Session token. Call action=create_session_token first, then reuse returned token.",
    )
    action: Literal[
        "create_session_token",
        "create_sandbox",
        "list_sandboxes",
        "get_sandbox",
        "delete_sandbox",
        "create_snapshot",
        "list_snapshots",
        "rollback_snapshot",
        "exec_command",
        "get_exec",
        "cancel_exec",
        "list_mounts",
        "list_mount_workspaces",
        "mount_path",
        "unmount_path",
        "copy_environment",
        "get_memory_pressure",
        "expand_memory",
        "list_session_exec_tasks",
        "run_workflow",
        "read_sandbox",
        "code_write",
    ] = Field(..., description="Runtime action name to execute.")
    sandbox_id: str | None = Field(None, description="Sandbox identifier.")
    snapshot_id: str | None = Field(None, description="Snapshot identifier.")
    exec_id: str | None = Field(None, description="Execution identifier.")
    mount_id: str | None = Field(None, description="Mount identifier.")
    command: str | None = Field(None, description="Command to execute in sandbox.")
    wait: bool | None = Field(None, description="Wait for exec result synchronously.")
    wait_timeout_seconds: int | None = Field(
        None, ge=1, le=300, description="Max seconds to wait when wait=true (default 10)."
    )
    image: str = Field("python:3.11-slim", description="Container image when creating a sandbox.")
    workspace_path: str | None = Field(
        None,
        description="Path relative to SERVER_MOUNT_ALLOWED_ROOT (leading '/' allowed).",
    )
    container_path: str | None = Field(None, description="Container mount path.")
    mode: str = Field("ro", description="Mount mode. Server currently enforces read-only mounts.")
    target_mb: int | None = Field(None, description="Target memory limit in MB.")
    memory_limit_mb: int | None = Field(None, description="Sandbox memory limit in MB.")
    confirm_low_memory: bool | None = Field(
        None,
        description="Confirm sandbox create when host available memory ratio is between 10% and 15%.",
    )
    request_timeout_seconds: float | None = Field(
        None,
        ge=1,
        le=3600,
        description="Optional HTTP timeout override for create_sandbox.",
    )
    session_id: str | None = Field(None, description="Session id for session history query.")
    limit: int = Field(20, ge=1, le=200, description="Pagination page size.")
    cursor: str | None = Field(None, description="Pagination cursor token.")
    workflow_steps: list[dict[str, Any]] | None = Field(
        None,
        description="Ordered workflow steps for run_workflow. Each step must include sandbox_id.",
    )
    read_command: str | None = Field(None, description="Whitelisted sandbox read command.")
    read_args: list[str] | None = Field(None, description="Arguments for read command.")
    cwd: str | None = Field(None, description="Working directory for read command.")
    timeout_seconds: int | None = Field(None, ge=1, le=300, description="Read operation timeout seconds.")
    max_output_bytes: int | None = Field(None, ge=1, le=1048576, description="Max captured bytes per stream.")
    max_lines: int | None = Field(None, ge=1, le=5000, description="Max lines kept in stdout/stderr.")
    output_offset: int | None = Field(None, ge=0, description="Opaque output offset hint.")
    write_mode: Literal["context_edit", "replace_file"] | None = Field(
        None,
        description="code_write mode: 'context_edit' for server-side context locate+replace, 'replace_file' for full file overwrite.",
    )
    before_context: str | None = Field(
        None,
        description="For context_edit: optional lines before old_text to help unique locate.",
    )
    old_text: str | None = Field(
        None,
        description="For context_edit: original snippet to replace (required).",
    )
    new_text: str | None = Field(
        None,
        description="For context_edit: replacement snippet (required).",
    )
    after_context: str | None = Field(
        None,
        description="For context_edit: optional lines after old_text to help unique locate.",
    )
    occurrence: int | None = Field(
        None,
        ge=1,
        description="For context_edit: 1-based match index when snippet appears multiple times.",
    )
    content: str | None = Field(
        None,
        description="For write_mode=replace_file: raw file content to write.",
    )
    sandbox_path: str | None = Field(
        None,
        description="Absolute target file path inside sandbox for code_write.",
    )
    build_language: str | None = Field(
        None,
        description="Optional build language for code_write (e.g. go/python/node/rust/java).",
    )

    @model_validator(mode="after")
    def _validate_action_requirements(self):
        if self.action != "code_write":
            if self.action == "run_workflow":
                if not self.workflow_steps:
                    raise ValueError("workflow_steps is required when action=run_workflow")
                for idx, step in enumerate(self.workflow_steps):
                    if not isinstance(step, dict):
                        raise ValueError(f"workflow_steps[{idx}] must be an object")
                    sandbox_id = step.get("sandbox_id")
                    if sandbox_id is None or str(sandbox_id).strip() == "":
                        raise ValueError(f"workflow_steps[{idx}].sandbox_id is required")
            if self.action == "copy_environment":
                if self.workspace_path is None or self.workspace_path.strip() == "":
                    raise ValueError("workspace_path is required when action=copy_environment")
                if self.sandbox_path is None or self.sandbox_path.strip() == "":
                    raise ValueError("sandbox_path is required when action=copy_environment")
            return self
        mode = (self.write_mode or "context_edit").strip()
        if mode == "context_edit":
            if self.sandbox_path is None or self.sandbox_path.strip() == "":
                raise ValueError("sandbox_path is required when action=code_write and write_mode=context_edit")
            if self.old_text is None or self.old_text.strip() == "":
                raise ValueError("old_text is required when action=code_write and write_mode=context_edit")
            if self.new_text is None or self.new_text.strip() == "":
                raise ValueError("new_text is required when action=code_write and write_mode=context_edit")
            return self
        if mode == "replace_file":
            if self.sandbox_path is None or self.sandbox_path.strip() == "":
                raise ValueError("sandbox_path is required when action=code_write and write_mode=replace_file")
            if self.content is None:
                raise ValueError("content is required when action=code_write and write_mode=replace_file")
            return self
        raise ValueError("write_mode must be one of: context_edit, replace_file")


class SleighLangChainClient:
    def __init__(self, base_url: str, timeout_seconds: float = 30.0):
        self.client = SleighClient(base_url=base_url, timeout_seconds=timeout_seconds)

    def _dispatch(self, data: SleighToolInput) -> dict:
        action = data.action
        token = data.session_token

        if action == "create_session_token":
            return self.client.create_session_token()
        token = _require(token, "session_token")

        if action == "create_sandbox":
            return self.client.create_sandbox(
                session_token=token,
                image=data.image,
                memory_limit_mb=data.memory_limit_mb,
                confirm_low_memory=data.confirm_low_memory,
                request_timeout_seconds=data.request_timeout_seconds,
            )
        if action == "list_sandboxes":
            return self.client.list_sandboxes(session_token=token)
        if action == "get_sandbox":
            return self.client.get_sandbox(session_token=token, sandbox_id=_require(data.sandbox_id, "sandbox_id"))
        if action == "delete_sandbox":
            return self.client.delete_sandbox(session_token=token, sandbox_id=_require(data.sandbox_id, "sandbox_id"))
        if action == "create_snapshot":
            return self.client.create_snapshot(session_token=token, sandbox_id=_require(data.sandbox_id, "sandbox_id"))
        if action == "list_snapshots":
            return self.client.list_snapshots(session_token=token, sandbox_id=_require(data.sandbox_id, "sandbox_id"))
        if action == "rollback_snapshot":
            return self.client.rollback_snapshot(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                snapshot_id=_require(data.snapshot_id, "snapshot_id"),
            )
        if action == "exec_command":
            return self.client.exec_command(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                command=_require(data.command, "command"),
                wait=data.wait,
                wait_timeout_seconds=data.wait_timeout_seconds,
            )
        if action == "get_exec":
            return self.client.get_exec(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                exec_id=_require(data.exec_id, "exec_id"),
            )
        if action == "cancel_exec":
            return self.client.cancel_exec(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                exec_id=_require(data.exec_id, "exec_id"),
            )
        if action == "list_mounts":
            return self.client.list_mounts(session_token=token, sandbox_id=_require(data.sandbox_id, "sandbox_id"))
        if action == "list_mount_workspaces":
            return self.client.list_mount_workspaces(session_token=token)
        if action == "mount_path":
            return self.client.mount_path(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                workspace_path=_require(data.workspace_path, "workspace_path"),
                container_path=_require(data.container_path, "container_path"),
            )
        if action == "unmount_path":
            return self.client.unmount_path(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                mount_id=_require(data.mount_id, "mount_id"),
            )
        if action == "copy_environment":
            return self.client.copy_environment(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                workspace_path=_require(data.workspace_path, "workspace_path"),
                sandbox_path=_require(data.sandbox_path, "sandbox_path"),
            )
        if action == "get_memory_pressure":
            return self.client.get_memory_pressure(
                session_token=token, sandbox_id=_require(data.sandbox_id, "sandbox_id")
            )
        if action == "expand_memory":
            return self.client.expand_memory(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                target_mb=int(_require(data.target_mb, "target_mb")),
            )
        if action == "list_session_exec_tasks":
            return self.client.list_session_exec_tasks(
                session_token=token,
                session_id=data.session_id,
                limit=data.limit,
                cursor=data.cursor,
            )
        if action == "run_workflow":
            return self.client.run_workflow(
                session_token=token,
                steps=_require(data.workflow_steps, "workflow_steps"),
            )
        if action == "read_sandbox":
            return self.client.read_sandbox(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                command=_require(data.read_command, "read_command"),
                args=data.read_args,
                cwd=data.cwd,
                timeout_seconds=data.timeout_seconds,
                max_output_bytes=data.max_output_bytes,
                max_lines=data.max_lines,
                output_offset=data.output_offset,
            )
        if action == "code_write":
            mode = (data.write_mode or "context_edit").strip()
            return self.client.code_write(
                session_token=token,
                sandbox_id=_require(data.sandbox_id, "sandbox_id"),
                sandbox_path=_require(data.sandbox_path, "sandbox_path"),
                old_text=data.old_text,
                new_text=data.new_text,
                before_context=data.before_context,
                after_context=data.after_context,
                occurrence=data.occurrence,
                write_mode=mode,
                content=data.content,
                build_language=data.build_language,
                timeout_seconds=data.timeout_seconds,
                max_output_bytes=data.max_output_bytes,
                max_lines=data.max_lines,
            )
        raise ValueError(f"unsupported action: {action}")

    def as_langchain_tool(
        self,
        name: str = "sleigh-runtime",
        description: str | None = None,
        return_direct: bool = True,
        handle_tool_error: bool = True,
    ):
        try:
            from langchain_core.tools import StructuredTool
        except Exception as exc:  # pragma: no cover
            raise RuntimeError(
                "LangChain is not installed. Run: pip install 'sleigh-sdk[langchain]'"
            ) from exc

        if description is None:
            description = (
                "Sleigh runtime unified tool. "
                "Use action to call sandbox create/exec/snapshot/mount/memory/history APIs. "
                "First call action=create_session_token, then pass session_token to other actions. "
                "For run_workflow, every step must include sandbox_id. "
                "For code_write, default to write_mode=context_edit with before/old/new/after raw code snippets; "
                "use write_mode=replace_file with sandbox_path/content when full overwrite is needed."
            )

        def runtime_tool(**kwargs) -> str:
            try:
                payload = SleighToolInput(**kwargs)
                result = self._dispatch(payload)
                return json.dumps(result, ensure_ascii=False)
            except Exception as exc:  # pragma: no cover
                return f"sleigh sdk error: {exc}"

        return StructuredTool.from_function(
            func=runtime_tool,
            name=name,
            description=description,
            args_schema=SleighToolInput,
            return_direct=return_direct,
            handle_tool_error=handle_tool_error,
        )


def _require(value, field_name: str):
    if value is None:
        raise ValueError(f"{field_name} is required for this action")
    if isinstance(value, str) and value.strip() == "":
        raise ValueError(f"{field_name} is required for this action")
    return value
