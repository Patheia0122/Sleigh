# Sleigh

[English](README.md)

![Sleigh Logo](data:image/svg+xml;base64,PHN2ZyB4bWxucz0naHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmcnIHZpZXdCb3g9JzAgMCA5NjAgMjgwJz48ZGVmcz48bGluZWFyR3JhZGllbnQgaWQ9J2cnIHgxPScwJyB5MT0nMCcgeDI9JzEnIHkyPScxJz48c3RvcCBzdG9wLWNvbG9yPScjMGYxNzJhJyBvZmZzZXQ9JzAnLz48c3RvcCBzdG9wLWNvbG9yPScjMWUyOTNiJyBvZmZzZXQ9JzEnLz48L2xpbmVhckdyYWRpZW50PjwvZGVmcz48cmVjdCB3aWR0aD0nOTYwJyBoZWlnaHQ9JzI4MCcgcng9JzI0JyBmaWxsPSd1cmwoI2cpJy8+PHRleHQgeD0nNTYnIHk9JzEyMCcgZm9udC1zaXplPSc2NCcgZm9udC1mYW1pbHk9J1NlZ29lIFVJLEFyaWFsLHNhbnMtc2VyaWYnIGZpbGw9JyNmZmZmZmYnIGZvbnQtd2VpZ2h0PSc3MDAnPlNsZWlnaDwvdGV4dD48dGV4dCB4PSc1NicgeT0nMTg1JyBmb250LXNpemU9JzI4JyBmb250LWZhbWlseT0nU2Vnb2UgVUksQXJpYWwsc2Fucy1zZXJpZicgZmlsbD0nI2NiZDVlMSc+QWdlbnQtbmF0aXZlIGVsYXN0aWMgc2FuZGJveCBydW50aW1lPC90ZXh0Pjwvc3ZnPg==)

**Sleigh — Agent-native elastic sandbox runtime.**

Sleigh 是面向 Agent 长周期、强状态、资源波动任务的弹性沙箱运行时。
它提供执行控制、故障恢复和可观测能力，用于构建稳定可持续的 Agent 执行闭环。

## 解决的问题

- 基于会话隔离沙箱可见性，避免跨会话越权访问
- 提供命令执行、状态查询与取消能力
- 支持快照与回滚，提高任务可恢复性
- 提供内存压力观测与扩容控制接口
- 支持带权限边界的宿主机目录挂载
- 事件推送具备有限重试与退避
- 执行历史支持游标分页与 TTL 自动清理

## 运行模型

- **服务端运行在宿主机**（systemd 服务模式）
- **沙箱运行在 Docker 容器**
- **会话级访问隔离** 通过 `session_token`
- **快照语义优先工作目录**（必要时容器兜底）

## 安装（宿主机服务模式）

执行：

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

## 本地开发模式

仓库仍提供 `docker compose` 便于本地调试：

```bash
docker compose up --build
```

> 生产建议：使用 `install_server.sh` 宿主机服务模式。

## API 核心能力

- `POST /sandboxes` 创建沙箱
- `GET /sandboxes` 列出当前会话沙箱
- `POST /sandboxes/{id}/exec` 执行命令
- `POST /sandboxes/{id}/snapshots` 创建快照
- `POST /sandboxes/{id}/rollback` 回滚快照
- `GET /sandboxes/{id}/memory/pressure` 查询内存压力
- `POST /sandboxes/{id}/memory/expand` 请求扩容
- `GET /sessions/{sessionId}/exec-tasks` 执行历史分页

受保护接口统一使用 `session_token`（请求体或 query）。

## Python SDK

仓库内置 Python SDK（`python_sdk/`），提供两种集成形态：

- **LangChain Tool 适配**：`sleigh_sdk.SleighLangChainClient`
- **MCP 适配**：`sleigh_sdk.run_stdio_server`

具体安装与用法见 `python_sdk/README.md`。

## 当前状态

当前版本已形成可用最小闭环，重点覆盖：

- 宿主机服务化部署
- Docker 沙箱执行
- 会话隔离
- 恢复与可观测基础能力