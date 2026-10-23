# 移动端日志接入工作台 · 2026-10-22

> 最新复审见 [再次复审与真实链路边界](sdk-chain-review-2026-10-22.md)。后续增加的实际双端 HTTP、补充证据与模型协议测试，及新修复以该报告为准。

## 本轮交付

业务链为：应用上传凭据确定项目、应用和环境 → 移动端发送原始加密文件 → 平台一次提交原文件、日志资产、接收回执和排队解析 → 解析完成后资料库可见 → 用户明确关联工单 → 日志 / 源码证据 → AI 或人工调查 → 验证 → 核对预览后回写内部工单。

新增独立入口 `POST /logan-web/api/workbench/v1/sdk/native`。旧 `/logan/upload.json`、旧服务数据、手动上传和原型均保留。旧入口的文件不会自动复制到新资料库；移动端需要按本页配置新入口。阿里云推送仍未接入，SDK 上传凭据也不能发起推送或修改工单。

## 服务端配置

在已有 `WORKBENCH_AUTH_FILE` 中增加 `sdkClients`，每项包括 `id`、`token`、`projectId`、`appId`、`environment`、`platforms`。项目必须已存在，platforms 为 android / ios；凭据至少 16 字符，与工作台访问令牌和其他上传凭据不能重复。生产环境通过保密配置发放与轮换，不把真实凭据写进仓库。

```json
{
  "sdkClients": [{
    "id": "android-test-ingestion",
    "token": "<独立的应用上传凭据>",
    "projectId": "<已有项目ID>",
    "appId": "com.hxl.xlb.test",
    "environment": "test",
    "platforms": ["android", "ios"]
  }],
  "maxSDKBytes": 33554432
}
```

此段需合并至原有 projects / tokens 配置。设置 `WORKBENCH_NATIVE_KEY_BASE64` 和 `WORKBENCH_NATIVE_IV_BASE64`，与移动端 CLogan 初始化参数一致。未配置解码器时拒绝上传。当前为服务级一组 key/IV；不同应用使用不同密钥时，应隔离服务实例，尚无按应用选择密钥能力。

单文件默认最大 32 MiB，可下调；手动导入限制保持原值。两个上传 / 解析任务并发及原有持久化容量限制继续生效。生产入口使用 HTTPS；Android 已恢复系统证书及主机名校验。

## 移动端接入

调用及测试说明见 [移动端接入示例](../../examples/mobile/README.md)。Android 使用原有自定义请求头接口；iOS 新增 `loganUploadWithHeaders`，旧 `loganUpload` 签名及行为保留。真实业务应用仍需要更新调用地址和上传凭据，不能把本地合成演示视为已发布到设备。

请求体为原始 Native 加密字节，不能改成 multipart、ZIP 或文本。请求头：

| 字段 | 含义 |
| --- | --- |
| X-Logan-Upload-Token | 仅上传权限；服务端据此限定项目 / 应用 / 环境 |
| X-Logan-Upload-ID | 可选稳定重试标识；同一 ID 不能用于变化后的文件或元数据 |
| appId | 当前应用 ID，若与凭据冲突则拒绝 |
| deviceId | 客户端上报设备标识，必填 |
| platform | Android=1，iOS=2，必须属于凭据允许的平台 |
| fileDate | 文件日期 YYYY-MM-DD，必填；不是日志覆盖范围证明 |
| appVersion | SDK 上报版本，可未知 |
| buildVersion / bundleVersion | Android / iOS 上报构建，可未知 |

应用凭据认证应用范围；设备、平台、版本和构建仍为 SDK 上报声明，不能替代设备认证或 CI 构建证明。源码精确关联仍需要可信的不可变构建映射及事件文件位置。AI 的提示约束已明确这一区别。

## 成功、重试与失败

HTTP 200 / code 200 表示原文件和初次解析任务已持久化接收。返回 data.receiptId / assetId / runId / status / receipt / run。status 为该次初始解析的当前真实状态，不能把“接收成功”当成“解析成功”。

工作台 API `/sdk-uploads`、`/workspace` 和 `/parse-runs/{id}` 查询回执、资产和解析状态，仍需工作台权限；上传凭据不能读取资料库。`/capabilities` 的 sdkReceiver 显示配置状态，不泄露凭据。

重复上传按项目、客户端、文件身份、完整元数据及字节摘要判断；同上传 ID 改内容返回 409。过期重试返回 410，不恢复旧内容。失败解析保留原文件和诊断，可在“解析 / 历史”新建解析版本；不会修改原失败记录。服务重启后未完成任务标为中断失败，已完成对象及重试记录恢复。

原始文件下载受现有权限控制。界面明确区分“下载加密原文件”和“导出解密事件（当前筛选）”。新增 SDK 资产以提醒进入资料库，不抢占当前工单 / 阅读版本；关联工单遵循身份差异确认与历史记录规则，不自动启动 AI 或回写。

## 本地复验

在 ServerGo 目录运行 `python3 script/generate-sdk-workbench-fixture.py --output /tmp/新的空目录`，通过真实 CLogan 公共接口生成 native.bin。此驱动仅使用公开合成测试密钥，不用于生产。

配置隔离项目、专用上传凭据及合成解码密钥后，在环境中提供 `WORKBENCH_ACCEPTANCE_TOKEN`、`WORKBENCH_SDK_TOKEN`，运行：

```text
python3 script/verify-sdk-workbench.py --project <隔离项目ID> --native /tmp/新的空目录/native.bin --report /tmp/sdk-http-report.json
```

脚本会创建合成日志、工单、验证和内部更新记录；应仅对专门的隔离环境使用。本轮 [HTTP 记录](evidence/sdk-ingestion-2026-10-22/http-report.json) 覆盖实际 CLogan → 本地 HTTP → Go 解码 → 工作台 → 工单证据 / 验证 / 回写，另已实测重启数据和重试记录保留。

## 验收结果与边界

- Go 全量测试、vet、build 通过；真实 CLogan HTTP 贯通已加入 `TestNativeSDKWorkbenchHTTPChain` 自动回归；工作台 / 连接器 / SDK 并发测试通过。
- 工作台前端 63 项、原型 124 项通过；Harness check 通过。
- iOS 实际上传封装主机测试通过，底层 CLogan 和传输使用测试替身；Android 实际发送器本地 HTTP 字节及请求头、HTTPS 主机名检查通过。
- 浏览器确认 SDK 来源、自动解析、真实解密事件、身份声明、工单关联后的证据 / 无法确定的验证 / 内部更新历史。新日志到达提醒保持当前工单和运行版本；原布局、三项导航、六个调查工具及 CSS 保留。
- 独立审查修正了重绑定项目后的回执串项目风险，并移除了 Android 原有接受任意主机名的校验。对应回归先失败、修正后通过。

尚未验收实际 Android / iOS 设备与外部业务应用的网络链路、生产凭据、阿里云推送、真实源码提供方和真实模型质量。本地未配置源码 / 模型时明确提示并保留人工路径，不生成模拟源码命中或 AI 结论；本轮回写为内部工单更新。

详细 [验收与独立复审记录](evidence/sdk-ingestion-2026-10-22/verification.md)。

截图：[日志与回写](evidence/sdk-ingestion-2026-10-22/reader-handoff.png)、[源码边界](evidence/sdk-ingestion-2026-10-22/reader-source-boundary.png)。截图是实际服务界面；合成日志的类型不是严重级别，缺失级别显示“未知”。
