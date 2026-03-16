# Code Write 接口详解

本文档详细说明 Sleigh 的代码写入接口：

- `POST /sandboxes/{id}/ops/code/write`

该接口用于在指定沙箱内进行 AI 编程写入，支持局部修改与整文件覆盖，并可选执行构建校验。

## 1. 核心语义

- 操作对象是**沙箱内文件**（不是直接改宿主机代码仓库）。
- 服务端会在受控流程中完成文件同步、修改、回写与检查。
- 请求必须携带 `session_token` 并通过沙箱访问鉴权。

## 2. 路径与模式

### 2.1 目标路径

- `sandbox_path`：沙箱内目标文件绝对路径（必填）
- 不允许使用根路径 `/`，也不允许系统敏感路径（如 `/proc`、`/sys`、`/dev`）

### 2.2 写入模式

- `write_mode=context_edit`（默认）
  - 适合局部修改
  - 关键字段：`old_text`、`new_text`
  - 可选辅助字段：`before_context`、`after_context`、`occurrence`
- `write_mode=replace_file`
  - 适合整文件重写
  - 关键字段：`content`

## 3. 请求体字段

通用字段：

- `session_token`：会话令牌（必填）
- `sandbox_path`：目标文件绝对路径（必填）
- `write_mode`：`context_edit` 或 `replace_file`（可选，默认 `context_edit`）
- `build_language`：可选构建语言（如 `go/python/node/rust/java`）
- `timeout_seconds`：接口整体超时（可选）
- `max_output_bytes`：输出截断字节上限（可选）
- `max_lines`：输出截断行数上限（可选）

`context_edit` 专属字段：

- `old_text`：要替换的原始片段（必填）
- `new_text`：替换后的片段（必填）
- `before_context`：前置上下文（可选）
- `after_context`：后置上下文（可选）
- `occurrence`：命中序号（1 开始，可选）

`replace_file` 专属字段：

- `content`：整文件新内容（必填，可为空字符串）

## 4. 响应结构

统一返回 AI 友好 envelope，常见字段：

- `status`：`ok` / `error`
- `duration_ms`
- `timed_out`
- `truncated`
- `stdout`
- `stderr`
- `error`
- `applied_files`
- `format_issues`
- `lint_issues`
- `build_status`：`not_run` / `passed` / `failed`

## 5. 质量检查与构建

- 若工作区存在 `.pre-commit-config.yaml`，优先执行 `pre-commit`。
- 若不存在，则按代码语言自动执行兜底质量检查。
- 当提供 `build_language` 时，会执行对应构建校验。
- `build_language` 未提供时，`build_status=not_run`。

## 6. 常见失败语义

### 6.1 context_edit 未命中

- 场景：`old_text` 在目标文件中找不到
- 典型错误：`context_edit no_match`
- 建议：先读取最新文件，再补充 `before_context/after_context` 缩小定位歧义

### 6.2 context_edit 多处命中

- 场景：片段在文件中出现多次
- 典型错误：`context_edit ambiguous_match`
- 建议：补充上下文或指定 `occurrence`

### 6.3 路径不合法

- 场景：`sandbox_path` 非绝对路径、指向受限目录或根路径
- 建议：改为沙箱内合法绝对文件路径

### 6.4 构建失败

- 场景：代码或依赖不满足构建条件
- 建议：根据 `stderr/error` 修正代码或依赖后重试

## 7. 最小示例

### 7.1 局部修改（context_edit）

```json
{
  "session_token": "sess_xxx",
  "sandbox_path": "/app/main.py",
  "write_mode": "context_edit",
  "old_text": "print('hello')\n",
  "new_text": "print('hello world')\n",
  "build_language": "python"
}
```

### 7.2 整文件覆盖（replace_file）

```json
{
  "session_token": "sess_xxx",
  "sandbox_path": "/app/main.py",
  "write_mode": "replace_file",
  "content": "print('fresh file')\n",
  "build_language": "python"
}
```

## 8. Agent 调用建议

- 先读后写：先用读接口获取最新内容，再做 context_edit。
- 小步提交：复杂修改拆成多次小修改，提升命中率与可恢复性。
- 错误自适应：根据 `no_match/ambiguous_match` 自动调整上下文或 `occurrence`。
- 需要编译保证时再传 `build_language`，避免不必要耗时。
