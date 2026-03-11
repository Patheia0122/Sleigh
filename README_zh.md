# Sleigh

[English](README.md)

![Sleigh Logo](docs/assets/Sleigh_logo.png)

**Sleigh — Agent-native elastic sandbox runtime.**

Sleigh 是面向 Agent 长周期、强状态、资源波动任务的弹性沙箱运行时。
它提供执行控制、故障恢复和可观测能力，用于构建稳定可持续的 Agent 执行闭环。

## 解决的问题

- 基于会话隔离沙箱可见性，避免跨会话越权访问
- 提供命令执行、状态查询与取消能力
- 支持 exec 同步等待模式（wait）
- 支持快照与回滚，提高任务可恢复性
- 提供内存压力观测与扩容控制接口
- 支持带权限边界的宿主机目录挂载
- 支持单请求内的有序工作流批量执行
- 支持沙箱内只读操作（命令白名单 + 截断分页）
- 支持沙箱内AI编程的patch流水线（`git apply` + `pre-commit` + 可选 build）
- 统一使用 OTEL 做运行时可观测
- 执行历史支持游标分页与 TTL 自动清理

## 运行模型

- **服务端运行在宿主机**（systemd 服务模式）
- **沙箱运行在 Docker 容器**
- **会话级访问隔离** 通过 `session_token`
- **快照语义优先工作目录**（必要时容器兜底）

## 安装

| 组件 | 推荐命令 |
| --- | --- |
| 服务端（宿主机服务） | `./install_server.sh` |
| Python 客户端（基础） | `pip install sleigh-sdk` |
| Python 客户端 + LangChain | `pip install "sleigh-sdk[langchain]"` |
| Python 客户端 + MCP | `pip install "sleigh-sdk[mcp]"` |

### 安装服务端（宿主机服务模式）

```bash
./install_server.sh
```

安装脚本会：

- 开头让你选择语言（English / 中文）
- 交互配置挂载白名单根目录
- 在宿主机构建服务端二进制
- 安装并启动 `systemd` 服务 `sleigh.service`

安装后常用命令：

```bash
sudo systemctl status sleigh.service
sudo journalctl -u sleigh.service -f
```

### 安装 Python 客户端（pip）

```bash
pip install sleigh-sdk
```

导入方式：

```python
from sdk import SleighClient
```

更多说明见：`sdks/python_sdk/README.md`。

## 本地开发模式

仓库仍提供 `docker compose` 便于本地调试：

```bash
docker compose up --build
```

> 生产建议：使用 `install_server.sh` 宿主机服务模式。

## API 核心能力

- `POST /sandboxes` 创建沙箱
- `POST /sessions/token` 由服务端签发会话令牌
- `GET /sandboxes` 列出当前会话沙箱
- `POST /sandboxes/{id}/exec` 执行命令
- `POST /workflow/run` 按序执行多步骤工作流
- `POST /sandboxes/{id}/snapshots` 创建快照
- `POST /sandboxes/{id}/rollback` 回滚快照
- `GET /sandboxes/{id}/memory/pressure` 查询内存压力
- `POST /sandboxes/{id}/memory/expand` 请求扩容
- `POST /sandboxes/{id}/ops/read` 沙箱读操作（同步，命令白名单）
- `POST /sandboxes/{id}/ops/patch` 沙箱内AI编程 patch 操作（作用于其挂载工作区）
- `GET /sessions/{sessionId}/exec-tasks` 执行历史分页

挂载写操作使用 `workspace_path`（相对 `SERVER_MOUNT_ALLOWED_ROOT`，允许带前导 `/`），服务端会在内部解析为宿主机绝对路径。  
patch 写操作使用 `sandbox_path`（沙箱内绝对路径），服务端会将该目录导出到宿主机临时区执行 patch 后再同步回沙箱。
patch 还支持 `write_mode=replace_file`，可直接用原始代码做整文件覆盖。
patch 质量检查策略：有 `.pre-commit-config.yaml` 时跑 `pre-commit`，否则自动检测语言执行兜底检查。
`patch` 参数必须是 unified diff 补丁文本（不是原始代码文本），支持 `*** Begin Patch` 或 `diff --git` 头格式。

受保护接口统一使用 `session_token`（请求体或 query）。  
推荐流程：先调用 `POST /sessions/token` 获取令牌，再在同一任务/会话中复用该令牌。

读写类接口统一返回 AI 友好 envelope：

- `status`, `duration_ms`, `timed_out`, `truncated`
- `stdout`, `stderr`, `error`
- 可选 `omitted_bytes`, `next_offset` 以及接口特定 artifacts

## 关键运行配置

通过 `install_server.sh` 交互配置并写入 `sleigh.env`。

- `SERVER_ADDR` 服务监听地址
- `SERVER_MOUNT_ALLOWED_ROOT` 挂载白名单根目录
- `WARM_POOL_SIZE` / `WARM_POOL_IMAGE` / `WARM_POOL_MEMORY_MB`
- `EXEC_TASK_TTL_DAYS` 与 `EXEC_CLEANUP_INTERVAL_SECONDS`
- `SANDBOX_IDLE_TTL_DAYS` 空闲沙箱回收阈值（默认 `14` 天）
- `SERVER_OTEL_EXPORTER_OTLP_ENDPOINT` 可选 OTLP gRPC 地址（留空关闭 OTEL）
- `IMAGE_PULL_TIMEOUT_SECONDS` 沙箱创建时镜像拉取超时

## 可观测与稳定性

- `create_sandbox` 响应包含 `startup_latency_ms`
- 创建响应包含镜像拉取观测字段：
  - `image_pull_triggered`
  - `image_pull_status`
  - `image_pull_duration_ms`
- 支持可选 OTLP gRPC OTEL 追踪（沙箱生命周期）
- 定时空闲沙箱清理会回收超时会话沙箱并输出审计日志

## SDK 集成

- **LangChain Tool 适配**：`sdk.SleighLangChainClient`
- **MCP 适配**：`sdk.run_stdio_server`
- 文档：`sdks/python_sdk/README.md`

## 当前状态

当前版本已形成可用最小闭环，重点覆盖：

- 宿主机服务化部署
- Docker 沙箱执行
- 会话隔离
- 恢复与可观测基础能力