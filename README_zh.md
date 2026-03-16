# Sleigh

[English](README.md)

![PyPI](https://img.shields.io/pypi/v/sleigh-sdk)
![Python](https://img.shields.io/badge/python-3.10%2B-3776AB?logo=python&logoColor=white)
![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go)

![Sleigh Logo](docs/assets/Sleigh_logo.png)

## Sleigh 是面向 Agent 的可弹性、强文件系统状态的自托管沙箱运行时

你可以在自己的一台高资源服务器上运行 Sleigh 的服务端，然后让多个 Agent 通过 Sleigh 的客户端获得强状态文件系统、可弹性扩容的沙箱。

---

## 为什么选择 Sleigh

Sleigh 面向希望获得“云沙箱能力”，但已有本地高资源服务器，不想被云产品绑定或希望数据不出网的团队。

- 会话级沙箱隔离
- 面向资源波动任务的弹性运行时控制
- 命令执行（异步 + 同步等待）
- 快照与回滚
- 面向长任务的文件系统强状态
- 面向 AI 编程回路的读写接口
- 支持宿主机目录只读挂载，安全复用数据与代码
- 支持环境区目录拷贝进沙箱，快速完成运行环境预热
- 内存压力观测与扩容控制
- OTEL 可观测接入

Sleigh 本身是开源 self-hosted 方案，运行在你自己的基础设施上。

## 适合谁 / 不适合谁

**适合：**

- 已有 Linux 服务器的个人开发者或中小团队
- 需要长周期、高资源消耗、或文件系统有状态 Agent 执行环境的团队
- 希望在自有硬件上获得更可控成本的团队

**可能不适合：**

- 你只想用全托管 SaaS，不希望维护任何服务端
- 你的任务非常轻量，使用云沙箱也不会产生高昂价格

## Self-hosted 与云沙箱对比

| 维度 | Sleigh（self-hosted） | 典型云沙箱 |
| --- | --- | --- |
| 部署方式 | 自有服务器部署 | 厂商托管 |
| 锁定风险 | 低（开源） | 通常更高 |
| 产品使用费 | 无费用 | 通常按量计费 |
| 可控性 | 完整控制权 | 受限于平台能力 |

---

## 2 分钟快速开始

### 前置条件

- Linux 主机
- 可用的 `systemd`
- 已安装并运行 Docker
- `git`、`bash`，以及可拉取依赖/镜像的网络

### 1）安装服务端（宿主机模式）

```bash
git clone git@github.com:Patheia0122/Sleigh.git
cd Sleigh
./install_server.sh
```

安装脚本会构建服务端并启动 `sleigh.service`。

### 2）检查服务状态

```bash
sudo systemctl status sleigh.service
curl -sS http://127.0.0.1:10122/healthz
```

### 3）安装 Python SDK

```bash
pip install sleigh-sdk
```

---

## 最小可跑通闭环（Token -> Sandbox -> Exec）

### 方案 A：curl

1）申请会话令牌：

```bash
TOKEN=$(curl -sS -X POST http://127.0.0.1:10122/sessions/token | python3 -c "import sys,json;print(json.load(sys.stdin)['session_token'])")
```

2）创建沙箱：

```bash
SANDBOX_ID=$(curl -sS -X POST http://127.0.0.1:10122/sandboxes \
  -H "Content-Type: application/json" \
  -d "{\"session_token\":\"$TOKEN\",\"image\":\"python:3.11-slim\"}" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['sandbox_id'])")
```

3）执行命令：

```bash
curl -sS -X POST "http://127.0.0.1:10122/sandboxes/$SANDBOX_ID/exec" \
  -H "Content-Type: application/json" \
  -d "{\"session_token\":\"$TOKEN\",\"command\":\"python -V\",\"wait\":true}"
```

### 方案 B：Python SDK

```python
from sdk import SleighClient

client = SleighClient(base_url="http://127.0.0.1:10122")
token = client.create_session_token()["session_token"]
sandbox_id = client.create_sandbox(session_token=token, image="python:3.11-slim")["sandbox_id"]
result = client.exec_command(
    session_token=token,
    sandbox_id=sandbox_id,
    command="python -V",
    wait=True,
)
print(result)
```

---

## 典型 Agent 场景

- 在隔离容器中运行 coding Agent 任务
- 为长链路 Agent 流程提供 checkpoint/rollback
- 在一台本地高配服务器上集中服务多个 Agent
- 把敏感任务留在自有网络边界内执行
- 运行高内存任务（例如计算生物学中的宏基因组比对，单任务会使用几百GB甚至1TB的内存）
- 将大型参考数据集以只读方式挂载到多个沙箱，避免误修改宿主机数据
- 将预置工具链/环境模板从环境区复制到沙箱，缩短任务冷启动时间

## 核心 API

- `POST /sessions/token`：签发会话令牌
- `POST /sandboxes`：创建沙箱
- `GET /sandboxes`：列出会话沙箱
- `POST /sandboxes/{id}/exec`：执行命令
- `POST /workflow/run`：按序执行多步骤工作流
- `POST /sandboxes/{id}/snapshots`：创建快照
- `POST /sandboxes/{id}/rollback`：回滚快照
- `POST /sandboxes/{id}/ops/read`：只读操作（命令白名单）
- `POST /sandboxes/{id}/ops/code/write`：AI Coding接口，支持格式/lint检查与可选编译校验
- `POST /sandboxes/{id}/environment/copy`：环境区目录拷贝入沙箱

## 运行模型

- 服务端运行在宿主机（`systemd`）
- 沙箱运行在 Docker 容器
- 受保护接口统一要求 `session_token`

## Python 集成安装

```bash
pip install sleigh-sdk
pip install "sleigh-sdk[langchain]"
pip install "sleigh-sdk[mcp]"
```

## SDK 与 Agent 集成

Sleigh 的 SDK 重点支持把能力直接作为 LangChain Tool 提供给 Agent。
也就是：Agent 不需要手动编排底层 HTTP 调用，可以通过统一工具入口完成沙箱生命周期、命令执行、读写代码、工作流等操作。

这带来的好处：

- 工具语义覆盖核心能力（创建/执行/读取/写入/回滚/编排）
- 请求发出前有参数校验，降低 Agent 调用歧义
- 对 Agent 友好的 action 设计（包含显式 code_write 动作）
- 如果你的平台偏好 MCP，也可直接切换 MCP 适配

最小 LangChain Tool 接入示例：

```python
from sdk import SleighLangChainClient

client = SleighLangChainClient(base_url="http://127.0.0.1:10122")
tool = client.get_sleigh_runtime_tool()

# 将 tool 注入你的 Agent 工具列表即可。
```

更完整的 Agent 友好示例见：
`examples/langchain_sleigh_runtime_tool.py`

## 超时、镜像拉取与自动扩容控制

## 说明与限制

- code_write 的 `build_language` 可选；若服务端缺少对应镜像，会先拉取，导致耗时增加。
- 挂载模式设计为只读（`ro`）。
- 环境拷贝通过白名单根目录控制宿主机访问边界。

## 延伸文档

- SDK 文档：`sdks/python_sdk/README.md`
- LangChain 示例：`examples/langchain_sleigh_runtime_tool.py`
- MCP stdio 示例：`examples/mcp_sleigh_runtime_server.py`