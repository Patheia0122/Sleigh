#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="/opt/sleigh-runtime"
SERVICE_NAME="sleigh"
DEFAULT_MOUNT_ROOT="${BASE_DIR}/mount-root-$(date +%Y%m%d)-$(hostname -s)-sleigh"
DEFAULT_ENV_ROOT="${BASE_DIR}/environment-root-$(date +%Y%m%d)-$(hostname -s)-sleigh"
INSTALL_BIN="${BASE_DIR}/bin/sleigh-server"
ENV_FILE="${BASE_DIR}/config/sleigh.env"
INSTALL_STATE_FILE="${BASE_DIR}/config/install-state.toml"
VENV_DIR="${BASE_DIR}/.venv"
SYSTEMD_UNIT="/etc/systemd/system/${SERVICE_NAME}.service"
DEFAULT_SERVER_ADDR=":10122"
DEFAULT_SERVER_VERSION="dev"
DEFAULT_DB_PATH="${BASE_DIR}/data/runtime.db"
DEFAULT_MEMORY_EXPAND_MIN_MB="64"
DEFAULT_MEMORY_EXPAND_MAX_MB="0"
DEFAULT_MEMORY_EXPAND_MAX_STEP_MB="2048"
DEFAULT_EXEC_TASK_TTL_DAYS="14"
DEFAULT_WARM_POOL_SIZE="1"
DEFAULT_WARM_POOL_IMAGE="python:3.11-slim"
DEFAULT_WARM_POOL_MEMORY_MB="256"
DEFAULT_IMAGE_PULL_TIMEOUT_SECONDS="120"
DEFAULT_SANDBOX_IDLE_TTL_DAYS="14"
DEFAULT_CURSOR_TOKEN_TTL_SECONDS="3600"
DEFAULT_EXEC_CLEANUP_INTERVAL_SECONDS="3600"
DEFAULT_SERVER_OTEL_EXPORTER_OTLP_ENDPOINT=""
DEFAULT_WEBHOOK_HMAC_SECRET=""

if [[ "${EUID}" -eq 0 ]]; then
  SUDO=""
else
  if ! command -v sudo >/dev/null 2>&1; then
    echo "sudo is required to install system service files." >&2
    exit 1
  fi
  SUDO="sudo"
fi

# Prefer the official Linux tarball install (/usr/local/go) over conda/miniconda or other shims
# earlier on PATH — otherwise an older `go` triggers automatic toolchain download (proxy.golang.org)
# and often times out on restricted networks.
if [[ -x "/usr/local/go/bin/go" ]]; then
  GO_BIN="/usr/local/go/bin/go"
elif command -v go >/dev/null 2>&1; then
  GO_BIN="$(command -v go)"
else
  GO_BIN=""
fi

echo "Select language / 选择语言:"
echo "1) English"
echo "2) 中文"
read -r -p "> " LANG_CHOICE
if [[ "${LANG_CHOICE}" == "2" ]]; then
  LOCALE="zh"
else
  LOCALE="en"
fi

msg() {
  local key="$1"
  if [[ "${LOCALE}" == "zh" ]]; then
    case "${key}" in
      title) echo "=== Sleigh 安装向导（宿主机服务模式）===" ;;
      check_deps) echo "检查依赖（go、docker、systemctl、python3）..." ;;
      deps_missing) echo "缺少依赖，请先安装后重试：" ;;
      installing_precommit) echo "安装 pre-commit（服务端 patch 质量检查依赖，使用独立虚拟环境）..." ;;
      creating_venv) echo "创建 Python 虚拟环境（${VENV_DIR}）..." ;;
      venv_create_failed) echo "错误: 创建 Python 虚拟环境失败，请确认已安装 python3-venv。" ;;
      precommit_install_failed) echo "错误: pre-commit 安装失败。请检查服务端网络、pip 源或代理配置后重试。" ;;
      existing_config_detected) echo "检测到已有安装配置。" ;;
      ask_config_apply_mode) echo "请选择安装方式: [1] 更新配置并重启 [2] 保留原有配置并重启 [3] 取消安装" ;;
      config_apply_cancelled) echo "已取消安装，不修改现有配置。" ;;
      keep_config_env_missing) echo "错误: 未找到现有环境配置文件，无法保留原有配置并重启。" ;;
      ask_mount) echo "请输入挂载区白名单根目录（回车使用默认目录）:" ;;
      ask_environment) echo "请输入环境区白名单根目录（回车使用默认目录）:" ;;
      ask_server_addr) echo "请输入服务监听地址（例如 :10122、0.0.0.0:10122）:" ;;
      ask_server_version) echo "请输入服务版本标识（用于诊断展示）:" ;;
      ask_warm_pool_size) echo "请输入预热池数量（WARM_POOL_SIZE）:" ;;
      ask_warm_pool_image) echo "请输入预热池镜像（WARM_POOL_IMAGE）:" ;;
      ask_warm_pool_memory) echo "请输入预热池内存（MB）:" ;;
      ask_exec_ttl_days) echo "请输入执行记录保留天数（EXEC_TASK_TTL_DAYS）:" ;;
      ask_image_pull_timeout) echo "请输入镜像拉取超时时间（秒，IMAGE_PULL_TIMEOUT_SECONDS）:" ;;
      ask_idle_ttl_days) echo "请输入空闲沙箱自动清理天数（SANDBOX_IDLE_TTL_DAYS）:" ;;
      ask_cursor_secret) echo "请输入 CURSOR_TOKEN_SECRET（回车随机生成）:" ;;
      ask_webhook_secret) echo "请输入 WEBHOOK_HMAC_SECRET（回车随机生成）:" ;;
      ask_otel_url) echo "请输入 OTEL gRPC endpoint（留空则关闭，例如 127.0.0.1:4317）:" ;;
      default_value) echo "默认值:" ;;
      default_dir) echo "默认目录:" ;;
      default_exists) echo "告警: 默认目录已存在，可能与历史安装重名:" ;;
      abort_install) echo "安装已退出，请重新执行并手动指定目录。" ;;
      abs_path) echo "错误: 目录必须是绝对路径。" ;;
      invalid_number) echo "错误: 请输入合法整数。" ;;
      invalid_port) echo "错误: 监听端口必须在 1-65535 之间。" ;;
      port_in_use) echo "错误: 监听端口已被占用，请修改 SERVER_ADDR 后重试。" ;;
      port_owned_by_running_service) echo "检测到该端口由当前运行中的 sleigh 服务占用，将继续并在后续询问是否重启。" ;;
      building) echo "编译 Sleigh 服务端二进制..." ;;
      installing) echo "安装二进制与配置..." ;;
      creating_unit) echo "写入 systemd 服务单元..." ;;
      starting) echo "启动并设置开机自启..." ;;
      ask_restart_running) echo "检测到服务已在运行，是否按新配置重启？[y/N]:" ;;
      restart_skipped) echo "已跳过重启。新配置将在下次重启服务后生效。" ;;
      done) echo "安装完成，服务已以宿主机模式运行。" ;;
      summary) echo "安装信息:" ;;
      unit) echo "systemd 单元:" ;;
      bin) echo "二进制路径:" ;;
      env) echo "环境文件:" ;;
      mount) echo "挂载区白名单根目录:" ;;
      environment) echo "环境区白名单根目录:" ;;
      addr) echo "监听地址:" ;;
      otel) echo "OTEL gRPC endpoint:" ;;
      webhook_secret) echo "Webhook HMAC Secret:" ;;
      image_pull_timeout) echo "镜像拉取超时（秒）:" ;;
      idle_ttl_days) echo "空闲沙箱清理天数:" ;;
      status_hint) echo "可使用以下命令查看状态:" ;;
      logs_hint) echo "查看日志:" ;;
      *) echo "${key}" ;;
    esac
  else
    case "${key}" in
      title) echo "=== Sleigh Installer (Host Service Mode) ===" ;;
      check_deps) echo "Checking dependencies (go, docker, systemctl, python3)..." ;;
      deps_missing) echo "Missing required dependencies. Please install first:" ;;
      installing_precommit) echo "Installing pre-commit in isolated virtual environment (required by server-side patch quality checks)..." ;;
      creating_venv) echo "Creating Python virtual environment (${VENV_DIR})..." ;;
      venv_create_failed) echo "Error: failed to create Python virtual environment. Please install python3-venv." ;;
      precommit_install_failed) echo "Error: failed to install pre-commit. Check server network, pip source, or proxy settings and retry." ;;
      existing_config_detected) echo "Detected existing installation config." ;;
      ask_config_apply_mode) echo "Choose install mode: [1] update config and restart [2] keep existing config and restart [3] cancel install" ;;
      config_apply_cancelled) echo "Install cancelled. Existing config is unchanged." ;;
      keep_config_env_missing) echo "Error: existing env config file not found; cannot keep existing config and restart." ;;
      ask_mount) echo "Enter mount-zone allowlist root (press Enter for default):" ;;
      ask_environment) echo "Enter environment-zone allowlist root (press Enter for default):" ;;
      ask_server_addr) echo "Enter server listen address (e.g. :10122, 0.0.0.0:10122):" ;;
      ask_server_version) echo "Enter server version label (for diagnostics):" ;;
      ask_warm_pool_size) echo "Enter warm pool size (WARM_POOL_SIZE):" ;;
      ask_warm_pool_image) echo "Enter warm pool image (WARM_POOL_IMAGE):" ;;
      ask_warm_pool_memory) echo "Enter warm pool memory in MB:" ;;
      ask_exec_ttl_days) echo "Enter exec task retention in days (EXEC_TASK_TTL_DAYS):" ;;
      ask_image_pull_timeout) echo "Enter image pull timeout in seconds (IMAGE_PULL_TIMEOUT_SECONDS):" ;;
      ask_idle_ttl_days) echo "Enter idle sandbox cleanup TTL days (SANDBOX_IDLE_TTL_DAYS):" ;;
      ask_cursor_secret) echo "Enter CURSOR_TOKEN_SECRET (press Enter to auto-generate):" ;;
      ask_webhook_secret) echo "Enter WEBHOOK_HMAC_SECRET (press Enter to auto-generate):" ;;
      ask_otel_url) echo "Enter OTEL gRPC endpoint (blank to disable, e.g. 127.0.0.1:4317):" ;;
      default_value) echo "Default value:" ;;
      default_dir) echo "Default directory:" ;;
      default_exists) echo "Warning: default directory already exists:" ;;
      abort_install) echo "Installation aborted. Re-run and provide a custom directory." ;;
      abs_path) echo "Error: directory path must be absolute." ;;
      invalid_number) echo "Error: please enter a valid integer." ;;
      invalid_port) echo "Error: listen port must be between 1 and 65535." ;;
      port_in_use) echo "Error: listen port is already in use. Update SERVER_ADDR and retry." ;;
      port_owned_by_running_service) echo "Port is currently used by running sleigh service; proceeding and will ask about restart later." ;;
      building) echo "Building Sleigh server binary..." ;;
      installing) echo "Installing binary and runtime config..." ;;
      creating_unit) echo "Writing systemd unit..." ;;
      starting) echo "Starting service and enabling autostart..." ;;
      ask_restart_running) echo "Service is already running. Restart with new config? [y/N]:" ;;
      restart_skipped) echo "Restart skipped. New config will apply on next service restart." ;;
      done) echo "Installation complete. Service is running on host." ;;
      summary) echo "Installation summary:" ;;
      unit) echo "systemd unit:" ;;
      bin) echo "binary path:" ;;
      env) echo "env file:" ;;
      mount) echo "mount-zone allowlist root:" ;;
      environment) echo "environment-zone allowlist root:" ;;
      addr) echo "listen address:" ;;
      otel) echo "OTEL gRPC endpoint:" ;;
      webhook_secret) echo "Webhook HMAC Secret:" ;;
      image_pull_timeout) echo "image pull timeout (seconds):" ;;
      idle_ttl_days) echo "idle sandbox cleanup TTL days:" ;;
      status_hint) echo "Check status with:" ;;
      logs_hint) echo "Check logs with:" ;;
      *) echo "${key}" ;;
    esac
  fi
}

trim() {
  echo "$1" | xargs
}

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
    return 0
  fi
  od -An -N 32 -tx1 /dev/urandom | tr -d ' \n'
}

extract_port_from_addr() {
  local addr="$1"
  if [[ "${addr}" =~ ^:([0-9]{1,5})$ ]]; then
    echo "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ "${addr}" =~ ^[^:]+:([0-9]{1,5})$ ]]; then
    echo "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ "${addr}" =~ ^\[[^]]+\]:([0-9]{1,5})$ ]]; then
    echo "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

is_port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    if ss -ltnH "sport = :${port}" | read -r _; then
      return 0
    fi
  fi
  if command -v lsof >/dev/null 2>&1; then
    if lsof -iTCP:"${port}" -sTCP:LISTEN -Pn >/dev/null 2>&1; then
      return 0
    fi
  fi
  return 1
}

echo "$(msg title)"
echo "$(msg check_deps)"

MISSING=()
for dep in docker systemctl python3; do
  if ! command -v "${dep}" >/dev/null 2>&1; then
    MISSING+=("${dep}")
  fi
done
if [[ -z "${GO_BIN}" ]]; then
  MISSING+=("go")
fi
if [[ "${#MISSING[@]}" -gt 0 ]]; then
  echo "$(msg deps_missing) ${MISSING[*]}" >&2
  exit 1
fi

CONFIG_APPLY_MODE="auto"
if [[ -f "${ENV_FILE}" || -f "${INSTALL_STATE_FILE}" ]]; then
  echo "$(msg existing_config_detected)"
  echo "$(msg ask_config_apply_mode)"
  read -r USER_INPUT
  APPLY_CHOICE="$(trim "${USER_INPUT}")"
  case "${APPLY_CHOICE}" in
    1|"")
      CONFIG_APPLY_MODE="update_restart"
      ;;
    2)
      CONFIG_APPLY_MODE="keep_restart"
      ;;
    3)
      echo "$(msg config_apply_cancelled)"
      exit 0
      ;;
    *)
      echo "$(msg config_apply_cancelled)"
      exit 1
      ;;
  esac
fi

if [[ "${CONFIG_APPLY_MODE}" == "keep_restart" ]]; then
  if [[ ! -f "${ENV_FILE}" ]]; then
    echo "$(msg keep_config_env_missing)" >&2
    exit 1
  fi
  SERVER_ADDR="$(sed -n 's/^SERVER_ADDR=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  SERVER_VERSION="$(sed -n 's/^SERVER_VERSION=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  WARM_POOL_SIZE="$(sed -n 's/^WARM_POOL_SIZE=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  WARM_POOL_IMAGE="$(sed -n 's/^WARM_POOL_IMAGE=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  WARM_POOL_MEMORY_MB="$(sed -n 's/^WARM_POOL_MEMORY_MB=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  EXEC_TASK_TTL_DAYS="$(sed -n 's/^EXEC_TASK_TTL_DAYS=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  IMAGE_PULL_TIMEOUT_SECONDS="$(sed -n 's/^IMAGE_PULL_TIMEOUT_SECONDS=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  SANDBOX_IDLE_TTL_DAYS="$(sed -n 's/^SANDBOX_IDLE_TTL_DAYS=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  CURSOR_TOKEN_SECRET="$(sed -n 's/^CURSOR_TOKEN_SECRET=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  WEBHOOK_HMAC_SECRET="$(sed -n 's/^WEBHOOK_HMAC_SECRET=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  SERVER_OTEL_EXPORTER_OTLP_ENDPOINT="$(sed -n 's/^SERVER_OTEL_EXPORTER_OTLP_ENDPOINT=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  MOUNT_ROOT="$(sed -n 's/^SERVER_MOUNT_ALLOWED_ROOT=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"
  ENV_ROOT="$(sed -n 's/^SERVER_ENV_ALLOWED_ROOT=//p' "${ENV_FILE}" | sed -n '1p' | xargs)"

  [[ -n "${SERVER_ADDR}" ]] || SERVER_ADDR="${DEFAULT_SERVER_ADDR}"
  [[ -n "${SERVER_VERSION}" ]] || SERVER_VERSION="${DEFAULT_SERVER_VERSION}"
  [[ -n "${WARM_POOL_SIZE}" ]] || WARM_POOL_SIZE="${DEFAULT_WARM_POOL_SIZE}"
  [[ -n "${WARM_POOL_IMAGE}" ]] || WARM_POOL_IMAGE="${DEFAULT_WARM_POOL_IMAGE}"
  [[ -n "${WARM_POOL_MEMORY_MB}" ]] || WARM_POOL_MEMORY_MB="${DEFAULT_WARM_POOL_MEMORY_MB}"
  [[ -n "${EXEC_TASK_TTL_DAYS}" ]] || EXEC_TASK_TTL_DAYS="${DEFAULT_EXEC_TASK_TTL_DAYS}"
  [[ -n "${IMAGE_PULL_TIMEOUT_SECONDS}" ]] || IMAGE_PULL_TIMEOUT_SECONDS="${DEFAULT_IMAGE_PULL_TIMEOUT_SECONDS}"
  [[ -n "${SANDBOX_IDLE_TTL_DAYS}" ]] || SANDBOX_IDLE_TTL_DAYS="${DEFAULT_SANDBOX_IDLE_TTL_DAYS}"
  [[ -n "${CURSOR_TOKEN_SECRET}" ]] || CURSOR_TOKEN_SECRET="$(random_secret)"
  [[ -n "${WEBHOOK_HMAC_SECRET}" ]] || WEBHOOK_HMAC_SECRET="${DEFAULT_WEBHOOK_HMAC_SECRET}"
  [[ -n "${MOUNT_ROOT}" ]] || MOUNT_ROOT="${DEFAULT_MOUNT_ROOT}"
  [[ -n "${ENV_ROOT}" ]] || ENV_ROOT="${DEFAULT_ENV_ROOT}"
else
  echo "$(msg default_dir) ${DEFAULT_MOUNT_ROOT}"
  echo "$(msg ask_mount)"
  read -r USER_INPUT
  MOUNT_ROOT="$(trim "${USER_INPUT}")"
  if [[ -z "${MOUNT_ROOT}" ]]; then
    MOUNT_ROOT="${DEFAULT_MOUNT_ROOT}"
    if [[ -e "${MOUNT_ROOT}" ]]; then
      echo "$(msg default_exists) ${MOUNT_ROOT}" >&2
      echo "$(msg abort_install)" >&2
      exit 1
    fi
  fi

  if [[ "${MOUNT_ROOT}" != /* ]]; then
    echo "$(msg abs_path)" >&2
    exit 1
  fi

  echo "$(msg default_dir) ${DEFAULT_ENV_ROOT}"
  echo "$(msg ask_environment)"
  read -r USER_INPUT
  ENV_ROOT="$(trim "${USER_INPUT}")"
  if [[ -z "${ENV_ROOT}" ]]; then
    ENV_ROOT="${DEFAULT_ENV_ROOT}"
    if [[ -e "${ENV_ROOT}" ]]; then
      echo "$(msg default_exists) ${ENV_ROOT}" >&2
      echo "$(msg abort_install)" >&2
      exit 1
    fi
  fi
  if [[ "${ENV_ROOT}" != /* ]]; then
    echo "$(msg abs_path)" >&2
    exit 1
  fi

  echo "$(msg default_value) ${DEFAULT_SERVER_ADDR}"
  echo "$(msg ask_server_addr)"
  read -r USER_INPUT
  SERVER_ADDR="$(trim "${USER_INPUT}")"
  if [[ -z "${SERVER_ADDR}" ]]; then
    SERVER_ADDR="${DEFAULT_SERVER_ADDR}"
  fi

  echo "$(msg default_value) ${DEFAULT_SERVER_VERSION}"
  echo "$(msg ask_server_version)"
  read -r USER_INPUT
  SERVER_VERSION="$(trim "${USER_INPUT}")"
  if [[ -z "${SERVER_VERSION}" ]]; then
    SERVER_VERSION="${DEFAULT_SERVER_VERSION}"
  fi

  echo "$(msg default_value) ${DEFAULT_WARM_POOL_SIZE}"
  echo "$(msg ask_warm_pool_size)"
  read -r USER_INPUT
  WARM_POOL_SIZE="$(trim "${USER_INPUT}")"
  if [[ -z "${WARM_POOL_SIZE}" ]]; then
    WARM_POOL_SIZE="${DEFAULT_WARM_POOL_SIZE}"
  fi
  if ! [[ "${WARM_POOL_SIZE}" =~ ^[0-9]+$ ]]; then
    echo "$(msg invalid_number) (WARM_POOL_SIZE)" >&2
    exit 1
  fi

  echo "$(msg default_value) ${DEFAULT_WARM_POOL_IMAGE}"
  echo "$(msg ask_warm_pool_image)"
  read -r USER_INPUT
  WARM_POOL_IMAGE="$(trim "${USER_INPUT}")"
  if [[ -z "${WARM_POOL_IMAGE}" ]]; then
    WARM_POOL_IMAGE="${DEFAULT_WARM_POOL_IMAGE}"
  fi

  echo "$(msg default_value) ${DEFAULT_WARM_POOL_MEMORY_MB}"
  echo "$(msg ask_warm_pool_memory)"
  read -r USER_INPUT
  WARM_POOL_MEMORY_MB="$(trim "${USER_INPUT}")"
  if [[ -z "${WARM_POOL_MEMORY_MB}" ]]; then
    WARM_POOL_MEMORY_MB="${DEFAULT_WARM_POOL_MEMORY_MB}"
  fi
  if ! [[ "${WARM_POOL_MEMORY_MB}" =~ ^[0-9]+$ ]]; then
    echo "$(msg invalid_number) (WARM_POOL_MEMORY_MB)" >&2
    exit 1
  fi

  echo "$(msg default_value) ${DEFAULT_EXEC_TASK_TTL_DAYS}"
  echo "$(msg ask_exec_ttl_days)"
  read -r USER_INPUT
  EXEC_TASK_TTL_DAYS="$(trim "${USER_INPUT}")"
  if [[ -z "${EXEC_TASK_TTL_DAYS}" ]]; then
    EXEC_TASK_TTL_DAYS="${DEFAULT_EXEC_TASK_TTL_DAYS}"
  fi
  if ! [[ "${EXEC_TASK_TTL_DAYS}" =~ ^[0-9]+$ ]]; then
    echo "$(msg invalid_number) (EXEC_TASK_TTL_DAYS)" >&2
    exit 1
  fi

  echo "$(msg default_value) ${DEFAULT_IMAGE_PULL_TIMEOUT_SECONDS}"
  echo "$(msg ask_image_pull_timeout)"
  read -r USER_INPUT
  IMAGE_PULL_TIMEOUT_SECONDS="$(trim "${USER_INPUT}")"
  if [[ -z "${IMAGE_PULL_TIMEOUT_SECONDS}" ]]; then
    IMAGE_PULL_TIMEOUT_SECONDS="${DEFAULT_IMAGE_PULL_TIMEOUT_SECONDS}"
  fi
  if ! [[ "${IMAGE_PULL_TIMEOUT_SECONDS}" =~ ^[0-9]+$ ]]; then
    echo "$(msg invalid_number) (IMAGE_PULL_TIMEOUT_SECONDS)" >&2
    exit 1
  fi

  echo "$(msg default_value) ${DEFAULT_SANDBOX_IDLE_TTL_DAYS}"
  echo "$(msg ask_idle_ttl_days)"
  read -r USER_INPUT
  SANDBOX_IDLE_TTL_DAYS="$(trim "${USER_INPUT}")"
  if [[ -z "${SANDBOX_IDLE_TTL_DAYS}" ]]; then
    SANDBOX_IDLE_TTL_DAYS="${DEFAULT_SANDBOX_IDLE_TTL_DAYS}"
  fi
  if ! [[ "${SANDBOX_IDLE_TTL_DAYS}" =~ ^[0-9]+$ ]]; then
    echo "$(msg invalid_number) (SANDBOX_IDLE_TTL_DAYS)" >&2
    exit 1
  fi

  echo "$(msg ask_cursor_secret)"
  read -r USER_INPUT
  CURSOR_TOKEN_SECRET="$(trim "${USER_INPUT}")"
  if [[ -z "${CURSOR_TOKEN_SECRET}" ]]; then
    CURSOR_TOKEN_SECRET="$(random_secret)"
  fi

  echo "$(msg ask_webhook_secret)"
  read -r USER_INPUT
  WEBHOOK_HMAC_SECRET="$(trim "${USER_INPUT}")"
  if [[ -z "${WEBHOOK_HMAC_SECRET}" ]]; then
    WEBHOOK_HMAC_SECRET="$(random_secret)"
  fi

  echo "$(msg default_value) ${DEFAULT_SERVER_OTEL_EXPORTER_OTLP_ENDPOINT:-<empty>}"
  echo "$(msg ask_otel_url)"
  read -r USER_INPUT
  SERVER_OTEL_EXPORTER_OTLP_ENDPOINT="$(trim "${USER_INPUT}")"
  if [[ -z "${SERVER_OTEL_EXPORTER_OTLP_ENDPOINT}" ]]; then
    SERVER_OTEL_EXPORTER_OTLP_ENDPOINT="${DEFAULT_SERVER_OTEL_EXPORTER_OTLP_ENDPOINT}"
  fi

  if PORT="$(extract_port_from_addr "${SERVER_ADDR}")"; then
    if [[ "${PORT}" -lt 1 || "${PORT}" -gt 65535 ]]; then
      echo "$(msg invalid_port) ${SERVER_ADDR}" >&2
      exit 1
    fi
    if is_port_in_use "${PORT}"; then
      SERVICE_ACTIVE="false"
      if ${SUDO} systemctl is-active --quiet "${SERVICE_NAME}.service"; then
        SERVICE_ACTIVE="true"
      fi
      CURRENT_SERVER_ADDR=""
      if [[ -f "${ENV_FILE}" ]]; then
        CURRENT_SERVER_ADDR="$(sed -n 's/^SERVER_ADDR=//p' "${ENV_FILE}" | head -n 1 | xargs)"
      fi
      if [[ "${SERVICE_ACTIVE}" == "true" && -n "${CURRENT_SERVER_ADDR}" && "${CURRENT_SERVER_ADDR}" == "${SERVER_ADDR}" ]]; then
        echo "$(msg port_owned_by_running_service)"
      else
        echo "$(msg port_in_use) ${SERVER_ADDR}" >&2
        exit 1
      fi
    fi
  fi
fi

echo "$(msg building)"
(
  cd "${PROJECT_ROOT}/server"
  # Module/toolchain fetches: proxy.golang.org is often slow or blocked in CN.
  export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
  export GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"
  # If PATH picks an older go (e.g. conda 1.21) but go.mod needs 1.26, Go must be allowed to download
  # the matching toolchain (GOTOOLCHAIN=local would block that). User may still set GOTOOLCHAIN=local
  # when their go version already satisfies go.mod.
  GOPATH="$(pwd)/.gopath" GOMODCACHE="$(pwd)/.gopath/pkg/mod" "${GO_BIN}" build -o /tmp/sleigh-server ./cmd/server
)

echo "$(msg installing)"
${SUDO} mkdir -p "${BASE_DIR}/bin" "${BASE_DIR}/config" "${BASE_DIR}/data/snapshots" "${MOUNT_ROOT}" "${ENV_ROOT}"
${SUDO} install -m 0755 /tmp/sleigh-server "${INSTALL_BIN}"
rm -f /tmp/sleigh-server

if [[ ! -x "${VENV_DIR}/bin/pre-commit" ]]; then
  if ! python3 -m venv --help >/dev/null 2>&1; then
    echo "$(msg venv_create_failed)" >&2
    exit 1
  fi
  echo "$(msg creating_venv)"
  ${SUDO} rm -rf "${VENV_DIR}"
  if ! ${SUDO} python3 -m venv "${VENV_DIR}"; then
    echo "$(msg venv_create_failed)" >&2
    exit 1
  fi
  echo "$(msg installing_precommit)"
  if ! ${SUDO} "${VENV_DIR}/bin/pip" install --upgrade pip pre-commit; then
    echo "$(msg precommit_install_failed)" >&2
    exit 1
  fi
fi

if [[ "${CONFIG_APPLY_MODE}" != "keep_restart" ]]; then
${SUDO} tee "${ENV_FILE}" >/dev/null <<EOF
SERVER_ADDR=${SERVER_ADDR}
SERVER_VERSION=${SERVER_VERSION}
SERVER_DB_PATH=${DEFAULT_DB_PATH}
MEMORY_EXPAND_MIN_MB=${DEFAULT_MEMORY_EXPAND_MIN_MB}
MEMORY_EXPAND_MAX_MB=${DEFAULT_MEMORY_EXPAND_MAX_MB}
MEMORY_EXPAND_MAX_STEP_MB=${DEFAULT_MEMORY_EXPAND_MAX_STEP_MB}
EXEC_TASK_TTL_DAYS=${EXEC_TASK_TTL_DAYS}
WARM_POOL_SIZE=${WARM_POOL_SIZE}
WARM_POOL_IMAGE=${WARM_POOL_IMAGE}
WARM_POOL_MEMORY_MB=${WARM_POOL_MEMORY_MB}
SNAPSHOT_ROOT_DIR=${BASE_DIR}/data/snapshots
IMAGE_PULL_TIMEOUT_SECONDS=${IMAGE_PULL_TIMEOUT_SECONDS}
SANDBOX_IDLE_TTL_DAYS=${SANDBOX_IDLE_TTL_DAYS}
CURSOR_TOKEN_SECRET=${CURSOR_TOKEN_SECRET}
WEBHOOK_HMAC_SECRET=${WEBHOOK_HMAC_SECRET}
CURSOR_TOKEN_TTL_SECONDS=${DEFAULT_CURSOR_TOKEN_TTL_SECONDS}
EXEC_CLEANUP_INTERVAL_SECONDS=${DEFAULT_EXEC_CLEANUP_INTERVAL_SECONDS}
SERVER_MOUNT_ALLOWED_ROOT=${MOUNT_ROOT}
SERVER_ENV_ALLOWED_ROOT=${ENV_ROOT}
SERVER_OTEL_EXPORTER_OTLP_ENDPOINT=${SERVER_OTEL_EXPORTER_OTLP_ENDPOINT}
PRE_COMMIT_BIN=${VENV_DIR}/bin/pre-commit
EOF

INSTALL_UPDATED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
FIRST_INSTALLED_AT="${INSTALL_UPDATED_AT}"
if [[ -f "${INSTALL_STATE_FILE}" ]]; then
  EXISTING_FIRST_INSTALLED_AT="$(sed -n 's/^first_installed_at = "\(.*\)"/\1/p' "${INSTALL_STATE_FILE}" | sed -n '1p')"
  if [[ -n "${EXISTING_FIRST_INSTALLED_AT}" ]]; then
    FIRST_INSTALLED_AT="${EXISTING_FIRST_INSTALLED_AT}"
  fi
fi

${SUDO} tee "${INSTALL_STATE_FILE}" >/dev/null <<EOF
# Sleigh installer state (auto-generated)
service_name = "${SERVICE_NAME}"
first_installed_at = "${FIRST_INSTALLED_AT}"
last_updated_at = "${INSTALL_UPDATED_AT}"
server_addr = "${SERVER_ADDR}"
mount_root = "${MOUNT_ROOT}"
env_root = "${ENV_ROOT}"
env_file = "${ENV_FILE}"
unit_file = "${SYSTEMD_UNIT}"
EOF
fi

echo "$(msg creating_unit)"
${SUDO} tee "${SYSTEMD_UNIT}" >/dev/null <<EOF
[Unit]
Description=Sleigh Runtime Service
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_BIN}
Restart=always
RestartSec=2
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

echo "$(msg starting)"
${SUDO} systemctl daemon-reload
${SUDO} systemctl enable "${SERVICE_NAME}.service" >/dev/null
if ${SUDO} systemctl is-active --quiet "${SERVICE_NAME}.service"; then
  if [[ "${CONFIG_APPLY_MODE}" == "update_restart" || "${CONFIG_APPLY_MODE}" == "keep_restart" ]]; then
    ${SUDO} systemctl restart "${SERVICE_NAME}.service"
  else
    echo "$(msg ask_restart_running)"
    read -r USER_INPUT
    RESTART_CHOICE="$(echo "${USER_INPUT}" | tr '[:upper:]' '[:lower:]' | xargs)"
    if [[ "${RESTART_CHOICE}" == "y" || "${RESTART_CHOICE}" == "yes" ]]; then
      ${SUDO} systemctl restart "${SERVICE_NAME}.service"
    else
      echo "$(msg restart_skipped)"
    fi
  fi
else
  ${SUDO} systemctl start "${SERVICE_NAME}.service"
fi

echo
echo "$(msg done)"
echo "$(msg summary)"
echo "- $(msg unit) ${SYSTEMD_UNIT}"
echo "- $(msg bin) ${INSTALL_BIN}"
echo "- $(msg env) ${ENV_FILE}"
echo "- $(msg addr) ${SERVER_ADDR}"
echo "- $(msg mount) ${MOUNT_ROOT}"
echo "- $(msg environment) ${ENV_ROOT}"
echo "- $(msg otel) ${SERVER_OTEL_EXPORTER_OTLP_ENDPOINT:-<disabled>}"
echo "- $(msg webhook_secret) <configured>"
echo "- $(msg image_pull_timeout) ${IMAGE_PULL_TIMEOUT_SECONDS}"
echo "- $(msg idle_ttl_days) ${SANDBOX_IDLE_TTL_DAYS}"
echo
echo "$(msg status_hint) ${SUDO:+sudo }systemctl status ${SERVICE_NAME}.service"
echo "$(msg logs_hint) ${SUDO:+sudo }journalctl -u ${SERVICE_NAME}.service -f"
