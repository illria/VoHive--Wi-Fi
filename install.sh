#!/bin/sh
set -eu

# VoHive portable installer
# Author: Eianun | X: @Eianunbits
AUTHOR_NAME="Eianun"
AUTHOR_X="@Eianunbits"

banner() {
  printf '\n'
  printf '============================================================\n'
  printf ' VoHive 一键安装器\n'
  printf ' 作者: %s | X: %s\n' "$AUTHOR_NAME" "$AUTHOR_X"
  printf '============================================================\n'
}

REPO="${VOHIVE_REPO:-illria/VoHive--Wi-Fi}"
VERSION="${VOHIVE_VERSION:-portable}"
ROOT="${VOHIVE_INSTALL_ROOT:-/opt/vohive}"
BIN_DIR="$ROOT/bin"
CFG_DIR="$ROOT/config"
DATA_DIR="$ROOT/data"
LOG_DIR="$ROOT/logs"
SERVICE="${VOHIVE_SYSTEMD_SERVICE_PATH:-/etc/systemd/system/vohive.service}"
LOCAL_BIN=""
SKIP_DEPS="${VOHIVE_SKIP_DEPS:-0}"
NO_SYSTEMD=0
USE_SYSTEMD=0
TMP=""

log() { printf '[vohive-install] %s\n' "$*"; }
err() { printf '[vohive-install] 错误: %s\n' "$*" >&2; }
die() { err "$1"; exit 1; }
cleanup() { [ -z "$TMP" ] || rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    die "需要 root 或 sudo 权限"
  fi
}

download() {
  URL="$1"
  DEST="$2"
  log "下载: $URL"
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 "$URL" -o "$DEST"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$DEST" "$URL"
  else
    die "缺少 curl 或 wget，请先安装其中一个"
  fi
}

sha256_check() {
  ARCHIVE="$1"
  CHECKSUM="$2"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname "$ARCHIVE")" && sha256sum -c "$(basename "$CHECKSUM")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$(dirname "$ARCHIVE")" && shasum -a 256 -c "$(basename "$CHECKSUM")")
  else
    die "缺少 sha256sum 或 shasum，无法校验下载文件"
  fi
}

detect_platform() {
  [ "$(uname -s)" = Linux ] || die "当前系统不是 Linux: $(uname -s)"

  MACHINE="$(uname -m)"
  case "$MACHINE" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    armv7|armv7l|armhf) ARCH=armv7 ;;
    *) die "不支持的 CPU 架构: $MACHINE（支持 amd64、arm64、armv7）" ;;
  esac

  DISTRO="Linux"
  if [ -r /etc/os-release ]; then
    . /etc/os-release
    DISTRO="${PRETTY_NAME:-${ID:-Linux}}"
  fi

  if [ "$NO_SYSTEMD" -eq 0 ] &&
     command -v systemctl >/dev/null 2>&1 &&
     [ -d /run/systemd/system ]; then
    USE_SYSTEMD=1
  fi

  log "系统: $DISTRO"
  log "架构: $MACHINE -> $ARCH"
  if [ "$USE_SYSTEMD" -eq 1 ]; then
    log "服务管理: systemd"
  else
    log "服务管理: 未检测到运行中的 systemd，跳过服务注册"
  fi
}

deps() {
  [ "$SKIP_DEPS" = 1 ] && {
    log "已跳过可选依赖安装"
    return
  }

  if command -v apt-get >/dev/null 2>&1; then
    root apt-get update
    root env DEBIAN_FRONTEND=noninteractive apt-get install -y socat usbutils pciutils
  elif command -v apk >/dev/null 2>&1; then
    root apk add --no-cache socat usbutils pciutils
  elif command -v opkg >/dev/null 2>&1; then
    root opkg update || true
    root opkg install socat usbutils pciutils || true
  else
    log "未识别包管理器，跳过可选依赖"
  fi
}

main() {
  banner
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --local-bin)
        [ "$#" -ge 2 ] || die "--local-bin 需要文件路径"
        LOCAL_BIN="$2"
        shift 2
        ;;
      --skip-deps)
        SKIP_DEPS=1
        shift
        ;;
      --no-systemd)
        NO_SYSTEMD=1
        shift
        ;;
      -h|--help)
        echo "用法: install.sh [--local-bin PATH] [--skip-deps] [--no-systemd]"
        exit 0
        ;;
      *)
        die "未知参数: $1"
        ;;
    esac
  done

  detect_platform
  TMP="$(mktemp -d)"

  if [ -z "$LOCAL_BIN" ]; then
    ASSET="vohive_${VERSION}_linux_${ARCH}.tar.gz"
    BASE="https://github.com/$REPO/releases/download/$VERSION"
    download "$BASE/$ASSET" "$TMP/$ASSET"
    download "$BASE/$ASSET.sha256" "$TMP/$ASSET.sha256"
    sha256_check "$TMP/$ASSET" "$TMP/$ASSET.sha256"

    mkdir "$TMP/unpack"
    tar -xzf "$TMP/$ASSET" -C "$TMP/unpack"
    LOCAL_BIN="$TMP/unpack/vohive"
  fi

  [ -f "$LOCAL_BIN" ] || die "找不到二进制文件: $LOCAL_BIN"
  [ -x "$LOCAL_BIN" ] || die "二进制文件不可执行: $LOCAL_BIN"

  deps
  root mkdir -p "$BIN_DIR" "$CFG_DIR" "$DATA_DIR" "$LOG_DIR"

  if [ -x "$BIN_DIR/vohive" ]; then
    root cp -f "$BIN_DIR/vohive" "$BIN_DIR/vohive.bak"
  fi
  root install -m 0755 "$LOCAL_BIN" "$BIN_DIR/vohive"

  if [ ! -f "$CFG_DIR/config.yaml" ]; then
    CFG="$TMP/config.yaml"
    cat > "$CFG" <<'EOF'
server:
    port: :7575
web:
    username: admin
    password: "admin"
devices: []
EOF
    root install -m 0600 "$CFG" "$CFG_DIR/config.yaml"
  fi

  if [ "$USE_SYSTEMD" -eq 1 ]; then
    UNIT="$TMP/vohive.service"
    cat > "$UNIT" <<EOF
[Unit]
Description=VoHive Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=$ROOT
ExecStart=$BIN_DIR/vohive -c $CFG_DIR/config.yaml
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
    root install -m 0644 "$UNIT" "$SERVICE"
    root systemctl daemon-reload
    root systemctl enable --now vohive
    sleep 2
    root systemctl is-active --quiet vohive ||
      die "服务未启动，请查看: journalctl -u vohive"
  else
    log "手动启动: $BIN_DIR/vohive -c $CFG_DIR/config.yaml"
  fi

  log "安装完成，端口 7575，默认账号密码 admin/admin（请登录后修改）"
}

main "$@"
