#!/bin/sh
set -eu
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
TMP=""

log(){ printf '[vohive-install] %s\n' "$*"; }
err(){ printf '[vohive-install] 错误: %s\n' "$*" >&2; }
cleanup(){ [ -z "$TMP" ] || rm -rf "$TMP"; }
trap cleanup EXIT INT TERM
root(){ if [ "$(id -u)" -eq 0 ]; then "$@"; elif command -v sudo >/dev/null 2>&1; then sudo "$@"; else err "需要 root 或 sudo"; exit 1; fi; }
download(){ if command -v curl >/dev/null 2>&1; then curl -fL --retry 3 "$1" -o "$2"; elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"; else err "缺少 curl 或 wget"; exit 1; fi; }
arch(){ case "$(uname -m)" in x86_64|amd64) echo amd64;; aarch64|arm64) echo arm64;; armv7|armv7l|armhf) echo armv7;; *) err "不支持架构: $(uname -m)"; exit 1;; esac; }
deps(){
  [ "$SKIP_DEPS" = 1 ] && return
  if command -v apt-get >/dev/null 2>&1; then root apt-get update; root env DEBIAN_FRONTEND=noninteractive apt-get install -y socat usbutils pciutils
  elif command -v apk >/dev/null 2>&1; then root apk add --no-cache socat usbutils pciutils
  elif command -v opkg >/dev/null 2>&1; then root opkg update || true; root opkg install socat usbutils pciutils || true
  else log "未识别包管理器，跳过可选依赖"; fi
}
main(){
  [ "$(uname -s)" = Linux ] || { err "只支持 Linux"; exit 1; }
  while [ "$#" -gt 0 ]; do case "$1" in
    --local-bin) LOCAL_BIN="$2"; shift 2;;
    --skip-deps) SKIP_DEPS=1; shift;;
    --no-systemd) NO_SYSTEMD=1; shift;;
    -h|--help) echo "install.sh [--local-bin PATH] [--skip-deps] [--no-systemd]"; exit 0;;
    *) err "未知参数: $1"; exit 1;;
  esac; done
  TMP="$(mktemp -d)"
  A="$(arch)"
  if [ -z "$LOCAL_BIN" ]; then
    ASSET="vohive_${VERSION}_linux_${A}.tar.gz"
    BASE="https://github.com/$REPO/releases/download/$VERSION"
    download "$BASE/$ASSET" "$TMP/$ASSET"
    download "$BASE/$ASSET.sha256" "$TMP/$ASSET.sha256"
    (cd "$TMP" && sha256sum -c "$ASSET.sha256")
    mkdir "$TMP/unpack"; tar -xzf "$TMP/$ASSET" -C "$TMP/unpack"
    LOCAL_BIN="$TMP/unpack/vohive"
  fi
  deps
  root mkdir -p "$BIN_DIR" "$CFG_DIR" "$DATA_DIR" "$LOG_DIR"
  [ ! -x "$BIN_DIR/vohive" ] || root cp -f "$BIN_DIR/vohive" "$BIN_DIR/vohive.bak"
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
  if [ "$NO_SYSTEMD" = 0 ]; then
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
  fi
  if [ "$NO_SYSTEMD" = 0 ] && command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    root systemctl daemon-reload; root systemctl enable --now vohive; sleep 2
    root systemctl is-active --quiet vohive || { err "服务未启动，请查看 journalctl -u vohive"; exit 1; }
  else
    log "手动启动: $BIN_DIR/vohive -c $CFG_DIR/config.yaml"
  fi
  log "安装完成，端口 7575，默认账号密码 admin/admin"
}
main "$@"
