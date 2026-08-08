# VoHive

VoHive 是面向 Linux 随身 Wi‑Fi、Qualcomm 模组和 VoWiFi 场景的设备管理后台。仓库保留现有 QMI、SIM、短信、VoWiFi 和设备管理能力；本项目的更新器与发布流程从本仓库自身读取版本和 Release。

## 支持平台

- Linux amd64
- Linux arm64 / aarch64
- Linux armv7

实际可用性还取决于 Linux 内核、root 权限、USB/QMI 驱动、模组固件和 systemd 环境。

## 一键安装

```sh
curl -fsSL https://raw.githubusercontent.com/illria/VoHive--Wi-Fi/main/install.sh | sh
```

安装器会读取 `illria/VoHive--Wi-Fi` 的最新稳定 SemVer Release，按 CPU 架构下载归档并校验 SHA-256。也可以显式指定版本：

```sh
curl -fsSL https://raw.githubusercontent.com/illria/VoHive--Wi-Fi/main/install.sh | VOHIVE_VERSION=v1.2.3 sh
```

默认安装目录是 `/opt/vohive`，默认 Web 端口是 `7575`。安装器只在首次安装时创建示例配置；升级时保留已有配置和数据目录。

## GitHub Actions 构建与发布

`.github/workflows/ci.yml` 在 Pull Request、`main` 推送和手动运行时执行前端 lint/typecheck/build、Go 测试，并为 amd64、arm64、armv7 生成可下载的测试二进制 artifact。

`.github/workflows/release.yml` 只接受 `vX.Y.Z` 或带预发布标识的 SemVer 标签，例如 `v1.2.3`、`v1.2.3-rc.1`。每个正式 Release 包含：

- `vohive_vX.Y.Z_linux_amd64`
- `vohive_vX.Y.Z_linux_arm64`
- `vohive_vX.Y.Z_linux_armv7`
- 对应裸二进制的 `.sha256`
- 包含二进制和安装器的 `.tar.gz` 及校验文件
- `manifest.json` 及其校验文件

发布流程使用 `CGO_ENABLED=0` 交叉编译，不会把构建产物自动提交回 `main`。

SIM PIN Web 状态、PIN1/UPIN 映射、单次验证边界和手工验证清单见 [`docs/SIM_PIN_SECURITY.md`](docs/SIM_PIN_SECURITY.md)。

## 在线更新

登录后可在“系统设置”中检查稳定 Release、阅读 Release Note，并确认更新。更新入口默认使用“自动（推荐）”：服务端会在检查阶段依次尝试多个 GitHub 加速入口，选定成功入口后，本次下载和校验任务会保持不变；也可以手动选择固定入口，或填写自己的自定义 HTTP(S) 加速地址。自定义地址应填写可拼接 GitHub URL 的前缀，例如 `https://proxy.example.com/`。服务端会按当前架构选择精确资产，下载后验证 SHA-256，再备份并原子替换运行文件；前端通过状态接口轮询下载、校验、替换、重启和回滚状态。

在线更新不会删除 `/opt/vohive/config/config.yaml`、`/opt/vohive/data`、用户账号、通知配置、APN、短信数据或设备配置。Docker 环境不执行运行中二进制替换，应由容器部署流程更新镜像。

公共 GitHub 代理是第三方入口，可能临时失效或速度波动；自定义入口的可用性、隐私和安全风险需要按使用环境自行判断。下载完成后仍会校验 Release 提供的 SHA-256。

## 企业微信机器人通知

在“系统设置 → 通知 → 企业微信”中打开企业微信机器人，粘贴企业微信群机器人 Webhook 地址并保存。可以先点击“测试通知”确认连通性；Webhook 地址包含机器人密钥，请勿提交到代码仓库或公开发布。该渠道仅向群聊推送 VoHive 通知，不接收命令。

## 手动升级

如果设备无法访问 GitHub，先在电脑浏览器或“系统设置”的 GitHub 加速入口中下载目标架构的两个文件：

- `vohive_vX.Y.Z_linux_arm64.tar.gz`（aarch64 设备）或对应的 `amd64`、`armv7` 文件
- 同名的 `.sha256` 校验文件

将两个文件上传到设备后执行以下命令（以 arm64 和 `/opt/vohive` 为例）：

```sh
cd /tmp
sha256sum -c vohive_vX.Y.Z_linux_arm64.tar.gz.sha256
rm -rf vohive-upgrade
mkdir vohive-upgrade
tar -xzf vohive_vX.Y.Z_linux_arm64.tar.gz -C vohive-upgrade
cp -a /opt/vohive/bin/vohive /opt/vohive/bin/vohive.bak
systemctl stop vohive
install -m 0755 vohive-upgrade/vohive /opt/vohive/bin/vohive
systemctl start vohive
systemctl is-active --quiet vohive
```

手动升级只替换运行文件，不删除 `/opt/vohive/config`、`/opt/vohive/data`；启动失败时可用 `/opt/vohive/bin/vohive.bak` 恢复后再启动服务。若设备没有 systemd，停止并启动现有 VoHive 进程即可。

## 社区

[Telegram 社区](https://t.me/eianunkeji)

## 许可

使用前请确认源码、依赖和设备固件的相应许可与合规要求。
