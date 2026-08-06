# Web SIM PIN 安全行为

## 范围

Web 端只处理当前物理 SIM，或 eUICC/XeSIM/9eSIM 当前 active profile 的 PIN 状态和一次 PIN 验证。实现走 QMI UIM `GetCardStatus` 详情与 `VerifyPIN`；不走 AT `CPIN`，也不调用 qmicli/mmcli。

返回状态固定为：`ready`、`pin_required`、`puk_required`、`blocked`、`absent`、`network_locked`、`initializing`、`unavailable`、`unsupported`。

当 QMI UIM 报告使用通用 PIN 时，`uses_upin=true`、`pin_kind=upin`，验证使用 UPIN 状态和次数；否则使用 PIN1 状态和次数。

## API

- `GET /api/devices/:device_id/sim/security`：读取当前安全状态、PIN 类型、PIN/PUK 剩余次数和是否允许验证。
- `POST /api/devices/:device_id/sim/actions/verify-pin`：请求体只能是 `application/json` 的 `{"pin":"1234"}`。

POST 的服务端流程是：读取状态 → 只有 `pin_required` 且仍有次数时发送一次 `VerifyPIN` → 成功或失败后重新读取状态。失败不会自动再次提交，也不会触发 PUK、改 PIN、启停 PIN、SIM 断电/上电、modem reset、worker rebuild 或 identity convergence。

PIN 只在请求处理期间存在：不写入配置、数据库、URL、SSE、通知、action log、debug snapshot 或日志；错误响应只返回安全错误码和不含 PIN 的状态。请求体限制约 1 KiB，拒绝未知 JSON 字段和多个 JSON 对象，接口超时后前端只刷新状态，不重复 POST。

## 前端行为

设备概览中的“SIM 安全”面板只在概览可见且控制面在线时读取状态。`pin_required` 时以低频约 12 秒轮询；进入 ready、不可见或恢复阶段立即停止。PIN 输入默认隐藏、只接受 4–8 位 ASCII 数字，不自动提交；剩余 1 次时需要二次确认。提交成功或失败都会清空输入框。

当前页面不提供 PUK 解锁、改 PIN、启用/禁用 PIN、保存 PIN 或自动解锁。

## 验证与发布

本任务不在本地执行 Go/Node 构建、测试或打包。提交后由仓库现有 `.github/workflows/ci.yml` 在 GitHub Actions 中验证，重点覆盖 QMI PIN ID、PIN1/UPIN 状态与次数映射、请求校验、错误安全、并发互斥和单次 VerifyPIN 行为。真实硬件行为仍需在目标 Qualcomm QMI 设备上用一次正确 PIN、一次错误 PIN、PUK 状态和 eSIM active profile 手工确认。

建议手工检查：

1. ready、absent、initializing、network_locked、PUK 和 blocked 状态只显示状态，不显示解锁输入框。
2. PIN required 时确认 PIN1/UPIN、剩余次数、最后一次警告和成功后 identity/status 刷新。
3. 用错误 PIN 验证一次，确认次数只减少一次；确认超时或网络中断后页面只 GET 状态，不重复 POST。
4. 在 eSIM 切换、设备恢复、QMI 不可用和非 QMI backend 下确认操作被安全拒绝。

回滚时删除本任务新增的 SIM security API、worker/backend/QMI 方法、前端面板和本文档，并恢复路由/OpenAPI 变更；不要修改已有 eSIM 确认码流程或 CI 工作流。
