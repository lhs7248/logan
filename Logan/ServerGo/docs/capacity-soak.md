# 并发负载与一小时稳定性对照

2026-10-19后续：已修复任务扫描、重复身份锁升级和空明细删除间隙锁三类冲突，实际重建镜像，仅Go完成有效一小时零错误/零重试复验。修复后完整结果见[Go锁冲突修复](go-mysql-lock-recovery.md)。下文保留第十九阶段失败原始结果，不改写为通过。

本轮只在专属容器、随机测试数据库和本地对象字节夹具上执行。原Java、SDK、golden、生产配置和原部署保持，不操作生产数据、不执行生产切流。用户最初选择两端各一小时，随后明确要求停止Java、集中验证Go。Java已停止，不再重跑；最终执行仅Go，短诊断不能计入Go一小时。

## 工作负载和对照口径

`cmd/soaktest` 使用已验证的原CLogan/WebSDK产物，以及既有protocol对照向量。完整工作流为上传→唯一合成device发现任务→首次解析/异步索引→明细内容精确核对；Native/Web交替。read档先在测量外预置并验证两个已解析种子，再仅查询固定明细。没有用健康检查、空查询、随机不存在ID或只看HTTP200冒充业务吞吐。

当前Go先做预热、50/200/500读取请求每秒、2/10/25完整工作流每秒，以及SDK短档；随后停止应用写入者、清空本次专属表、重启MySQL（禁用buffer pool dump/load）与对象夹具/应用，执行相同预热后进入3660秒容器计时的SDK混合持续负载（增加一分钟时钟余量）。持续负载固定10工作流/秒、16并发、32排队位。每工作流有多次HTTP调用，异步索引可能重试，不能把工作流/秒写成HTTP QPS。

protocol模式用于功能等价的性能对照。实际SDK的Java Native仍有此前已确认的多gzip成员日志缺失；只有恰好少后续记录且剩余首条完整匹配oracle时标为 `native_content_incomplete`，仍计入HTTP内容错误和严格工作流失败。未知差异、超时、业务code或持久化不完整不能归入该项。SDK吞吐差异不能直接解释为语言/HTTP框架性能差异，也不能将Java的严格错误率说成0。

## 资源和计量

专属Lima VM为3CPU/4GiB、linux/arm64、Docker29.9.1/cgroup2；应用各1CPU/1GiB且禁用容器swap。Go使用GOMAXPROCS1/GOMEMLIMIT768MiB，Java使用ActiveProcessorCount1/Xmx768m。MySQL8.4.11另限1CPU/1GiB、256MiB buffer pool/128连接，客户端0.5CPU/256MiB，对象夹具0.25CPU/128MiB。应用UID使用与只读测试挂载匹配的合成运行身份；不是重新认证生产文件权限。所有业务端口不发布，网络internal，默认readiness503保持。

Java保留原Tomcat JDBC池maxActive/maxIdle100/minIdle10，Go保留默认maxOpen20/maxIdle10；两端数据库/CPU/内存预算一致，但这不是归一化所有库和连接池参数的语言微基准。控制器默认仅Go（`--runtimes go`）；保留显式both供另行授权对照。Java本轮已中止，单机单次序/单规格数据不能确定生产容量上限或普遍性能比值。内部HTTP/MySQL明文，不包含TLS、网关或真实云OSS延迟。

负载采用有界开放到达模型。attempted为理想到达数，包含客户端未及时投递的到达；Sent为实际开始的工作流，dropped包含队列拒绝、调度错过和窗口到期未启动工作。调度窗口结束后不启动排队工作，只允许已开始流程在有界drain内结束。高QPS尾部调度丢弃必须列出，不能归为服务端错误，也不能隐藏。

HTTP延迟从客户端调用开始至响应正文和内容校验完成，含连接等待和失败，另列成功分位数；不含工作流入队前等待。工作流延迟包括异步索引重试等待；当前轮询间隔500ms，因此不是独立精密的后台解析耗时，后台完成还需SQL/内部job状态验证。分位数为nearest-rank，每路由、聚合、分钟窗口分别保存P50/P95/P99。报告保留总请求/成功/错误类别、工作流完成/严格成功、丢弃及实际elapsed，不能只比较成功RPS。

每10秒采样应用、MySQL、对象夹具和客户端cgroup CPU计数、节流、内存/峰值、进程RSS、线程/PID、OOM；cgroup memory.current包含缓存，不称为RSS。SQL同时记录连接/运行线程、慢查询、行锁等待、任务待处理数和job状态；待处理数包含刚上传尚未查询触发的任务。Go额外记录堆、goroutine、GC、连接池和job指标；Java保存统一GC日志。

验收校验客户端自身满3660秒；宿主单调时钟观测首尾资源采样跨度扣除20秒排空余量后仍至少3600秒，各角色采样覆盖开始/结尾且最大间隔不超过25秒。上传任务ID与完整工作流ledger逐一对应，最终Native/Web内容、status及Go job done逐项检查；汇总报告保留counts/SHA等合成证据；私有原始SQL核对记录仅含合成内容，不写入真实用户日志或真实凭据。持续段不允许丢弃、报告写入失败、意外错误、积压、OOM或中断；Java已知内容失败保持单列，不等于严格功能验收成功。

## 复验入口

```sh
go build -tags stdjson -o <linux-soak-loader> ./cmd/soaktest
python3 script/verify-capacity-soak.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <linux-soak-loader> --go-image <verified-production-image> --sdk-fixtures <fresh-verified-sdk-directory> --legacy-rsa-compat --runtimes go --soak-qps 10 --out <fresh-evidence> --work-dir <fresh-private-shared-directory>
```

需用GOOS=linux/GOARCH=arm64/CGO_ENABLED=0交叉编译上述loader，或在对应架构构建。已有两端短诊断确认输入和测量可用；当前按用户更新后的范围仅启动Go；短诊断必须同时给 `--duration 15 --stage-seconds 5 --allow-short-diagnostic`，报告明确 `one_hour_measurement_completed=false`。正式默认duration3660，每短档30秒。验证SDK弱RSA仍需显式诊断兼容开关，未修改默认生产配置。

每个fixture还先在network none容器中执行 `--validate-only`，确认字段/真实SDK日期/合成device覆盖和有界数据合法，然后才创建业务数据库/应用。输出/工作目录必须全新，私钥与带测试凭据WAR失败时也回收，资源按标签清理。

真实OSS缺凭据，只能测官方SDK到内部字节夹具的请求；不验证云签名、权限、限流和公网性能。真实SDK指原SDK代码生成的产物，不包含Android/iOS包装/真机或浏览器原生IndexedDB和网络。当前样本是小日志包、单一内容分布，一小时结果不证明大日志/多版本/跨地域/24小时稳定或无内存泄漏，也不是生产放量凭据。

## 计时口径与首轮拒绝

第一轮容器Go单调时钟elapsed3600.413s，但同一进程UTC起止仅3588.556s；宿主独立时钟也不足3600s，控制器安全拒绝并清理，不计为一小时通过。VZ虚拟机内时钟与宿主存在差异，未将容器elapsed简单视为真实墙钟。随后Java重跑按用户要求提前结束（退出130，归属资源清理通过），未宣称一小时完成。最终Go专项轮保留全部原始时钟，容器调度3660s；宿主应用采样首尾跨度扣除20s排空余量后仍必须>=3600s，控制器总时长也必须>=3600s。该额外余量不改变内容校验或负载。HTTP延迟为容器内Go单调时钟观测值，非跨时钟精密延迟认证；汇总另外给宿主控制器时间归一化HTTP QPS（含启动和收尾，略保守），不隐藏时钟差异。

## 本轮实测结果与结论（2026-10-17）

**仅Go完成有效一小时测量，零错误稳定性门槛未通过。** 用户要求停止Java后，Java第二轮exit130并清理，没有再运行Java。保留其已完成短档供参考，不宣称完成Java一小时对照。Go业务镜像和原Java/SDK/golden/部署未修改，本轮新增负载/计量/控制工具及测试。

Go持续段完成36,600个工作流、146,370次HTTP调用，客户端观测39.99 HTTP QPS，P95 14.78ms、P99 65.12ms。10次`web-upload/business_code`，HTTP严格错误率0.00683%，工作流失败10/36600；调度丢弃0、报告写入失败0。工作流P95 57.94ms、P99 110.95ms。

365组宿主资源采样首尾跨度3640.561s，扣除20s排空余量的保守活动下界3620.561s，超过3600s；容器monotonic 3660.001s、UTC跨度3644.960s，原始时间差保留。原控制器summary为failed/exit1、`one_hour_measurement_completed=false`（该字段同时要求所有验收通过）；独立复核单独标记`measurement_time_completed=true`和`strict_stability_passed=false`，没有覆盖失败摘要。

### 并发短档（每档30秒）

| 场景 | 目标到达/秒 | 实际HTTP QPS | HTTP P95/P99 ms | HTTP错误 | 调度丢弃 |
| --- | ---: | ---: | --- | ---: | ---: |
| 已解析读取 | 50 | 50.00 | 4.65/24.39 | 0 | 0 |
| 已解析读取 | 200 | 199.96 | 6.51/75.76 | 0 | 1 |
| 已解析读取 | 500 | 495.63 | 15.13/85.74 | 0 | 131 |
| protocol完整工作流 | 2 | 8.00 | 16.93/50.46 | 0 | 0 |
| protocol完整工作流 | 10 | 40.00 | 14.48/40.69 | 0 | 0 |
| protocol完整工作流 | 25 | 100.00 | 14.78/33.27 | 0 | 0 |
| SDK产物完整工作流 | 2 | 8.00 | 16.28/33.02 | 0 | 0 |
| SDK产物完整工作流 | 10 | 40.00 | 24.66/28.18 | 0 | 0 |

读取档目标单位为HTTP请求/秒，其他为完整工作流/秒。500读取档有131个丢弃，不能声称500 QPS零丢弃容量；25工作流/秒（约100 HTTP QPS）只测30秒，不能外推一小时或生产额定容量。

### 资源与最终数据

| 角色 | 平均单核占用 | 采样RSS首/尾/峰值 MiB | 采样cgroup memory.current峰值 MiB |
| --- | ---: | --- | ---: |
| Go应用 | 6.70% | 21.17/21.84/22.16 | 9.37 |
| MySQL | 14.62% | 485.32/583.86/583.86 | 850.38 |
| 对象字节夹具 | 3.04% | 20.67/28.66/28.66 | 32.27 |
| 客户端 | 2.06% | 6.67/46.95/60.71 | 65.71 |

全部采样OOM计数0。Go无CPU节流；MySQL、对象夹具、客户端累计节流计数分别约2.31s、250.10s、4.00s，不能把端到端尾延迟全部归因于Go框架。goroutine首/尾32/35，采样峰值41；堆采样峰值约3.34MiB，持续段连接池等待计数0。RSS与cgroup内存不是同一指标，内核累计memory.peak还包含预热，不混称RSS。

独立回放控制器实际执行的只读SQL输出，核对全部任务ID集合摘要、每任务完整内容和状态：Native18300任务/36600条明细、Web18290任务/18290条明细；成功上传共36590任务均完成、内容一致，未发现已接受日志缺失。10个失败工作流均在Web上传阶段，TaskID为0，其余ledger与数据库完整集合相同。Native有4个任务发生重试，最终job全部done且无last_error；Web无重试。重试恢复成功不能抵消请求失败或通过零重试门槛。

### 发现的问题与后续修复边界

捕获到真实InnoDB死锁：Native过期/耗尽任务的范围UPDATE，与持有job主键/二级索引锁的完成UPDATE形成锁冲突；服务日志同时出现Native/Web processing scan failed。持续采样行锁等待增加7529次、累计等待约9.99s，无慢查询增量。该死锁与4次解析重试是需修复的稳定性风险。

另有10次Web上传业务失败。当前通用business_code分类和Upload层ErrSave映射没有保留足够的底层错误上下文，**尚不能证明这10次失败均由上述死锁引起**，也不能判断具体业务code数值。下一步应补充脱敏数值错误码/关联观测，按主键分批且有条件处理过期任务、评估索引及锁顺序，并以真实MySQL并发/死锁测试证明租约、fence和幂等保护保留；只对已证明可安全重放的完整事务做有界重试。随后重建镜像，重新执行Go专项一小时，不复用本轮为修复后通过证据。

本轮保持测量二进制和业务镜像冻结，未在运行中改业务或放宽错误门槛。当前版本不能据此通过上线稳定性验收。

### 验证、证据及环境限制

Go full test/race/vet/build通过（最终clock版本，摘要logan-agent-b21auovv）；新增计时及Go单端选择回归有红绿证据，123工具测试通过。前置原Java基线已验证，用户停止Java后未再执行Java测试。控制器只启动Go的实际frozen-plan及运行事件均已核对；源码和二进制冻结摘要复核一致。

私有原始证据：`/private/tmp/logan-phase19-go-long/summary.json`（失败原样保留）、`independent-go-review.json`、`go-soak-observed.json`；工作目录`/private/tmp/logan-phase19-go-long-work/go-soak/`含最终report和resources.jsonl。`go-soak-observed.json`是从原报告/采样/最终SQL读记录重建的观察汇总，明确标记失败，不冒充控制器通过。冻结摘要`/private/tmp/logan-phase19-go-long-provenance.json`，死锁诊断`/private/tmp/logan-phase19-innodb-investigation*.txt`，错误计数摘要`/private/tmp/logan-phase19-mysql-error-digests.txt`。证据只在本地私有目录，系统临时目录可能被清理。

真实云OSS缺凭据，云签名/鉴权/延迟未验证；本轮为原SDK生成产物回放，不包含真机包装、真实浏览器加密/IndexedDB/网络。并发覆盖Native/Web各上传、任务发现、解析索引/简表、详情共8个调用类别，不等于全部20接口同时压测。单机arm64、单资源规格、小日志、内部明文HTTP/MySQL、一小时窗口，不能外推大日志、TLS性能、多实例容量、24小时或无泄漏认证。原Java保留，无生产数据写入、无生产切流，readiness503保持。

收尾：控制器按归属清理通过；额外末态读取曾因自动审批复核超时未执行，不认定为容器故障。后续独立库存确认专属容器/卷/网络为0，VM配置与创建记录一致后停止并删除，socket消失，临时env/WAR/PEM/P12/KEY残留0。清理证据`/private/tmp/logan-phase19-cleanup.json`。Harness check和17自测通过，tracked diff为空。未故意注入采样器强杀、磁盘写满或穷举新工具所有故障路径；已测非法时间/覆盖缺口拒绝、报告写失败、取消/退出及归属清理回归，不将其说成完整故障认证。
