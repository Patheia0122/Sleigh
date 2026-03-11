from __future__ import annotations

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
        return client.create_session_token()

    @mcp.tool()
    def create_sandbox(
        session_token: str,
        image: str = "alpine:3.20",
        memory_limit_mb: int | None = None,
        confirm_low_memory: bool | None = None,
        request_timeout_seconds: float | None = None,
    ):
        return client.create_sandbox(
            session_token=session_token,
            image=image,
            memory_limit_mb=memory_limit_mb,
            confirm_low_memory=confirm_low_memory,
            request_timeout_seconds=request_timeout_seconds,
        )

    @mcp.tool()
    def list_sandboxes(session_token: str):
        return client.list_sandboxes(session_token=session_token)

    @mcp.tool()
    def get_sandbox(session_token: str, sandbox_id: str):
        return client.get_sandbox(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def delete_sandbox(session_token: str, sandbox_id: str):
        return client.delete_sandbox(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def exec_command(
        session_token: str,
        sandbox_id: str,
        command: str,
        wait: bool | None = None,
        wait_timeout_seconds: int | None = None,
    ):
        return client.exec_command(
            session_token=session_token,
            sandbox_id=sandbox_id,
            command=command,
            wait=wait,
            wait_timeout_seconds=wait_timeout_seconds,
        )

    @mcp.tool()
    def get_exec(session_token: str, sandbox_id: str, exec_id: str):
        return client.get_exec(session_token=session_token, sandbox_id=sandbox_id, exec_id=exec_id)

    @mcp.tool()
    def create_snapshot(session_token: str, sandbox_id: str):
        return client.create_snapshot(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def rollback_snapshot(session_token: str, sandbox_id: str, snapshot_id: str):
        return client.rollback_snapshot(
            session_token=session_token,
            sandbox_id=sandbox_id,
            snapshot_id=snapshot_id,
        )

    @mcp.tool()
    def list_mounts(session_token: str, sandbox_id: str):
        return client.list_mounts(session_token=session_token, sandbox_id=sandbox_id)

    @mcp.tool()
    def mount_path(session_token: str, sandbox_id: str, workspace_path: str, container_path: str, mode: str = "rw"):
        return client.mount_path(
            session_token=session_token,
            sandbox_id=sandbox_id,
            workspace_path=workspace_path,
            container_path=container_path,
            mode=mode,
        )

    @mcp.tool()
    def unmount_path(session_token: str, sandbox_id: str, mount_id: str):
        return client.unmount_path(session_token=session_token, sandbox_id=sandbox_id, mount_id=mount_id)

    @mcp.tool()
    def list_session_exec_tasks(session_token: str, session_id: str | None = None, limit: int = 20, cursor: str | None = None):
        return client.list_session_exec_tasks(
            session_token=session_token,
            session_id=session_id,
            limit=limit,
            cursor=cursor,
        )

    @mcp.tool()
    def run_workflow(session_token: str, steps: list[dict]):
        return client.run_workflow(session_token=session_token, steps=steps)

    @mcp.tool()
    def read_sandbox(
        session_token: str,
        sandbox_id: str,
        command: str,
        args: list[str] | None = None,
        cwd: str | None = None,
        timeout_seconds: int | None = None,
        max_output_bytes: int | None = None,
        max_lines: int | None = None,
        output_offset: int | None = None,
    ):
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
    def patch_workspace(
        session_token: str,
        sandbox_id: str,
        sandbox_path: str,
        patch: str | None = None,
        write_mode: str | None = None,
        target_file_path: str | None = None,
        content: str | None = None,
        build_language: str | None = None,
        timeout_seconds: int | None = None,
        max_output_bytes: int | None = None,
        max_lines: int | None = None,
    ):
        return client.patch_workspace(
            session_token=session_token,
            sandbox_id=sandbox_id,
            sandbox_path=sandbox_path,
            patch=patch,
            write_mode=write_mode,
            target_file_path=target_file_path,
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
