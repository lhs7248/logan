# Logan 最小 Harness

供 Agent 使用的工程入口、约束与验证组合。复用现有测试，不改变开发人员命令、CI 或 Git Hook。Python 3.9+ 标准库即可运行入口；业务验证仍需要对应工具链。所有命令在 Git 仓库根目录执行，也可用脚本绝对路径从其他目录运行。

## 使用

```sh
python3 .harness/run.py check
python3 .harness/run.py self-test
python3 .harness/run.py verify --profile quick
python3 .harness/run.py verify --profile full
python3 .harness/run.py verify --profile java
```

- `check`：校验必需资产、实际文件与登记表一致性、Markdown 内联相对链接的文件存在性、Markdown 中的常见机器路径、用例字段类型与引用，以及固定验证组合。链接锚点、引用式链接和非 Markdown 内容不在此项检查范围。
- `self-test`：先执行自检，再验证检查器及执行失败/缺失工具的报告行为；零测试不得通过。
- `quick`：原有 `make test`、`make vet`。
- `full`：原有 `make test`、`make race`、`make vet`、`make build`。
- `java`：原有 `make java-baseline`，需要 Maven 和 JDK；只生成原有 captures，不更新 golden。

`verify` 先执行 Harness 自检，再运行所选配置。退出码非零表示失败或执行受阻。每步先检查 make 与对应 go 或 mvn/java 是否位于 PATH，缺失则记录 blocked；发现工具不等于版本和运行环境有效。每步输出状态，并在系统临时目录生成 JSON 摘要及原始日志，路径打印在末尾；运行中断返回 130。日志可能含测试数据，仅保留本地，不自动上传。摘要只证明所选命令结果，不能证明端到端验收或发布就绪。

## 按任务读取

- 所有代码变更：[验证与证据](rules/validation.md)。
- API、协议、迁移、任务处理：[兼容规则](rules/compatibility.md)、[迁移流程](workflows/migration.md)。
- Bug 修复：[修复流程](workflows/bugfix.md)。
- 修改 Harness：执行 check 和 self-test，维护 [登记表](manifest.json) 与 [预期行为案例](evals/cases.json)。

实现与配置事实分别读取 [Go 说明](../Logan/ServerGo/README.md)、[Makefile](../Logan/ServerGo/Makefile)、[前端配置](../Logan/LoganSite/package.json)。迁移状态读取 [进度记录](../Logan/ServerGo/docs/progress.md)，不要在 Harness 重复维护版本号或已实现接口数。

## 采用边界

仅为 Agent 增加旁路入口。没有接入 CI，没有新增开发人员义务，没有自动提交、部署、迁移、基线更新或子 Agent 调度。前端与 SDK 验证按相关工程现有命令单独执行；当前 runner 仅组合 Go/Java 验证。

`evals` 是预期行为和引用检查，不能证明宿主实际加载或模型遵守规则。真实 SDK、MySQL/OSS、前端联调、故障恢复与容量需要独立环境和证据；现有测试替身不能代替这些证据。

## 规则的实际执行覆盖

| 约束 | 当前执行机制 | 仍需评审或其他环境 |
| --- | --- | --- |
| 文件和路由登记完整 | check 比较实际资产、必需文件及用例引用 | 用例中的自然语言断言不执行，不能证明 Agent 行为 |
| full 不漏掉 race/vet/build | 配置必须与 runner 固定组合一致 | 不能阻止同时篡改 runner；命令实现仍需代码评审 |
| 验证失败不得报告通过 | 非零退出、缺工具、执行器异常和中断写入非成功摘要；回归测试验证 | 环境错误与产品错误的根因需要读日志 |
| Java 兼容与源码指纹 | quick/full 运行已有 contract、golden、source 测试 | 不能证明 golden 来源真实，不能阻止同时改基线与测试 |
| 不修改开发命令、CI、业务及授权边界 | 本入口不写这些文件；本次变更做差异审计 | 没有全仓库写入拦截或 Git/CI 门禁，依靠 Agent 范围约束和评审 |
| 按风险选择验证并完成需求 | validation/workflows 指导执行者 | 不自动从 diff 选择 profile，不校验需求覆盖或发布授权 |
| 日志脱敏、真实依赖与 SDK 验收 | 无自动检查 | 人工脱敏和独立集成环境；不能用 self-test 替代 |

这套检查器不是安全沙箱。它执行当前工作副本中的既有 Makefile，调用前需要信任并审查相关改动；不会证明测试本身未被削弱。系统临时目录的证据可能被清理，需要长期留存时另行保存脱敏结果。当前没有单步超时控制，长任务可中断，不应把运行中视为通过。
