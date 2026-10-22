# 业务复审证据

范围及逐项结论见 [复审报告](../../business-review-2026-10-22.md)。基于工作区真实修复，使用本机合成资料，未连接真实推送或模型。

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 前端行为及所有嵌入模块语法 | 52/52，退出 0 | [完整输出](frontend-tests.txt) |
| 原型回归 | 124/124，退出 0 | 原型文件及 CSS 无修改；本轮命令输出 |
| Go 全量 test/race/vet/build | 各步骤退出 0 | [完整组合结果](go-full-summary.json) |
| 最后前端收尾后 Go UI 测试及最终二进制构建 | 退出 0 | 本轮实际命令输出；最终本机服务使用该构建 |
| 实际 HTTP | 17 项通过，退出 0 | [接口结果](http-acceptance.json) |
| 独立交叉复审 | 导入多文件确认及源码排除依赖回归通过 | TestImport.*Context、TestLogLink、TestIncludeSourceFalse、TestExcludedSourceDoesNotRestrict |
| Harness check / git diff --check | 退出 0 | 本轮实际输出 |

红测分别复现了旧筛选覆盖、源码残留、表单旧值、串问题通知、草稿过期、源代码排除失效、源码行范围误匹配、脱敏目标损坏及确认旁路；没有保留临时失败测试，也没有将失败结果计为通过。正式负路径保存在相应 Go/前端测试中。

浏览器实测截图：

- [既有日志关联前的身份差异核对](link-context-confirmation.png)
- [关联后的追加历史与不变原始身份](linked-history.png)
- [保留文件选择的批次差异核对](import-context-confirmation.png)
- [补充事件到人工内部回写的实际结果](handoff-result.png)
- [保留脱敏目标并修改用途成功](masked-draft-saved.png)
- [最终调查页面与恢复的源码排除选择](final-investigation.png)

设计验收：原导航、四份基线 CSS、阅读区/六工具与三步回捞结构保留。最终页面在 1440×1000 核对，可与 [原稿同尺寸页面](../design-restoration-2026-10-22/baseline-investigation.png) 对照；仅补产品契约已有的源码范围选择和身份差异确认，不改主题、导航或工作台布局。不同历史轮次和未配置状态如实保留，不复制原型的模拟成功。

这次没有重新执行所有屏宽的视觉审查；上一轮窄屏记录仍保留。本轮不因此宣称所有设备或真实供应商集成通过。服务仍为本机隔离验收进程，不代表开机自启或生产发布。
