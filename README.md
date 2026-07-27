# VoHive Linux 多架构构建与安装

本项目面向 Linux 设备提供 VoHive 的多架构构建产物、校验文件和自动化安装程序。项目通过 GitHub Actions 在云端完成交叉编译，不依赖本地构建环境。

## 项目能力

- 支持 amd64、arm64（aarch64）和 armv7 架构
- 根据当前 CPU 架构自动选择对应安装包
- 支持 Debian、Ubuntu、Alpine、OpenWrt 等常见 Linux 环境
- 自动校验下载文件 SHA-256
- 支持 systemd 服务注册与开机启动
- 默认安装目录：`/opt/vohive`
- 默认 Web 端口：`7575`
- 初始账号密码：`admin/admin`

## 一键安装

```sh
curl -fsSL https://raw.githubusercontent.com/illria/VoHive--Wi-Fi/main/install-portable.sh | sh
```

安装器会自动识别系统发行版、CPU 架构和服务管理方式。你的 aarch64 设备对应使用 arm64 构建产物。

## 构建与发布

构建流程由 GitHub Actions 执行，当前发布包位于：

[Portable Releases](https://github.com/illria/VoHive--Wi-Fi/releases/tag/portable)

构建目标：

- `vohive_portable_linux_amd64.tar.gz`
- `vohive_portable_linux_arm64.tar.gz`
- `vohive_portable_linux_armv7.tar.gz`

每个构建包均提供对应的 SHA-256 校验文件。

## 源码来源

Actions 使用固定版本的 VoHive 社区源码快照：

```
hzlmy2002/vohive-collection
commit: 0c3052c524865a92d546f8fea12d873214c5f8e3
```

构建过程不使用本机环境，也不使用 x86-64 离线备份二进制。

## 兼容性说明

本项目提供的是经过多架构交叉编译的 Linux 原生程序，不承诺对所有 Linux 发行版和硬件环境绝对兼容。实际运行还取决于：

- Linux 内核及系统调用支持
- root 权限或 sudo 权限
- USB、QMI 和 Qualcomm 4G/5G 模组支持
- systemd 或其他服务管理环境
- 可用磁盘空间和内存

资源受限设备建议使用原生二进制，不建议使用 Docker。

## 作者

Eianun  
X：[@Eianunbits](https://x.com/Eianunbits)

## 许可说明

源码快照的许可为 PolyForm Noncommercial 1.0.0，仅限非商业用途。使用前请确认相关源码、依赖和二进制发布许可。
