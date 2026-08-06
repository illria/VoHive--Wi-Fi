# Release and updater contract

本文档定义 `illria/VoHive--Wi-Fi` 的 Release 资产命名、校验和在线更新边界。修改命名时必须同时修改 `release.yml`、安装器和 `internal/updater`。

## Version

稳定版本使用 SemVer 标签，例如 `v1.2.3`。预发布版本可以使用 `v1.2.3-rc.1`；稳定通道只读取 GitHub 的 latest stable Release，预发布通道从 Release 列表中选择最高合法 SemVer 的预发布版本。

构建时注入以下字段：

- `internal/global.Version`
- `internal/global.BuildTime`
- `internal/global.Commit`

未注入的开发默认值不能被当成已发布版本。

## Asset names

对 Linux 目标架构 `amd64`、`arm64`、`armv7`，裸二进制资产必须使用：

```text
vohive_<tag>_linux_<arch>
vohive_<tag>_linux_<arch>.sha256
```

安装器归档使用：

```text
vohive_<tag>_linux_<arch>.tar.gz
vohive_<tag>_linux_<arch>.tar.gz.sha256
```

更新器只接受与目标版本、目标 OS 和目标架构完全匹配的裸二进制资产，并且要求相邻的 SHA-256 资产。它不会把归档直接交给二进制替换逻辑。

## Update lifecycle

1. UI 调用 `/system/update/check`。
2. 用户阅读 Release Note 后调用 `/system/update/apply`。
3. 服务端下载匹配的裸二进制和 `.sha256` 文件。
4. 服务端检查文件大小、计算 SHA-256，并将当前二进制备份到运行文件旁的 `update/backup`。
5. 服务端原子替换运行文件，写入 `update/status.json`，发送重启信号。
6. 新进程启动后调用健康确认；异常启动场景保留回滚信息并在健康窗口结束后尝试恢复旧版本。

状态通过 `/system/update/status` 返回。失败、校验不一致、资产缺失、架构不支持和 Docker 环境都会返回稳定错误码，便于 UI 展示和日志排查。

## Data boundary

更新流程只使用运行二进制所在目录下的 `update/` 工作目录。它不读取或删除业务配置、数据库、短信数据、设备配置、用户账号或通知配置。Docker 运行时不执行在线热替换。

## Manual verification

创建 Release 后，应在 GitHub Actions 中确认 CI 与 Release job 均成功，并在目标设备上人工确认：版本信息、登录、设备发现、QMI 数据连接、SIM/APDU、短信、VoWiFi 和回滚路径。云端交叉编译通过不等于目标设备硬件运行验证通过。

## Publish checklist

1. 在 GitHub 中创建 `v1.2.3` 或 `v1.2.3-rc.1` 标签并推送；Release workflow 会从该标签 checkout。手动运行 workflow 时必须填写同样格式的 SemVer 和构建 ref。
2. 等待 CI 和 Release 的所有 job 变绿；CI 的测试二进制保存在 Actions artifact，正式资产只出现在对应 GitHub Release。
3. 检查每个架构是否同时存在裸二进制、裸二进制校验文件、归档和归档校验文件，并确认 `manifest.json` 的三个 `linux_*` 条目和 SHA-256 与资产一致。
4. 如果 Release 说明或资产错误，先停止分发并在 GitHub Release 页面删除错误 Release/标签，再修正代码或版本后创建新的 SemVer 版本；不要覆盖历史正式 Release，也不要手工修改 manifest 哈希。

本仓库禁止在本机、开发机或目标设备执行依赖安装、前端构建、Go 测试、编译、打包或发布。所有这些步骤必须由 GitHub Actions 完成。
