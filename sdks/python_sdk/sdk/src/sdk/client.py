from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from urllib.parse import urljoin

import requests


class SleighClientError(RuntimeError):
    """Raised when runtime server returns an error."""


@dataclass
class SleighClient:
    base_url: str
    timeout_seconds: float = 30.0

    def _request(
        self,
        method: str,
        path: str,
        *,
        query: dict[str, Any] | None = None,
        json_body: dict[str, Any] | None = None,
    ) -> Any:
        url = urljoin(self.base_url.rstrip("/") + "/", path.lstrip("/"))
        response = requests.request(
            method=method,
            url=url,
            params={k: v for k, v in (query or {}).items() if v is not None},
            json=json_body,
            timeout=self.timeout_seconds,
        )

        if response.status_code >= 400:
            try:
                payload = response.json()
            except ValueError:
                payload = {"error": response.text}
            raise SleighClientError(
                f"{method} {path} failed ({response.status_code}): {payload}"
            )

        if response.status_code == 204:
            return {"ok": True}
        if not response.text.strip():
            return {"ok": True}
        try:
            return response.json()
        except ValueError:
            return {"raw": response.text}

    def create_sandbox(
        self,
        *,
        session_token: str,
        image: str = "alpine:3.20",
        labels: dict[str, str] | None = None,
        memory_limit_mb: int | None = None,
        confirm_low_memory: bool | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "session_token": session_token,
            "image": image,
        }
        if labels:
            body["labels"] = labels
        if memory_limit_mb is not None:
            body["memory_limit_mb"] = memory_limit_mb
        if confirm_low_memory is not None:
            body["confirm_low_memory"] = confirm_low_memory
        return self._request("POST", "/sandboxes", json_body=body)

    def list_sandboxes(self, *, session_token: str) -> dict[str, Any]:
        return self._request("GET", "/sandboxes", query={"session_token": session_token})

    def get_sandbox(self, *, session_token: str, sandbox_id: str) -> dict[str, Any]:
        return self._request(
            "GET",
            f"/sandboxes/{sandbox_id}",
            query={"session_token": session_token},
        )

    def delete_sandbox(self, *, session_token: str, sandbox_id: str) -> dict[str, Any]:
        return self._request(
            "DELETE",
            f"/sandboxes/{sandbox_id}",
            query={"session_token": session_token},
        )

    def create_snapshot(self, *, session_token: str, sandbox_id: str) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/snapshots",
            json_body={"session_token": session_token},
        )

    def list_snapshots(self, *, session_token: str, sandbox_id: str) -> dict[str, Any]:
        return self._request(
            "GET",
            f"/sandboxes/{sandbox_id}/snapshots",
            query={"session_token": session_token},
        )

    def rollback_snapshot(
        self, *, session_token: str, sandbox_id: str, snapshot_id: str
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/rollback",
            json_body={"session_token": session_token, "snapshot_id": snapshot_id},
        )

    def exec_command(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        command: str,
        wait: bool | None = None,
        wait_timeout_seconds: int | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {"session_token": session_token, "command": command}
        if wait is not None:
            body["wait"] = wait
        if wait_timeout_seconds is not None:
            body["wait_timeout_seconds"] = wait_timeout_seconds
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/exec",
            json_body=body,
        )

    def get_exec(
        self, *, session_token: str, sandbox_id: str, exec_id: str
    ) -> dict[str, Any]:
        return self._request(
            "GET",
            f"/sandboxes/{sandbox_id}/exec/{exec_id}",
            query={"session_token": session_token},
        )

    def cancel_exec(
        self, *, session_token: str, sandbox_id: str, exec_id: str
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/exec/{exec_id}/cancel",
            query={"session_token": session_token},
        )

    def list_mounts(self, *, session_token: str, sandbox_id: str) -> dict[str, Any]:
        return self._request(
            "GET",
            f"/sandboxes/{sandbox_id}/mounts",
            query={"session_token": session_token},
        )

    def mount_path(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        workspace_path: str,
        container_path: str,
        mode: str = "rw",
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/mounts",
            json_body={
                "session_token": session_token,
                "workspace_path": workspace_path,
                "container_path": container_path,
                "mode": mode,
            },
        )

    def unmount_path(
        self, *, session_token: str, sandbox_id: str, mount_id: str
    ) -> dict[str, Any]:
        return self._request(
            "DELETE",
            f"/sandboxes/{sandbox_id}/mounts/{mount_id}",
            query={"session_token": session_token},
        )

    def get_memory_pressure(
        self, *, session_token: str, sandbox_id: str
    ) -> dict[str, Any]:
        return self._request(
            "GET",
            f"/sandboxes/{sandbox_id}/memory/pressure",
            query={"session_token": session_token},
        )

    def expand_memory(
        self, *, session_token: str, sandbox_id: str, target_mb: int
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/memory/expand",
            json_body={"session_token": session_token, "target_mb": target_mb},
        )

    def list_session_exec_tasks(
        self,
        *,
        session_token: str,
        session_id: str | None = None,
        limit: int = 20,
        cursor: str | None = None,
    ) -> dict[str, Any]:
        if session_id is None or str(session_id).strip() == "":
            session_id = session_token
        return self._request(
            "GET",
            f"/sessions/{session_id}/exec-tasks",
            query={
                "session_token": session_token,
                "limit": limit,
                "cursor": cursor,
            },
        )

    def run_workflow(self, *, session_token: str, steps: list[dict[str, Any]]) -> dict[str, Any]:
        return self._request(
            "POST",
            "/workflow/run",
            json_body={"session_token": session_token, "steps": steps},
        )

    def read_sandbox(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        command: str,
        args: list[str] | None = None,
        cwd: str | None = None,
        timeout_seconds: int | None = None,
        max_output_bytes: int | None = None,
        max_lines: int | None = None,
        output_offset: int | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "session_token": session_token,
            "command": command,
        }
        if args is not None:
            body["args"] = args
        if cwd is not None:
            body["cwd"] = cwd
        if timeout_seconds is not None:
            body["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not None:
            body["max_output_bytes"] = max_output_bytes
        if max_lines is not None:
            body["max_lines"] = max_lines
        if output_offset is not None:
            body["output_offset"] = output_offset
        return self._request("POST", f"/sandboxes/{sandbox_id}/ops/read", json_body=body)

    def patch_workspace(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        workspace_path: str,
        patch: str,
        build_language: str | None = None,
        timeout_seconds: int | None = None,
        max_output_bytes: int | None = None,
        max_lines: int | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "session_token": session_token,
            "workspace_path": workspace_path,
            "patch": patch,
        }
        if build_language is not None:
            body["build_language"] = build_language
        if timeout_seconds is not None:
            body["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not None:
            body["max_output_bytes"] = max_output_bytes
        if max_lines is not None:
            body["max_lines"] = max_lines
        return self._request("POST", f"/sandboxes/{sandbox_id}/ops/patch", json_body=body)
