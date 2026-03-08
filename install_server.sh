#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="/opt/sleigh-runtime"
SERVICE_NAME="sleigh"
DEFAULT_MOUNT_ROOT="${BASE_DIR}/mount-root-$(date +%Y%m%d)-$(hostname -s)-sleigh"
INSTALL_BIN="${BASE_DIR}/bin/sleigh-server"
ENV_FILE="${BASE_DIR}/config/sleigh.env"
SYSTEMD_UNIT="/etc/systemd/system/${SERVICE_NAME}.service"

if [[ "${EUID}" -eq 0 ]]; then
  SUDO=""
else
  if ! command -v sudo >/dev/null 2>&1; then
    echo "sudo is required to install system service files." >&2
    exit 1
  fi
  SUDO="sudo"
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
      check_deps) echo "检查依赖（go、docker、systemctl）..." ;;
      deps_missing) echo "缺少依赖，请先安装后重试：" ;;
      ask_mount) echo "请输入挂载白名单根目录（回车使用默认目录）:" ;;
      default_dir) echo "默认目录:" ;;
      default_exists) echo "告警: 默认目录已存在，可能与历史安装重名:" ;;
      abort_install) echo "安装已退出，请重新执行并手动指定目录。" ;;
      abs_path) echo "错误: 挂载白名单根目录必须是绝对路径。" ;;
      building) echo "编译 Sleigh 服务端二进制..." ;;
      installing) echo "安装二进制与配置..." ;;
      creating_unit) echo "写入 systemd 服务单元..." ;;
      starting) echo "启动并设置开机自启..." ;;
      done) echo "安装完成，服务已以宿主机模式运行。" ;;
      summary) echo "安装信息:" ;;
      unit) echo "systemd 单元:" ;;
      bin) echo "二进制路径:" ;;
      env) echo "环境文件:" ;;
      mount) echo "挂载白名单根目录:" ;;
      status_hint) echo "可使用以下命令查看状态:" ;;
      logs_hint) echo "查看日志:" ;;
      *) echo "${key}" ;;
    esac
  else
    case "${key}" in
      title) echo "=== Sleigh Installer (Host Service Mode) ===" ;;
      check_deps) echo "Checking dependencies (go, docker, systemctl)..." ;;
      deps_missing) echo "Missing required dependencies. Please install first:" ;;
      ask_mount) echo "Enter mount allowlist root (press Enter for default):" ;;
      default_dir) echo "Default directory:" ;;
      default_exists) echo "Warning: default directory already exists:" ;;
      abort_install) echo "Installation aborted. Re-run and provide a custom directory." ;;
      abs_path) echo "Error: mount allowlist root must be an absolute path." ;;
      building) echo "Building Sleigh server binary..." ;;
      installing) echo "Installing binary and runtime config..." ;;
      creating_unit) echo "Writing systemd unit..." ;;
      starting) echo "Starting service and enabling autostart..." ;;
      done) echo "Installation complete. Service is running on host." ;;
      summary) echo "Installation summary:" ;;
      unit) echo "systemd unit:" ;;
      bin) echo "binary path:" ;;
      env) echo "env file:" ;;
      mount) echo "mount allowlist root:" ;;
      status_hint) echo "Check status with:" ;;
      logs_hint) echo "Check logs with:" ;;
      *) echo "${key}" ;;
    esac
  fi
}

echo "$(msg title)"
echo "$(msg check_deps)"

MISSING=()
for dep in go docker systemctl; do
  if ! command -v "${dep}" >/dev/null 2>&1; then
    MISSING+=("${dep}")
  fi
done
if [[ "${#MISSING[@]}" -gt 0 ]]; then
  echo "$(msg deps_missing) ${MISSING[*]}" >&2
  exit 1
fi

echo "$(msg default_dir) ${DEFAULT_MOUNT_ROOT}"
echo "$(msg ask_mount)"
read -r USER_INPUT
MOUNT_ROOT="$(echo "${USER_INPUT}" | xargs)"
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

echo "$(msg building)"
(
  cd "${PROJECT_ROOT}/server"
  GOPATH="$(pwd)/.gopath" GOMODCACHE="$(pwd)/.gopath/pkg/mod" go build -o /tmp/sleigh-server ./cmd/server
)

echo "$(msg installing)"
${SUDO} mkdir -p "${BASE_DIR}/bin" "${BASE_DIR}/config" "${BASE_DIR}/data/snapshots" "${MOUNT_ROOT}"
${SUDO} install -m 0755 /tmp/sleigh-server "${INSTALL_BIN}"
rm -f /tmp/sleigh-server

${SUDO} tee "${ENV_FILE}" >/dev/null <<EOF
SERVER_ADDR=:8080
SERVER_VERSION=dev
SERVER_DB_PATH=${BASE_DIR}/data/runtime.db
SESSION_MANAGER_EVENTS_URL=
MEMORY_EXPAND_MIN_MB=64
MEMORY_EXPAND_MAX_MB=4096
MEMORY_EXPAND_MAX_STEP_MB=2048
EXEC_TASK_TTL_DAYS=14
WARM_POOL_SIZE=2
WARM_POOL_IMAGE=alpine:3.20
WARM_POOL_MEMORY_MB=256
SNAPSHOT_ROOT_DIR=${BASE_DIR}/data/snapshots
EVENT_RETRY_MAX=5
EVENT_RETRY_INITIAL_MS=500
EVENT_RETRY_MAX_MS=10000
EVENT_QUEUE_SIZE=1024
CURSOR_TOKEN_SECRET=dev-cursor-secret
CURSOR_TOKEN_TTL_SECONDS=3600
EXEC_CLEANUP_INTERVAL_SECONDS=3600
SERVER_MOUNT_ALLOWED_ROOT=${MOUNT_ROOT}
EOF

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
${SUDO} systemctl enable --now "${SERVICE_NAME}.service"

echo
echo "$(msg done)"
echo "$(msg summary)"
echo "- $(msg unit) ${SYSTEMD_UNIT}"
echo "- $(msg bin) ${INSTALL_BIN}"
echo "- $(msg env) ${ENV_FILE}"
echo "- $(msg mount) ${MOUNT_ROOT}"
echo
echo "$(msg status_hint) ${SUDO:+sudo }systemctl status ${SERVICE_NAME}.service"
echo "$(msg logs_hint) ${SUDO:+sudo }journalctl -u ${SERVICE_NAME}.service -f"
