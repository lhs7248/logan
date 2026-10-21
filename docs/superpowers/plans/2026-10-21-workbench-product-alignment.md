# Workbench product alignment implementation plan

**Goal:** 将已确认的四组规则落实到现有本地交互原型，保留旧能力与会话。
**Architecture:** 保留现有三个入口及阅读工作台；增加纯规则模块和分领域 UI 适配，使用 v2 存储的可选新增字段，不迁移/删除旧数据。
**Tech Stack:** 原生 JavaScript、CSS、Node test，本地模拟，不接真实接口。

## Global constraints

- 以 docs/product/2026-10-21-logan-workbench 中的规格为产品基线。
- 旧回答、旧解析、原文件及工单更新不可覆盖；权限/过期演练采用可恢复的显示与操作限制，不实际清理内容。
- 产品布局不重构；缺失或不支持的能力明确显示原因。
- 当前任务直接执行和自审，不新增真实模型、渠道、仓库、服务端接口。

## Tasks

- [x] 1. 新增 product-model.js / product-model.test.js：采集实际范围、证据可用性、修复验证、草稿版本、幂等回写。先验证终态迟到、无有效验证拒绝关闭、陈旧草稿和重复提交。
- [x] 2. 新增 product-collection.js：采集能力选择、范围扩大确认、送达未知演练、取消/迟到记录；日粒度混合文件仍可阅读所有样例事件。
- [x] 3. 新增 product-evidence.js：原文件/解析版本说明、取消解析、可恢复过期演练、源码不可访问、AI 本轮包含/排除清单，重试不越过证据限制。
- [x] 4. 新增 product-handoff.js：独立修复验证表单与历史、AI/人工双模式草稿、重新预览保留编辑、版本冲突、关闭及重新调查。
- [x] 5. 在 index.html 加载以上模块和 product.css。完善交互设计文档及验收记录，列出 P 与真实 S/I/R 验收的区别。
- [x] 6. 完整运行旧测试和新规则测试、语法检查、文档链接检查；浏览器新建测试工单，演练采集未知回执、补证追问、人工/AI 回写及刷新保留；不重置既有数据。

## Interfaces

product-model.js 导出 LoganProduct：captureScope(begin,end,profile)、availability(issue,evidence)、token(issue)、validateFix(record)、validateHandoff(issue,draft)、commitHandoff(issue,draft)。UI 负责展示和调用；纯规则模块不依赖 DOM、localStorage 或网络。

关键边界示例：
```js
assert.throws(() => P.validateHandoff(issue, {...draft, status:'已解决'}), /修复验证|待验证/);
assert.equal(P.captureScope('2026-10-19T10:40','2026-10-19T10:45','day').expanded, true);
```

## Checks

在原型目录运行 `node --test *.test.js`，全部通过；新增脚本运行 `node --check`。对仓库与原预览副本先比较，仅同步确认相同基线的变更文件，保留 localStorage。最终手工走查屏幕状态，保存截图与通过/未执行的区别。
