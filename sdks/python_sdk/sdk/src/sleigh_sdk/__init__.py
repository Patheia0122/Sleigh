from .client import SleighClient, SleighClientError

def _missing_optional(feature: str, extra: str, exc: Exception):
    def _raiser(*args, **kwargs):
        raise RuntimeError(
            f"{feature} requires optional dependencies. Install with: pip install 'sleigh-sdk[{extra}]'"
        ) from exc

    return _raiser


try:
    from .langchain_tool import SleighLangChainClient
except Exception as _langchain_exc:  # pragma: no cover - optional dependency
    SleighLangChainClient = _missing_optional("SleighLangChainClient", "langchain", _langchain_exc)

try:
    from .mcp_server import build_mcp_server, run_stdio_server
except Exception as _mcp_exc:  # pragma: no cover - optional dependency
    build_mcp_server = _missing_optional("build_mcp_server", "mcp", _mcp_exc)
    run_stdio_server = _missing_optional("run_stdio_server", "mcp", _mcp_exc)

__all__ = [
    "SleighClient",
    "SleighClientError",
    "SleighLangChainClient",
    "build_mcp_server",
    "run_stdio_server",
]
