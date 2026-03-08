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
    def create_sandbox(session_token: str, image: str = "alpine:3.20", memory_limit_mb: int | None = None):
        return client.create_sandbox(
            session_token=session_token,
            image=image,
            memory_limit_mb=memory_limit_mb,
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
    def exec_command(session_token: str, sandbox_id: str, command: str):
        return client.exec_command(session_token=session_token, sandbox_id=sandbox_id, command=command)

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
    def mount_path(session_token: str, sandbox_id: str, host_path: str, container_path: str, mode: str = "rw"):
        return client.mount_path(
            session_token=session_token,
            sandbox_id=sandbox_id,
            host_path=host_path,
            container_path=container_path,
            mode=mode,
        )

    @mcp.tool()
    def unmount_path(session_token: str, sandbox_id: str, mount_id: str):
        return client.unmount_path(session_token=session_token, sandbox_id=sandbox_id, mount_id=mount_id)

    @mcp.tool()
    def list_session_exec_tasks(session_token: str, session_id: str, limit: int = 20, cursor: str | None = None):
        return client.list_session_exec_tasks(
            session_token=session_token,
            session_id=session_id,
            limit=limit,
            cursor=cursor,
        )

    return mcp


def run_stdio_server(base_url: str, timeout_seconds: float = 30.0):
    mcp = build_mcp_server(base_url=base_url, timeout_seconds=timeout_seconds)
    mcp.run()
