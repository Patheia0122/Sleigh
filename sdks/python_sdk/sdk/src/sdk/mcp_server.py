from __future__ import annotations

from typing import Annotated

from pydantic import Field

from .client import SleighClient


def build_mcp_server(base_url: str, timeout_seconds: float = 30.0):
    try:
        from mcp.server.fastmcp import FastMCP
    except Exception as exc:  # pragma: no cover
        raise RuntimeError(
            "MCP SDK is not installed. Run: pip install 'sleigh-sdk[mcp]'"
        ) from exc

    client = SleighClient(base_url=base_url, timeout_seconds=timeout_seconds)
    mcp = FastMCP("sleigh-runtime")

    @mcp.tool()
    def create_session_token():
        """Issue a new session token. Call this first."""
        return client.create_session_token()

    @mcp.tool()
    def create_sandbox(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        image: Annotated[str, Field(description="Container image for new sandbox.")] = "python:3.11-slim",
        memory_limit_mb: Annotated[int | None, Field(description="Optional sandbox memory limit in MB.")] = None,
        confirm_low_memory: Annotated[
            bool | None,
            Field(description="Required when host available memory ratio is between 10% and 15%."),
        ] = None,
        request_timeout_seconds: Annotated[
            float | None,
            Field(description="Optional HTTP timeout override for create_sandbox request."),
        ] = None,
    ):
        """Create a sandbox for the current session."""
        return client.create_sandbox(
            session_token=session_token,
            image=image,
            memory_limit_mb=memory_limit_mb,
            confirm_low_memory=confirm_low_memory,
            request_timeout_seconds=request_timeout_seconds,
        )

    @mcp.tool()
    def list_sandboxes(session_token: Annotated[str, Field(description="Session token from create_session_token.")]):
        """List sandboxes visible to current session."""
        return client.list_sandboxes(session_token=session_token)

    @mcp.tool()
    def get_sandbox(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
    ):
        """Get sandbox metadata by sandbox_id."""
        return client.get_sandbox(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def delete_sandbox(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id to delete.")],
    ):
        """Delete a sandbox by sandbox_id."""
        return client.delete_sandbox(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def exec_command(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        command: Annotated[str, Field(description="Shell command executed inside sandbox.")],
        wait: Annotated[bool | None, Field(description="Wait synchronously for terminal result.")] = None,
        wait_timeout_seconds: Annotated[
            int | None,
            Field(description="Wait timeout seconds when wait=true. Default is 10."),
        ] = None,
    ):
        """Execute a shell command in sandbox (optional synchronous wait)."""
        return client.exec_command(
            session_token=session_token,
            sandbox_id=sandbox_id,
            command=command,
            wait=wait,
            wait_timeout_seconds=wait_timeout_seconds,
        )

    @mcp.tool()
    def get_exec(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        exec_id: Annotated[str, Field(description="Execution id returned by exec_command.")],
    ):
        """Get execution result by exec_id."""
        return client.get_exec(session_token=session_token, sandbox_id=sandbox_id, exec_id=exec_id)

    @mcp.tool()
    def create_snapshot(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
    ):
        """Create a snapshot for sandbox."""
        return client.create_snapshot(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def rollback_snapshot(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        snapshot_id: Annotated[str, Field(description="Snapshot id to rollback to.")],
    ):
        """Rollback sandbox to an existing snapshot_id."""
        return client.rollback_snapshot(
            session_token=session_token,
            sandbox_id=sandbox_id,
            snapshot_id=snapshot_id,
        )

    @mcp.tool()
    def list_mounts(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
    ):
        """List current mounts attached to sandbox."""
        return client.list_mounts(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def mount_path(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        workspace_path: Annotated[
            str,
            Field(description="Mount-zone path relative to SERVER_MOUNT_ALLOWED_ROOT."),
        ],
        container_path: Annotated[str, Field(description="Absolute target path inside sandbox.")],
    ):
        """Mount a host mount-zone path into sandbox (read-only)."""
        return client.mount_path(
            session_token=session_token,
            sandbox_id=sandbox_id,
            workspace_path=workspace_path,
            container_path=container_path,
        )

    @mcp.tool()
    def list_mount_workspaces(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")]
    ):
        """List available directories under SERVER_MOUNT_ALLOWED_ROOT."""
        return client.list_mount_workspaces(session_token=session_token)

    @mcp.tool()
    def list_environment_workspaces(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")]
    ):
        """List available directories under SERVER_ENV_ALLOWED_ROOT."""
        return client.list_environment_workspaces(session_token=session_token)

    @mcp.tool()
    def copy_environment(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        environment_path: Annotated[
            str,
            Field(description="Environment-zone path relative to SERVER_ENV_ALLOWED_ROOT."),
        ],
        sandbox_path: Annotated[
            str,
            Field(description="Absolute destination path inside sandbox (must not be '/')."),
        ],
    ):
        """Copy host environment-zone directory into target sandbox path."""
        return client.copy_environment(
            session_token=session_token,
            sandbox_id=sandbox_id,
            environment_path=environment_path,
            sandbox_path=sandbox_path,
        )

    @mcp.tool()
    def unmount_path(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        mount_id: Annotated[str, Field(description="Mount id from list_mounts response.")],
    ):
        """Detach one mount from sandbox by mount_id."""
        return client.unmount_path(session_token=session_token, sandbox_id=sandbox_id, mount_id=mount_id)

    @mcp.tool()
    def list_session_exec_tasks(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        session_id: Annotated[
            str | None,
            Field(description="Optional session id. Defaults to session_token when omitted."),
        ] = None,
        limit: Annotated[int, Field(description="Page size, default 20.")] = 20,
        cursor: Annotated[str | None, Field(description="Pagination cursor from previous page.")] = None,
    ):
        """List paginated exec history for current session."""
        return client.list_session_exec_tasks(
            session_token=session_token,
            session_id=session_id,
            limit=limit,
            cursor=cursor,
        )

    @mcp.tool()
    def run_workflow(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        steps: Annotated[
            list[dict],
            Field(
                description=(
                    "Ordered workflow steps. Each step must include sandbox_id and action. "
                    "Supported action: create_sandbox, exec_command, create_snapshot, rollback_snapshot, delete_sandbox."
                )
            ),
        ],
    ):
        """Run ordered workflow steps and stop early on failure/timeout."""
        return client.run_workflow(session_token=session_token, steps=steps)

    @mcp.tool()
    def read_sandbox(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        command: Annotated[str, Field(description="Read-only command (must be in server allowlist).")],
        args: Annotated[list[str] | None, Field(description="Arguments for read command.")] = None,
        cwd: Annotated[str | None, Field(description="Optional working directory inside sandbox.")] = None,
        timeout_seconds: Annotated[int | None, Field(description="Execution timeout in seconds.")] = None,
        max_output_bytes: Annotated[int | None, Field(description="Max captured bytes for stdout/stderr.")] = None,
        max_lines: Annotated[int | None, Field(description="Max captured lines for stdout/stderr.")] = None,
        output_offset: Annotated[int | None, Field(description="Optional output offset hint for pagination.")] = None,
    ):
        """Run allowlisted read command in sandbox and return bounded output."""
        return client.read_sandbox(
            session_token=session_token,
            sandbox_id=sandbox_id,
            command=command,
            args=args,
            cwd=cwd,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            max_lines=max_lines,
            output_offset=output_offset,
        )

    @mcp.tool()
    def code_write(
        session_token: Annotated[str, Field(description="Session token from create_session_token.")],
        sandbox_id: Annotated[str, Field(description="Target sandbox_id.")],
        sandbox_path: Annotated[str, Field(description="Absolute target file path inside sandbox.")],
        old_text: Annotated[
            str | None,
            Field(description="For context_edit: snippet to replace (required in context_edit mode)."),
        ] = None,
        new_text: Annotated[
            str | None,
            Field(description="For context_edit: replacement snippet (required in context_edit mode)."),
        ] = None,
        before_context: Annotated[
            str | None,
            Field(description="For context_edit: optional context before old_text to improve matching."),
        ] = None,
        after_context: Annotated[
            str | None,
            Field(description="For context_edit: optional context after old_text to improve matching."),
        ] = None,
        occurrence: Annotated[
            int | None,
            Field(description="For context_edit: 1-based match index when snippet appears multiple times."),
        ] = None,
        write_mode: Annotated[
            str | None,
            Field(description="context_edit (default) or replace_file."),
        ] = None,
        content: Annotated[
            str | None,
            Field(description="For replace_file: full file content (required in replace_file mode)."),
        ] = None,
        build_language: Annotated[
            str | None,
            Field(
                description=(
                    "Optional build language: go/python/node/rust/java. "
                    "If required image is missing, server may pull image first and latency can increase."
                )
            ),
        ] = None,
        timeout_seconds: Annotated[int | None, Field(description="Overall code_write timeout in seconds.")] = None,
        max_output_bytes: Annotated[int | None, Field(description="Max captured bytes for stdout/stderr.")] = None,
        max_lines: Annotated[int | None, Field(description="Max captured lines for stdout/stderr.")] = None,
    ):
        """Apply code changes in sandbox file via context_edit or replace_file modes.

        Optional build_language may trigger image pull when image is missing,
        which can increase request latency.
        """
        return client.code_write(
            session_token=session_token,
            sandbox_id=sandbox_id,
            sandbox_path=sandbox_path,
            old_text=old_text,
            new_text=new_text,
            before_context=before_context,
            after_context=after_context,
            occurrence=occurrence,
            write_mode=write_mode,
            content=content,
            build_language=build_language,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            max_lines=max_lines,
        )

    return mcp


def run_stdio_server(base_url: str, timeout_seconds: float = 30.0):
    mcp = build_mcp_server(base_url=base_url, timeout_seconds=timeout_seconds)
    mcp.run()
