"""Sleigh MCP server example (verified usable).

Usage:
1) Install SDK MCP extra:
   pip install "sleigh-sdk[mcp]"

2) Quick connectivity check:
   python examples/mcp_sleigh_runtime_server.py --mode smoke

3) Start stdio MCP server (for Cursor MCP):
   python examples/mcp_sleigh_runtime_server.py --mode serve

Env:
- SLEIGH_RUNTIME_BASE_URL (default: http://127.0.0.1:10122)
- SLEIGH_RUNTIME_TIMEOUT_SECONDS (default: 30)
"""

from __future__ import annotations

import argparse
import json
import os
import sys

from sdk import SleighClient, run_stdio_server


def _runtime_config() -> tuple[str, float]:
    base_url = (os.getenv("SLEIGH_RUNTIME_BASE_URL") or "http://127.0.0.1:10122").strip()
    timeout_seconds = float(os.getenv("SLEIGH_RUNTIME_TIMEOUT_SECONDS", "30"))
    return base_url, timeout_seconds


def smoke_test(base_url: str, timeout_seconds: float) -> int:
    """Verify server availability via normal SDK calls."""
    client = SleighClient(base_url=base_url, timeout_seconds=timeout_seconds)
    try:
        session = client.create_session_token()
    except Exception as exc:
        print(
            "smoke failed: cannot connect to Sleigh runtime. "
            f"base_url={base_url}, error={exc}"
        )
        return 2

    token = session.get("session_token")
    if not token:
        print("smoke failed: create_session_token returned no session_token")
        return 2

    sandboxes = client.list_sandboxes(session_token=token)
    result = {
        "base_url": base_url,
        "session_token_prefix": token[:10] + "...",
        "sandbox_count": len(sandboxes.get("items", [])) if isinstance(sandboxes, dict) else None,
        "status": "ok",
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="Sleigh MCP stdio server example")
    parser.add_argument(
        "--mode",
        choices=("smoke", "serve"),
        default="serve",
        help="smoke: verify runtime API; serve: run MCP stdio server",
    )
    args = parser.parse_args()

    base_url, timeout_seconds = _runtime_config()
    if args.mode == "smoke":
        return smoke_test(base_url=base_url, timeout_seconds=timeout_seconds)

    print(f"Starting Sleigh MCP stdio server on base_url={base_url}", file=sys.stderr)
    run_stdio_server(base_url=base_url, timeout_seconds=timeout_seconds)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
