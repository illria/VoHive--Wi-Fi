# VoHive portable builds

本仓库用 GitHub Actions 构建 VoHive 的 Linux 多架构二进制，并提供一键安装。由于旧版 `install.sh` 可能被 Raw CDN 缓存，当前推荐使用全新的 `install-v2.sh` 入口。

```sh
curl -fsSL https://raw.githubusercontent.com/illria/VoHive--Wi-Fi/main/install-v2.sh | sh
```

安装器按 `uname -m` 选择 amd64、arm64 或 armv7，下载 `portable` Release 并校验 SHA-256；你的 `aarch64` 设备使用 arm64 产物。默认安装到 `/opt/vohive`，后台端口 7575，默认账号密码 `admin/admin`。

Actions 固定使用社区源码快照 `hzlmy2002/vohive-collection@0c3052c524865a92d546f8fea12d873214c5f8e3`，不使用本机编译，也不使用 x86-64 备份二进制。产物覆盖 amd64、arm64、armv7。

这不是“所有 Linux 版本绝对兼容”：仍需要兼容的 Linux 内核、QMI/USB 模组、root/sudo 和可执行权限。你的设备只有约 371MB 内存、3.3GB 磁盘，建议原生二进制，不使用 Docker。源码快照使用 PolyForm Noncommercial 1.0.0，限非商业用途。
