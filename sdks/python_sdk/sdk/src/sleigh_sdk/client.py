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
        timeout_seconds: float | None = None,
    ) -> Any:
        url = urljoin(self.base_url.rstrip("/") + "/", path.lstrip("/"))
        effective_timeout = self.timeout_seconds if timeout_seconds is None else timeout_seconds
        try:
            response = requests.request(
                method=method,
                url=url,
                params={k: v for k, v in (query or {}).items() if v is not None},
                json=json_body,
                timeout=effective_timeout,
            )
        except requests.exceptions.Timeout as exc:
            raise SleighClientError(
                "request timed out before server finished. "
                "If this is a wait-style operation, increase wait_timeout_seconds in the action "
                "or increase SLEIGH_RUNTIME_TIMEOUT_SECONDS (default 30)."
            ) from exc
        except requests.exceptions.RequestException as exc:
            raise SleighClientError(f"{method} {path} request failed: {exc}") from exc

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
        image: str = "python:3.11-slim",
        labels: dict[str, str] | None = None,
        memory_limit_mb: int | None = None,
        confirm_low_memory: bool | None = None,
        auto_expand_memory: bool | None = None,
        image_pull_policy: str | None = "notify",
        request_timeout_seconds: float | None = None,
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
        if auto_expand_memory is not None:
            body["auto_expand_memory"] = auto_expand_memory
        if image_pull_policy is not None:
            body["image_pull_policy"] = image_pull_policy
        timeout_seconds = request_timeout_seconds
        if timeout_seconds is None:
            timeout_seconds = max(self.timeout_seconds, 180.0)
        return self._request(
            "POST",
            "/sandboxes",
            json_body=body,
            timeout_seconds=timeout_seconds,
        )

    def create_session_token(self) -> dict[str, Any]:
        return self._request("POST", "/sessions/token")

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
        self, *, session_token: str, sandbox_id: str, snapshot_id: str, auto_expand: bool | None = None
    ) -> dict[str, Any]:
        body: dict[str, Any] = {"session_token": session_token, "snapshot_id": snapshot_id}
        if auto_expand is not None:
            body["auto_expand"] = auto_expand
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/rollback",
            json_body=body,
        )

    def exec_command(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        command: str,
        wait: bool | None = None,
        wait_timeout_seconds: int | None = None,
        webhook_url: str | None = None,
        request_timeout_seconds: float | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {"session_token": session_token, "command": command}
        if wait is not None:
            body["wait"] = wait
        if wait_timeout_seconds is not None:
            body["wait_timeout_seconds"] = wait_timeout_seconds
        if webhook_url is not None and str(webhook_url).strip() != "":
            body["webhook_url"] = webhook_url
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/exec",
            json_body=body,
            timeout_seconds=request_timeout_seconds,
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

    def subscribe_exec_webhook(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        exec_id: str,
        webhook_url: str,
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            "/webhooks/exec/subscribe",
            json_body={
                "session_token": session_token,
                "sandbox_id": sandbox_id,
                "exec_id": exec_id,
                "webhook_url": webhook_url,
            },
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
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/mounts",
            json_body={
                "session_token": session_token,
                "workspace_path": workspace_path,
                "container_path": container_path,
            },
        )

    def list_mount_workspaces(self, *, session_token: str) -> dict[str, Any]:
        return self._request(
            "GET",
            "/mounts/workspaces",
            query={"session_token": session_token},
        )

    def list_environment_workspaces(self, *, session_token: str) -> dict[str, Any]:
        return self._request(
            "GET",
            "/environments/workspaces",
            query={"session_token": session_token},
        )

    def copy_environment(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        environment_path: str,
        sandbox_path: str,
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/environment/copy",
            json_body={
                "session_token": session_token,
                "environment_path": environment_path,
                "sandbox_path": sandbox_path,
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
        self,
        *,
        session_token: str,
        sandbox_id: str,
        target_mb: int | None = None,
        auto_expand: bool | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {"session_token": session_token}
        if target_mb is not None:
            body["target_mb"] = target_mb
        if auto_expand is not None:
            body["auto_expand"] = auto_expand
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/memory/expand",
            json_body=body,
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
        if not steps:
            raise ValueError("steps is required for run_workflow")
        for idx, step in enumerate(steps):
            if not isinstance(step, dict):
                raise ValueError(f"steps[{idx}] must be an object")
            sandbox_id = step.get("sandbox_id")
            if sandbox_id is None or str(sandbox_id).strip() == "":
                raise ValueError(f"steps[{idx}].sandbox_id is required")
        total_wait_budget = 0
        for step in steps:
            if not isinstance(step, dict):
                continue
            if str(step.get("action", "")).strip().lower() != "exec_command":
                continue
            wait = step.get("wait")
            if wait is False:
                continue
            ws = step.get("wait_timeout_seconds")
            if ws is None or int(ws) <= 0:
                total_wait_budget += 10
            else:
                total_wait_budget += int(ws)
        http_timeout = None
        if total_wait_budget > 0:
            http_timeout = max(self.timeout_seconds, float(total_wait_budget) + 15.0)
        return self._request(
            "POST",
            "/workflow/run",
            json_body={"session_token": session_token, "steps": steps},
            timeout_seconds=http_timeout,
        )

    def read_sandbox(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        command: str,
        args: list[str] | str | None = None,
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
        server_wait = 10
        if timeout_seconds is not None and timeout_seconds > 0:
            server_wait = timeout_seconds
        http_timeout = max(self.timeout_seconds, float(server_wait) + 5.0)
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/ops/read",
            json_body=body,
            timeout_seconds=http_timeout,
        )

    def code_write(
        self,
        *,
        session_token: str,
        sandbox_id: str,
        sandbox_path: str,
        old_text: str | None = None,
        new_text: str | None = None,
        before_context: str | None = None,
        after_context: str | None = None,
        occurrence: int | None = None,
        write_mode: str | None = None,
        content: str | None = None,
        build_language: str | None = None,
        timeout_seconds: int | None = None,
        max_output_bytes: int | None = None,
        max_lines: int | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "session_token": session_token,
            "sandbox_path": sandbox_path,
        }
        if old_text is not None:
            body["old_text"] = old_text
        if new_text is not None:
            body["new_text"] = new_text
        if before_context is not None:
            body["before_context"] = before_context
        if after_context is not None:
            body["after_context"] = after_context
        if occurrence is not None:
            body["occurrence"] = occurrence
        if write_mode is not None:
            body["write_mode"] = write_mode
        if content is not None:
            body["content"] = content
        if build_language is not None:
            body["build_language"] = build_language
        if timeout_seconds is not None:
            body["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not None:
            body["max_output_bytes"] = max_output_bytes
        if max_lines is not None:
            body["max_lines"] = max_lines
        server_wait = 120
        if timeout_seconds is not None and timeout_seconds > 0:
            server_wait = timeout_seconds
        http_timeout = max(self.timeout_seconds, float(server_wait) + 5.0)
        return self._request(
            "POST",
            f"/sandboxes/{sandbox_id}/ops/code/write",
            json_body=body,
            timeout_seconds=http_timeout,
        )
