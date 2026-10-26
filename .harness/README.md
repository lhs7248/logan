# Logan 最小 Harness

供 Agent 使用的工程入口、约束与验证组合。复用现有测试，不改变开发人员命令、CI 或 Git Hook。Python 3.9+ 标准库即可运行入口；业务验证仍需要对应工具链。所有命令在 Git 仓库根目录执行，也可用脚本绝对路径从其他目录运行。

## 使用

```sh
python3 .harness/run.py check
python3 .harness/run.py self-test
python3 .harness/run.py verify --profile quick
python3 .harness/run.py verify --profile full
python3 .harness/run.py verify --profile java
python3 .harness/run.py plan
python3 .harness/run.py verify --profile workbench
python3 .harness/run.py verify --profile workbench-http --timeout 600
python3 .harness/run.py acceptance --record /tmp/design-acceptance.json
```

- `self-test` profile：使用 `verify --profile self-test --task <id>` 在子进程运行回归测试，并生成可汇总的任务报告；原 `self-test` 命令仍可直接使用。
- `check`：校验必需资产、实际文件与登记表一致性、Markdown 内联相对链接的文件存在性、Markdown 中的常见机器路径、用例字段类型与引用，以及固定验证组合。链接锚点、引用式链接和非 Markdown 内容不在此项检查范围。
- `self-test`：先执行自检，再验证检查器及执行失败/缺失工具的报告行为；零测试不得通过。
- `quick`：原有 `make test`、`make vet`。
- `full`：原有 `make test`、`make race`、`make vet`、`make build`。
- `workbench`：执行已有 Node 前端测试、工作台 Go 测试，并在证据目录构建独立工作台程序。
- `workbench-http`：构建独立程序后执行现有 manual-v1、reading、push-v1 隔离 HTTP 脚本；需要本机端口，仅用临时合成数据，不使用 `--serve`，不连接真实推送服务。
- `java`：原有 `make java-baseline`，需要 Maven 和 JDK；只生成原有 captures，不更新 golden。

`verify` 先创建报告，再执行自检和所选组合。缺工具记为 blocked，失败/超时/中断均返回非零。每条命令默认最多 600 秒，可用 `--timeout` 调整。执行使用 POSIX 进程组；超时、Ctrl-C、SIGTERM 会终止组内子进程，Windows 当前标为受阻。主动脱离进程组的子进程不在此保证内。

报告在系统私有临时目录，开始时、每步前后及结束时原子更新，运行中退出码为 null。强制 SIGKILL/掉电不能保证收尾，但已有检查点不会表示通过。结构化摘要记录 UTC 起止时间、工具版本、Git commit、改动路径及内容哈希、运行前后代码状态变化；不保存原始 diff 或环境变量。忽略文件及外部依赖不属于代码指纹，运行快照非原子；`source_changed_during_run=true` 时成功命令组合会转为 stale 并返回非零；无法读取代码身份时转为 unbound，均需补齐后重验。原始命令日志可能含敏感数据，仅本地保存，不自动上传，留存时需人工脱敏。

## 按改动选择验证

`plan` 合并 HEAD 相对的暂存/未暂存改动与未跟踪文件；`--base <revision>` 可包含相对指定版本的已提交变化。只生成建议，不执行、不把建议当作通过。输出推荐 profile、设计验收要求、登记集成项（初始均为 not_run）及 unmapped 路径。公共依赖与构建文件（go.mod、go.sum、Makefile、build.sh、Dockerfile）和公共 config/protocol/repository/storage 改动会额外推荐 workbench 与 workbench-http。前缀匹配是保守提示，调用方仍需核对需求和间接影响，未知路径不得忽略。

[验证登记表](registry.json) 记录 SDK、恢复、容量及本机 HTTP 验证的脚本、影响路径和环境要求。check 验证登记结构、脚本存在，以及可执行登记与组合步骤/脚本适配的一致性；缺登记、错误组合或错误脚本都会失败。外部 SDK、数据库恢复、长时容量验证不自动运行：需要按原脚本参数准备隔离环境和授权，结果须另行提交证据。它们不会因 Go 测试通过而被标为通过。

## 设计验收记录

复制 [未验证模板](acceptance-template.json) 到任务证据目录，填写 task、code_revision（完整 commit）、code_workspace_sha256（计划中的工作区指纹）、reviewer、完整 scope（页面/状态 ID）及逐项 records。路径字段相对记录文件解析，也可提供绝对路径。每项需有设计基线及版本、实现位置、可比较条件、两份截图、交互结果和证据，并分开记录 functional_result/design_result。存在 differences 时还需 authorization_evidence。

`acceptance --record ...` 检查字段、scope 全覆盖、不重复记录、两种结论均为 passed、证据文件存在且非空，并记录证据 SHA-256。报告保留验收记录自身哈希与完整声明；核对声明的任务、commit 和工作区指纹。缺失或过期版本绑定不能通过。字段完整性 completeness 与版本绑定 binding 分开输出，修改记录或证据后需重新验收。失败返回非零。模板本身不可通过。成功仅叫“声明的证据完整”，不会判断截图内容、视觉相似度、授权真实性、scope 是否遗漏需求，也不会代替人工评审。该命令不生成或伪造产品验收记录。

## 同一任务的最终核对

为本次目标选择稳定 task ID。计划和报告放在仓库外，避免产物本身改变代码指纹。`--path` 可重复指定明确授权的文件/目录范围；未指定则覆盖全部已发现改动。被排除的改动会列在 out_of_scope，汇总结果仅适用于 selection，不能宣称整个工作区已验收。

以只修改 Harness 的任务为例：

```sh
python3 .harness/run.py plan --task harness-review --path .harness --output /tmp/harness-review-plan.json
python3 .harness/run.py verify --profile self-test --task harness-review
python3 .harness/run.py summarize --plan /tmp/harness-review-plan.json --report /tmp/实际运行目录/summary.json
```

`--output` 不覆盖已有计划。开始验证前完成源码编辑；后续修改会使旧计划和报告过期。重新生成新计划、运行相关验证，再核对。实际报告路径以命令输出为准。

`summarize` 重算计划的验证需求，逐项校验 task、运行前后代码身份、步骤覆盖、退出码和证据哈希，检查输入报告在核对期间是否变化。结果区分 missing、failed、stale、wrong_task、ambiguous、invalid、passed；任何缺项、未知路径、过期或失败均非零退出。每项只提交一份报告，重复报告会被拒绝，不自动从多份结果中挑选成功的那份。输出包含计划及输入报告的哈希。没有验证需求的空计划不计为通过。

设计任务另运行 `acceptance --task <id> --record ...`，将其报告一起通过多个 `--report` 传入。`complete` 的字段记录仍可能是 `stale` 的版本证据，只有当前版本绑定且证据未变才能满足 design 项。

外部 SDK/恢复/容量项不会被本机组合代替。需独立执行和评审，再提供人工签注的 JSON 报告：`kind` 为 `external-integration`，包含 task、integration（登记 ID）、source_start/source_end（计划相同的 commit 与 workspace_sha256）、status、exit_code、reviewer、environment，以及 artifacts（每项绝对 path 与 sha256）。未执行时不创建成功报告，汇总将保留 missing。人工签注的真实性和环境有效性仍需评审；本工具不验证签名，也不把汇总通过等同于产品或发布授权。

现有无 task 的 verify 报告可独立查看，但不能作为任务汇总的通过证据。普通 self-test 输出也不能代替 `verify --profile self-test` 的带版本报告。

## 按任务读取

- 所有代码变更：[验证与证据](rules/validation.md)。
- 产品页面、交互及原型落地：[设计基线与交付一致性](rules/design-fidelity.md)。
- API、协议、迁移、任务处理：[兼容规则](rules/compatibility.md)、[迁移流程](workflows/migration.md)。
- Bug 修复：[修复流程](workflows/bugfix.md)。
- 修改 Harness：执行 check 和 self-test，维护 [登记表](manifest.json) 与 [预期行为案例](evals/cases.json)。

实现与配置事实分别读取 [Go 说明](../Logan/ServerGo/README.md)、[Makefile](../Logan/ServerGo/Makefile)、[前端配置](../Logan/LoganSite/package.json)。迁移状态读取 [进度记录](../Logan/ServerGo/docs/progress.md)，不要在 Harness 重复维护版本号或已实现接口数。

## 采用边界

仅为 Agent 增加旁路入口。没有接入 CI，没有新增开发人员义务，没有自动提交、部署、迁移、基线更新或子 Agent 调度。前端与 SDK 验证按相关工程现有命令单独执行；当前 runner 组合 Go/Java、工作台 Node 与本机隔离 HTTP 验证。

`evals` 是预期行为和引用检查，不能证明宿主实际加载或模型遵守规则。真实 SDK、MySQL/OSS、前端联调、故障恢复与容量需要独立环境和证据；现有测试替身不能代替这些证据。

## 规则的实际执行覆盖

| 约束 | 当前执行机制 | 仍需评审或其他环境 |
| --- | --- | --- |
| 文件和路由登记完整 | check 比较实际资产、必需文件及用例引用 | 用例中的自然语言断言不执行，不能证明 Agent 行为 |
| full 不漏掉 race/vet/build | 配置必须与 runner 固定组合一致 | 不能阻止同时篡改 runner；命令实现仍需代码评审 |
| 验证失败不得报告通过 | 非零退出、缺工具、执行器异常和中断写入非成功摘要；回归测试验证 | 环境错误与产品错误的根因需要读日志 |
| Java 兼容与源码指纹 | quick/full 运行已有 contract、golden、source 测试 | 不能证明 golden 来源真实，不能阻止同时改基线与测试 |
| 不修改开发命令、CI、业务及授权边界 | 本入口不写这些文件；本次变更做差异审计 | 没有全仓库写入拦截或 Git/CI 门禁，依靠 Agent 范围约束和评审 |
| 按风险选择验证并完成需求 | validation/workflows 指导执行者 | plan 从改动给出保守建议，不自动执行；不校验完整需求覆盖或发布授权 |
| 日志脱敏、真实依赖与 SDK 验收 | 无自动检查 | 人工脱敏和独立集成环境；不能用 self-test 替代 |

这套检查器不是安全沙箱。它执行当前工作副本中的既有 Makefile，调用前需要信任并审查相关改动；不会证明测试本身未被削弱。系统临时目录的证据可能被清理，需要长期留存时另行保存脱敏结果。执行进度和成功仅限所选组合，不应把运行中视为通过。
