# 2026-10-22 设计恢复证据

结论、基线、逐页矩阵与范围见 [重新验收报告](../../design-restoration-acceptance.md)。全部使用本机合成验收资料；没有真实客户日志、供应商凭据或真实模型回答。

## 运行验证

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 正式前端 `node --test internal/workbenchui/*.test.mjs` | 34/34，退出 0；包括所有嵌入模块语法、上下文隔离、草稿及范围差异 | 本轮命令输出，新增行为回归见对应测试文件 |
| 原型 `node --test prototypes/logan-investigation/*.test.js` | 124/124，退出 0 | 原型源码未改动 |
| Harness check / self-test | 通过；自检 18 项，退出 0 | 自检中的失败/缺工具日志是测试夹具，不是实际业务验证失败 |
| Harness full | test / race / vet / build 全通过，退出 0 | [命令结果](go-full-summary.json) |
| 最后前端修复后 Go UI 测试及最终可执行程序构建 | 通过，退出 0 | 只改变嵌入 UI 后重验受影响包并重建 |
| 本机实际 HTTP 验收 | 15 项通过，退出 0 | [HTTP 结果](http-acceptance.json)；模型与推送明确未配置 |
| 最终独立只读复审 | 无未解决的本轮 P1/P2；独立重跑 34 项前端测试通过 | 查看原稿/恢复图、版本比较、上下文切换及草稿语义 |

首次独立组合测试误写不存在的 `internal/connectors` 目录，命令失败；更正为 `internal/workbench/connectors` 后通过，随后完整 Harness full 通过。没有将该失败当成成功。

最终界面运行在本机 `http://127.0.0.1:8091/workbench/`，仍使用原验收数据目录；原型 `http://127.0.0.1:3011/` 未替换。当前本机进程不是生产服务或开机自启保证。

## 截图索引

桌面均为 1440×1000；两组窄屏均为 650×1000。`baseline-*` 是实际运行原型，`restored-*` 是实际服务。对照数据使用同五条登录事件；历史请求、证据数、身份可信度和连接状态不同，原因见报告。

| 页面 | 原型 | 正式服务 |
| --- | --- | --- |
| 调查工作台 / AI | [原型](baseline-investigation.png) | [恢复](restored-investigation.png) |
| 日志阅读 | [原型](baseline-reader.png) | [恢复](restored-reader.png) |
| 资料库 | [原型](baseline-library.png) | [恢复](restored-library.png) |
| 问题列表 | [原型](baseline-issues.png) | [恢复](restored-issues.png) |
| 源码 | [原型](baseline-source.png) | [恢复](restored-source.png) |
| 证据 | [原型](baseline-evidence.png) | [恢复](restored-evidence.png) |
| 补充采集 | [原型](baseline-collect.png) | [恢复](restored-collect.png) |
| 验证 | [原型](baseline-verify.png) | [恢复](restored-verify.png) |
| 回写 | [原型](baseline-handoff.png) | [恢复](restored-handoff.png) |
| 回捞页面 | [原型](baseline-collection.png) | [恢复](restored-collection.png) |
| 回捞向导 | [原型](baseline-wizard.png) | [恢复](restored-wizard.png) |
| 窄屏日志 | [原型](baseline-reader-narrow.png) | [恢复](restored-reader-narrow.png) |
| 窄屏向导 | [原型](baseline-wizard-narrow.png) | [恢复](restored-wizard-narrow.png) |

流程状态：[回捞核对](restored-wizard-confirm.png)、[模型未配置](restored-ai-unconfigured.png)、[解析失败](restored-parse-failed.png)、[重解析恢复](restored-parse-recovered.png)、[内部回写结果](restored-handoff-result.png)。

缺陷与复验：[新建空工单仍残留旧日志的失败证据](restored-empty-issue.png)、[最终同路径修复复验](restored-empty-issue-fixed.png)。保留失败证据，不能将它作为最终通过截图。

## 未验范围

真实阿里云和客户端回捞、真实源码身份链与仓库、真实模型诊断质量、旧服务联调、生产数据库/容量与部署未通过本轮替代验证。原型模拟成功不迁入正式数据。650px 检查不能推断所有设备尺寸。
